package wave

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	minute             = time.Minute
	defaultIssueMaxAge = 45 * time.Minute
	defaultWaveMaxAge  = 3 * time.Hour
)

// WaveConfig is the top-level configuration for the wave pipeline.
type WaveConfig struct {
	Repo           string        `yaml:"repo"`
	PollInterval   int           `yaml:"poll_interval"`
	Phases         []PhaseConfig `yaml:"phases"`
	ModelRouting   ModelRouting  `yaml:"model_routing"`
	Verification   Verification  `yaml:"verification"`
	StallDetection StallConfig   `yaml:"stall_detection"`
	AutoDAG        *bool         `yaml:"auto_dag"`
}

// PhaseConfig describes a phase in the wave config YAML.
type PhaseConfig struct {
	Name      string          `yaml:"name"`
	Milestone string          `yaml:"milestone"`
	Epic      string          `yaml:"epic"`
	Waves     []WaveConfigWave `yaml:"waves"`
}

// WaveConfigWave describes a wave inside a phase in the YAML config.
type WaveConfigWave struct {
	Issues      []string `yaml:"issues"`
	Description string   `yaml:"description"`
}

// ModelRouting controls how issues are routed to agent models.
type ModelRouting struct {
	DefaultLabel string        `yaml:"default_label"`
	HeavyLabel   string        `yaml:"heavy_label"`
	DefaultModel string        `yaml:"default_model"`
	Rules        []RoutingRule `yaml:"rules"`
}

// RoutingRule maps issue labels to a specific model.
type RoutingRule struct {
	Labels []string `yaml:"labels"`
	Model  string   `yaml:"model"`
	Heavy  bool     `yaml:"heavy"`
}

// Verification controls how wave completion is verified.
type Verification struct {
	PropagationDelay int  `yaml:"propagation_delay"`
	RequireMergedPR  bool `yaml:"require_merged_pr"`
}

// StallConfig defines thresholds for stall detection.
type StallConfig struct {
	MaxAgeMinutes     int `yaml:"max_age_minutes"`
	WaveMaxAgeMinutes int `yaml:"wave_max_age_minutes"`
	MaxRetries        int `yaml:"max_retries"`
}

// IssueMaxAge returns the maximum age for an issue before it is considered stalled.
// Defaults to 45 minutes if not configured.
func (c StallConfig) IssueMaxAge() time.Duration {
	if c.MaxAgeMinutes <= 0 {
		return defaultIssueMaxAge
	}
	return time.Duration(c.MaxAgeMinutes) * minute
}

// WaveMaxAge returns the maximum age for a wave before it is considered stalled.
// Defaults to 3 hours if not configured.
func (c StallConfig) WaveMaxAge() time.Duration {
	if c.WaveMaxAgeMinutes <= 0 {
		return defaultWaveMaxAge
	}
	return time.Duration(c.WaveMaxAgeMinutes) * minute
}

// IsAutoDAG returns true if auto-DAG mode is active (AutoDAG is nil or true).
func (c WaveConfig) IsAutoDAG() bool {
	return c.AutoDAG == nil || *c.AutoDAG
}

// ParseConfig reads and parses a WaveConfig from the given YAML file path.
// Returns nil, nil if the file does not exist.
func ParseConfig(path string) (*WaveConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg WaveConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	return &cfg, nil
}

// MergeWithDAG combines a WaveConfig (optional) with a DAG to produce a Pipeline.
//
// If cfg is nil, waves are derived from dag.ComputeWaves() and placed in a single
// auto-generated phase.
//
// If cfg is provided, the config grouping is used as-is, but each phase/wave is
// validated against the DAG: an issue in wave N must not depend (directly or
// transitively) on an issue that appears in wave N+1 or later within the same phase.
func MergeWithDAG(cfg *WaveConfig, dag *DAG) (*Pipeline, error) {
	if cfg == nil {
		return mergeAutoDAG(dag), nil
	}
	return mergeConfigDAG(cfg, dag)
}

// mergeAutoDAG builds a Pipeline from ComputeWaves with a single auto phase.
func mergeAutoDAG(dag *DAG) *Pipeline {
	dagWaves := dag.ComputeWaves()
	phase := Phase{
		Name:  "auto",
		Waves: dagWaves,
	}
	return &Pipeline{
		Phases: []Phase{phase},
		DAG:    dag,
		Config: nil,
	}
}

// mergeConfigDAG builds a Pipeline from the config, validating against the DAG.
func mergeConfigDAG(cfg *WaveConfig, dag *DAG) (*Pipeline, error) {
	phases := make([]Phase, 0, len(cfg.Phases))

	for _, pc := range cfg.Phases {
		waves := make([]Wave, 0, len(pc.Waves))
		for i, wc := range pc.Waves {
			waves = append(waves, Wave{
				Index:       i,
				Issues:      wc.Issues,
				Description: wc.Description,
			})
		}

		// Validate: no issue in wave N may depend on an issue in wave M > N
		if err := validateWaveOrder(waves, dag); err != nil {
			return nil, fmt.Errorf("phase %q: %w", pc.Name, err)
		}

		phases = append(phases, Phase{
			Name:      pc.Name,
			Milestone: pc.Milestone,
			Epic:      pc.Epic,
			Waves:     waves,
		})
	}

	return &Pipeline{
		Phases: phases,
		DAG:    dag,
		Config: cfg,
	}, nil
}

// validateWaveOrder checks that no issue in wave N depends on an issue in wave M > N.
func validateWaveOrder(waves []Wave, dag *DAG) error {
	// Build a map: issue → wave index
	issueWave := make(map[string]int)
	for _, w := range waves {
		for _, id := range w.Issues {
			issueWave[id] = w.Index
		}
	}

	for _, w := range waves {
		for _, id := range w.Issues {
			node, ok := dag.Nodes[id]
			if !ok {
				continue
			}
			for _, depID := range node.BlockedBy {
				depWave, depKnown := issueWave[depID]
				if !depKnown {
					continue
				}
				// depID must appear in an earlier wave (lower index)
				if depWave > w.Index {
					return fmt.Errorf(
						"issue %q (wave %d) depends on %q (wave %d): dependency must appear in an earlier wave",
						id, w.Index, depID, depWave,
					)
				}
			}
		}
	}
	return nil
}
