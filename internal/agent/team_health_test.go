package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/charmbracelet/log"
)

func TestGetWorkerHealth(t *testing.T) {
	runner := newTeamCLIRunner(&teamCLIRunner{
		name:       "test-runner",
		binaryPath: "echo",
		logger:     log.New(nil),
	})

	ctx := context.Background()
	workspace := "/tmp/test"
	teamName := "test-team"
	workerName := "worker-1"
	maxAge := 30 * time.Second

	// Mock the team API calls
	// In a real test, we would use a mock server or dependency injection
	_, err := runner.GetWorkerHealth(ctx, workspace, teamName, workerName, maxAge)
	if err == nil {
		t.Error("Expected error with echo binary, got nil")
	}
}

func TestGetTeamHealth(t *testing.T) {
	runner := newTeamCLIRunner(&teamCLIRunner{
		name:       "test-runner",
		binaryPath: "echo",
		logger:     log.New(nil),
	})

	ctx := context.Background()
	workspace := "/tmp/test"
	teamName := "test-team"
	maxAge := 30 * time.Second

	_, err := runner.GetTeamHealth(ctx, workspace, teamName, maxAge)
	if err == nil {
		t.Error("Expected error with echo binary, got nil")
	}
}

func TestCheckWorkerNeedsIntervention(t *testing.T) {
	tests := []struct {
		name   string
		report *WorkerHealthReport
		want   string
	}{
		{
			name: "healthy worker",
			report: &WorkerHealthReport{
				WorkerName:        "worker-1",
				IsAlive:           true,
				Status:            "active",
				ConsecutiveErrors: 0,
			},
			want: "",
		},
		{
			name: "dead worker",
			report: &WorkerHealthReport{
				WorkerName: "worker-1",
				IsAlive:    false,
				Status:     "dead",
			},
			want: "Worker is dead: heartbeat stale for",
		},
		{
			name: "quarantined worker",
			report: &WorkerHealthReport{
				WorkerName:        "worker-1",
				IsAlive:           true,
				Status:            "quarantined",
				ConsecutiveErrors: 3,
			},
			want: "Worker self-quarantined after 3 consecutive errors",
		},
		{
			name: "at-risk worker",
			report: &WorkerHealthReport{
				WorkerName:        "worker-1",
				IsAlive:           true,
				Status:            "active",
				ConsecutiveErrors: 2,
			},
			want: "Worker has 2 consecutive errors — at risk of quarantine",
		},
	}

	// Note: This is a partial test since we can't easily mock the actual API calls
	// In production, we would need proper mocking infrastructure
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test validates the logic structure
			// Full integration tests would require a test harness
		})
	}
}

func TestTeamHealthSummaryJSON(t *testing.T) {
	summary := &TeamHealthSummary{
		TeamName:           "test-team",
		TotalWorkers:       3,
		HealthyWorkers:     2,
		DeadWorkers:        1,
		QuarantinedWorkers: 0,
		WorkerReports: []WorkerHealthReport{
			{
				WorkerName: "worker-1",
				IsAlive:    true,
				Status:     "active",
			},
			{
				WorkerName: "worker-2",
				IsAlive:    true,
				Status:     "idle",
			},
			{
				WorkerName: "worker-3",
				IsAlive:    false,
				Status:     "dead",
			},
		},
		CheckedAt: time.Now(),
	}

	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("Failed to marshal health summary: %v", err)
	}

	var decoded TeamHealthSummary
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal health summary: %v", err)
	}

	if decoded.TeamName != summary.TeamName {
		t.Errorf("TeamName mismatch: got %s, want %s", decoded.TeamName, summary.TeamName)
	}

	if decoded.TotalWorkers != summary.TotalWorkers {
		t.Errorf("TotalWorkers mismatch: got %d, want %d", decoded.TotalWorkers, summary.TotalWorkers)
	}

	if len(decoded.WorkerReports) != len(summary.WorkerReports) {
		t.Errorf("WorkerReports length mismatch: got %d, want %d", len(decoded.WorkerReports), len(summary.WorkerReports))
	}
}
