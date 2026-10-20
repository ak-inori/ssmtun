package cli

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ak-inori/ssmtun/internal/ansi"
	"github.com/ak-inori/ssmtun/internal/tunnel"
)

// newWorkerCmd is the hidden per-tunnel supervisor spawned by `up`.
func newWorkerCmd() *cobra.Command {
	var detached, noColor bool
	var colorIndex int
	cmd := &cobra.Command{
		Use:    "worker <target>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			t, err := cfg.Lookup(args[0])
			if err != nil {
				return err
			}
			color := ""
			if !noColor && ansi.Enabled(os.Stderr) {
				color, _ = ansi.Parse(t.Color)
				if color == "" {
					color = ansi.Palette[colorIndex%len(ansi.Palette)]
				}
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			w := &tunnel.Worker{
				Config:   cfg,
				Target:   t,
				Log:      tunnel.NewLogger(os.Stderr, t.Name, color),
				Detached: detached,
			}
			return w.Run(ctx)
		},
	}
	cmd.Flags().BoolVar(&detached, "detached", false, "mark the worker as detached in its state file")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "disable colored output")
	cmd.Flags().IntVar(&colorIndex, "color-index", 0, "palette slot used when the target has no color configured")
	return cmd
}
