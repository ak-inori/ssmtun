package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ak-inori/ssmtun/internal/config"
	"github.com/ak-inori/ssmtun/internal/probe"
	"github.com/ak-inori/ssmtun/internal/state"
	"github.com/ak-inori/ssmtun/internal/tunnel"
)

func newDownCmd() *cobra.Command {
	var all, force bool
	cmd := &cobra.Command{
		Use:   "down [<target>...]",
		Short: "Stop tunnels started with `up --detach`",
		Long: `Stop tunnel workers. Workers end their SSM session cleanly on SIGTERM.

--force additionally kills whatever still listens on a target's local port,
which cleans up session-manager-plugin processes left behind by other tools.`,
		ValidArgsFunction: completeTargets,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !all && len(args) == 0 {
				return errors.New("specify target names or --all")
			}
			return runDown(args, all, force)
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "stop every running tunnel")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "also kill unknown processes holding the local port")
	return cmd
}

func runDown(names []string, all, force bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if all {
		recs, err := state.List()
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, r := range recs {
			seen[r.Target] = true
			names = append(names, r.Target)
		}
		if force {
			for _, n := range cfg.Names() {
				if !seen[n] {
					names = append(names, n)
				}
			}
		}
	}
	if len(names) == 0 {
		fmt.Println("no running tunnels")
		return nil
	}

	names = dedupe(names)
	results := make([]downResult, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = stopTarget(cfg, name, force)
		}()
	}
	wg.Wait()

	var problems []string
	for _, r := range results {
		if r.err != nil {
			return r.err
		}
		for _, line := range r.lines {
			fmt.Println(line)
		}
		problems = append(problems, r.problems...)
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

type downResult struct {
	lines    []string
	problems []string
	err      error
}

func stopTarget(cfg *config.Config, name string, force bool) downResult {
	var res downResult
	rec, err := state.Load(name)
	if err != nil {
		res.err = err
		return res
	}
	stopped := false
	if rec != nil {
		if rec.Alive() && strings.Contains(tunnel.ProcessCommand(rec.PID), "ssmtun") {
			if err := stopWorker(rec.PID); err != nil {
				res.problems = append(res.problems, fmt.Sprintf("[%s] %v", name, err))
			} else {
				stopped = true
			}
		}
		_ = state.Remove(name)
	}

	port := 0
	if t, ok := cfg.Targets[name]; ok {
		port = t.LocalPort
	} else if rec != nil {
		port = rec.LocalPort
	}
	if port != 0 && probe.PortInUse(port) {
		pids := tunnel.ListenerPIDs(port)
		if force {
			var wg sync.WaitGroup
			for _, pid := range pids {
				wg.Add(1)
				go func() {
					defer wg.Done()
					killPID(pid)
				}()
			}
			wg.Wait()
			time.Sleep(500 * time.Millisecond)
			if probe.PortInUse(port) {
				res.problems = append(res.problems, fmt.Sprintf("[%s] port %d is still in use after --force", name, port))
			} else {
				res.lines = append(res.lines, fmt.Sprintf("[%s] killed %d process(es) holding port %d", name, len(pids), port))
				stopped = true
			}
		} else {
			res.problems = append(res.problems, fmt.Sprintf("[%s] port %d is still in use by pid(s) %v; rerun with --force", name, port, pids))
		}
	}
	if stopped {
		res.lines = append(res.lines, fmt.Sprintf("[%s] stopped", name))
	} else if rec == nil && len(res.problems) == 0 {
		res.lines = append(res.lines, fmt.Sprintf("[%s] not running", name))
	}
	return res
}

func dedupe(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func stopWorker(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal pid %d: %w", pid, err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !state.ProcessAlive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = p.Kill()
	return fmt.Errorf("worker pid %d did not exit in time and was killed", pid)
}

func killPID(pid int) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = p.Signal(syscall.SIGINT)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !state.ProcessAlive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = p.Kill()
}
