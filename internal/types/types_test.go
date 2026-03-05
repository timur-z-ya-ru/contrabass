package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIssueStateString(t *testing.T) {
	tests := []struct {
		state    IssueState
		expected string
	}{
		{Unclaimed, "Unclaimed"},
		{Claimed, "Claimed"},
		{Running, "Running"},
		{RetryQueued, "RetryQueued"},
		{Released, "Released"},
		{IssueState(999), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}

func TestRunPhaseString(t *testing.T) {
	tests := []struct {
		phase    RunPhase
		expected string
	}{
		{PreparingWorkspace, "PreparingWorkspace"},
		{BuildingPrompt, "BuildingPrompt"},
		{LaunchingAgentProcess, "LaunchingAgentProcess"},
		{InitializingSession, "InitializingSession"},
		{StreamingTurn, "StreamingTurn"},
		{Finishing, "Finishing"},
		{Succeeded, "Succeeded"},
		{Failed, "Failed"},
		{TimedOut, "TimedOut"},
		{Stalled, "Stalled"},
		{CanceledByReconciliation, "CanceledByReconciliation"},
		{RunPhase(999), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.phase.String())
		})
	}
}

func TestIssueStateEnumContiguity(t *testing.T) {
	// Verify that IssueState enum values are contiguous (no gaps)
	expected := []IssueState{Unclaimed, Claimed, Running, RetryQueued, Released}
	for i, state := range expected {
		assert.Equal(t, IssueState(i), state, "IssueState enum values should be contiguous")
	}
}

func TestRunPhaseEnumContiguity(t *testing.T) {
	// Verify that RunPhase enum values are contiguous (no gaps)
	expected := []RunPhase{
		PreparingWorkspace,
		BuildingPrompt,
		LaunchingAgentProcess,
		InitializingSession,
		StreamingTurn,
		Finishing,
		Succeeded,
		Failed,
		TimedOut,
		Stalled,
		CanceledByReconciliation,
	}
	for i, phase := range expected {
		assert.Equal(t, RunPhase(i), phase, "RunPhase enum values should be contiguous")
	}
}

func TestIssueStateCount(t *testing.T) {
	// Verify that IssueState has exactly 5 values
	assert.Equal(t, 5, int(Released)+1, "IssueState should have exactly 5 values")
}

func TestRunPhaseCount(t *testing.T) {
	// Verify that RunPhase has exactly 11 values
	assert.Equal(t, 11, int(CanceledByReconciliation)+1, "RunPhase should have exactly 11 values")
}
