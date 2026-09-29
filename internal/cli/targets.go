package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newTargetsCmd() *cobra.Command {
	var quiet bool
	cmd := &cobra.Command{
		Use:   "targets",
		Short: "List configured targets",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if quiet {
				for _, n := range cfg.Names() {
					fmt.Println(n)
				}
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "TARGET\tPROFILE\tLOCAL\tREMOTE\tENGINE\tBASTION")
			for _, n := range cfg.Names() {
				t := cfg.Targets[n]
				bastion := t.Instance
				if bastion == "" {
					bastion = "tag:" + t.InstanceTag
				}
				fmt.Fprintf(w, "%s\t%s\t:%d\t%s:%d\t%s\t%s\n", n, t.Profile, t.LocalPort, t.Host, t.Port, t.Engine, bastion)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print names only")
	return cmd
}
