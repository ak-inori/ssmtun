package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ak-inori/ssmtun/internal/awsx"
	"github.com/ak-inori/ssmtun/internal/config"
	"github.com/ak-inori/ssmtun/internal/state"
	"github.com/ak-inori/ssmtun/internal/tunnel"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check local prerequisites and configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd.Context())
		},
	}
}

func runDoctor(ctx context.Context) error {
	failed := false
	report := func(ok bool, what string, detail string) {
		mark := "ok  "
		if !ok {
			mark = "FAIL"
			failed = true
		}
		fmt.Printf("%s  %-28s %s\n", mark, what, detail)
	}

	if path, err := tunnel.LookupPlugin(); err != nil {
		report(false, "session-manager-plugin", err.Error())
	} else {
		ver, _ := exec.Command(path, "--version").Output()
		report(true, "session-manager-plugin", strings.TrimSpace(string(ver))+" ("+path+")")
	}

	if _, err := exec.LookPath("lsof"); err != nil {
		report(false, "lsof", "not found; port-holder detection will be limited")
	} else {
		report(true, "lsof", "found")
	}

	if err := os.MkdirAll(state.Dir(), 0o700); err != nil {
		report(false, "state dir", err.Error())
	} else {
		report(true, "state dir", state.Dir())
	}

	cfg, err := loadConfig()
	if err != nil {
		report(false, "config", err.Error())
		return fmt.Errorf("doctor found problems")
	}
	if len(cfg.Sources) == 0 {
		report(false, "config", "no config file found; run `ssmtun config init` ("+config.UserConfigPath()+")")
	} else {
		report(true, "config", fmt.Sprintf("%d target(s) from %s", len(cfg.Targets), strings.Join(cfg.Sources, ", ")))
	}

	var targets []*config.Target
	for _, n := range cfg.Names() {
		targets = append(targets, cfg.Targets[n])
	}
	for _, profile := range config.Profiles(targets) {
		if _, err := awsx.LoadProfile(ctx, profile); err != nil {
			report(false, "profile "+profile, err.Error())
			continue
		}
		sso, err := awsx.ResolveSSO(ctx, profile)
		switch {
		case err != nil:
			report(false, "profile "+profile, err.Error())
		case sso == nil:
			report(true, "profile "+profile, "static/assume-role credentials")
		default:
			if err := awsx.CheckCredentials(ctx, profile); err != nil {
				report(true, "profile "+profile, "SSO ("+sso.StartURL+"), not logged in")
			} else {
				report(true, "profile "+profile, "SSO ("+sso.StartURL+"), logged in")
			}
		}
	}
	if failed {
		return fmt.Errorf("doctor found problems")
	}
	return nil
}
