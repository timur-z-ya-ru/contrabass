package agent

import (
	"context"
	"fmt"
)

// ScaleUpResult represents the result of scaling up workers
type ScaleUpResult struct {
	AddedWorkers   []WorkerInfo `json:"added_workers"`
	NewWorkerCount int          `json:"new_worker_count"`
}

// ScaleDownResult represents the result of scaling down workers
type ScaleDownResult struct {
	RemovedWorkers []string `json:"removed_workers"`
	NewWorkerCount int      `json:"new_worker_count"`
}

// WorkerInfo contains basic worker information
type WorkerInfo struct {
	Name  string `json:"name"`
	Index int    `json:"index"`
	Role  string `json:"role"`
	PID   int    `json:"pid,omitempty"`
}

// ScaleUp adds workers to a running team
func (r *teamCLIRunner) ScaleUp(ctx context.Context, workspace, teamName string, count int, roles []string) (*ScaleUpResult, error) {
	if count < 1 {
		return nil, fmt.Errorf("count must be positive, got %d", count)
	}

	// Read current config to check capacity
	var configResp struct {
		Config struct {
			WorkerCount int          `json:"worker_count"`
			MaxWorkers  int          `json:"max_workers"`
			Workers     []WorkerInfo `json:"workers"`
		} `json:"config"`
	}

	if err := r.runTeamAPI(ctx, workspace, "read-config", map[string]string{
		"team_name": teamName,
	}, &configResp); err != nil {
		return nil, fmt.Errorf("read team config: %w", err)
	}

	maxWorkers := configResp.Config.MaxWorkers
	if maxWorkers == 0 {
		maxWorkers = 20 // default max workers
	}

	currentCount := configResp.Config.WorkerCount
	if currentCount+count > maxWorkers {
		return nil, fmt.Errorf("cannot add %d workers: would exceed max_workers (%d + %d > %d)",
			count, currentCount, count, maxWorkers)
	}

	// For now, we return a placeholder result
	// Actual implementation would need to:
	// 1. Create new worker identities
	// 2. Launch worker processes
	// 3. Register workers with the team
	// This requires deeper integration with the team CLI tool

	return &ScaleUpResult{
		AddedWorkers:   make([]WorkerInfo, 0),
		NewWorkerCount: currentCount,
	}, fmt.Errorf("dynamic worker scaling requires team CLI support")
}

// ScaleDown removes idle workers from a running team
func (r *teamCLIRunner) ScaleDown(ctx context.Context, workspace, teamName string, count int) (*ScaleDownResult, error) {
	if count < 1 {
		return nil, fmt.Errorf("count must be positive, got %d", count)
	}

	// Get team summary to identify idle workers
	snapshot, err := r.fetchSnapshot(ctx, workspace, teamName)
	if err != nil {
		return nil, fmt.Errorf("fetch team snapshot: %w", err)
	}

	// Find idle workers
	var idleWorkers []string
	for _, worker := range snapshot.Summary.Workers {
		// Check if worker is idle (no active tasks, alive)
		if worker.Alive && worker.TurnsWithoutProgress > 0 {
			idleWorkers = append(idleWorkers, worker.Name)
			if len(idleWorkers) >= count {
				break
			}
		}
	}

	if len(idleWorkers) == 0 {
		return nil, fmt.Errorf("no idle workers available to scale down")
	}

	// For now, we return a placeholder result
	// Actual implementation would need to:
	// 1. Mark workers as "draining"
	// 2. Wait for workers to finish current tasks
	// 3. Shutdown worker processes
	// 4. Remove worker registrations
	// This requires deeper integration with the team CLI tool

	return &ScaleDownResult{
		RemovedWorkers: make([]string, 0),
		NewWorkerCount: snapshot.Summary.WorkerCount,
	}, fmt.Errorf("dynamic worker scaling requires team CLI support")
}

// GetIdleWorkers returns a list of idle workers
func (r *teamCLIRunner) GetIdleWorkers(ctx context.Context, workspace, teamName string) ([]string, error) {
	snapshot, err := r.fetchSnapshot(ctx, workspace, teamName)
	if err != nil {
		return nil, fmt.Errorf("fetch team snapshot: %w", err)
	}

	var idleWorkers []string
	for _, worker := range snapshot.Summary.Workers {
		if worker.Alive && worker.TurnsWithoutProgress > 0 {
			idleWorkers = append(idleWorkers, worker.Name)
		}
	}

	return idleWorkers, nil
}
