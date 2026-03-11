package team

import (
	"context"
	"errors"
	"testing"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaxConcurrentTasksRuleCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rule      MaxConcurrentTasksRule
		decision  Decision
		wantError bool
	}{
		{
			name:      "non-claim decision ignored",
			rule:      MaxConcurrentTasksRule{MaxTasksPerWorker: 1},
			decision:  Decision{Type: DecisionTransition},
			wantError: false,
		},
		{
			name:      "disabled max allows",
			rule:      MaxConcurrentTasksRule{MaxTasksPerWorker: 0},
			decision:  Decision{Type: DecisionClaim, WorkerID: "worker-1", WorkerActiveTasks: 100},
			wantError: false,
		},
		{
			name:      "below max allows",
			rule:      MaxConcurrentTasksRule{MaxTasksPerWorker: 2},
			decision:  Decision{Type: DecisionClaim, WorkerID: "worker-1", WorkerActiveTasks: 1},
			wantError: false,
		},
		{
			name:      "at max denies",
			rule:      MaxConcurrentTasksRule{MaxTasksPerWorker: 2},
			decision:  Decision{Type: DecisionClaim, WorkerID: "worker-1", WorkerActiveTasks: 2},
			wantError: true,
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.rule.Check(context.Background(), tc.decision)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestPhaseGateRuleCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		decision  Decision
		wantError bool
	}{
		{
			name:      "non-transition decision ignored",
			decision:  Decision{Type: DecisionClaim},
			wantError: false,
		},
		{
			name:      "terminal tasks allow transition",
			decision:  Decision{Type: DecisionTransition, Phase: types.PhaseExec, CurrentPhaseTasksTerminal: true},
			wantError: false,
		},
		{
			name:      "non-terminal tasks deny transition",
			decision:  Decision{Type: DecisionTransition, Phase: types.PhaseExec, CurrentPhaseTasksTerminal: false},
			wantError: true,
		},
	}

	rule := PhaseGateRule{}
	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := rule.Check(context.Background(), tc.decision)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestWorkerCapacityRuleCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rule      WorkerCapacityRule
		decision  Decision
		wantError bool
	}{
		{
			name:      "non-claim decision ignored",
			rule:      WorkerCapacityRule{MaxWorkers: 2},
			decision:  Decision{Type: DecisionTransition, ActiveWorkerCount: 5},
			wantError: false,
		},
		{
			name:      "disabled max allows",
			rule:      WorkerCapacityRule{MaxWorkers: 0},
			decision:  Decision{Type: DecisionClaim, ActiveWorkerCount: 10},
			wantError: false,
		},
		{
			name:      "within max allows",
			rule:      WorkerCapacityRule{MaxWorkers: 2},
			decision:  Decision{Type: DecisionClaim, ActiveWorkerCount: 2},
			wantError: false,
		},
		{
			name:      "above max denies",
			rule:      WorkerCapacityRule{MaxWorkers: 2},
			decision:  Decision{Type: DecisionClaim, ActiveWorkerCount: 3},
			wantError: true,
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.rule.Check(context.Background(), tc.decision)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

type testRule struct {
	name string
	err  error
}

func (r testRule) Name() string {
	return r.name
}

func (r testRule) Check(_ context.Context, _ Decision) error {
	return r.err
}

func TestGovernancePolicyCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		ctx       context.Context
		policy    *GovernancePolicy
		decision  Decision
		assertErr func(t *testing.T, err error)
	}{
		{
			name: "all rules pass",
			ctx:  context.Background(),
			policy: NewGovernancePolicy(
				MaxConcurrentTasksRule{MaxTasksPerWorker: 2},
				WorkerCapacityRule{MaxWorkers: 3},
			),
			decision: Decision{Type: DecisionClaim, WorkerID: "worker-1", TeamName: "team-a", WorkerActiveTasks: 1, ActiveWorkerCount: 2},
			assertErr: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "rule violation wrapped",
			ctx:  context.Background(),
			policy: NewGovernancePolicy(
				MaxConcurrentTasksRule{MaxTasksPerWorker: 1},
			),
			decision: Decision{Type: DecisionClaim, WorkerID: "worker-1", TeamName: "team-a", WorkerActiveTasks: 1},
			assertErr: func(t *testing.T, err error) {
				require.Error(t, err)
				var violation *PolicyViolation
				require.ErrorAs(t, err, &violation)
				assert.Equal(t, "max_concurrent_tasks", violation.Rule)
				assert.Equal(t, DecisionClaim, violation.Decision.Type)
				assert.Equal(t, "team-a", violation.Decision.TeamName)
			},
		},
		{
			name: "existing policy violation gets rule populated",
			ctx:  context.Background(),
			policy: NewGovernancePolicy(
				testRule{name: "custom_rule", err: &PolicyViolation{Err: errors.New("custom failure")}},
			),
			decision: Decision{Type: DecisionAssign, TeamName: "team-a"},
			assertErr: func(t *testing.T, err error) {
				require.Error(t, err)
				var violation *PolicyViolation
				require.ErrorAs(t, err, &violation)
				assert.Equal(t, "custom_rule", violation.Rule)
				assert.Equal(t, DecisionAssign, violation.Decision.Type)
			},
		},
		{
			name: "context cancellation propagated",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			}(),
			policy: NewGovernancePolicy(testRule{name: "any", err: nil}),
			decision: Decision{
				Type: DecisionClaim,
			},
			assertErr: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, context.Canceled)
			},
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.policy.Check(tc.ctx, tc.decision)
			tc.assertErr(t, err)
		})
	}
}
