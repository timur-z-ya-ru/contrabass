package multiproject

import "sync"

// ProjectGuard enforces cross-project concurrency limits.
// It tracks which projects currently have running agents and ensures
// no more than MaxActiveProjects have active runs simultaneously.
type ProjectGuard struct {
	mu                sync.Mutex
	activeProjects    map[string]int // projectID → running agent count
	maxActiveProjects int
}

// NewProjectGuard creates a guard with the given project limit.
func NewProjectGuard(maxActiveProjects int) *ProjectGuard {
	return &ProjectGuard{
		activeProjects:    make(map[string]int),
		maxActiveProjects: maxActiveProjects,
	}
}

// CanDispatch is the DispatchGuard callback for an Orchestrator.
// It allows dispatch if:
//   - The project already has running agents (already occupies a slot)
//   - There are free project slots available
func (g *ProjectGuard) CanDispatch(projectID string, currentRunning int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if currentRunning > 0 {
		// Project already active — update count and allow.
		g.activeProjects[projectID] = currentRunning
		return true
	}

	// Project wants to start its first agent.
	// Count how many distinct projects currently have running agents.
	activeCount := 0
	for pid, count := range g.activeProjects {
		if pid == projectID {
			continue
		}
		if count > 0 {
			activeCount++
		}
	}

	if activeCount >= g.maxActiveProjects {
		return false
	}

	// Reserve the slot.
	g.activeProjects[projectID] = 1
	return true
}

// Update refreshes the running count for a project.
// Called periodically by each orchestrator.
func (g *ProjectGuard) Update(projectID string, running int) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if running == 0 {
		delete(g.activeProjects, projectID)
	} else {
		g.activeProjects[projectID] = running
	}
}

// ActiveProjects returns the number of projects with running agents.
func (g *ProjectGuard) ActiveProjects() int {
	g.mu.Lock()
	defer g.mu.Unlock()

	count := 0
	for _, running := range g.activeProjects {
		if running > 0 {
			count++
		}
	}
	return count
}

// Snapshot returns a copy of active project states.
func (g *ProjectGuard) Snapshot() map[string]int {
	g.mu.Lock()
	defer g.mu.Unlock()

	snap := make(map[string]int, len(g.activeProjects))
	for k, v := range g.activeProjects {
		snap[k] = v
	}
	return snap
}
