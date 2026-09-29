// Package state persists one small JSON record per running tunnel so that
// `status` and `down` can find worker processes started earlier.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/ak-inori/ssmtun/internal/config"
)

// Status is the coarse lifecycle phase of a worker.
type Status string

const (
	StatusStarting     Status = "starting"
	StatusUp           Status = "up"
	StatusReconnecting Status = "reconnecting"
	StatusAuthRequired Status = "auth_required"
	StatusError        Status = "error"
	StatusStopping     Status = "stopping"
)

// Record is the on-disk shape of one worker.
type Record struct {
	Target      string    `json:"target"`
	PID         int       `json:"pid"`
	PluginPID   int       `json:"plugin_pid,omitempty"`
	LocalPort   int       `json:"local_port"`
	Host        string    `json:"host"`
	RemotePort  int       `json:"remote_port"`
	Profile     string    `json:"profile"`
	SessionID   string    `json:"session_id,omitempty"`
	Status      Status    `json:"status"`
	LastError   string    `json:"last_error,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	ConnectedAt time.Time `json:"connected_at,omitempty"`
	LastProbeAt time.Time `json:"last_probe_at,omitempty"`
	Reconnects  int       `json:"reconnects"`
	LogPath     string    `json:"log_path,omitempty"`
	Detached    bool      `json:"detached"`
}

// Dir returns $XDG_STATE_HOME/ssmtun or ~/.local/state/ssmtun.
func Dir() string {
	if v := os.Getenv("SSMTUN_STATE_DIR"); v != "" {
		return v
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(os.TempDir(), config.AppName)
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, config.AppName)
}

func recordPath(target string) string {
	return filepath.Join(Dir(), target+".json")
}

// LogPath is where a detached worker writes its log.
func LogPath(target string) string {
	return filepath.Join(Dir(), target+".log")
}

// Save writes the record atomically.
func Save(r *Record) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := recordPath(r.Target) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, recordPath(r.Target))
}

// Load returns the record for a target, or nil when none exists.
func Load(target string) (*Record, error) {
	data, err := os.ReadFile(recordPath(target))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("corrupt state file %s: %w", recordPath(target), err)
	}
	return &r, nil
}

// Remove deletes the record; missing files are fine.
func Remove(target string) error {
	err := os.Remove(recordPath(target))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// List returns every record sorted by target name.
func List() ([]*Record, error) {
	entries, err := os.ReadDir(Dir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		r, err := Load(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil || r == nil {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out, nil
}

// Alive reports whether the worker recorded in r still runs.
func (r *Record) Alive() bool {
	return ProcessAlive(r.PID)
}

// ProcessAlive uses signal 0 to test for a live process.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
