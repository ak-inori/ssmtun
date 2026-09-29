package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/ak-inori/ssmtun/internal/awsx"
)

// PluginBinary is the executable the AWS CLI also shells out to.
const PluginBinary = "session-manager-plugin"

const readyTimeout = 30 * time.Second

// Plugin is one running session-manager-plugin process.
type Plugin struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	log  *Logger
}

// LookupPlugin returns the resolved path or an actionable error.
func LookupPlugin() (string, error) {
	p, err := exec.LookPath(PluginBinary)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH; install it with `brew install --cask session-manager-plugin` (macOS) or see https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html", PluginBinary)
	}
	return p, nil
}

// StartPlugin launches the plugin and waits until it reports the local port
// is open. Output lines are forwarded to the logger.
func StartPlugin(ctx context.Context, sess *awsx.Session, log *Logger) (*Plugin, error) {
	path, err := LookupPlugin()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, sess.PluginArgs()...)
	cmd.Stdin = nil // /dev/null; the plugin only reads stdin for shell sessions
	cmd.Env = append(os.Environ(), awsx.SessionResponseEnv+"="+sess.SessionArg)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", PluginBinary, err)
	}

	p := &Plugin{cmd: cmd, done: make(chan struct{}), log: log}
	ready := make(chan struct{})
	go p.pump(stdout, ready)
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()

	select {
	case <-ready:
		return p, nil
	case <-p.done:
		return nil, fmt.Errorf("%s exited before opening the port: %v", PluginBinary, p.err)
	case <-time.After(readyTimeout):
		p.Stop()
		return nil, fmt.Errorf("%s did not open the port within %s", PluginBinary, readyTimeout)
	case <-ctx.Done():
		p.Stop()
		return nil, ctx.Err()
	}
}

func (p *Plugin) pump(r io.Reader, ready chan struct{}) {
	sc := bufio.NewScanner(r)
	signalled := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !signalled && (strings.Contains(line, "Waiting for connections") || strings.Contains(line, "opened for sessionId")) {
			signalled = true
			close(ready)
		}
		if strings.HasPrefix(line, "Starting session with SessionId") || strings.HasPrefix(line, "Waiting for connections") || strings.HasPrefix(line, "Connection accepted for session") {
			continue
		}
		p.log.Printf("plugin: %s", line)
	}
}

// PID of the plugin process.
func (p *Plugin) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Done is closed once the process has exited.
func (p *Plugin) Done() <-chan struct{} { return p.done }

// Err is the exit error, valid after Done is closed.
func (p *Plugin) Err() error { return p.err }

// Stop asks the plugin to end the session (SIGINT makes it call
// TerminateSession itself) and kills it if it lingers.
func (p *Plugin) Stop() {
	if p.cmd.Process == nil {
		return
	}
	select {
	case <-p.done:
		return
	default:
	}
	_ = p.cmd.Process.Signal(syscall.SIGINT)
	select {
	case <-p.done:
		return
	case <-time.After(3 * time.Second):
	}
	_ = p.cmd.Process.Kill()
	<-p.done
}

// ExitedCleanly is true when the process ended with status 0 or by SIGINT.
func (p *Plugin) ExitedCleanly() bool {
	if p.err == nil {
		return true
	}
	var ee *exec.ExitError
	if errors.As(p.err, &ee) {
		if st, ok := ee.Sys().(syscall.WaitStatus); ok && st.Signaled() && st.Signal() == syscall.SIGINT {
			return true
		}
	}
	return false
}
