package wave

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestManager_NewManager_NilTracker verifies that the manager can be created
// without a tracker (offline mode).
func TestManager_NewManager_NilTracker(t *testing.T) {
	m, err := NewManager(nil, "", nil)
	if err != nil {
		t.Fatalf("NewManager(nil, \"\", nil) error = %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil Manager")
	}
}

// TestManager_Refresh verifies that Refresh builds the DAG and updates openSet.
func TestManager_Refresh(t *testing.T) {
	mt := tracker.NewMockTracker()
	m, err := NewManager(mt, "", nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	issues := []types.Issue{
		{ID: "1", State: types.Unclaimed},
		{ID: "2", State: types.Unclaimed, BlockedBy: []string{"1"}},
	}

	if err := m.Refresh(issues); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	m.mu.RLock()
	open := m.openSet
	pipeline := m.pipeline
	m.mu.RUnlock()

	if !open["1"] || !open["2"] {
		t.Errorf("openSet should contain both issues, got %v", open)
	}
	if pipeline == nil {
		t.Fatal("pipeline should not be nil after Refresh")
	}
	if pipeline.DAG == nil {
		t.Fatal("pipeline.DAG should not be nil")
	}
	if len(pipeline.DAG.Nodes) != 2 {
		t.Errorf("expected 2 DAG nodes, got %d", len(pipeline.DAG.Nodes))
	}
}

// TestManager_FilterDispatchable_BlocksDeps verifies that issue 2 is blocked
// when both issue 1 and issue 2 are open.
func TestManager_FilterDispatchable_BlocksDeps(t *testing.T) {
	mt := tracker.NewMockTracker()
	m, err := NewManager(mt, "", nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	issues := []types.Issue{
		{ID: "1", State: types.Unclaimed},
		{ID: "2", State: types.Unclaimed, BlockedBy: []string{"1"}},
	}

	if err := m.Refresh(issues); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	dispatchable := m.FilterDispatchable(context.Background(), issues)
	if len(dispatchable) != 1 {
		t.Fatalf("expected 1 dispatchable issue, got %d: %v", len(dispatchable), dispatchable)
	}
	if dispatchable[0].ID != "1" {
		t.Errorf("expected issue 1 to be dispatchable, got %q", dispatchable[0].ID)
	}
}

// TestManager_FilterDispatchable_DepSatisfied verifies that issue 2 is
// dispatchable when its dependency (issue 1) is absent from openSet (closed).
func TestManager_FilterDispatchable_DepSatisfied(t *testing.T) {
	mt := tracker.NewMockTracker()
	m, err := NewManager(mt, "", nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Mark issue 1 as having a merged PR so PRVerifier check passes.
	mt.MergedPRs["1"] = true

	// Only issue 2 is open; issue 1 is absent (already closed).
	allIssues := []types.Issue{
		{ID: "1", State: types.Unclaimed},
		{ID: "2", State: types.Unclaimed, BlockedBy: []string{"1"}},
	}
	if err := m.Refresh(allIssues); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Simulate issue 1 being closed: remove it from openSet.
	m.mu.Lock()
	delete(m.openSet, "1")
	m.mu.Unlock()

	// FilterDispatchable only receives the currently open issues.
	openIssues := []types.Issue{
		{ID: "2", State: types.Unclaimed, BlockedBy: []string{"1"}},
	}

	dispatchable := m.FilterDispatchable(context.Background(), openIssues)
	if len(dispatchable) != 1 {
		t.Fatalf("expected 1 dispatchable issue, got %d", len(dispatchable))
	}
	if dispatchable[0].ID != "2" {
		t.Errorf("expected issue 2 to be dispatchable, got %q", dispatchable[0].ID)
	}
}

// TestManager_FilterDispatchable_WaveOrdered verifies that in a diamond graph
// (root → left, root → right, left → sink, right → sink) only the root is
// dispatchable initially.
func TestManager_FilterDispatchable_WaveOrdered(t *testing.T) {
	mt := tracker.NewMockTracker()
	m, err := NewManager(mt, "", nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Diamond: root has no deps; left and right depend on root; sink depends on both.
	issues := []types.Issue{
		{ID: "root", State: types.Unclaimed},
		{ID: "left", State: types.Unclaimed, BlockedBy: []string{"root"}},
		{ID: "right", State: types.Unclaimed, BlockedBy: []string{"root"}},
		{ID: "sink", State: types.Unclaimed, BlockedBy: []string{"left", "right"}},
	}

	if err := m.Refresh(issues); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	dispatchable := m.FilterDispatchable(context.Background(), issues)
	if len(dispatchable) != 1 {
		t.Fatalf("expected 1 dispatchable issue (root), got %d: %v", len(dispatchable), issueIDs(dispatchable))
	}
	if dispatchable[0].ID != "root" {
		t.Errorf("expected root to be dispatchable, got %q", dispatchable[0].ID)
	}
}

func issueIDs(issues []types.Issue) []string {
	ids := make([]string, len(issues))
	for i, iss := range issues {
		ids[i] = iss.ID
	}
	return ids
}

func TestManager_MultiWavePromotion(t *testing.T) {
	// Setup: 2 waves. Wave 0: issues 1,2. Wave 1: issues 3,4.
	// Complete all Wave 0 issues -> Wave 1 should become current.
	mock := tracker.NewMockTracker()
	mock.MergedPRs["1"] = true
	mock.MergedPRs["2"] = true

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "wave-config.yaml")
	os.WriteFile(cfgPath, []byte(`
phases:
  - name: "Phase 1"
    waves:
      - issues: ["1", "2"]
      - issues: ["3", "4"]
`), 0644)

	mgr, err := NewManager(mock, cfgPath, nil)
	require.NoError(t, err)

	// All 4 issues start open
	issues := []types.Issue{
		{ID: "1", State: types.Unclaimed},
		{ID: "2", State: types.Unclaimed},
		{ID: "3", State: types.Unclaimed, BlockedBy: []string{"1"}},
		{ID: "4", State: types.Unclaimed, BlockedBy: []string{"2"}},
	}
	mgr.Refresh(issues)

	// Verify Wave 0 is current
	cw := mgr.findCurrentWave()
	require.NotNil(t, cw)
	assert.Equal(t, 0, cw.Index)

	// Complete issue 1 (remove from open set)
	mgr.mu.Lock()
	delete(mgr.openSet, "1")
	mgr.mu.Unlock()

	// Wave 0 still current (issue 2 still open)
	cw = mgr.findCurrentWave()
	require.NotNil(t, cw)
	assert.Equal(t, 0, cw.Index)

	// Complete issue 2
	mgr.mu.Lock()
	delete(mgr.openSet, "2")
	mgr.mu.Unlock()

	// Now Wave 1 should be current
	cw = mgr.findCurrentWave()
	require.NotNil(t, cw)
	assert.Equal(t, 1, cw.Index)

	// Next wave should be nil (Wave 1 is the last)
	nw := mgr.findNextWave()
	assert.Nil(t, nw)
}

func TestManager_FindNextWave_CrossPhase(t *testing.T) {
	mock := tracker.NewMockTracker()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "wave-config.yaml")
	os.WriteFile(cfgPath, []byte(`
phases:
  - name: "Phase 1"
    waves:
      - issues: ["1"]
  - name: "Phase 2"
    waves:
      - issues: ["2"]
`), 0644)

	mgr, _ := NewManager(mock, cfgPath, nil)
	issues := []types.Issue{
		{ID: "1", State: types.Unclaimed},
		{ID: "2", State: types.Unclaimed},
	}
	mgr.Refresh(issues)

	// Current wave is Phase 1 Wave 0
	nw := mgr.findNextWave()
	require.NotNil(t, nw)
	// Next wave should be Phase 2 Wave 0 (issue "2")
	assert.Contains(t, nw.Issues, "2")
}

func TestManager_AllWavesComplete(t *testing.T) {
	mock := tracker.NewMockTracker()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "wave-config.yaml")
	os.WriteFile(cfgPath, []byte(`
phases:
  - name: "Phase 1"
    waves:
      - issues: ["1"]
`), 0644)

	mgr, _ := NewManager(mock, cfgPath, nil)
	issues := []types.Issue{{ID: "1", State: types.Unclaimed}}
	mgr.Refresh(issues)

	// Complete all
	mgr.mu.Lock()
	delete(mgr.openSet, "1")
	mgr.mu.Unlock()

	assert.Nil(t, mgr.findCurrentWave())
	assert.Nil(t, mgr.findNextWave())
}
