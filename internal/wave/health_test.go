package wave

import (
	"context"
	"testing"

	"github.com/junhoyeo/contrabass/internal/types"
)

// TestHealthCheck_AllGood verifies that a manager with simple issues returns all OK results.
func TestHealthCheck_AllGood(t *testing.T) {
	m, err := NewManager(nil, "", nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	issues := []types.Issue{
		{ID: "1", State: types.Unclaimed},
		{ID: "2", State: types.Unclaimed, BlockedBy: []string{"1"}},
	}
	if err := m.Refresh(issues); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	results := m.HealthCheck(context.Background())
	if len(results) == 0 {
		t.Fatal("expected at least one health result")
	}

	for _, r := range results {
		if !r.OK {
			t.Errorf("health check %q failed: %s", r.Name, r.Message)
		}
	}
}
