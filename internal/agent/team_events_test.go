package agent

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTeamEventJSON(t *testing.T) {
	event := &TeamEvent{
		EventID:   "evt_123",
		Team:      "test-team",
		Type:      "task_completed",
		Worker:    "worker-1",
		TaskID:    "task-1",
		State:     "completed",
		PrevState: "in_progress",
		Metadata: map[string]interface{}{
			"duration_ms": 1500,
		},
		CreatedAt: time.Now(),
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	var decoded TeamEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	if decoded.EventID != event.EventID {
		t.Errorf("EventID mismatch: got %s, want %s", decoded.EventID, event.EventID)
	}

	if decoded.Type != event.Type {
		t.Errorf("Type mismatch: got %s, want %s", decoded.Type, event.Type)
	}

	if decoded.Worker != event.Worker {
		t.Errorf("Worker mismatch: got %s, want %s", decoded.Worker, event.Worker)
	}

	if decoded.State != event.State {
		t.Errorf("State mismatch: got %s, want %s", decoded.State, event.State)
	}

	if decoded.PrevState != event.PrevState {
		t.Errorf("PrevState mismatch: got %s, want %s", decoded.PrevState, event.PrevState)
	}
}

func TestEventFilter(t *testing.T) {
	filter := &EventFilter{
		AfterEventID: "evt_100",
		Type:         "worker_state_changed",
		Worker:       "worker-1",
		WakeableOnly: true,
	}

	if filter.AfterEventID != "evt_100" {
		t.Errorf("AfterEventID mismatch: got %s, want evt_100", filter.AfterEventID)
	}

	if filter.Type != "worker_state_changed" {
		t.Errorf("Type mismatch: got %s, want worker_state_changed", filter.Type)
	}

	if !filter.WakeableOnly {
		t.Error("WakeableOnly should be true")
	}
}

func TestIdleStateJSON(t *testing.T) {
	state := &IdleState{
		TeamName:        "test-team",
		WorkerCount:     3,
		IdleWorkerCount: 2,
		IdleWorkers:     []string{"worker-1", "worker-2"},
		NonIdleWorkers:  []string{"worker-3"},
		AllWorkersIdle:  false,
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Failed to marshal idle state: %v", err)
	}

	var decoded IdleState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal idle state: %v", err)
	}

	if decoded.TeamName != state.TeamName {
		t.Errorf("TeamName mismatch: got %s, want %s", decoded.TeamName, state.TeamName)
	}

	if decoded.IdleWorkerCount != state.IdleWorkerCount {
		t.Errorf("IdleWorkerCount mismatch: got %d, want %d", decoded.IdleWorkerCount, state.IdleWorkerCount)
	}

	if decoded.AllWorkersIdle != state.AllWorkersIdle {
		t.Errorf("AllWorkersIdle mismatch: got %v, want %v", decoded.AllWorkersIdle, state.AllWorkersIdle)
	}
}

func TestStallStateJSON(t *testing.T) {
	state := &StallState{
		TeamName:         "test-team",
		TeamStalled:      true,
		LeaderStale:      false,
		StalledWorkers:   []string{"worker-2"},
		DeadWorkers:      []string{"worker-3"},
		PendingTaskCount: 5,
		AllWorkersIdle:   false,
		IdleWorkers:      []string{"worker-1"},
		Reasons:          []string{"workers_non_reporting:worker-2", "dead_workers_with_pending_work:worker-3"},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Failed to marshal stall state: %v", err)
	}

	var decoded StallState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal stall state: %v", err)
	}

	if decoded.TeamStalled != state.TeamStalled {
		t.Errorf("TeamStalled mismatch: got %v, want %v", decoded.TeamStalled, state.TeamStalled)
	}

	if len(decoded.Reasons) != len(state.Reasons) {
		t.Errorf("Reasons length mismatch: got %d, want %d", len(decoded.Reasons), len(state.Reasons))
	}

	if decoded.PendingTaskCount != state.PendingTaskCount {
		t.Errorf("PendingTaskCount mismatch: got %d, want %d", decoded.PendingTaskCount, state.PendingTaskCount)
	}
}
