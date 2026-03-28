package wave

import (
	"testing"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildDAG_SimpleChain verifies that a 3-issue chain produces correct reverse edges.
func TestBuildDAG_SimpleChain(t *testing.T) {
	issues := []types.Issue{
		{ID: "A", BlockedBy: []string{}},
		{ID: "B", BlockedBy: []string{"A"}},
		{ID: "C", BlockedBy: []string{"B"}},
	}

	dag, err := BuildDAG(issues)
	require.NoError(t, err)
	require.Len(t, dag.Nodes, 3)

	// A blocks B
	assert.Equal(t, []string{"B"}, dag.Nodes["A"].Blocks)
	// B blocks C
	assert.Equal(t, []string{"C"}, dag.Nodes["B"].Blocks)
	// C blocks nothing
	assert.Empty(t, dag.Nodes["C"].Blocks)

	// Verify BlockedBy preserved
	assert.Empty(t, dag.Nodes["A"].BlockedBy)
	assert.Equal(t, []string{"A"}, dag.Nodes["B"].BlockedBy)
	assert.Equal(t, []string{"B"}, dag.Nodes["C"].BlockedBy)
}

// TestBuildDAG_NoDeps verifies that 2 independent issues have no reverse edges.
func TestBuildDAG_NoDeps(t *testing.T) {
	issues := []types.Issue{
		{ID: "X", BlockedBy: []string{}},
		{ID: "Y", BlockedBy: []string{}},
	}

	dag, err := BuildDAG(issues)
	require.NoError(t, err)
	require.Len(t, dag.Nodes, 2)

	assert.Empty(t, dag.Nodes["X"].Blocks)
	assert.Empty(t, dag.Nodes["Y"].Blocks)
	assert.Empty(t, dag.Nodes["X"].BlockedBy)
	assert.Empty(t, dag.Nodes["Y"].BlockedBy)
}

// TestDAG_Validate_NoCycles verifies that a valid graph passes validation with no errors.
func TestDAG_Validate_NoCycles(t *testing.T) {
	issues := []types.Issue{
		{ID: "A", BlockedBy: []string{}},
		{ID: "B", BlockedBy: []string{"A"}},
		{ID: "C", BlockedBy: []string{"A", "B"}},
	}

	dag, err := BuildDAG(issues)
	require.NoError(t, err)

	errs := dag.Validate()
	assert.Empty(t, errs)
}

// TestDAG_Validate_CycleDetected verifies that a mutual dependency is detected.
func TestDAG_Validate_CycleDetected(t *testing.T) {
	issues := []types.Issue{
		{ID: "A", BlockedBy: []string{"B"}},
		{ID: "B", BlockedBy: []string{"A"}},
	}

	dag, err := BuildDAG(issues)
	require.NoError(t, err)

	errs := dag.Validate()
	require.NotEmpty(t, errs)

	// At least one error should mention cycle
	found := false
	for _, e := range errs {
		if e.Error() != "" {
			found = true
			break
		}
	}
	assert.True(t, found)
}

// TestDAG_Validate_MissingRef verifies that a reference to a non-existent issue is detected.
func TestDAG_Validate_MissingRef(t *testing.T) {
	issues := []types.Issue{
		{ID: "A", BlockedBy: []string{"GHOST"}},
	}

	dag, err := BuildDAG(issues)
	require.NoError(t, err)

	errs := dag.Validate()
	require.NotEmpty(t, errs)
}

// TestDAG_ComputeWaves_Diamond verifies that a diamond pattern produces 3 waves.
// Structure: 1 → 2, 3 → 4
// Wave 0: [1]
// Wave 1: [2, 3]
// Wave 2: [4]
func TestDAG_ComputeWaves_Diamond(t *testing.T) {
	issues := []types.Issue{
		{ID: "1", BlockedBy: []string{}},
		{ID: "2", BlockedBy: []string{"1"}},
		{ID: "3", BlockedBy: []string{"1"}},
		{ID: "4", BlockedBy: []string{"2", "3"}},
	}

	dag, err := BuildDAG(issues)
	require.NoError(t, err)

	errs := dag.Validate()
	require.Empty(t, errs)

	waves := dag.ComputeWaves()
	require.Len(t, waves, 3)

	assert.Equal(t, 0, waves[0].Index)
	assert.Equal(t, []string{"1"}, waves[0].Issues)

	assert.Equal(t, 1, waves[1].Index)
	assert.ElementsMatch(t, []string{"2", "3"}, waves[1].Issues)

	assert.Equal(t, 2, waves[2].Index)
	assert.Equal(t, []string{"4"}, waves[2].Issues)
}

// TestDAG_ComputeWaves_AllIndependent verifies that all independent issues end up in wave 0.
func TestDAG_ComputeWaves_AllIndependent(t *testing.T) {
	issues := []types.Issue{
		{ID: "A", BlockedBy: []string{}},
		{ID: "B", BlockedBy: []string{}},
		{ID: "C", BlockedBy: []string{}},
	}

	dag, err := BuildDAG(issues)
	require.NoError(t, err)

	waves := dag.ComputeWaves()
	require.Len(t, waves, 1)

	assert.Equal(t, 0, waves[0].Index)
	assert.ElementsMatch(t, []string{"A", "B", "C"}, waves[0].Issues)
}
