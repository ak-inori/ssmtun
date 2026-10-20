package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ak-inori/ssmtun/internal/ansi"
	"github.com/ak-inori/ssmtun/internal/awsx"
	"github.com/ak-inori/ssmtun/internal/config"
	"github.com/ak-inori/ssmtun/internal/probe"
	"github.com/ak-inori/ssmtun/internal/state"
	"github.com/ak-inori/ssmtun/internal/tunnel"
)

const detachReadyTimeout = 90 * time.Second

func newUpCmd() *cobra.Command {
	var detach, noLogin, noColor bool
	cmd := &cobra.Command{
		Use:   "up <target>...",
		Short: "Start one supervised tunnel process per target",
		Long: `Start tunnels. Each target runs in its own process that probes the
connection every few seconds and reconnects with backoff when it drops.

By default ssmtun stays in the foreground and Ctrl-C stops every tunnel.
With --detach the tunnels keep running after ssmtun exits; use
"ssmtun status" and "ssmtun down" to manage them.`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeTargets,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUp(cmd.Context(), args, upOptions{detach: detach, noLogin: noLogin, noColor: noColor})
		},
	}
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "run tunnels in the background and return")
	cmd.Flags().BoolVar(&noLogin, "no-login", false, "skip the SSO credential check before starting")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "disable colored log prefixes (NO_COLOR is honored too)")
	return cmd
}

type upOptions struct {
	detach, noLogin, noColor bool
}

func runUp(ctx context.Context, names []string, opts upOptions) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if _, err := tunnel.LookupPlugin(); err != nil {
		return err
	}

	var targets []*config.Target
	for _, n := range names {
		t, err := cfg.Lookup(n)
		if err != nil {
			return err
		}
		targets = append(targets, t)
	}

	// Refuse to start anything if any target cannot start; half-started sets are confusing.
	var problems []string
	var toStart []*config.Target
	for _, t := range targets {
		rec, err := state.Load(t.Name)
		if err != nil {
			return err
		}
		if rec != nil && rec.Alive() && strings.Contains(tunnel.ProcessCommand(rec.PID), "ssmtun") {
			fmt.Fprintf(os.Stderr, "[%s] already running (pid %d, %s); skipping\n", t.Name, rec.PID, rec.Status)
			continue
		}
		if rec != nil {
			_ = state.Remove(t.Name)
		}
		if probe.PortInUse(t.LocalPort) {
			problems = append(problems, describeBusyPort(t))
			continue
		}
		toStart = append(toStart, t)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s\nfree the port(s) or run `ssmtun down --force <target>`", strings.Join(problems, "\n"))
	}
	if len(toStart) == 0 {
		return nil
	}

	if !opts.noLogin {
		for _, profile := range config.Profiles(toStart) {
			if err := awsx.EnsureLogin(ctx, profile, os.Stderr); err != nil {
				return fmt.Errorf("profile %s: %w", profile, err)
			}
		}
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	if opts.detach {
		return startDetached(ctx, self, toStart)
	}
	return startForeground(ctx, self, toStart, opts.noColor)
}

func describeBusyPort(t *config.Target) string {
	pids := tunnel.ListenerPIDs(t.LocalPort)
	if len(pids) == 0 {
		return fmt.Sprintf("[%s] local port %d is already in use", t.Name, t.LocalPort)
	}
	var parts []string
	for _, pid := range pids {
		parts = append(parts, fmt.Sprintf("pid %d (%s)", pid, tunnel.ProcessCommand(pid)))
	}
	return fmt.Sprintf("[%s] local port %d is already in use by %s", t.Name, t.LocalPort, strings.Join(parts, ", "))
}

func workerCommand(self string, t *config.Target, detach bool, extra ...string) *exec.Cmd {
	args := []string{"worker", t.Name}
	if detach {
		args = append(args, "--detached")
	}
	args = append(args, extra...)
	cmd := exec.Command(self, args...)
	cmd.Env = os.Environ()
	return cmd
}

func startForeground(ctx context.Context, self string, targets []*config.Target, noColor bool) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	type running struct {
		t    *config.Target
		cmd  *exec.Cmd
		done chan error
	}
	var procs []*running
	colors, err := assignColors(targets, noColor)
	if err != nil {
		return err
	}
	for i, t := range targets {
		cmd := workerCommand(self, t, false, "--color", colors[i])
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			for _, p := range procs {
				_ = p.cmd.Process.Signal(syscall.SIGTERM)
			}
			return fmt.Errorf("start worker for %s: %w", t.Name, err)
		}
		r := &running{t: t, cmd: cmd, done: make(chan error, 1)}
		go func() { r.done <- cmd.Wait() }()
		procs = append(procs, r)
	}
	fmt.Fprintf(os.Stderr, "%d tunnel(s) starting; press Ctrl-C to stop all\n", len(procs))

	alive := len(procs)
	var failed []string
	for alive > 0 {
		select {
		case <-ctx.Done():
			stop()
			for _, p := range procs {
				_ = p.cmd.Process.Signal(syscall.SIGTERM)
			}
			deadline := time.After(10 * time.Second)
			for alive > 0 {
				progressed := false
				for _, p := range procs {
					if p.done == nil {
						continue
					}
					select {
					case <-p.done:
						p.done = nil
						alive--
						progressed = true
					default:
					}
				}
				if alive == 0 {
					break
				}
				if !progressed {
					select {
					case <-deadline:
						for _, p := range procs {
							if p.done != nil {
								_ = p.cmd.Process.Kill()
							}
						}
						return errors.New("some tunnels did not stop in time and were killed")
					case <-time.After(50 * time.Millisecond):
					}
				}
			}
			return nil
		default:
		}
		for _, p := range procs {
			if p.done == nil {
				continue
			}
			select {
			case err := <-p.done:
				p.done = nil
				alive--
				if err != nil {
					failed = append(failed, p.t.Name)
					fmt.Fprintf(os.Stderr, "[%s] worker exited: %v\n", p.t.Name, err)
				}
			default:
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(failed) > 0 {
		return fmt.Errorf("worker(s) failed: %s", strings.Join(failed, ", "))
	}
	return nil
}

func startDetached(ctx context.Context, self string, targets []*config.Target) error {
	if err := os.MkdirAll(state.Dir(), 0o700); err != nil {
		return err
	}
	for _, t := range targets {
		logf, err := os.OpenFile(state.LogPath(t.Name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		cmd := workerCommand(self, t, true)
		cmd.Stdout = logf
		cmd.Stderr = logf
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			logf.Close()
			return fmt.Errorf("start worker for %s: %w", t.Name, err)
		}
		logf.Close()
		_ = cmd.Process.Release()
	}

	// Wait until every worker reports a terminal-ish status so the user sees the outcome.
	pending := map[string]*config.Target{}
	for _, t := range targets {
		pending[t.Name] = t
	}
	deadline := time.Now().Add(detachReadyTimeout)
	var failures []string
	for len(pending) > 0 && time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for name, t := range pending {
			rec, err := state.Load(name)
			if err != nil || rec == nil {
				continue
			}
			switch rec.Status {
			case state.StatusUp:
				fmt.Printf("[%s] up: 127.0.0.1:%d -> %s:%d (pid %d, log %s)\n", name, t.LocalPort, t.Host, t.Port, rec.PID, rec.LogPath)
				delete(pending, name)
			case state.StatusError, state.StatusAuthRequired:
				failures = append(failures, fmt.Sprintf("[%s] %s: %s (still retrying; see %s)", name, rec.Status, rec.LastError, rec.LogPath))
				delete(pending, name)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	for name := range pending {
		failures = append(failures, fmt.Sprintf("[%s] not up after %s; see %s", name, detachReadyTimeout, state.LogPath(name)))
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "\n"))
	}
	return nil
}

// assignColors gives every target a distinct log color unless color is off.
func assignColors(targets []*config.Target, noColor bool) ([]string, error) {
	if noColor {
		return make([]string, len(targets)), nil
	}
	explicit := make([]string, len(targets))
	for i, t := range targets {
		explicit[i] = t.Color
	}
	return ansi.Assign(explicit)
}
