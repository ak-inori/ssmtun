package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ak-inori/ssmtun/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration files",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "path",
			Short: "Print the config files that would be read, in merge order",
			RunE: func(cmd *cobra.Command, args []string) error {
				cwd, _ := os.Getwd()
				for _, p := range config.CandidatePaths(cwd) {
					mark := " "
					if _, err := os.Stat(p); err == nil {
						mark = "*"
					}
					fmt.Printf("%s %s\n", mark, p)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "init",
			Short: "Write an example config to the user config path",
			RunE: func(cmd *cobra.Command, args []string) error {
				p := config.UserConfigPath()
				if _, err := os.Stat(p); err == nil {
					return fmt.Errorf("%s already exists", p)
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(p, []byte(config.ExampleYAML), 0o644); err != nil {
					return err
				}
				fmt.Printf("wrote %s\n", p)
				return nil
			},
		},
		&cobra.Command{
			Use:   "check",
			Short: "Parse and validate the configuration",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := loadConfig()
				if err != nil {
					return err
				}
				fmt.Printf("ok: %d target(s) from %d file(s)\n", len(cfg.Targets), len(cfg.Sources))
				for _, s := range cfg.Sources {
					fmt.Println("  ", s)
				}
				return nil
			},
		},
	)
	return cmd
}
