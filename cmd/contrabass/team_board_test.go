package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

func TestDefaultTeamNameForIssue(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "issue-cb-12", defaultTeamNameForIssue("CB-12"))
	assert.Equal(t, "issue-issue-9-alpha", defaultTeamNameForIssue("Issue 9 / alpha"))
	assert.Equal(t, "issue-issue", defaultTeamNameForIssue("   "))
}

func TestResolveTeamNameForIssue(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "team-alpha", resolveTeamNameForIssue(tracker.LocalBoardIssue{
		ID:       "CB-1",
		Assignee: "Team Alpha",
	}, ""))
	assert.Equal(t, "ops", resolveTeamNameForIssue(tracker.LocalBoardIssue{
		ID: "CB-2",
	}, "Ops"))
	assert.Equal(t, "issue-cb-3", resolveTeamNameForIssue(tracker.LocalBoardIssue{
		ID: "CB-3",
	}, ""))
}

func TestBuildTeamTasksFromBoardIssue(t *testing.T) {
	t.Parallel()

	issue := tracker.LocalBoardIssue{
		ID:          "CB-12",
		Title:       "Ship autonomous board sync",
		Description: "Add automatic board status updates from team lifecycle events.",
		State:       tracker.LocalBoardStateTodo,
		Assignee:    "team-alpha",
		Labels:      []string{"tracker", "team"},
		URL:         "local://CB-12",
		BlockedBy:   []string{"CB-9"},
	}

	tasks := buildTeamTasksFromBoardIssue(issue)
	require.Len(t, tasks, 3)

	assert.Equal(t, "001-cb-12-plan", tasks[0].ID)
	assert.Equal(t, "002-cb-12-prd", tasks[1].ID)
	assert.Equal(t, "003-cb-12-exec", tasks[2].ID)
	assert.Empty(t, tasks[0].DependsOn)
	assert.Equal(t, []string{"001-cb-12-plan"}, tasks[1].DependsOn)
	assert.Equal(t, []string{"002-cb-12-prd"}, tasks[2].DependsOn)
	assert.Contains(t, tasks[2].Description, "Issue ID: CB-12")
	assert.Contains(t, tasks[2].Description, "Add automatic board status updates")
	assert.Contains(t, tasks[2].Description, "Assigned to: team-alpha")
	assert.Contains(t, tasks[2].Description, "Blocked by: CB-9")
}

func TestBoardIssueSyncerLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	localTracker := tracker.NewLocalTracker(tracker.LocalConfig{
		BoardDir:    filepath.Join(t.TempDir(), "board"),
		IssuePrefix: "CB",
		Actor:       "team:issue-cb-1",
	})

	_, err := localTracker.InitBoard(ctx)
	require.NoError(t, err)

	issue, err := localTracker.CreateIssue(ctx, "Ship board sync", "Wire team events back into the board", []string{"team"})
	require.NoError(t, err)

	syncer := newBoardIssueSyncer(localTracker, issue.ID, "issue-cb-1")
	require.NoError(t, syncer.Start(ctx))

	syncer.HandleEvent(ctx, types.TeamEvent{
		Type:      "phase_started",
		TeamName:  "issue-cb-1",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"phase": string(types.PhaseExec),
		},
	})
	syncer.HandleEvent(ctx, types.TeamEvent{
		Type:      "task_claimed",
		TeamName:  "issue-cb-1",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"worker_id": "worker-1",
			"task_id":   "003-cb-1-exec",
			"task":      "Implement CB-1",
		},
	})
	syncer.HandleEvent(ctx, types.TeamEvent{
		Type:      "pipeline_completed",
		TeamName:  "issue-cb-1",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"phase": string(types.PhaseComplete),
		},
	})

	updated, err := localTracker.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, tracker.LocalBoardStateDone, updated.State)
	assert.Empty(t, updated.ClaimedBy)
	assert.Equal(t, "issue-cb-1", updated.TrackerMeta["team_name"])
	assert.Equal(t, "complete", updated.TrackerMeta["team_status"])
	assert.Equal(t, string(types.PhaseExec), updated.TrackerMeta["team_phase"])
	assert.Equal(t, "worker-1", updated.TrackerMeta["last_worker_id"])

	comments, err := localTracker.ListComments(ctx, issue.ID)
	require.NoError(t, err)
	require.NotEmpty(t, comments)

	var bodies []string
	for _, comment := range comments {
		bodies = append(bodies, comment.Body)
	}
	assert.Contains(t, strings.Join(bodies, "\n"), "team issue-cb-1 started execution")
	assert.Contains(t, strings.Join(bodies, "\n"), "entered phase team-exec")
	assert.Contains(t, strings.Join(bodies, "\n"), "completed with phase complete")
}

func TestBoardIssueSyncerFinalizeErrorMarksRetry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	localTracker := tracker.NewLocalTracker(tracker.LocalConfig{
		BoardDir:    filepath.Join(t.TempDir(), "board"),
		IssuePrefix: "CB",
		Actor:       "team:issue-cb-2",
	})

	_, err := localTracker.InitBoard(ctx)
	require.NoError(t, err)

	issue, err := localTracker.CreateIssue(ctx, "Retry me", "This run should be marked for retry", nil)
	require.NoError(t, err)

	syncer := newBoardIssueSyncer(localTracker, issue.ID, "issue-cb-2")
	require.NoError(t, syncer.Start(ctx))

	syncer.Finalize(ctx, errors.New("boom"))

	updated, err := localTracker.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, tracker.LocalBoardStateRetry, updated.State)
	assert.Equal(t, "retry", updated.TrackerMeta["team_status"])

	comments, err := localTracker.ListComments(ctx, issue.ID)
	require.NoError(t, err)
	require.NotEmpty(t, comments)
	assert.Contains(t, comments[len(comments)-1].Body, "ended with error: boom")
}
