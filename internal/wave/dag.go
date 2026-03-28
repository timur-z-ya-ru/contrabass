package wave

import (
	"fmt"

	"github.com/junhoyeo/contrabass/internal/types"
)

// Node represents an issue in the dependency DAG.
type Node struct {
	IssueID        string
	BlockedBy      []string
	Blocks         []string // reverse edges, computed
	Labels         []string
	State          types.IssueState
	TotalTokensIn  int64
	TotalTokensOut int64
	Attempts       int
}

// DAG is a directed acyclic graph of issue dependencies.
type DAG struct {
	Nodes map[string]*Node
}

// Wave represents a group of issues that can execute in parallel.
type Wave struct {
	Index       int
	Issues      []string
	Description string
}

// BuildDAG constructs a DAG from the given issues and computes reverse edges (Blocks).
func BuildDAG(issues []types.Issue) (*DAG, error) {
	dag := &DAG{
		Nodes: make(map[string]*Node, len(issues)),
	}

	// First pass: create all nodes
	for _, issue := range issues {
		dag.Nodes[issue.ID] = &Node{
			IssueID:   issue.ID,
			BlockedBy: append([]string(nil), issue.BlockedBy...),
			Blocks:    []string{},
			Labels:    append([]string(nil), issue.Labels...),
			State:     issue.State,
		}
	}

	// Second pass: compute reverse edges (Blocks)
	for _, issue := range issues {
		for _, depID := range issue.BlockedBy {
			if dep, ok := dag.Nodes[depID]; ok {
				dep.Blocks = append(dep.Blocks, issue.ID)
			}
			// Missing references are reported in Validate(), not here
		}
	}

	return dag, nil
}

// Validate checks for cycles (via Kahn's algorithm) and missing references.
// Returns a slice of errors; empty slice means the graph is valid.
func (d *DAG) Validate() []error {
	var errs []error

	// Check for missing references
	for _, node := range d.Nodes {
		for _, depID := range node.BlockedBy {
			if _, ok := d.Nodes[depID]; !ok {
				errs = append(errs, fmt.Errorf("issue %q references unknown dependency %q", node.IssueID, depID))
			}
		}
	}

	// Kahn's algorithm to detect cycles
	// Build in-degree map
	inDegree := make(map[string]int, len(d.Nodes))
	for id := range d.Nodes {
		inDegree[id] = 0
	}
	for _, node := range d.Nodes {
		for _, depID := range node.BlockedBy {
			if _, ok := d.Nodes[depID]; ok {
				inDegree[node.IssueID]++
			}
		}
	}

	// Enqueue all nodes with zero in-degree
	queue := make([]string, 0, len(d.Nodes))
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++

		for _, nextID := range d.Nodes[cur].Blocks {
			inDegree[nextID]--
			if inDegree[nextID] == 0 {
				queue = append(queue, nextID)
			}
		}
	}

	if visited != len(d.Nodes) {
		errs = append(errs, fmt.Errorf("cycle detected: %d of %d nodes reachable in topological order", visited, len(d.Nodes)))
	}

	return errs
}

// ComputeWaves performs a topological sort and groups issues into waves by level.
// Wave 0 contains issues with no dependencies; Wave N contains issues whose
// dependencies all appear in waves 0..N-1.
func (d *DAG) ComputeWaves() []Wave {
	// Build in-degree map (counting only existing deps)
	inDegree := make(map[string]int, len(d.Nodes))
	for id := range d.Nodes {
		inDegree[id] = 0
	}
	for _, node := range d.Nodes {
		for _, depID := range node.BlockedBy {
			if _, ok := d.Nodes[depID]; ok {
				inDegree[node.IssueID]++
			}
		}
	}

	// BFS level by level
	var waves []Wave
	waveIndex := 0

	// Seed with zero in-degree nodes
	current := make([]string, 0)
	for id, deg := range inDegree {
		if deg == 0 {
			current = append(current, id)
		}
	}

	for len(current) > 0 {
		// Sort for determinism
		sortStrings(current)

		waves = append(waves, Wave{
			Index:  waveIndex,
			Issues: current,
		})

		next := make([]string, 0)
		for _, id := range current {
			for _, nextID := range d.Nodes[id].Blocks {
				inDegree[nextID]--
				if inDegree[nextID] == 0 {
					next = append(next, nextID)
				}
			}
		}

		current = next
		waveIndex++
	}

	return waves
}

// sortStrings sorts a string slice in place (insertion sort for small slices).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		key := s[i]
		j := i - 1
		for j >= 0 && s[j] > key {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = key
	}
}
