package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ak-inori/ssmtun/internal/awsx"
	"github.com/ak-inori/ssmtun/internal/config"
)

func newLoginCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "login [<profile>...]",
		Short: "Make sure AWS SSO credentials are valid (device-code flow)",
		Long: `Check that each profile can obtain credentials and run the AWS SSO
device authorization flow when it cannot. Without arguments every profile
referenced by the configured targets is checked. Tokens are written to
~/.aws/sso/cache in the same format the AWS CLI uses, so both tools share
one login.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogin(cmd.Context(), args, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "log in even if credentials are still valid")
	return cmd
}

func runLogin(ctx context.Context, profiles []string, force bool) error {
	if len(profiles) == 0 {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		var targets []*config.Target
		for _, n := range cfg.Names() {
			targets = append(targets, cfg.Targets[n])
		}
		profiles = config.Profiles(targets)
	}
	if len(profiles) == 0 {
		return fmt.Errorf("no profiles to check; pass a profile name or configure targets")
	}

	done := map[string]bool{} // sso-session name -> already logged in this run
	for _, profile := range profiles {
		if !force {
			if err := awsx.CheckCredentials(ctx, profile); err == nil {
				fmt.Printf("%s: credentials valid\n", profile)
				continue
			}
		}
		sso, err := awsx.ResolveSSO(ctx, profile)
		if err != nil {
			return err
		}
		if sso == nil {
			return fmt.Errorf("profile %q is not an SSO profile and has no valid credentials", profile)
		}
		key := sso.SessionName
		if key == "" {
			key = sso.StartURL
		}
		if !done[key] {
			fmt.Fprintf(os.Stderr, "%s: logging in via %s\n", profile, key)
			if err := awsx.DeviceLogin(ctx, sso, os.Stderr); err != nil {
				return fmt.Errorf("profile %s: %w", profile, err)
			}
			done[key] = true
		}
		if err := awsx.CheckCredentials(ctx, profile); err != nil {
			return fmt.Errorf("profile %s still cannot obtain credentials after login: %w", profile, err)
		}
		fmt.Printf("%s: credentials valid\n", profile)
	}
	return nil
}
