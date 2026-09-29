// Package cli wires the cobra commands.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ak-inori/ssmtun/internal/config"
)

// Version is set by the linker at release time.
var Version = "dev"

// Execute runs the root command.
func Execute(ctx context.Context) int {
	root := newRoot()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "ssmtun",
		Short: "Supervised AWS SSM port-forwarding tunnels to RDS and friends",
		Long: `ssmtun opens AWS Systems Manager port-forwarding sessions to private
databases through an SSM-managed bastion, keeps them alive, and
reconnects when they drop. Targets are named in a YAML config so the
whole team shares one set of definitions.`,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newUpCmd(),
		newDownCmd(),
		newStatusCmd(),
		newLoginCmd(),
		newTargetsCmd(),
		newConfigCmd(),
		newDoctorCmd(),
		newWorkerCmd(),
	)
	return root
}

func loadConfig() (*config.Config, error) {
	cwd, _ := os.Getwd()
	return config.Load(cwd)
}

// completeTargets offers configured target names for shell completion.
func completeTargets(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	seen := map[string]bool{}
	for _, a := range args {
		seen[a] = true
	}
	var out []string
	for _, n := range cfg.Names() {
		if !seen[n] {
			desc := cfg.Targets[n].Description
			if desc == "" {
				desc = fmt.Sprintf("%s:%d -> :%d", cfg.Targets[n].Host, cfg.Targets[n].Port, cfg.Targets[n].LocalPort)
			}
			out = append(out, n+"\t"+desc)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
