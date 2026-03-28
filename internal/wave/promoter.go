package wave

import (
	"context"

	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

const (
	labelAgentReady      = "agent-ready"
	labelAgentReadyHeavy = "agent-ready-heavy"
)

// Promoter manages agent-ready label assignment and model routing for waves.
type Promoter struct {
	tracker      tracker.Tracker
	modelRouting ModelRouting
}

// NewPromoter creates a new Promoter with the given tracker and model routing config.
func NewPromoter(t tracker.Tracker, routing ModelRouting) *Promoter {
	return &Promoter{
		tracker:      t,
		modelRouting: routing,
	}
}

// PromoteWave adds agent-ready or agent-ready-heavy labels to each issue in the wave.
// If the tracker does not implement LabelManager, the promotion is skipped silently.
// Returns the list of promoted issue IDs.
func (p *Promoter) PromoteWave(ctx context.Context, wave Wave, allIssues map[string]types.Issue) ([]string, error) {
	lm, ok := p.tracker.(tracker.LabelManager)
	if !ok {
		return nil, nil
	}

	promoted := make([]string, 0, len(wave.Issues))
	for _, id := range wave.Issues {
		issue, exists := allIssues[id]
		if !exists {
			continue
		}

		label := labelAgentReady
		if p.isHeavy(issue) {
			label = labelAgentReadyHeavy
		}

		if err := lm.AddLabel(ctx, id, label); err != nil {
			return promoted, err
		}
		promoted = append(promoted, id)
	}
	return promoted, nil
}

// DemoteIssue removes both agent-ready labels from the issue.
// If the tracker does not implement LabelManager, the operation is skipped silently.
func (p *Promoter) DemoteIssue(ctx context.Context, issueID string) error {
	lm, ok := p.tracker.(tracker.LabelManager)
	if !ok {
		return nil
	}

	if err := lm.RemoveLabel(ctx, issueID, labelAgentReady); err != nil {
		return err
	}
	return lm.RemoveLabel(ctx, issueID, labelAgentReadyHeavy)
}

// ResolveModel returns the model to use for the given issue based on routing rules.
// Falls back to DefaultModel if no rule matches.
func (p *Promoter) ResolveModel(issue types.Issue) string {
	for _, rule := range p.modelRouting.Rules {
		for _, ruleLabel := range rule.Labels {
			for _, issueLabel := range issue.Labels {
				if issueLabel == ruleLabel {
					return rule.Model
				}
			}
		}
	}
	return p.modelRouting.DefaultModel
}

// isHeavy reports whether any of the issue's labels match a routing rule with Heavy=true.
func (p *Promoter) isHeavy(issue types.Issue) bool {
	for _, rule := range p.modelRouting.Rules {
		if !rule.Heavy {
			continue
		}
		for _, ruleLabel := range rule.Labels {
			for _, issueLabel := range issue.Labels {
				if issueLabel == ruleLabel {
					return true
				}
			}
		}
	}
	return false
}
