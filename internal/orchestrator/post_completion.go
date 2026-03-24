package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/junhoyeo/contrabass/internal/logging"
	"github.com/junhoyeo/contrabass/internal/types"
)

// postCompletionPushAndPR pushes the agent's commits and creates a PR.
// This is a best-effort safety net: if the agent already created a PR, this is a no-op.
func (o *Orchestrator) postCompletionPushAndPR(ctx context.Context, workspace string, issue types.Issue) {
	if workspace == "" {
		return
	}

	// Check if there are commits ahead of main
	diffOut, err := runGitCmd(ctx, workspace, "log", "main..HEAD", "--oneline")
	if err != nil || len(bytes.TrimSpace(diffOut)) == 0 {
		logging.LogIssueEvent(o.logger, issue.ID, "post_completion_no_commits")
		return
	}

	// Get current branch
	branchOut, err := runGitCmd(ctx, workspace, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		logging.LogIssueEvent(o.logger, issue.ID, "post_completion_branch_failed", "err", err)
		return
	}
	branch := strings.TrimSpace(string(branchOut))
	if branch == "" || branch == "HEAD" {
		branch = fmt.Sprintf("agent/issue-%s", issue.ID)
	}

	// Push branch to origin
	pushOut, err := runGitCmd(ctx, workspace, "push", "origin", "HEAD:refs/heads/"+branch)
	if err != nil {
		// If push fails because branch exists and is up-to-date, that's OK
		if strings.Contains(string(pushOut), "Everything up-to-date") {
			logging.LogIssueEvent(o.logger, issue.ID, "post_completion_already_pushed")
		} else {
			logging.LogIssueEvent(o.logger, issue.ID, "post_completion_push_failed", "err", err, "output", string(pushOut))
			return
		}
	} else {
		logging.LogIssueEvent(o.logger, issue.ID, "post_completion_pushed", "branch", branch)
	}

	// Check if PR already exists for this branch
	prCheckOut, err := runCmd(ctx, workspace, "gh", "pr", "list", "--head", branch, "--json", "number", "--jq", "length")
	if err == nil && strings.TrimSpace(string(prCheckOut)) != "0" {
		logging.LogIssueEvent(o.logger, issue.ID, "post_completion_pr_exists", "branch", branch)
		return
	}

	// Create PR
	title := issue.Title
	body := fmt.Sprintf("## Changes\nAgent-generated implementation for #%s\n\nCloses #%s\n\nAutomatically created by Contrabass post-completion hook.", issue.ID, issue.ID)

	prOut, err := runCmd(ctx, workspace, "gh", "pr", "create",
		"--base", "main",
		"--head", branch,
		"--title", title,
		"--body", body,
	)
	if err != nil {
		logging.LogIssueEvent(o.logger, issue.ID, "post_completion_pr_failed", "err", err, "output", string(prOut))
		return
	}

	prURL := strings.TrimSpace(string(prOut))
	logging.LogIssueEvent(o.logger, issue.ID, "post_completion_pr_created", "url", prURL)
}

func runGitCmd(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func runCmd(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}
