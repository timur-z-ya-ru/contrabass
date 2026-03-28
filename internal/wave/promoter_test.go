package wave

import (
	"context"
	"testing"

	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

func testRouting() ModelRouting {
	return ModelRouting{
		DefaultModel: "sonnet",
		DefaultLabel: "agent-ready",
		HeavyLabel:   "agent-ready-heavy",
		Rules: []RoutingRule{
			{Labels: []string{"frontend"}, Model: "opus", Heavy: true},
			{Labels: []string{"docs"}, Model: "haiku", Heavy: false},
		},
	}
}

func TestPromoter_PromoteWave_DefaultLabels(t *testing.T) {
	mock := tracker.NewMockTracker()
	p := NewPromoter(mock, testRouting())

	wave := Wave{Index: 0, Issues: []string{"issue-1", "issue-2"}}
	allIssues := map[string]types.Issue{
		"issue-1": {ID: "issue-1", Labels: []string{"backend"}},
		"issue-2": {ID: "issue-2", Labels: []string{"backend"}},
	}

	promoted, err := p.PromoteWave(context.Background(), wave, allIssues)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(promoted) != 2 {
		t.Fatalf("expected 2 promoted, got %d", len(promoted))
	}

	for _, id := range []string{"issue-1", "issue-2"} {
		labels := mock.Labels[id]
		if len(labels) != 1 || labels[0] != labelAgentReady {
			t.Errorf("issue %s: expected label %q, got %v", id, labelAgentReady, labels)
		}
	}
}

func TestPromoter_PromoteWave_HeavyLabels(t *testing.T) {
	mock := tracker.NewMockTracker()
	p := NewPromoter(mock, testRouting())

	wave := Wave{Index: 0, Issues: []string{"fe-1", "be-1"}}
	allIssues := map[string]types.Issue{
		"fe-1": {ID: "fe-1", Labels: []string{"frontend"}},
		"be-1": {ID: "be-1", Labels: []string{"backend"}},
	}

	promoted, err := p.PromoteWave(context.Background(), wave, allIssues)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(promoted) != 2 {
		t.Fatalf("expected 2 promoted, got %d", len(promoted))
	}

	feLabels := mock.Labels["fe-1"]
	if len(feLabels) != 1 || feLabels[0] != labelAgentReadyHeavy {
		t.Errorf("fe-1: expected label %q, got %v", labelAgentReadyHeavy, feLabels)
	}

	beLabels := mock.Labels["be-1"]
	if len(beLabels) != 1 || beLabels[0] != labelAgentReady {
		t.Errorf("be-1: expected label %q, got %v", labelAgentReady, beLabels)
	}
}

func TestPromoter_ResolveModel(t *testing.T) {
	p := NewPromoter(tracker.NewMockTracker(), testRouting())

	cases := []struct {
		name     string
		issue    types.Issue
		expected string
	}{
		{"frontend->opus", types.Issue{ID: "fe-1", Labels: []string{"frontend"}}, "opus"},
		{"docs->haiku", types.Issue{ID: "doc-1", Labels: []string{"docs"}}, "haiku"},
		{"backend->sonnet(default)", types.Issue{ID: "be-1", Labels: []string{"backend"}}, "sonnet"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := p.ResolveModel(tc.issue)
			if got != tc.expected {
				t.Errorf("ResolveModel(%v): expected %q, got %q", tc.issue.Labels, tc.expected, got)
			}
		})
	}
}

func TestPromoter_DemoteIssue(t *testing.T) {
	mock := tracker.NewMockTracker()
	p := NewPromoter(mock, testRouting())

	// Pre-populate both labels
	mock.Labels["issue-1"] = []string{labelAgentReady, labelAgentReadyHeavy}

	if err := p.DemoteIssue(context.Background(), "issue-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.Labels["issue-1"]) != 0 {
		t.Errorf("expected no labels after demotion, got %v", mock.Labels["issue-1"])
	}
}
