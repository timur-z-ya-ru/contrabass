package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

const stateDir = ".contrabass"
const stateFile = "state.json"

// PersistentState is the serializable orchestrator state saved between restarts.
type PersistentState struct {
	Backoff []types.BackoffEntry `json:"backoff"`
	SavedAt time.Time            `json:"saved_at"`
	Version int                  `json:"version"`
}

// statePath returns the full path to the state file.
// Uses o.stateBasePath if set (for tests), otherwise cwd.
func (o *Orchestrator) statePath() string {
	base := o.stateBasePath
	if base == "" {
		base = "."
	}
	return filepath.Join(base, stateDir, stateFile)
}

// stateDirectory returns the directory containing the state file.
func (o *Orchestrator) stateDirectory() string {
	base := o.stateBasePath
	if base == "" {
		base = "."
	}
	return filepath.Join(base, stateDir)
}

// SaveState persists the backoff queue to disk.
func (o *Orchestrator) SaveState() error {
	o.mu.Lock()
	backoff := make([]types.BackoffEntry, len(o.backoff))
	copy(backoff, o.backoff)
	o.mu.Unlock()

	path := o.statePath()

	if len(backoff) == 0 {
		_ = os.Remove(path)
		return nil
	}

	state := PersistentState{
		Backoff: backoff,
		SavedAt: time.Now(),
		Version: 1,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	if err := os.MkdirAll(o.stateDirectory(), 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// LoadState restores the backoff queue from disk. Expired entries dispatch immediately.
// Returns nil if no state file exists.
func (o *Orchestrator) LoadState() error {
	path := o.statePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read state: %w", err)
	}

	var state PersistentState
	if err := json.Unmarshal(data, &state); err != nil {
		o.logger.Printf("persistence: corrupt state file, ignoring: %v", err)
		return nil
	}

	now := time.Now()
	var restored []types.BackoffEntry
	for _, entry := range state.Backoff {
		if entry.RetryAt.Before(now) {
			entry.RetryAt = now
		}
		restored = append(restored, entry)
	}

	if len(restored) > 0 {
		o.mu.Lock()
		o.backoff = append(o.backoff, restored...)
		o.mu.Unlock()
		o.logger.Printf("persistence: restored %d backoff entries from %s", len(restored), state.SavedAt.Format(time.RFC3339))
	}

	_ = os.Remove(path)
	return nil
}
