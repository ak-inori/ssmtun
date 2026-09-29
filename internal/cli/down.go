package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

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

	var problems []string
	for _, name := range names {
		rec, err := state.Load(name)
		if err != nil {
			return err
		}
		stopped := false
		if rec != nil {
			if rec.Alive() && strings.Contains(tunnel.ProcessCommand(rec.PID), "ssmtun") {
				if err := stopWorker(rec.PID); err != nil {
					problems = append(problems, fmt.Sprintf("[%s] %v", name, err))
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
				for _, pid := range pids {
					killPID(pid)
				}
				time.Sleep(500 * time.Millisecond)
				if probe.PortInUse(port) {
					problems = append(problems, fmt.Sprintf("[%s] port %d is still in use after --force", name, port))
				} else {
					fmt.Printf("[%s] killed %d process(es) holding port %d\n", name, len(pids), port)
					stopped = true
				}
			} else {
				problems = append(problems, fmt.Sprintf("[%s] port %d is still in use by pid(s) %v; rerun with --force", name, port, pids))
			}
		}
		if stopped {
			fmt.Printf("[%s] stopped\n", name)
		} else if rec == nil && len(problems) == 0 {
			fmt.Printf("[%s] not running\n", name)
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func stopWorker(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal pid %d: %w", pid, err)
	}
	deadline := time.Now().Add(8 * time.Second)
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
