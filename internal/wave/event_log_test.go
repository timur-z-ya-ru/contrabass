package wave

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventLog_EmitAndQuery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	el, err := NewEventLog(path)
	require.NoError(t, err)
	defer el.Close()

	before := time.Now().Add(-time.Second)

	ev := Event{
		Timestamp: time.Now(),
		Type:      WaveEventWavePromoted,
		Phase:     1,
		Wave:      2,
		IssueID:   "issue-42",
		Issues:    []string{"issue-42", "issue-43"},
	}

	require.NoError(t, el.Emit(ev))

	// query with matching type
	results, err := el.Query(before, []EventType{WaveEventWavePromoted})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, WaveEventWavePromoted, results[0].Type)
	assert.Equal(t, 1, results[0].Phase)
	assert.Equal(t, 2, results[0].Wave)
	assert.Equal(t, "issue-42", results[0].IssueID)
	assert.Equal(t, []string{"issue-42", "issue-43"}, results[0].Issues)

	// query with non-matching type returns nothing
	results, err = el.Query(before, []EventType{WaveEventDAGBuilt})
	require.NoError(t, err)
	assert.Empty(t, results)

	// query with empty types returns all
	results, err = el.Query(before, nil)
	require.NoError(t, err)
	assert.Len(t, results, 1)

	// query with future since returns nothing
	results, err = el.Query(time.Now().Add(time.Hour), nil)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestEventLog_Subscribe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	el, err := NewEventLog(path)
	require.NoError(t, err)
	defer el.Close()

	ch := el.Subscribe()

	ev := Event{
		Timestamp: time.Now(),
		Type:      WaveEventPhaseCompleted,
		Phase:     3,
	}

	require.NoError(t, el.Emit(ev))

	select {
	case received := <-ch:
		assert.Equal(t, WaveEventPhaseCompleted, received.Type)
		assert.Equal(t, 3, received.Phase)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event on subscriber channel")
	}
}

func TestEventLog_CloseReleasesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	el, err := NewEventLog(path)
	require.NoError(t, err)

	require.NoError(t, el.Close())

	// file should exist after close
	_, err = os.Stat(path)
	assert.NoError(t, err)
}
