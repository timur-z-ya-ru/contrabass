package wave

import "time"

type EventType string

const (
	WaveEventDAGBuilt       EventType = "dag_built"
	WaveEventWavePromoted   EventType = "wave_promoted"
	WaveEventWaveCompleted  EventType = "wave_completed"
	WaveEventPhaseCompleted EventType = "phase_completed"
	WaveEventPipelineDone   EventType = "pipeline_done"
	WaveEventIssueBlocked   EventType = "issue_blocked"
	WaveEventIssueUnblocked EventType = "issue_unblocked"
	WaveEventStallDetected  EventType = "stall_detected"
	WaveEventHealthCheck    EventType = "health_check"
	WaveEventReconcileDrift EventType = "reconcile_drift"
)

type Event struct {
	Timestamp time.Time              `json:"ts"`
	Type      EventType              `json:"type"`
	Phase     int                    `json:"phase,omitempty"`
	Wave      int                    `json:"wave,omitempty"`
	IssueID   string                 `json:"issue_id,omitempty"`
	Issues    []string               `json:"issues,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
}
