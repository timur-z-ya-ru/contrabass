package tracker

import (
	"context"

	"github.com/junhoyeo/contrabass/internal/types"
)

// Tracker defines the interface for interacting with an issue tracker.
type Tracker interface {
	// FetchIssues retrieves candidate issues from the tracker.
	FetchIssues(ctx context.Context) ([]types.Issue, error)

	// ClaimIssue marks an issue as claimed in the tracker.
	ClaimIssue(ctx context.Context, issueID string) error

	// ReleaseIssue removes the claim on an issue in the tracker.
	ReleaseIssue(ctx context.Context, issueID string) error

	// UpdateIssueState updates the state of an issue in the tracker.
	UpdateIssueState(ctx context.Context, issueID string, state types.IssueState) error

	// PostComment posts a comment on an issue in the tracker.
	PostComment(ctx context.Context, issueID string, body string) error
}

// LabelManager is an optional interface for trackers that support label management.
type LabelManager interface {
	AddLabel(ctx context.Context, issueID string, label string) error
	RemoveLabel(ctx context.Context, issueID string, label string) error
}

// PRVerifier is an optional interface for trackers that can verify merged PRs.
type PRVerifier interface {
	HasMergedPR(ctx context.Context, issueID string) (bool, error)
}
