package wave

import (
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

// StallAction indicates what the orchestrator should do for a run.
type StallAction int

const (
	Continue StallAction = iota // ok, keep going
	Retry                       // requeue to backoff
	Escalate                    // retry exhausted, needs human
)

// RunInfo is a wave-package-owned abstraction to avoid importing orchestrator's runEntry.
type RunInfo struct {
	StartTime   time.Time
	LastEventAt time.Time
	Phase       types.RunPhase
	Attempt     int
}

// StallDetector evaluates run health at both the issue and wave level.
type StallDetector struct {
	config StallConfig
}

// NewStallDetector creates a StallDetector with the given configuration.
func NewStallDetector(cfg StallConfig) *StallDetector {
	return &StallDetector{config: cfg}
}

// CheckIssue returns the appropriate StallAction for a single issue run.
// - Escalate if attempt exceeds MaxRetries.
// - Retry if the phase is a terminal failure (Failed, TimedOut, Stalled).
// - Continue otherwise.
func (d *StallDetector) CheckIssue(info RunInfo) StallAction {
	if d.config.MaxRetries > 0 && info.Attempt > d.config.MaxRetries {
		return Escalate
	}
	switch info.Phase {
	case types.Failed, types.TimedOut, types.Stalled:
		return Retry
	}
	return Continue
}

// CheckWave inspects all running issues in the wave and returns a stall Event
// if any issue has been running longer than WaveMaxAge. Returns nil if healthy.
func (d *StallDetector) CheckWave(wave Wave, running map[string]RunInfo) *Event {
	maxAge := d.config.WaveMaxAge()
	now := time.Now()

	var stalled []string
	for _, issueID := range wave.Issues {
		info, ok := running[issueID]
		if !ok {
			continue
		}
		if now.Sub(info.StartTime) > maxAge {
			stalled = append(stalled, issueID)
		}
	}

	if len(stalled) == 0 {
		return nil
	}

	return &Event{
		Timestamp: now,
		Type:      WaveEventStallDetected,
		Wave:      wave.Index,
		Issues:    stalled,
	}
}
