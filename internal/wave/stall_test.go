package wave

import (
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStallDetector_CheckIssue_Continue(t *testing.T) {
	cfg := StallConfig{MaxRetries: 3}
	d := NewStallDetector(cfg)

	info := RunInfo{
		StartTime:   time.Now().Add(-5 * time.Minute),
		LastEventAt: time.Now(),
		Phase:       types.StreamingTurn,
		Attempt:     1,
	}

	assert.Equal(t, Continue, d.CheckIssue(info))
}

func TestStallDetector_CheckIssue_Escalate(t *testing.T) {
	cfg := StallConfig{MaxRetries: 3}
	d := NewStallDetector(cfg)

	info := RunInfo{
		StartTime:   time.Now().Add(-10 * time.Minute),
		LastEventAt: time.Now(),
		Phase:       types.Failed,
		Attempt:     4, // > MaxRetries
	}

	assert.Equal(t, Escalate, d.CheckIssue(info))
}

func TestStallDetector_CheckIssue_Retry(t *testing.T) {
	cfg := StallConfig{MaxRetries: 3}
	d := NewStallDetector(cfg)

	info := RunInfo{
		StartTime:   time.Now().Add(-10 * time.Minute),
		LastEventAt: time.Now(),
		Phase:       types.Failed,
		Attempt:     2,
	}

	assert.Equal(t, Retry, d.CheckIssue(info))
}

func TestStallDetector_CheckWave_NoStall(t *testing.T) {
	cfg := StallConfig{WaveMaxAgeMinutes: 180} // 3 hours
	d := NewStallDetector(cfg)

	wave := Wave{Index: 0, Issues: []string{"A", "B"}}
	running := map[string]RunInfo{
		"A": {StartTime: time.Now().Add(-10 * time.Minute), Phase: types.StreamingTurn},
		"B": {StartTime: time.Now().Add(-20 * time.Minute), Phase: types.StreamingTurn},
	}

	event := d.CheckWave(wave, running)
	assert.Nil(t, event)
}

func TestStallDetector_CheckWave_Stalled(t *testing.T) {
	cfg := StallConfig{WaveMaxAgeMinutes: 60} // 1 hour
	d := NewStallDetector(cfg)

	wave := Wave{Index: 1, Issues: []string{"A", "B", "C"}}
	running := map[string]RunInfo{
		"A": {StartTime: time.Now().Add(-90 * time.Minute), Phase: types.StreamingTurn}, // stalled
		"B": {StartTime: time.Now().Add(-10 * time.Minute), Phase: types.StreamingTurn}, // ok
		"C": {StartTime: time.Now().Add(-120 * time.Minute), Phase: types.StreamingTurn}, // stalled
	}

	event := d.CheckWave(wave, running)
	require.NotNil(t, event)
	assert.Equal(t, WaveEventStallDetected, event.Type)
	assert.Equal(t, 1, event.Wave)
	assert.ElementsMatch(t, []string{"A", "C"}, event.Issues)
}
