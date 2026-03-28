package wave

import "context"

// HealthResult holds the outcome of a single health check.
type HealthResult struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// HealthCheck runs a set of diagnostic checks and returns one HealthResult per check.
func (m *Manager) HealthCheck(ctx context.Context) []HealthResult {
	var results []HealthResult

	// 1. DAG validation
	m.mu.RLock()
	pipeline := m.pipeline
	m.mu.RUnlock()

	if pipeline != nil && pipeline.DAG != nil {
		errs := pipeline.DAG.Validate()
		if len(errs) == 0 {
			results = append(results, HealthResult{Name: "dag", OK: true, Message: "no cycles, no missing refs"})
		} else {
			results = append(results, HealthResult{Name: "dag", OK: false, Message: errs[0].Error()})
		}
	} else {
		results = append(results, HealthResult{Name: "dag", OK: true, Message: "no DAG loaded (offline mode)"})
	}

	// 2. Config validation
	if m.configPath != "" {
		_, err := ParseConfig(m.configPath)
		if err != nil {
			results = append(results, HealthResult{Name: "config", OK: false, Message: err.Error()})
		} else {
			results = append(results, HealthResult{Name: "config", OK: true, Message: "valid"})
		}
	} else {
		results = append(results, HealthResult{Name: "config", OK: true, Message: "no config path (auto-DAG mode)"})
	}

	return results
}
