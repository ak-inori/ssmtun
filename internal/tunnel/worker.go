// Package tunnel runs one supervised port-forwarding session per process.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/ak-inori/ssmtun/internal/awsx"
	"github.com/ak-inori/ssmtun/internal/config"
	"github.com/ak-inori/ssmtun/internal/probe"
	"github.com/ak-inori/ssmtun/internal/state"
)

const (
	authRetryEvery       = 30 * time.Second
	stableAfter          = 60 * time.Second
	probeFailuresToReset = 2
)

// Worker supervises a single tunnel: start session, run plugin, probe,
// reconnect. It owns the state record for its target.
type Worker struct {
	Config   *config.Config
	Target   *config.Target
	Log      *Logger
	Detached bool

	rec     *state.Record
	backoff Backoff
}

// Run blocks until ctx is cancelled. It returns nil on a clean shutdown.
func (w *Worker) Run(ctx context.Context) error {
	t := w.Target
	w.backoff = Backoff{Base: time.Second, Max: 30 * time.Second}
	w.rec = &state.Record{
		Target:     t.Name,
		PID:        os.Getpid(),
		LocalPort:  t.LocalPort,
		Host:       t.Host,
		RemotePort: t.Port,
		Profile:    t.Profile,
		Status:     state.StatusStarting,
		StartedAt:  time.Now(),
		Detached:   w.Detached,
	}
	if w.Detached {
		w.rec.LogPath = state.LogPath(t.Name)
	}
	w.save()
	defer func() {
		_ = state.Remove(t.Name)
	}()

	for {
		if ctx.Err() != nil {
			return nil
		}
		wasUp, err := w.runOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		var delay time.Duration
		switch {
		case errors.As(err, new(*authError)):
			w.setStatus(state.StatusAuthRequired, err)
			w.Log.Printf("credentials unavailable: %v", err)
			w.Log.Printf("run `ssmtun login %s` in another terminal; retrying every %s", t.Profile, authRetryEvery)
			delay = authRetryEvery
		case errors.As(err, new(*portBusyError)):
			w.setStatus(state.StatusError, err)
			w.Log.Printf("%v", err)
			w.Log.Printf("free the port or run `ssmtun down --force %s`; retrying in %s", t.Name, w.backoff.Max)
			delay = w.backoff.Max
		default:
			if wasUp {
				w.rec.Reconnects++
			}
			w.setStatus(state.StatusReconnecting, err)
			delay = w.backoff.Next()
			w.Log.Printf("tunnel down: %v; reconnecting in %s", err, delay.Round(100*time.Millisecond))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

type authError struct{ err error }

func (e *authError) Error() string { return e.err.Error() }
func (e *authError) Unwrap() error { return e.err }

type portBusyError struct {
	port int
	pids []int
}

func (e *portBusyError) Error() string {
	if len(e.pids) == 0 {
		return fmt.Sprintf("local port %d is already in use", e.port)
	}
	var parts []string
	for _, pid := range e.pids {
		cmd := ProcessCommand(pid)
		if len(cmd) > 60 {
			cmd = cmd[:60] + "…"
		}
		parts = append(parts, fmt.Sprintf("pid %d (%s)", pid, cmd))
	}
	return fmt.Sprintf("local port %d is already in use by %s", e.port, strings.Join(parts, ", "))
}

// runOnce establishes one session and supervises it until it fails.
// wasUp tells the caller whether the failure interrupted a working tunnel.
func (w *Worker) runOnce(ctx context.Context) (wasUp bool, err error) {
	t := w.Target
	if probe.PortInUse(t.LocalPort) {
		return false, &portBusyError{port: t.LocalPort, pids: ListenerPIDs(t.LocalPort)}
	}

	awsCfg, err := awsx.LoadProfile(ctx, t.Profile)
	if err != nil {
		return false, &authError{err}
	}
	if t.Region != "" {
		awsCfg.Region = t.Region
	}
	if _, err := awsCfg.Credentials.Retrieve(ctx); err != nil {
		return false, &authError{err}
	}

	instanceID := t.Instance
	if instanceID == "" {
		instanceID, err = awsx.ResolveInstanceByTag(ctx, awsCfg, t.InstanceTag)
		if err != nil {
			return false, err
		}
	}

	sess, err := awsx.StartSession(ctx, awsCfg, awsx.PortForwardRequest{
		Profile:    t.Profile,
		Region:     awsCfg.Region,
		InstanceID: instanceID,
		Host:       t.Host,
		RemotePort: t.Port,
		LocalPort:  t.LocalPort,
	})
	if err != nil {
		if isAccessDenied(err) {
			return false, &authError{err}
		}
		return false, err
	}
	w.rec.SessionID = sess.SessionID

	plugin, err := StartPlugin(ctx, sess, w.Log)
	if err != nil {
		w.terminate(awsCfg, sess.SessionID)
		return false, err
	}
	defer func() {
		plugin.Stop()
		w.terminate(awsCfg, sess.SessionID)
		w.rec.PluginPID = 0
		w.rec.SessionID = ""
	}()

	w.rec.PluginPID = plugin.PID()
	w.rec.ConnectedAt = time.Now()
	w.rec.LastError = ""
	w.setStatus(state.StatusUp, nil)
	w.Log.Printf("up: 127.0.0.1:%d -> %s:%d via %s (%s), session %s", t.LocalPort, t.Host, t.Port, instanceID, t.Profile, sess.SessionID)

	interval := w.Config.Defaults.HealthInterval
	timeout := w.Config.Defaults.ProbeTimeout
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failures := 0

	for {
		select {
		case <-ctx.Done():
			w.setStatus(state.StatusStopping, nil)
			w.Log.Printf("stopping")
			return true, nil
		case <-plugin.Done():
			return true, fmt.Errorf("%s exited: %v", PluginBinary, plugin.Err())
		case <-ticker.C:
		}

		err := probe.Run(ctx, t.Engine, t.LocalPort, timeout)
		w.rec.LastProbeAt = time.Now()
		if err == nil {
			failures = 0
			if time.Since(w.rec.ConnectedAt) > stableAfter {
				w.backoff.Reset()
			}
			w.save()
			continue
		}
		failures++
		w.Log.Printf("probe failed (%d/%d): %v", failures, probeFailuresToReset, err)
		if failures >= probeFailuresToReset {
			return true, fmt.Errorf("health probe failed %d times: %w", failures, err)
		}
	}
}

func (w *Worker) terminate(cfg aws.Config, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := awsx.TerminateSession(ctx, cfg, sessionID); err != nil && !sessionAlreadyGone(err) {
		w.Log.Printf("terminate session %s: %v", sessionID, err)
	}
}

func (w *Worker) setStatus(s state.Status, err error) {
	w.rec.Status = s
	if err != nil {
		w.rec.LastError = err.Error()
	}
	w.save()
}

func (w *Worker) save() {
	if err := state.Save(w.rec); err != nil {
		w.Log.Printf("write state: %v", err)
	}
}

func isAccessDenied(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "ExpiredToken") || strings.Contains(msg, "UnauthorizedException") ||
		strings.Contains(msg, "InvalidGrantException") || strings.Contains(msg, "token has expired") ||
		strings.Contains(msg, "failed to refresh cached credentials")
}

// sessionAlreadyGone is true when the plugin already terminated the session
// itself, so our belt-and-braces TerminateSession call has nothing to do.
func sessionAlreadyGone(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "InvalidSession") || strings.Contains(msg, "not in a valid state")
}
