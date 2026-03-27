package wave

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testYAML = `
repo: myorg/myrepo
poll_interval: 30
phases:
  - name: Phase 1
    milestone: "v1.0"
    epic: "epic-1"
    waves:
      - issues: ["A", "B"]
        description: "foundation"
      - issues: ["C"]
        description: "integration"
model_routing:
  default_label: agent
  heavy_label: agent-heavy
  default_model: claude-sonnet
  rules:
    - labels: ["complex"]
      model: claude-opus
      heavy: true
verification:
  propagation_delay: 10
  require_merged_pr: true
stall_detection:
  max_age_minutes: 60
  wave_max_age_minutes: 180
  max_retries: 3
auto_dag: false
`

func TestParseConfig_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wave.yaml")
	require.NoError(t, os.WriteFile(path, []byte(testYAML), 0o644))

	cfg, err := ParseConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "myorg/myrepo", cfg.Repo)
	assert.Equal(t, 30, cfg.PollInterval)

	require.Len(t, cfg.Phases, 1)
	phase := cfg.Phases[0]
	assert.Equal(t, "Phase 1", phase.Name)
	assert.Equal(t, "v1.0", phase.Milestone)
	assert.Equal(t, "epic-1", phase.Epic)
	require.Len(t, phase.Waves, 2)
	assert.Equal(t, []string{"A", "B"}, phase.Waves[0].Issues)
	assert.Equal(t, "foundation", phase.Waves[0].Description)
	assert.Equal(t, []string{"C"}, phase.Waves[1].Issues)
	assert.Equal(t, "integration", phase.Waves[1].Description)

	assert.Equal(t, "agent", cfg.ModelRouting.DefaultLabel)
	assert.Equal(t, "agent-heavy", cfg.ModelRouting.HeavyLabel)
	assert.Equal(t, "claude-sonnet", cfg.ModelRouting.DefaultModel)
	require.Len(t, cfg.ModelRouting.Rules, 1)
	assert.Equal(t, []string{"complex"}, cfg.ModelRouting.Rules[0].Labels)
	assert.Equal(t, "claude-opus", cfg.ModelRouting.Rules[0].Model)
	assert.True(t, cfg.ModelRouting.Rules[0].Heavy)

	assert.Equal(t, 10, cfg.Verification.PropagationDelay)
	assert.True(t, cfg.Verification.RequireMergedPR)

	assert.Equal(t, 60, cfg.StallDetection.MaxAgeMinutes)
	assert.Equal(t, 180, cfg.StallDetection.WaveMaxAgeMinutes)
	assert.Equal(t, 3, cfg.StallDetection.MaxRetries)

	require.NotNil(t, cfg.AutoDAG)
	assert.False(t, *cfg.AutoDAG)
	assert.False(t, cfg.IsAutoDAG())
}

func TestParseConfig_FileNotExists(t *testing.T) {
	cfg, err := ParseConfig("/nonexistent/path/wave.yaml")
	assert.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestMergeWithDAG_PureDAG(t *testing.T) {
	// nil config → waves from ComputeWaves, single auto-phase
	issues := []types.Issue{
		{ID: "A", BlockedBy: []string{}},
		{ID: "B", BlockedBy: []string{"A"}},
		{ID: "C", BlockedBy: []string{"A"}},
	}
	dag, err := BuildDAG(issues)
	require.NoError(t, err)

	pipeline, err := MergeWithDAG(nil, dag)
	require.NoError(t, err)
	require.NotNil(t, pipeline)

	assert.Nil(t, pipeline.Config)
	assert.Equal(t, dag, pipeline.DAG)
	require.Len(t, pipeline.Phases, 1)

	phase := pipeline.Phases[0]
	// Wave 0: A, Wave 1: B and C
	require.Len(t, phase.Waves, 2)
	assert.Equal(t, []string{"A"}, phase.Waves[0].Issues)
	assert.ElementsMatch(t, []string{"B", "C"}, phase.Waves[1].Issues)
}

func TestMergeWithDAG_ConfigOverride(t *testing.T) {
	// config grouping used, descriptions preserved
	issues := []types.Issue{
		{ID: "A", BlockedBy: []string{}},
		{ID: "B", BlockedBy: []string{"A"}},
		{ID: "C", BlockedBy: []string{"B"}},
	}
	dag, err := BuildDAG(issues)
	require.NoError(t, err)

	cfg := &WaveConfig{
		Phases: []PhaseConfig{
			{
				Name:      "My Phase",
				Milestone: "v2.0",
				Epic:      "epic-2",
				Waves: []WaveConfigWave{
					{Issues: []string{"A"}, Description: "bootstrap"},
					{Issues: []string{"B", "C"}, Description: "build"},
				},
			},
		},
	}

	pipeline, err := MergeWithDAG(cfg, dag)
	require.NoError(t, err)
	require.NotNil(t, pipeline)

	assert.Equal(t, cfg, pipeline.Config)
	assert.Equal(t, dag, pipeline.DAG)
	require.Len(t, pipeline.Phases, 1)

	phase := pipeline.Phases[0]
	assert.Equal(t, "My Phase", phase.Name)
	assert.Equal(t, "v2.0", phase.Milestone)
	assert.Equal(t, "epic-2", phase.Epic)
	require.Len(t, phase.Waves, 2)

	assert.Equal(t, []string{"A"}, phase.Waves[0].Issues)
	assert.Equal(t, "bootstrap", phase.Waves[0].Description)
	assert.Equal(t, []string{"B", "C"}, phase.Waves[1].Issues)
	assert.Equal(t, "build", phase.Waves[1].Description)
}

func TestMergeWithDAG_ConfigConflictsWithDAG(t *testing.T) {
	// Issue in wave 1 depends on issue in wave 2 → error
	// A is in wave 0, B is in wave 1, but config puts B before A
	issues := []types.Issue{
		{ID: "A", BlockedBy: []string{}},
		{ID: "B", BlockedBy: []string{"A"}}, // B depends on A
	}
	dag, err := BuildDAG(issues)
	require.NoError(t, err)

	// config puts B in wave 0 and A in wave 1 — but B depends on A
	cfg := &WaveConfig{
		Phases: []PhaseConfig{
			{
				Name: "Conflict Phase",
				Waves: []WaveConfigWave{
					{Issues: []string{"B"}, Description: "wave 0 with B"},
					{Issues: []string{"A"}, Description: "wave 1 with A"},
				},
			},
		},
	}

	pipeline, err := MergeWithDAG(cfg, dag)
	assert.Error(t, err)
	assert.Nil(t, pipeline)
}

func TestStallConfig_Defaults(t *testing.T) {
	cfg := StallConfig{}
	assert.Equal(t, defaultIssueMaxAge, cfg.IssueMaxAge())
	assert.Equal(t, defaultWaveMaxAge, cfg.WaveMaxAge())
}

func TestStallConfig_CustomValues(t *testing.T) {
	cfg := StallConfig{
		MaxAgeMinutes:     120,
		WaveMaxAgeMinutes: 240,
	}
	assert.Equal(t, 120*minute, cfg.IssueMaxAge())
	assert.Equal(t, 240*minute, cfg.WaveMaxAge())
}

func TestWaveConfig_IsAutoDAG(t *testing.T) {
	// nil AutoDAG → true (auto)
	cfg := &WaveConfig{}
	assert.True(t, cfg.IsAutoDAG())

	// explicit true
	trueVal := true
	cfg.AutoDAG = &trueVal
	assert.True(t, cfg.IsAutoDAG())

	// explicit false
	falseVal := false
	cfg.AutoDAG = &falseVal
	assert.False(t, cfg.IsAutoDAG())
}
