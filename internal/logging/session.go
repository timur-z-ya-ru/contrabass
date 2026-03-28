package logging

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// RunRecord is a structured log entry for a completed agent run.
// Written as JSONL to enable post-hoc analysis of orchestrator sessions.
type RunRecord struct {
	Timestamp   time.Time `json:"timestamp"`
	Project     string    `json:"project,omitempty"`
	IssueID     string    `json:"issue_id"`
	IssueTitle  string    `json:"issue_title,omitempty"`
	IssueURL    string    `json:"issue_url,omitempty"`
	Attempt     int       `json:"attempt"`
	Phase       string    `json:"phase"`       // Succeeded, Failed, TimedOut, Stalled, etc.
	Error       string    `json:"error,omitempty"`
	TokensIn    int64     `json:"tokens_in"`
	TokensOut   int64     `json:"tokens_out"`
	DurationMs  int64     `json:"duration_ms"`
	PRNumber    string    `json:"pr_number,omitempty"`
	PRMerged    bool      `json:"pr_merged,omitempty"`
	IssueClosed bool      `json:"issue_closed,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	Workspace   string    `json:"workspace,omitempty"`
}

// SessionLogger writes structured JSONL run records to a file.
type SessionLogger struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
}

// NewSessionLogger creates a JSONL session logger.
// path is the output file (e.g., "contrabass-runs.jsonl").
// Returns nil if the file cannot be opened (non-fatal).
func NewSessionLogger(path string) *SessionLogger {
	if path == "" {
		return nil
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil
	}

	return &SessionLogger{
		file: f,
		enc:  json.NewEncoder(f),
	}
}

// LogRun writes a run record to the JSONL file.
func (s *SessionLogger) LogRun(record RunRecord) {
	if s == nil {
		return
	}

	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.enc.Encode(record)
}

// Close flushes and closes the JSONL file.
func (s *SessionLogger) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Close()
}
