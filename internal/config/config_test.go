package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sample = `
defaults:
  health_interval: 10s
targets:
  app-dev:
    profile: app
    instance: i-0123456789abcdef0
    host: dev.example.rds.amazonaws.com
    port: 5432
    local_port: 20001
  app-mysql:
    profile: app
    instance_tag: Name=bastion
    host: mysql.example.rds.amazonaws.com
    port: 3306
    local_port: 20002
`

func TestParseGuessesEngineAndAppliesDefaults(t *testing.T) {
	cfg, err := Parse("test", []byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.HealthInterval != 10*time.Second {
		t.Fatalf("health_interval = %v", cfg.Defaults.HealthInterval)
	}
	if cfg.Defaults.ProbeTimeout != defaultProbeTimeout {
		t.Fatalf("probe_timeout default not applied: %v", cfg.Defaults.ProbeTimeout)
	}
	if got := cfg.Targets["app-dev"].Engine; got != EnginePostgres {
		t.Fatalf("engine = %q", got)
	}
	if got := cfg.Targets["app-mysql"].Engine; got != EngineMySQL {
		t.Fatalf("engine = %q", got)
	}
	if cfg.Targets["app-dev"].Name != "app-dev" {
		t.Fatal("name not populated")
	}
}

func TestValidateRejectsDuplicatePortsAndBadInstance(t *testing.T) {
	bad := `
targets:
  a:
    profile: p
    instance: i-1
    host: h
    port: 5432
    local_port: 1000
  b:
    profile: p
    instance: i-2
    instance_tag: Name=x
    host: h
    port: 5432
    local_port: 1000
`
	_, err := Parse("bad", []byte(bad))
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "exactly one of instance or instance_tag") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnknownFieldIsAnError(t *testing.T) {
	_, err := Parse("typo", []byte("targets:\n  a:\n    profil: p\n"))
	if err == nil || !strings.Contains(err.Error(), "profil") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestLoadMergesProjectFileOverUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SSMTUN_CONFIG_DIR", filepath.Join(home, "cfg"))
	if err := os.MkdirAll(filepath.Join(home, "cfg", "config.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	must := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(home, "cfg", "config.yaml"), sample)
	must(filepath.Join(home, "cfg", "config.d", "team.yaml"), `
targets:
  team-db:
    profile: team
    instance: i-team
    host: team.example
    port: 5432
    local_port: 20010
`)
	project := filepath.Join(home, "repo", "sub")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	must(filepath.Join(home, "repo", projectFileName), `
targets:
  app-dev:
    profile: app
    instance: i-override
    host: dev.example.rds.amazonaws.com
    port: 5432
    local_port: 20001
`)

	cfg, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) != 3 {
		t.Fatalf("sources = %v", cfg.Sources)
	}
	if cfg.Targets["app-dev"].Instance != "i-override" {
		t.Fatalf("project file did not override: %+v", cfg.Targets["app-dev"])
	}
	if _, ok := cfg.Targets["team-db"]; !ok {
		t.Fatal("config.d target missing")
	}
	if _, err := cfg.Lookup("nope"); err == nil || !strings.Contains(err.Error(), "app-dev") {
		t.Fatalf("lookup error should list targets: %v", err)
	}
}
