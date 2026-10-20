// Package config loads and merges ssmtun target definitions.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ak-inori/ssmtun/internal/ansi"
)

const (
	AppName             = "ssmtun"
	projectFileName     = ".ssmtun.yaml"
	defaultHealthEvery  = 15 * time.Second
	defaultProbeTimeout = 5 * time.Second
)

// Engine selects the protocol-level health probe.
type Engine string

const (
	EnginePostgres Engine = "postgres"
	EngineMySQL    Engine = "mysql"
	EngineTCP      Engine = "tcp"
)

// Target is one tunnel definition.
type Target struct {
	Name        string `yaml:"-"`
	Profile     string `yaml:"profile"`
	Region      string `yaml:"region,omitempty"`
	Instance    string `yaml:"instance,omitempty"`
	InstanceTag string `yaml:"instance_tag,omitempty"`
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	LocalPort   int    `yaml:"local_port"`
	Engine      Engine `yaml:"engine,omitempty"`
	Color       string `yaml:"color,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// Defaults are applied to every target and to the supervisor loop.
type Defaults struct {
	HealthInterval time.Duration `yaml:"health_interval,omitempty"`
	ProbeTimeout   time.Duration `yaml:"probe_timeout,omitempty"`
	SSOSession     string        `yaml:"sso_session,omitempty"`
}

// Config is the merged view of every config file that was found.
type Config struct {
	Defaults Defaults           `yaml:"defaults"`
	Targets  map[string]*Target `yaml:"targets"`
	Sources  []string           `yaml:"-"`
}

type rawFile struct {
	Defaults Defaults           `yaml:"defaults"`
	Targets  map[string]*Target `yaml:"targets"`
}

// UserConfigDir returns $XDG_CONFIG_HOME/ssmtun or ~/.config/ssmtun.
func UserConfigDir() string {
	if v := os.Getenv("SSMTUN_CONFIG_DIR"); v != "" {
		return v
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".", "."+AppName)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, AppName)
}

// UserConfigPath is the primary config file.
func UserConfigPath() string {
	return filepath.Join(UserConfigDir(), "config.yaml")
}

// CandidatePaths lists every file that Load would read, in merge order.
// Later files override earlier ones on a per-target basis.
func CandidatePaths(cwd string) []string {
	var paths []string
	paths = append(paths, UserConfigPath())
	if entries, err := filepath.Glob(filepath.Join(UserConfigDir(), "config.d", "*.yaml")); err == nil {
		sort.Strings(entries)
		paths = append(paths, entries...)
	}
	if entries, err := filepath.Glob(filepath.Join(UserConfigDir(), "config.d", "*.yml")); err == nil {
		sort.Strings(entries)
		paths = append(paths, entries...)
	}
	if cwd != "" {
		if p := findProjectFile(cwd); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

func findProjectFile(dir string) string {
	dir = filepath.Clean(dir)
	for {
		p := filepath.Join(dir, projectFileName)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Load reads and merges every candidate file. Missing files are skipped.
func Load(cwd string) (*Config, error) {
	cfg := &Config{Targets: map[string]*Target{}}
	for _, p := range CandidatePaths(cwd) {
		data, err := os.ReadFile(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		if err := cfg.mergeYAML(p, data); err != nil {
			return nil, err
		}
		cfg.Sources = append(cfg.Sources, p)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Parse loads a single YAML document, mostly for tests and `config check`.
func Parse(name string, data []byte) (*Config, error) {
	cfg := &Config{Targets: map[string]*Target{}}
	if err := cfg.mergeYAML(name, data); err != nil {
		return nil, err
	}
	cfg.Sources = []string{name}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) mergeYAML(source string, data []byte) error {
	var raw rawFile
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil && !errors.Is(err, os.ErrNotExist) {
		if err.Error() == "EOF" {
			return nil
		}
		return fmt.Errorf("parse %s: %w", source, err)
	}
	if raw.Defaults.HealthInterval != 0 {
		c.Defaults.HealthInterval = raw.Defaults.HealthInterval
	}
	if raw.Defaults.ProbeTimeout != 0 {
		c.Defaults.ProbeTimeout = raw.Defaults.ProbeTimeout
	}
	if raw.Defaults.SSOSession != "" {
		c.Defaults.SSOSession = raw.Defaults.SSOSession
	}
	for name, t := range raw.Targets {
		if t == nil {
			return fmt.Errorf("parse %s: target %q is empty", source, name)
		}
		t.Name = name
		c.Targets[name] = t
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Defaults.HealthInterval == 0 {
		c.Defaults.HealthInterval = defaultHealthEvery
	}
	if c.Defaults.ProbeTimeout == 0 {
		c.Defaults.ProbeTimeout = defaultProbeTimeout
	}
	for _, t := range c.Targets {
		if t.Engine == "" {
			t.Engine = guessEngine(t.Port)
		}
	}
}

func guessEngine(port int) Engine {
	switch port {
	case 5432:
		return EnginePostgres
	case 3306:
		return EngineMySQL
	default:
		return EngineTCP
	}
}

func (c *Config) validate() error {
	usedPorts := map[int]string{}
	names := c.Names()
	for _, name := range names {
		t := c.Targets[name]
		var problems []string
		if strings.ContainsAny(name, " /\\") {
			problems = append(problems, "name must not contain spaces or slashes")
		}
		if t.Profile == "" {
			problems = append(problems, "profile is required")
		}
		if (t.Instance == "") == (t.InstanceTag == "") {
			problems = append(problems, "exactly one of instance or instance_tag is required")
		}
		if t.InstanceTag != "" && !strings.Contains(t.InstanceTag, "=") {
			problems = append(problems, "instance_tag must look like Key=Value")
		}
		if t.Host == "" {
			problems = append(problems, "host is required")
		}
		if t.Port <= 0 || t.Port > 65535 {
			problems = append(problems, "port must be 1-65535")
		}
		if t.LocalPort <= 0 || t.LocalPort > 65535 {
			problems = append(problems, "local_port must be 1-65535")
		}
		switch t.Engine {
		case EnginePostgres, EngineMySQL, EngineTCP:
		default:
			problems = append(problems, fmt.Sprintf("engine %q must be postgres, mysql or tcp", t.Engine))
		}
		if _, err := ansi.Parse(t.Color); err != nil {
			problems = append(problems, err.Error())
		}
		if other, dup := usedPorts[t.LocalPort]; dup && t.LocalPort > 0 {
			problems = append(problems, fmt.Sprintf("local_port %d is also used by %s", t.LocalPort, other))
		}
		usedPorts[t.LocalPort] = name
		if len(problems) > 0 {
			return fmt.Errorf("target %s: %s", name, strings.Join(problems, "; "))
		}
	}
	return nil
}

// Names returns target names sorted for stable output.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Targets))
	for n := range c.Targets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Lookup returns the target or a helpful error listing what exists.
func (c *Config) Lookup(name string) (*Target, error) {
	if t, ok := c.Targets[name]; ok {
		return t, nil
	}
	if len(c.Targets) == 0 {
		return nil, fmt.Errorf("unknown target %q: no targets configured (run `ssmtun config init`)", name)
	}
	return nil, fmt.Errorf("unknown target %q: available targets are %s", name, strings.Join(c.Names(), ", "))
}

// Profiles returns the distinct AWS profiles used by the given targets.
func Profiles(targets []*Target) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range targets {
		if !seen[t.Profile] {
			seen[t.Profile] = true
			out = append(out, t.Profile)
		}
	}
	return out
}

// ExampleYAML is written by `ssmtun config init`.
const ExampleYAML = `# ssmtun configuration
# Docs: https://github.com/ak-inori/ssmtun
#
# Files are merged in this order (later wins per target):
#   ~/.config/ssmtun/config.yaml
#   ~/.config/ssmtun/config.d/*.yaml
#   ./.ssmtun.yaml (searched upward from the current directory)

defaults:
  health_interval: 15s   # how often each tunnel is probed end-to-end
  probe_timeout: 5s

targets:
  # myapp-dev:
  #   profile: myapp               # AWS profile in ~/.aws/config (SSO supported)
  #   instance: i-0123456789abcdef0 # bastion managed by SSM; or use instance_tag: Name=bastion
  #   host: dev-myapp.cluster-xxxx.ap-northeast-1.rds.amazonaws.com
  #   port: 5432
  #   local_port: 20001
  #   engine: postgres             # postgres | mysql | tcp (guessed from port when omitted)
  #   color: cyan                  # log prefix color; auto-assigned when omitted
`
