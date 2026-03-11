package ipc

import "time"

type Heartbeat struct {
	WorkerID    string    `json:"worker_id"`
	PID         int       `json:"pid"`
	CurrentTask string    `json:"current_task,omitempty"`
	Status      string    `json:"status"`
	Timestamp   time.Time `json:"timestamp"`
}

type HeartbeatWriter interface {
	Write(teamName string, hb Heartbeat) error
}

type Event struct {
	ID        string                 `json:"id,omitempty"`
	Type      string                 `json:"type"`
	TeamName  string                 `json:"team_name,omitempty"`
	WorkerID  string                 `json:"worker_id,omitempty"`
	TaskID    string                 `json:"task_id,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

type EventWriter interface {
	Log(teamName string, event Event) error
}

type DispatchEntry struct {
	TaskID       string     `json:"task_id"`
	WorkerID     string     `json:"worker_id"`
	Prompt       string     `json:"prompt"`
	DispatchedAt time.Time  `json:"dispatched_at"`
	AckedAt      *time.Time `json:"acked_at,omitempty"`
	Status       string     `json:"status,omitempty"`
}

type Dispatcher interface {
	Dispatch(teamName string, entry DispatchEntry) error
	Ack(teamName, taskID, workerID string) error
	Complete(teamName, taskID string) error
}
