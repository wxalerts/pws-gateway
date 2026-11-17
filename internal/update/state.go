package update

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/wxalerts/pws-gateway/internal/atomicfile"
	"github.com/wxalerts/pws-gateway/internal/platform"
)

// Update states, the contract's update.state values.
const (
	StateIdle        = "idle"
	StateDownloading = "downloading"
	StateStaged      = "staged"
	StateRebooting   = "rebooting"
	StateGood        = "good"
	StateRolledBack  = "rolled_back"
	StateFailed      = "failed"
	StateDeferred    = "deferred"
)

// Why an update is deferred. Not on the wire (the contract carries only the
// state); the web page shows it.
const (
	DeferWindow   = "window"
	DeferLTE      = "lte"
	DeferDownload = "download"
)

// Pending is an installed but unconfirmed update. It survives the restart or
// reboot that applies it, which is how the next start knows a health check
// is owed and, if the old version is running again, that a rollback happened.
type Pending struct {
	// Kind is "os" or "app".
	Kind          string        `json:"kind"`
	Version       string        `json:"version"`
	VersionNumber int           `json:"version_number"`
	OSVersionNum  int           `json:"os_version_number,omitempty"`
	From          string        `json:"from"`
	Slot          platform.Slot `json:"slot,omitempty"`
	// Boots counts starts since the switch. A new version that keeps
	// restarting without ever passing its health check is rolled back.
	Boots int `json:"boots"`
	// Reason is set when the new version already decided to roll back, so
	// the old version can report why.
	Reason string `json:"reason,omitempty"`
}

// State is the persisted updater state. The fields up to TS mirror the
// contract's update message.
type State struct {
	State   string    `json:"state"`
	Current string    `json:"current"`
	Target  *string   `json:"target"`
	Error   *string   `json:"error"`
	TS      time.Time `json:"ts"`

	// Detail is a human sentence for the web page.
	Detail string `json:"detail,omitempty"`
	// DeferKind is why the update is deferred.
	DeferKind string `json:"defer_kind,omitempty"`
	// Code is the rejection code when state is failed after a rejected
	// manifest.
	Code string `json:"code,omitempty"`
	// Progress is the download percentage, kept in memory only.
	Progress int `json:"progress,omitempty"`

	// Failures counts consecutive failed download attempts.
	Failures   int       `json:"failures"`
	DeferUntil time.Time `json:"defer_until,omitempty"`
	LastCheck  time.Time `json:"last_check,omitempty"`
	// OSVersionNumber is the OS image revision the device runs.
	OSVersionNumber int `json:"os_version_number"`
	// OSDeferred is true while an OS image is waiting for a better link.
	OSDeferred bool `json:"os_deferred,omitempty"`
	// FailedVersions are version numbers that were rolled back, never retried
	// unless an operator forces it.
	FailedVersions []int    `json:"failed_versions,omitempty"`
	Pending        *Pending `json:"pending,omitempty"`
}

func statePath(dataDir string) string { return filepath.Join(dataDir, "update", "state.json") }

func loadState(dataDir string) (State, error) {
	b, err := os.ReadFile(statePath(dataDir))
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, err
	}
	s.Progress = 0
	return s, nil
}

func saveState(dataDir string, s State) error {
	if err := os.MkdirAll(filepath.Dir(statePath(dataDir)), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(statePath(dataDir), b, 0o600)
}
