package agent

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTaskV2JSON(t *testing.T) {
	now := time.Now()
	task := &TaskV2{
		ID:          "1",
		Subject:     "Implement feature X",
		Description: "Add support for feature X with tests",
		Status:      "in_progress",
		Owner:       "worker-1",
		Version:     2,
		Claim: &TaskClaim{
			Owner:       "worker-1",
			Token:       "abc123",
			LeasedUntil: now.Add(5 * time.Minute),
		},
		CreatedAt: now,
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("Failed to marshal task: %v", err)
	}

	var decoded TaskV2
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal task: %v", err)
	}

	if decoded.ID != task.ID {
		t.Errorf("ID mismatch: got %s, want %s", decoded.ID, task.ID)
	}

	if decoded.Subject != task.Subject {
		t.Errorf("Subject mismatch: got %s, want %s", decoded.Subject, task.Subject)
	}

	if decoded.Status != task.Status {
		t.Errorf("Status mismatch: got %s, want %s", decoded.Status, task.Status)
	}

	if decoded.Version != task.Version {
		t.Errorf("Version mismatch: got %d, want %d", decoded.Version, task.Version)
	}

	if decoded.Claim == nil {
		t.Fatal("Claim is nil")
	}

	if decoded.Claim.Owner != task.Claim.Owner {
		t.Errorf("Claim owner mismatch: got %s, want %s", decoded.Claim.Owner, task.Claim.Owner)
	}

	if decoded.Claim.Token != task.Claim.Token {
		t.Errorf("Claim token mismatch: got %s, want %s", decoded.Claim.Token, task.Claim.Token)
	}
}

func TestClaimTaskResultJSON(t *testing.T) {
	result := &ClaimTaskResult{
		OK: true,
		Task: &TaskV2{
			ID:      "1",
			Subject: "Test task",
			Status:  "in_progress",
			Version: 1,
		},
		ClaimToken: "token123",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal claim result: %v", err)
	}

	var decoded ClaimTaskResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal claim result: %v", err)
	}

	if decoded.OK != result.OK {
		t.Errorf("OK mismatch: got %v, want %v", decoded.OK, result.OK)
	}

	if decoded.ClaimToken != result.ClaimToken {
		t.Errorf("ClaimToken mismatch: got %s, want %s", decoded.ClaimToken, result.ClaimToken)
	}

	if decoded.Task == nil {
		t.Fatal("Task is nil")
	}

	if decoded.Task.ID != result.Task.ID {
		t.Errorf("Task ID mismatch: got %s, want %s", decoded.Task.ID, result.Task.ID)
	}
}

func TestTaskWithDependencies(t *testing.T) {
	task := &TaskV2{
		ID:          "2",
		Subject:     "Dependent task",
		Description: "Task that depends on others",
		Status:      "blocked",
		BlockedBy:   []string{"1"},
		DependsOn:   []string{"1"},
		Version:     1,
		CreatedAt:   time.Now(),
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("Failed to marshal task: %v", err)
	}

	var decoded TaskV2
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal task: %v", err)
	}

	if len(decoded.BlockedBy) != len(task.BlockedBy) {
		t.Errorf("BlockedBy length mismatch: got %d, want %d", len(decoded.BlockedBy), len(task.BlockedBy))
	}

	if len(decoded.DependsOn) != len(task.DependsOn) {
		t.Errorf("DependsOn length mismatch: got %d, want %d", len(decoded.DependsOn), len(task.DependsOn))
	}

	if decoded.Status != "blocked" {
		t.Errorf("Status should be blocked for task with dependencies: got %s", decoded.Status)
	}
}
