package main

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

var teamNamePattern = regexp.MustCompile(`[^a-z0-9]+`)

type teamEventHandler func(context.Context, types.TeamEvent)

func consumeTeamEvents(
	ctx context.Context,
	logger *slog.Logger,
	events <-chan types.TeamEvent,
	handlers ...teamEventHandler,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}

			logger.Info("team event",
				"type", event.Type,
				"team", event.TeamName,
				"data", event.Data,
			)
			for _, handler := range handlers {
				if handler != nil {
					handler(ctx, event)
				}
			}
		}
	}
}

func defaultTeamNameForIssue(issueID string) string {
	normalized := sanitizeTeamName(issueID)
	if normalized == "" {
		normalized = "issue"
	}
	return "issue-" + normalized
}

func sanitizeTeamName(raw string) string {
	normalized := teamNamePattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(raw)), "-")
	return strings.Trim(normalized, "-")
}

func resolveTeamNameForIssue(issue tracker.LocalBoardIssue, override string) string {
	if normalized := sanitizeTeamName(override); normalized != "" {
		return normalized
	}
	if normalized := sanitizeTeamName(issue.Assignee); normalized != "" {
		return normalized
	}
	return defaultTeamNameForIssue(issue.ID)
}

func buildTeamTasksFromBoardIssue(issue tracker.LocalBoardIssue) []types.TeamTask {
	issueContext := formatBoardIssueContext(issue)
	baseID := sanitizeTaskIDPart(issue.ID)
	if baseID == "" {
		baseID = "board-issue"
	}

	return []types.TeamTask{
		{
			ID:          "001-" + baseID + "-plan",
			Subject:     fmt.Sprintf("Plan %s %s", issue.ID, issue.Title),
			Description: "Analyze the internal board issue and produce an implementation plan.\n\n" + issueContext,
		},
		{
			ID:          "002-" + baseID + "-prd",
			Subject:     fmt.Sprintf("Refine execution strategy for %s", issue.ID),
			Description: "Turn the internal board issue into an executable implementation strategy with concrete files, risks, and validation steps.\n\n" + issueContext,
			DependsOn:   []string{"001-" + baseID + "-plan"},
		},
		{
			ID:          "003-" + baseID + "-exec",
			Subject:     fmt.Sprintf("Implement %s %s", issue.ID, issue.Title),
			Description: "Implement the internal board issue end to end, including tests and any required follow-up notes.\n\n" + issueContext,
			DependsOn:   []string{"002-" + baseID + "-prd"},
		},
	}
}

func formatBoardIssueContext(issue tracker.LocalBoardIssue) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Issue ID: %s\n", issue.ID))
	b.WriteString(fmt.Sprintf("Title: %s\n", issue.Title))
	if issue.URL != "" {
		b.WriteString(fmt.Sprintf("URL: %s\n", issue.URL))
	}
	if len(issue.Labels) > 0 {
		b.WriteString(fmt.Sprintf("Labels: %s\n", strings.Join(issue.Labels, ", ")))
	}
	b.WriteString(fmt.Sprintf("Current board state: %s\n", issue.State))
	if issue.Assignee != "" {
		b.WriteString(fmt.Sprintf("Assigned to: %s\n", issue.Assignee))
	}
	if issue.ParentID != "" {
		b.WriteString(fmt.Sprintf("Parent issue: %s\n", issue.ParentID))
	}
	if len(issue.ChildIDs) > 0 {
		b.WriteString(fmt.Sprintf("Child issues: %s\n", strings.Join(issue.ChildIDs, ", ")))
	}
	if len(issue.BlockedBy) > 0 {
		b.WriteString(fmt.Sprintf("Blocked by: %s\n", strings.Join(issue.BlockedBy, ", ")))
	}
	if issue.ClaimedBy != "" {
		b.WriteString(fmt.Sprintf("Claimed by: %s\n", issue.ClaimedBy))
	}
	if strings.TrimSpace(issue.Description) != "" {
		b.WriteString("\nDescription:\n")
		b.WriteString(strings.TrimSpace(issue.Description))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func sanitizeTaskIDPart(raw string) string {
	normalized := teamNamePattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(raw)), "-")
	return strings.Trim(normalized, "-")
}

type boardIssueSyncer struct {
	tracker  *tracker.LocalTracker
	issueID  string
	teamName string
	mu       sync.Mutex
	terminal bool
}

func newBoardIssueSyncer(localTracker *tracker.LocalTracker, issueID, teamName string) *boardIssueSyncer {
	return &boardIssueSyncer{
		tracker:  localTracker,
		issueID:  issueID,
		teamName: teamName,
	}
}

func (s *boardIssueSyncer) Start(ctx context.Context) error {
	if s == nil {
		return nil
	}

	if _, err := s.tracker.UpdateIssue(ctx, s.issueID, func(issue *tracker.LocalBoardIssue) error {
		if issue.TrackerMeta == nil {
			issue.TrackerMeta = map[string]interface{}{}
		}
		if strings.TrimSpace(issue.Assignee) == "" {
			issue.Assignee = s.teamName
		}
		issue.ClaimedBy = "team:" + s.teamName
		issue.TrackerMeta["team_name"] = s.teamName
		issue.TrackerMeta["team_status"] = "running"
		issue.TrackerMeta["last_team_event"] = "pipeline_started"
		issue.TrackerMeta["last_team_event_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		return nil
	}); err != nil {
		return err
	}

	if err := s.tracker.UpdateIssueState(ctx, s.issueID, types.Claimed); err != nil {
		return err
	}

	return s.tracker.PostComment(ctx, s.issueID, fmt.Sprintf("team %s started execution", s.teamName))
}

func (s *boardIssueSyncer) HandleEvent(ctx context.Context, event types.TeamEvent) {
	if s == nil {
		return
	}

	_, _ = s.tracker.UpdateIssue(ctx, s.issueID, func(issue *tracker.LocalBoardIssue) error {
		if issue.TrackerMeta == nil {
			issue.TrackerMeta = map[string]interface{}{}
		}
		issue.TrackerMeta["team_name"] = s.teamName
		issue.TrackerMeta["last_team_event"] = event.Type
		issue.TrackerMeta["last_team_event_at"] = event.Timestamp.UTC().Format(time.RFC3339Nano)

		switch event.Type {
		case "phase_started":
			issue.TrackerMeta["team_phase"] = stringFromMap(event.Data, "phase")
		case "task_claimed", "task_completed", "task_failed":
			issue.TrackerMeta["last_worker_id"] = stringFromMap(event.Data, "worker_id")
			issue.TrackerMeta["last_task_id"] = stringFromMap(event.Data, "task_id")
		case "pipeline_completed":
			issue.TrackerMeta["team_status"] = stringFromMap(event.Data, "phase")
		}
		return nil
	})

	if comment := formatBoardSyncComment(s.teamName, event); comment != "" {
		_ = s.tracker.PostComment(ctx, s.issueID, comment)
	}

	if event.Type != "pipeline_completed" {
		return
	}

	phase := stringFromMap(event.Data, "phase")
	if phase == string(types.PhaseComplete) {
		_ = s.tracker.UpdateIssueState(ctx, s.issueID, types.Released)
	} else {
		_ = s.tracker.UpdateIssueState(ctx, s.issueID, types.RetryQueued)
	}

	s.mu.Lock()
	s.terminal = true
	s.mu.Unlock()
}

func (s *boardIssueSyncer) Finalize(ctx context.Context, runErr error) {
	if s == nil {
		return
	}

	s.mu.Lock()
	terminal := s.terminal
	s.mu.Unlock()
	if terminal {
		return
	}

	if runErr == nil {
		_ = s.tracker.UpdateIssueState(ctx, s.issueID, types.Released)
		_, _ = s.tracker.UpdateIssue(ctx, s.issueID, func(issue *tracker.LocalBoardIssue) error {
			if issue.TrackerMeta == nil {
				issue.TrackerMeta = map[string]interface{}{}
			}
			issue.TrackerMeta["team_name"] = s.teamName
			issue.TrackerMeta["team_status"] = "complete"
			issue.TrackerMeta["last_team_event"] = "pipeline_completed"
			issue.TrackerMeta["last_team_event_at"] = time.Now().UTC().Format(time.RFC3339Nano)
			return nil
		})
		_ = s.tracker.PostComment(ctx, s.issueID, fmt.Sprintf("team %s completed successfully", s.teamName))
		return
	}

	_ = s.tracker.UpdateIssueState(ctx, s.issueID, types.RetryQueued)
	_, _ = s.tracker.UpdateIssue(ctx, s.issueID, func(issue *tracker.LocalBoardIssue) error {
		if issue.TrackerMeta == nil {
			issue.TrackerMeta = map[string]interface{}{}
		}
		issue.TrackerMeta["team_name"] = s.teamName
		issue.TrackerMeta["team_status"] = "retry"
		issue.TrackerMeta["last_team_event"] = "run_error"
		issue.TrackerMeta["last_team_event_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		return nil
	})
	_ = s.tracker.PostComment(ctx, s.issueID, fmt.Sprintf("team %s ended with error: %v", s.teamName, runErr))
}

func formatBoardSyncComment(teamName string, event types.TeamEvent) string {
	switch event.Type {
	case "phase_started":
		phase := stringFromMap(event.Data, "phase")
		if phase == "" {
			return ""
		}
		return fmt.Sprintf("team %s entered phase %s", teamName, phase)
	case "task_claimed":
		workerID := stringFromMap(event.Data, "worker_id")
		taskID := stringFromMap(event.Data, "task_id")
		subject := stringFromMap(event.Data, "task")
		return fmt.Sprintf("team %s: %s claimed %s %s", teamName, workerID, taskID, subject)
	case "task_completed":
		return fmt.Sprintf(
			"team %s: %s completed %s",
			teamName,
			stringFromMap(event.Data, "worker_id"),
			stringFromMap(event.Data, "task_id"),
		)
	case "task_failed":
		return fmt.Sprintf(
			"team %s: %s failed %s (%s)",
			teamName,
			stringFromMap(event.Data, "worker_id"),
			stringFromMap(event.Data, "task_id"),
			stringFromMap(event.Data, "error"),
		)
	case "pipeline_completed":
		return fmt.Sprintf("team %s completed with phase %s", teamName, stringFromMap(event.Data, "phase"))
	default:
		return ""
	}
}

func stringFromMap(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	raw, ok := values[key]
	if !ok {
		return ""
	}
	if text, ok := raw.(string); ok {
		return text
	}
	return fmt.Sprint(raw)
}
