package cli

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ak-inori/ssmtun/internal/state"
)

func newStatusCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"ls", "ps"},
		Short:   "Show running tunnels",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(all)
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include configured targets that are not running")
	return cmd
}

func runStatus(all bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	recs, err := state.List()
	if err != nil {
		return err
	}
	byName := map[string]*state.Record{}
	for _, r := range recs {
		if !r.Alive() {
			_ = state.Remove(r.Target)
			continue
		}
		byName[r.Target] = r
	}

	names := cfg.Names()
	for _, r := range recs {
		if _, ok := cfg.Targets[r.Target]; !ok && byName[r.Target] != nil {
			names = append(names, r.Target)
		}
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TARGET\tSTATUS\tLOCAL\tREMOTE\tPID\tSINCE\tRECONNECTS\tNOTE")
	shown := 0
	for _, name := range names {
		r := byName[name]
		if r == nil {
			if !all {
				continue
			}
			t := cfg.Targets[name]
			fmt.Fprintf(w, "%s\t-\t:%d\t%s:%d\t\t\t\t\n", name, t.LocalPort, t.Host, t.Port)
			shown++
			continue
		}
		since := "-"
		if !r.ConnectedAt.IsZero() && r.Status == state.StatusUp {
			since = time.Since(r.ConnectedAt).Round(time.Second).String()
		}
		note := r.LastError
		if r.Status == state.StatusUp {
			note = ""
			if r.Detached {
				note = "detached"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t:%d\t%s:%d\t%d\t%s\t%d\t%s\n", name, r.Status, r.LocalPort, r.Host, r.RemotePort, r.PID, since, r.Reconnects, note)
		shown++
	}
	if shown == 0 {
		fmt.Println("no running tunnels (use --all to list configured targets)")
		return nil
	}
	return w.Flush()
}
