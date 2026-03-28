# Wave Manager Integration into Contrabass — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate wave management (DAG, wave promotion, stall detection, token optimization, health checks, dashboard) into the Contrabass Go binary, replacing the bash wave-manager daemon.

**Architecture:** New `internal/wave/` package with clean interfaces. Orchestrator calls wave.Manager at 3 integration points (Refresh, FilterDispatchable, OnIssueCompleted). Optional tracker interfaces (LabelManager, PRVerifier) avoid breaking existing Tracker contract. Token optimization engine (9 proposals) reduces waste by 500k-1.5M tokens per pipeline.

**Tech Stack:** Go 1.25, gopkg.in/yaml.v3, github.com/osteele/liquid (existing), Cobra CLI, React dashboard (existing packages/dashboard/)

**Spec:** `docs/superpowers/specs/2026-03-27-wave-manager-integration-design.md`

---

## File Structure

### New files (internal/wave/)
| File | Responsibility |
|------|---------------|
| `internal/wave/dag.go` | DAG build from issues, cycle detection, topological sort |
| `internal/wave/dag_test.go` | DAG unit tests |
| `internal/wave/wave.go` | Wave, Phase, Pipeline structs |
| `internal/wave/config.go` | wave-config.yaml parsing, MergeWithDAG |
| `internal/wave/config_test.go` | Config parsing tests |
| `internal/wave/manager.go` | Manager: FilterDispatchable, OnIssueCompleted, Refresh |
| `internal/wave/manager_test.go` | Manager integration tests |
| `internal/wave/promoter.go` | Label management, model routing |
| `internal/wave/promoter_test.go` | Promoter tests |
| `internal/wave/stall.go` | Unified stall detection (issue + wave level) |
| `internal/wave/stall_test.go` | Stall detection tests |
| `internal/wave/events.go` | Wave event types (for JSONL) |
| `internal/wave/event_log.go` | JSONL writer + subscriber |
| `internal/wave/event_log_test.go` | Event log tests |
| `internal/wave/health.go` | Pipeline health check, reconcile |
| `internal/wave/health_test.go` | Health check tests |

### New files (CLI)
| File | Responsibility |
|------|---------------|
| `cmd/contrabass/wave_cmd.go` | `contrabass wave [status\|health\|reconcile\|promote]` |
| `cmd/contrabass/wave_cmd_test.go` | CLI tests |

### New files (Dashboard)
| File | Responsibility |
|------|---------------|
| `packages/dashboard/src/components/WavePipeline.tsx` | Wave progress visualization |
| `packages/dashboard/src/components/DependencyGraph.tsx` | DAG SVG visualization |
| `packages/dashboard/src/components/WaveTimeline.tsx` | Wave events timeline |

### Modified files
| File | Changes |
|------|---------|
| `internal/tracker/tracker.go` | Add LabelManager, PRVerifier optional interfaces |
| `internal/tracker/github.go` | Implement LabelManager, PRVerifier |
| `internal/tracker/mock.go` | Add LabelManager, PRVerifier mock methods |
| `internal/agent/runner.go` | Extend Start with *RunOptions |
| `internal/agent/claude.go` | Use RunOptions (MaxTurns, ModelOverride, retry context) |
| `internal/agent/codex.go` | Accept *RunOptions (pass through) |
| `internal/agent/opencode.go` | Accept *RunOptions (pass through) |
| `internal/agent/ohmyopencode.go` | Accept *RunOptions (pass through) |
| `internal/agent/omc.go` | Accept *RunOptions (pass through) |
| `internal/agent/omx.go` | Accept *RunOptions (pass through) |
| `internal/agent/tmux_runner.go` | Accept *RunOptions (pass through to inner) |
| `internal/agent/teamcli.go` | Accept *RunOptions (pass through) |
| `internal/agent/mock.go` | Accept *RunOptions |
| `internal/config/template.go` | NO CHANGE — retry context injected via orchestrator, not template |
| `internal/config/config.go` | WaveConfig path helper |
| `internal/orchestrator/orchestrator.go` | wave.Manager field + SetWaveManager + integration |
| `internal/orchestrator/orchestrator_runtime.go` | completeRun integration (success + failure paths) |
| `internal/orchestrator/events.go` | New event types + payloads |
| `internal/web/server.go` | Wave API endpoints |
| `cmd/contrabass/main.go` | Wave manager wiring + wave subcommand |

---

## Chunk 1: Core Wave Package — DAG, Waves, Config

### Task 1: DAG data structures and cycle detection

**Files:**
- Create: `internal/wave/dag.go`
- Create: `internal/wave/dag_test.go`

- [ ] **Step 1: Write failing test — BuildDAG from issues**

```go
// internal/wave/dag_test.go
package wave

import (
    "testing"

    "github.com/junhoyeo/contrabass/internal/types"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestBuildDAG_SimpleChain(t *testing.T) {
    issues := []types.Issue{
        {ID: "1", BlockedBy: nil, Labels: []string{"backend"}},
        {ID: "2", BlockedBy: []string{"1"}, Labels: []string{"backend"}},
        {ID: "3", BlockedBy: []string{"2"}, Labels: []string{"frontend"}},
    }
    dag, err := BuildDAG(issues)
    require.NoError(t, err)
    assert.Len(t, dag.Nodes, 3)

    // Check reverse edges
    assert.Contains(t, dag.Nodes["1"].Blocks, "2")
    assert.Contains(t, dag.Nodes["2"].Blocks, "3")
    assert.Empty(t, dag.Nodes["3"].Blocks)
}

func TestBuildDAG_NoDeps(t *testing.T) {
    issues := []types.Issue{
        {ID: "1"},
        {ID: "2"},
    }
    dag, err := BuildDAG(issues)
    require.NoError(t, err)
    assert.Len(t, dag.Nodes, 2)
    assert.Empty(t, dag.Nodes["1"].BlockedBy)
    assert.Empty(t, dag.Nodes["1"].Blocks)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestBuildDAG -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Write minimal implementation**

```go
// internal/wave/dag.go
package wave

import (
    "github.com/junhoyeo/contrabass/internal/types"
)

// Node represents an issue in the dependency DAG.
type Node struct {
    IssueID   string
    BlockedBy []string
    Blocks    []string           // reverse edges, computed
    Labels    []string
    State     types.IssueState
}

// DAG is a directed acyclic graph of issue dependencies.
type DAG struct {
    Nodes map[string]*Node
}

// BuildDAG constructs a dependency graph from a list of issues.
// Computes reverse edges (Blocks) from BlockedBy relationships.
func BuildDAG(issues []types.Issue) (*DAG, error) {
    dag := &DAG{Nodes: make(map[string]*Node, len(issues))}

    for _, issue := range issues {
        dag.Nodes[issue.ID] = &Node{
            IssueID:   issue.ID,
            BlockedBy: issue.BlockedBy,
            Blocks:    nil,
            Labels:    issue.Labels,
            State:     issue.State,
        }
    }

    // Compute reverse edges
    for _, node := range dag.Nodes {
        for _, depID := range node.BlockedBy {
            if dep, ok := dag.Nodes[depID]; ok {
                dep.Blocks = append(dep.Blocks, node.IssueID)
            }
        }
    }

    return dag, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestBuildDAG -v`
Expected: PASS

- [ ] **Step 5: Write failing test — Validate (cycles + missing refs)**

```go
// internal/wave/dag_test.go — append

func TestDAG_Validate_NoCycles(t *testing.T) {
    issues := []types.Issue{
        {ID: "1"},
        {ID: "2", BlockedBy: []string{"1"}},
    }
    dag, _ := BuildDAG(issues)
    errs := dag.Validate()
    assert.Empty(t, errs)
}

func TestDAG_Validate_CycleDetected(t *testing.T) {
    issues := []types.Issue{
        {ID: "1", BlockedBy: []string{"2"}},
        {ID: "2", BlockedBy: []string{"1"}},
    }
    dag, _ := BuildDAG(issues)
    errs := dag.Validate()
    require.NotEmpty(t, errs)
    assert.Contains(t, errs[0].Error(), "cycle")
}

func TestDAG_Validate_MissingRef(t *testing.T) {
    issues := []types.Issue{
        {ID: "1", BlockedBy: []string{"999"}},
    }
    dag, _ := BuildDAG(issues)
    errs := dag.Validate()
    require.NotEmpty(t, errs)
    assert.Contains(t, errs[0].Error(), "999")
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestDAG_Validate -v`
Expected: FAIL — Validate not defined

- [ ] **Step 7: Implement Validate with cycle detection (Kahn's algorithm)**

```go
// internal/wave/dag.go — append

import "fmt"

// Validate checks for cycles and missing references.
func (d *DAG) Validate() []error {
    var errs []error

    // Check missing refs
    for _, node := range d.Nodes {
        for _, depID := range node.BlockedBy {
            if _, ok := d.Nodes[depID]; !ok {
                errs = append(errs, fmt.Errorf("issue %s references missing dependency %s", node.IssueID, depID))
            }
        }
    }

    // Cycle detection via Kahn's algorithm
    inDegree := make(map[string]int, len(d.Nodes))
    for id := range d.Nodes {
        inDegree[id] = 0
    }
    for _, node := range d.Nodes {
        for _, depID := range node.BlockedBy {
            if _, ok := d.Nodes[depID]; ok {
                inDegree[node.IssueID]++
            }
        }
    }

    queue := make([]string, 0)
    for id, deg := range inDegree {
        if deg == 0 {
            queue = append(queue, id)
        }
    }

    visited := 0
    for len(queue) > 0 {
        current := queue[0]
        queue = queue[1:]
        visited++
        for _, blocked := range d.Nodes[current].Blocks {
            inDegree[blocked]--
            if inDegree[blocked] == 0 {
                queue = append(queue, blocked)
            }
        }
    }

    if visited < len(d.Nodes) {
        errs = append(errs, fmt.Errorf("cycle detected: %d nodes unreachable via topological sort", len(d.Nodes)-visited))
    }

    return errs
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestDAG_Validate -v`
Expected: PASS

- [ ] **Step 9: Write failing test — ComputeWaves**

```go
// internal/wave/dag_test.go — append

func TestDAG_ComputeWaves_Diamond(t *testing.T) {
    // Diamond: 1 → 2,3 → 4
    issues := []types.Issue{
        {ID: "1"},
        {ID: "2", BlockedBy: []string{"1"}},
        {ID: "3", BlockedBy: []string{"1"}},
        {ID: "4", BlockedBy: []string{"2", "3"}},
    }
    dag, _ := BuildDAG(issues)
    waves := dag.ComputeWaves()

    require.Len(t, waves, 3)
    assert.ElementsMatch(t, waves[0].Issues, []string{"1"})
    assert.ElementsMatch(t, waves[1].Issues, []string{"2", "3"})
    assert.ElementsMatch(t, waves[2].Issues, []string{"4"})
}

func TestDAG_ComputeWaves_AllIndependent(t *testing.T) {
    issues := []types.Issue{
        {ID: "1"}, {ID: "2"}, {ID: "3"},
    }
    dag, _ := BuildDAG(issues)
    waves := dag.ComputeWaves()

    require.Len(t, waves, 1)
    assert.ElementsMatch(t, waves[0].Issues, []string{"1", "2", "3"})
}
```

- [ ] **Step 10: Run test to verify it fails**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestDAG_ComputeWaves -v`
Expected: FAIL — ComputeWaves not defined

- [ ] **Step 11: Implement ComputeWaves (topological sort → levels)**

```go
// internal/wave/dag.go — append

// Wave represents a group of issues that can execute in parallel.
type Wave struct {
    Index       int
    Issues      []string
    Description string
}

// ComputeWaves performs topological sort and groups nodes into waves.
// Wave 0 = nodes with no dependencies. Wave N = nodes whose all deps are in waves 0..N-1.
func (d *DAG) ComputeWaves() []Wave {
    if len(d.Nodes) == 0 {
        return nil
    }

    nodeWave := make(map[string]int, len(d.Nodes))
    inDegree := make(map[string]int, len(d.Nodes))

    for id := range d.Nodes {
        inDegree[id] = 0
    }
    for _, node := range d.Nodes {
        for _, depID := range node.BlockedBy {
            if _, ok := d.Nodes[depID]; ok {
                inDegree[node.IssueID]++
            }
        }
    }

    // BFS by levels
    currentLevel := make([]string, 0)
    for id, deg := range inDegree {
        if deg == 0 {
            currentLevel = append(currentLevel, id)
            nodeWave[id] = 0
        }
    }

    waveIndex := 0
    var waves []Wave

    for len(currentLevel) > 0 {
        waves = append(waves, Wave{
            Index:  waveIndex,
            Issues: currentLevel,
        })

        nextLevel := make([]string, 0)
        for _, id := range currentLevel {
            for _, blocked := range d.Nodes[id].Blocks {
                inDegree[blocked]--
                if inDegree[blocked] == 0 {
                    nextLevel = append(nextLevel, blocked)
                    nodeWave[blocked] = waveIndex + 1
                }
            }
        }
        currentLevel = nextLevel
        waveIndex++
    }

    return waves
}
```

- [ ] **Step 12: Run test to verify it passes**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestDAG_ComputeWaves -v`
Expected: PASS

- [ ] **Step 13: Commit**

```bash
cd /home/booster/tools/contrabass
git add internal/wave/dag.go internal/wave/dag_test.go
git commit -m "feat(wave): add DAG with cycle detection and topological wave computation"
```

---

### Task 2: Wave config parsing and MergeWithDAG

**Files:**
- Create: `internal/wave/config.go`
- Create: `internal/wave/config_test.go`
- Modify: `internal/wave/wave.go` (create — Phase, Pipeline structs)

- [ ] **Step 1: Write failing test — ParseConfig**

```go
// internal/wave/config_test.go
package wave

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestParseConfig_ValidFile(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "wave-config.yaml")
    content := `
repo: owner/repo
poll_interval: 60
phases:
  - name: "Phase 1: Foundation"
    milestone: "Phase 1: Foundation"
    epic: "1"
    waves:
      - issues: ["2", "3"]
        description: "Go scaffold + Docker"
      - issues: ["4", "5"]
model_routing:
  default_label: agent-ready
  heavy_label: agent-ready-heavy
  default_model: claude-sonnet-4-6
  rules:
    - labels: [frontend]
      model: claude-opus-4-6
      heavy: true
stall_detection:
  max_age_minutes: 45
  wave_max_age_minutes: 180
  max_retries: 3
`
    os.WriteFile(path, []byte(content), 0644)

    cfg, err := ParseConfig(path)
    require.NoError(t, err)
    require.NotNil(t, cfg)
    assert.Equal(t, "owner/repo", cfg.Repo)
    assert.Len(t, cfg.Phases, 1)
    assert.Len(t, cfg.Phases[0].Waves, 2)
    assert.Equal(t, 45, cfg.StallDetection.MaxAgeMinutes)
    assert.Equal(t, 3, cfg.StallDetection.MaxRetries)
}

func TestParseConfig_FileNotExists(t *testing.T) {
    cfg, err := ParseConfig("/nonexistent/wave-config.yaml")
    assert.NoError(t, err)
    assert.Nil(t, cfg)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestParseConfig -v`
Expected: FAIL — ParseConfig not defined

- [ ] **Step 3: Implement config parsing**

```go
// internal/wave/config.go
package wave

import (
    "errors"
    "os"
    "time"

    "gopkg.in/yaml.v3"
)

type WaveConfig struct {
    Repo           string         `yaml:"repo"`
    PollInterval   int            `yaml:"poll_interval"`
    Phases         []PhaseConfig  `yaml:"phases"`
    ModelRouting   ModelRouting   `yaml:"model_routing"`
    Verification   Verification   `yaml:"verification"`
    StallDetection StallConfig    `yaml:"stall_detection"`
    AutoDAG        *bool          `yaml:"auto_dag"`
}

type PhaseConfig struct {
    Name      string            `yaml:"name"`
    Milestone string            `yaml:"milestone"`
    Epic      string            `yaml:"epic"`
    Waves     []WaveConfig_Wave `yaml:"waves"`
}

type WaveConfig_Wave struct {
    Issues      []string `yaml:"issues"`
    Description string   `yaml:"description"`
}

type ModelRouting struct {
    DefaultLabel string        `yaml:"default_label"`
    HeavyLabel   string        `yaml:"heavy_label"`
    DefaultModel string        `yaml:"default_model"`
    Rules        []RoutingRule `yaml:"rules"`
}

type RoutingRule struct {
    Labels []string `yaml:"labels"`
    Model  string   `yaml:"model"`
    Heavy  bool     `yaml:"heavy"`
}

type Verification struct {
    PropagationDelay int  `yaml:"propagation_delay"`
    RequireMergedPR  bool `yaml:"require_merged_pr"`
}

type StallConfig struct {
    MaxAgeMinutes     int `yaml:"max_age_minutes"`
    WaveMaxAgeMinutes int `yaml:"wave_max_age_minutes"`
    MaxRetries        int `yaml:"max_retries"`
}

func (c StallConfig) IssueMaxAge() time.Duration {
    if c.MaxAgeMinutes <= 0 {
        return 45 * time.Minute
    }
    return time.Duration(c.MaxAgeMinutes) * time.Minute
}

func (c StallConfig) WaveMaxAge() time.Duration {
    if c.WaveMaxAgeMinutes <= 0 {
        return 3 * time.Hour
    }
    return time.Duration(c.WaveMaxAgeMinutes) * time.Minute
}

func (c WaveConfig) IsAutoDAG() bool {
    if c.AutoDAG == nil {
        return true
    }
    return *c.AutoDAG
}

// ParseConfig reads wave-config.yaml. Returns nil, nil if file doesn't exist.
func ParseConfig(path string) (*WaveConfig, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return nil, nil
        }
        return nil, err
    }

    var cfg WaveConfig
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}
```

- [ ] **Step 4: Create wave.go with Phase and Pipeline structs**

```go
// internal/wave/wave.go
package wave

// Phase represents a major milestone containing multiple waves.
type Phase struct {
    Name      string
    Milestone string
    Epic      string
    Waves     []Wave
}

// Pipeline is the complete state of the wave pipeline.
type Pipeline struct {
    Phases      []Phase
    ActivePhase int
    ActiveWave  int
    DAG         *DAG
    Config      *WaveConfig
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -v`
Expected: ALL PASS

- [ ] **Step 6: Write failing test — MergeWithDAG**

```go
// internal/wave/config_test.go — append

func TestMergeWithDAG_ConfigOverride(t *testing.T) {
    issues := []types.Issue{
        {ID: "2"}, {ID: "3"},
        {ID: "4", BlockedBy: []string{"2"}},
        {ID: "5", BlockedBy: []string{"3"}},
    }
    dag, _ := BuildDAG(issues)

    cfg := &WaveConfig{
        Phases: []PhaseConfig{
            {
                Name: "Phase 1",
                Waves: []WaveConfig_Wave{
                    {Issues: []string{"2", "3"}, Description: "scaffold"},
                    {Issues: []string{"4", "5"}, Description: "features"},
                },
            },
        },
    }

    pipeline, err := MergeWithDAG(cfg, dag)
    require.NoError(t, err)
    require.Len(t, pipeline.Phases, 1)
    assert.Len(t, pipeline.Phases[0].Waves, 2)
    assert.Equal(t, "scaffold", pipeline.Phases[0].Waves[0].Description)
}

func TestMergeWithDAG_PureDAG(t *testing.T) {
    issues := []types.Issue{
        {ID: "1"},
        {ID: "2", BlockedBy: []string{"1"}},
    }
    dag, _ := BuildDAG(issues)

    pipeline, err := MergeWithDAG(nil, dag)
    require.NoError(t, err)
    require.Len(t, pipeline.Phases, 1) // single auto-phase
    assert.Len(t, pipeline.Phases[0].Waves, 2)
}

func TestMergeWithDAG_ConfigConflictsWithDAG(t *testing.T) {
    // Issue 4 in wave 1 depends on issue 5 in wave 2 — invalid
    issues := []types.Issue{
        {ID: "4", BlockedBy: []string{"5"}},
        {ID: "5"},
    }
    dag, _ := BuildDAG(issues)

    cfg := &WaveConfig{
        Phases: []PhaseConfig{
            {
                Waves: []WaveConfig_Wave{
                    {Issues: []string{"4"}},
                    {Issues: []string{"5"}},
                },
            },
        },
    }

    _, err := MergeWithDAG(cfg, dag)
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "depends on")
}
```

- [ ] **Step 7: Run test to verify it fails**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestMergeWithDAG -v`
Expected: FAIL — MergeWithDAG not defined

- [ ] **Step 8: Implement MergeWithDAG**

```go
// internal/wave/config.go — append

import "fmt"

// MergeWithDAG builds a Pipeline. If cfg is not nil, uses config grouping
// and validates against DAG. If cfg is nil, generates waves from DAG.ComputeWaves().
func MergeWithDAG(cfg *WaveConfig, dag *DAG) (*Pipeline, error) {
    pipeline := &Pipeline{DAG: dag, Config: cfg}

    if cfg == nil || len(cfg.Phases) == 0 {
        // Pure DAG mode — single auto-phase
        waves := dag.ComputeWaves()
        pipeline.Phases = []Phase{{
            Name:  "Auto",
            Waves: waves,
        }}
        return pipeline, nil
    }

    // Config override mode — validate against DAG
    issueWave := make(map[string]int) // issue ID → global wave index
    globalWave := 0

    for _, phaseCfg := range cfg.Phases {
        var phase Phase
        phase.Name = phaseCfg.Name
        phase.Milestone = phaseCfg.Milestone
        phase.Epic = phaseCfg.Epic

        for _, waveCfg := range phaseCfg.Waves {
            wave := Wave{
                Index:       globalWave,
                Issues:      waveCfg.Issues,
                Description: waveCfg.Description,
            }
            for _, issueID := range waveCfg.Issues {
                issueWave[issueID] = globalWave
            }
            phase.Waves = append(phase.Waves, wave)
            globalWave++
        }
        pipeline.Phases = append(pipeline.Phases, phase)
    }

    // Validate: no issue depends on an issue in a later wave
    for issueID, waveIdx := range issueWave {
        node, ok := dag.Nodes[issueID]
        if !ok {
            continue
        }
        for _, depID := range node.BlockedBy {
            depWave, depInConfig := issueWave[depID]
            if depInConfig && depWave >= waveIdx {
                return nil, fmt.Errorf(
                    "issue %s (wave %d) depends on issue %s (wave %d): invalid ordering",
                    issueID, waveIdx, depID, depWave,
                )
            }
        }
    }

    return pipeline, nil
}
```

- [ ] **Step 9: Run all tests**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -v`
Expected: ALL PASS

- [ ] **Step 10: Commit**

```bash
cd /home/booster/tools/contrabass
git add internal/wave/config.go internal/wave/config_test.go internal/wave/wave.go
git commit -m "feat(wave): add config parsing, MergeWithDAG with validation"
```

---

## Chunk 2: Tracker Extensions + Event System

### Task 3: Optional tracker interfaces (LabelManager, PRVerifier)

**Files:**
- Modify: `internal/tracker/tracker.go`
- Modify: `internal/tracker/github.go`
- Modify: `internal/tracker/mock.go`

- [ ] **Step 1: Add optional interfaces to tracker.go**

```go
// internal/tracker/tracker.go — append after existing Tracker interface

// LabelManager is an optional interface for trackers that support label management.
// Use type assertion: if lm, ok := tracker.(LabelManager); ok { ... }
type LabelManager interface {
    AddLabel(ctx context.Context, issueID string, label string) error
    RemoveLabel(ctx context.Context, issueID string, label string) error
}

// PRVerifier is an optional interface for trackers that can verify merged PRs.
type PRVerifier interface {
    HasMergedPR(ctx context.Context, issueID string) (bool, error)
}
```

- [ ] **Step 2: Implement LabelManager + PRVerifier in github.go**

Add methods to `GitHubClient`:

```go
// internal/tracker/github.go — append

func (c *GitHubClient) AddLabel(ctx context.Context, issueID string, label string) error {
    url := fmt.Sprintf("%s/repos/%s/%s/issues/%s/labels",
        c.endpoint, c.owner, c.repo, issueID)
    body := fmt.Sprintf(`{"labels":[%q]}`, label)
    req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
    if err != nil {
        return err
    }
    req.Header.Set("Authorization", "Bearer "+c.apiToken)
    req.Header.Set("Content-Type", "application/json")
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 400 {
        return fmt.Errorf("add label %q to issue %s: HTTP %d", label, issueID, resp.StatusCode)
    }
    return nil
}

func (c *GitHubClient) RemoveLabel(ctx context.Context, issueID string, label string) error {
    url := fmt.Sprintf("%s/repos/%s/%s/issues/%s/labels/%s",
        c.endpoint, c.owner, c.repo, issueID, label)
    req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
    if err != nil {
        return err
    }
    req.Header.Set("Authorization", "Bearer "+c.apiToken)
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode == 404 {
        return nil // label already removed
    }
    if resp.StatusCode >= 400 {
        return fmt.Errorf("remove label %q from issue %s: HTTP %d", label, issueID, resp.StatusCode)
    }
    return nil
}

func (c *GitHubClient) HasMergedPR(ctx context.Context, issueID string) (bool, error) {
    url := fmt.Sprintf("%s/repos/%s/%s/issues/%s/timeline",
        c.endpoint, c.owner, c.repo, issueID)
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return false, err
    }
    req.Header.Set("Authorization", "Bearer "+c.apiToken)
    req.Header.Set("Accept", "application/vnd.github+json")
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return false, err
    }
    defer resp.Body.Close()

    var events []struct {
        Event  string `json:"event"`
        Source *struct {
            Issue *struct {
                PullRequest *struct {
                    MergedAt *string `json:"merged_at"`
                } `json:"pull_request"`
            } `json:"issue"`
        } `json:"source"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
        return false, err
    }
    for _, ev := range events {
        if ev.Event == "cross-referenced" && ev.Source != nil &&
            ev.Source.Issue != nil && ev.Source.Issue.PullRequest != nil &&
            ev.Source.Issue.PullRequest.MergedAt != nil {
            return true, nil
        }
    }
    return false, nil
}
```

- [ ] **Step 3: Add LabelManager + PRVerifier to MockTracker**

```go
// internal/tracker/mock.go — append

func (m *MockTracker) AddLabel(_ context.Context, issueID string, label string) error {
    if m.LabelErr != nil {
        return m.LabelErr
    }
    if m.Labels == nil {
        m.Labels = make(map[string][]string)
    }
    m.Labels[issueID] = append(m.Labels[issueID], label)
    return nil
}

func (m *MockTracker) RemoveLabel(_ context.Context, issueID string, label string) error {
    if m.LabelErr != nil {
        return m.LabelErr
    }
    if m.Labels == nil {
        return nil
    }
    labels := m.Labels[issueID]
    for i, l := range labels {
        if l == label {
            m.Labels[issueID] = append(labels[:i], labels[i+1:]...)
            break
        }
    }
    return nil
}

func (m *MockTracker) HasMergedPR(_ context.Context, issueID string) (bool, error) {
    if m.MergedPRErr != nil {
        return false, m.MergedPRErr
    }
    if m.MergedPRs == nil {
        return false, nil
    }
    return m.MergedPRs[issueID], nil
}
```

Add fields to MockTracker struct:
```go
// Add to MockTracker struct fields
Labels     map[string][]string
LabelErr   error
MergedPRs  map[string]bool
MergedPRErr error
```

- [ ] **Step 4: Run existing tracker tests to ensure nothing broke**

Run: `cd /home/booster/tools/contrabass && go test ./internal/tracker/ -v`
Expected: ALL PASS (existing tests unaffected — new methods don't change interface)

- [ ] **Step 5: Commit**

```bash
cd /home/booster/tools/contrabass
git add internal/tracker/tracker.go internal/tracker/github.go internal/tracker/mock.go
git commit -m "feat(tracker): add optional LabelManager and PRVerifier interfaces"
```

---

### Task 4: Wave event types and JSONL event log

**Files:**
- Create: `internal/wave/events.go`
- Create: `internal/wave/event_log.go`
- Create: `internal/wave/event_log_test.go`
- Modify: `internal/orchestrator/events.go`

- [ ] **Step 1: Create wave event types**

```go
// internal/wave/events.go
package wave

import "time"

// EventType for wave-internal JSONL log.
type EventType string

const (
    WaveEventDAGBuilt       EventType = "dag_built"
    WaveEventWavePromoted   EventType = "wave_promoted"
    WaveEventWaveCompleted  EventType = "wave_completed"
    WaveEventPhaseCompleted EventType = "phase_completed"
    WaveEventPipelineDone   EventType = "pipeline_done"
    WaveEventIssueBlocked   EventType = "issue_blocked"
    WaveEventIssueUnblocked EventType = "issue_unblocked"
    WaveEventStallDetected  EventType = "stall_detected"
    WaveEventHealthCheck    EventType = "health_check"
    WaveEventReconcileDrift EventType = "reconcile_drift"
)

// Event is written to JSONL and delivered to subscribers.
type Event struct {
    Timestamp time.Time              `json:"ts"`
    Type      EventType              `json:"type"`
    Phase     int                    `json:"phase,omitempty"`
    Wave      int                    `json:"wave,omitempty"`
    IssueID   string                 `json:"issue_id,omitempty"`
    Issues    []string               `json:"issues,omitempty"`
    Data      map[string]interface{} `json:"data,omitempty"`
}
```

- [ ] **Step 2: Write failing test — EventLog**

```go
// internal/wave/event_log_test.go
package wave

import (
    "path/filepath"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestEventLog_EmitAndQuery(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "events.jsonl")

    log, err := NewEventLog(path)
    require.NoError(t, err)
    defer log.Close()

    event := Event{
        Timestamp: time.Now(),
        Type:      WaveEventWavePromoted,
        Phase:     0,
        Wave:      1,
        Issues:    []string{"4", "5"},
    }
    err = log.Emit(event)
    require.NoError(t, err)

    events, err := log.Query(time.Time{}, nil)
    require.NoError(t, err)
    require.Len(t, events, 1)
    assert.Equal(t, WaveEventWavePromoted, events[0].Type)
}

func TestEventLog_Subscribe(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "events.jsonl")

    log, err := NewEventLog(path)
    require.NoError(t, err)
    defer log.Close()

    ch := log.Subscribe()

    event := Event{Type: WaveEventDAGBuilt, Timestamp: time.Now()}
    log.Emit(event)

    select {
    case received := <-ch:
        assert.Equal(t, WaveEventDAGBuilt, received.Type)
    case <-time.After(time.Second):
        t.Fatal("subscriber did not receive event")
    }
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestEventLog -v`
Expected: FAIL — NewEventLog not defined

- [ ] **Step 4: Implement EventLog**

```go
// internal/wave/event_log.go
package wave

import (
    "bufio"
    "encoding/json"
    "os"
    "sync"
    "time"
)

type EventLog struct {
    path        string
    file        *os.File
    mu          sync.Mutex
    subscribers []chan Event
}

func NewEventLog(path string) (*EventLog, error) {
    f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
    if err != nil {
        return nil, err
    }
    return &EventLog{path: path, file: f}, nil
}

func (l *EventLog) Emit(event Event) error {
    if event.Timestamp.IsZero() {
        event.Timestamp = time.Now()
    }

    data, err := json.Marshal(event)
    if err != nil {
        return err
    }

    l.mu.Lock()
    _, writeErr := l.file.Write(append(data, '\n'))
    subs := make([]chan Event, len(l.subscribers))
    copy(subs, l.subscribers)
    l.mu.Unlock()

    // Notify subscribers (non-blocking)
    for _, ch := range subs {
        select {
        case ch <- event:
        default: // slow subscriber, drop
        }
    }

    return writeErr
}

func (l *EventLog) Subscribe() <-chan Event {
    ch := make(chan Event, 64)
    l.mu.Lock()
    l.subscribers = append(l.subscribers, ch)
    l.mu.Unlock()
    return ch
}

func (l *EventLog) Query(since time.Time, types []EventType) ([]Event, error) {
    f, err := os.Open(l.path)
    if err != nil {
        return nil, err
    }
    defer f.Close()

    typeSet := make(map[EventType]bool, len(types))
    for _, t := range types {
        typeSet[t] = true
    }

    var events []Event
    scanner := bufio.NewScanner(f)
    for scanner.Scan() {
        var event Event
        if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
            continue
        }
        if !since.IsZero() && event.Timestamp.Before(since) {
            continue
        }
        if len(typeSet) > 0 && !typeSet[event.Type] {
            continue
        }
        events = append(events, event)
    }
    return events, scanner.Err()
}

func (l *EventLog) Close() error {
    l.mu.Lock()
    defer l.mu.Unlock()
    for _, ch := range l.subscribers {
        close(ch)
    }
    l.subscribers = nil
    if l.file != nil {
        return l.file.Close()
    }
    return nil
}
```

- [ ] **Step 5: Run tests**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestEventLog -v`
Expected: PASS

- [ ] **Step 6: Add orchestrator event types for wave**

```go
// internal/orchestrator/events.go — add new const block and payloads

const (
    EventWavePromoted   EventType = 100
    EventWaveCompleted  EventType = 101
    EventPhaseCompleted EventType = 102
    EventPipelineDone   EventType = 103
    EventWaveStall      EventType = 104
)

// Update String() method to cover new values

type WavePromoted struct {
    IssueID string
    Phase   int
    Wave    int
    Issues  []string
}
func (WavePromoted) eventPayload() {}

type WaveCompleted struct {
    Phase int
    Wave  int
}
func (WaveCompleted) eventPayload() {}

type PhaseCompleted struct {
    Phase int
}
func (PhaseCompleted) eventPayload() {}

type WaveStallPayload struct {
    Phase  int
    Wave   int
    Issues []string
}
func (WaveStallPayload) eventPayload() {}
```

- [ ] **Step 7: Run all tests**

Run: `cd /home/booster/tools/contrabass && go test ./internal/... -v -count=1`
Expected: ALL PASS

- [ ] **Step 8: Commit**

```bash
cd /home/booster/tools/contrabass
git add internal/wave/events.go internal/wave/event_log.go internal/wave/event_log_test.go internal/orchestrator/events.go
git commit -m "feat(wave): add event system with JSONL log and orchestrator event types"
```

---

## Chunk 3: Wave Manager Core (Filter, Promoter, Stall)

### Task 5: Promoter — label management and model routing

**Files:**
- Create: `internal/wave/promoter.go`
- Create: `internal/wave/promoter_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/wave/promoter_test.go
package wave

import (
    "context"
    "testing"

    "github.com/junhoyeo/contrabass/internal/tracker"
    "github.com/junhoyeo/contrabass/internal/types"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestPromoter_PromoteWave_DefaultLabels(t *testing.T) {
    mock := tracker.NewMockTracker()
    p := NewPromoter(mock, ModelRouting{
        DefaultLabel: "agent-ready",
        HeavyLabel:   "agent-ready-heavy",
        DefaultModel: "claude-sonnet-4-6",
    })

    issues := map[string]types.Issue{
        "2": {ID: "2", Labels: []string{"backend"}},
        "3": {ID: "3", Labels: []string{"backend"}},
    }
    wave := Wave{Index: 0, Issues: []string{"2", "3"}}

    promoted, err := p.PromoteWave(context.Background(), wave, issues)
    require.NoError(t, err)
    assert.ElementsMatch(t, promoted, []string{"2", "3"})
    assert.Contains(t, mock.Labels["2"], "agent-ready")
    assert.Contains(t, mock.Labels["3"], "agent-ready")
}

func TestPromoter_PromoteWave_HeavyLabels(t *testing.T) {
    mock := tracker.NewMockTracker()
    p := NewPromoter(mock, ModelRouting{
        DefaultLabel: "agent-ready",
        HeavyLabel:   "agent-ready-heavy",
        Rules: []RoutingRule{
            {Labels: []string{"frontend"}, Heavy: true, Model: "claude-opus-4-6"},
        },
    })

    issues := map[string]types.Issue{
        "2": {ID: "2", Labels: []string{"frontend"}},
        "3": {ID: "3", Labels: []string{"backend"}},
    }
    wave := Wave{Index: 0, Issues: []string{"2", "3"}}

    promoted, err := p.PromoteWave(context.Background(), wave, issues)
    require.NoError(t, err)
    assert.Len(t, promoted, 2)
    assert.Contains(t, mock.Labels["2"], "agent-ready-heavy")
    assert.Contains(t, mock.Labels["3"], "agent-ready")
}

func TestPromoter_ResolveModel(t *testing.T) {
    p := NewPromoter(nil, ModelRouting{
        DefaultModel: "claude-sonnet-4-6",
        Rules: []RoutingRule{
            {Labels: []string{"frontend"}, Model: "claude-opus-4-6"},
            {Labels: []string{"docs", "config"}, Model: "claude-haiku-4-5"},
        },
    })

    assert.Equal(t, "claude-opus-4-6", p.ResolveModel(types.Issue{Labels: []string{"frontend"}}))
    assert.Equal(t, "claude-haiku-4-5", p.ResolveModel(types.Issue{Labels: []string{"docs"}}))
    assert.Equal(t, "claude-sonnet-4-6", p.ResolveModel(types.Issue{Labels: []string{"backend"}}))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestPromoter -v`
Expected: FAIL

- [ ] **Step 3: Implement Promoter**

```go
// internal/wave/promoter.go
package wave

import (
    "context"

    "github.com/charmbracelet/log"
    "github.com/junhoyeo/contrabass/internal/tracker"
    "github.com/junhoyeo/contrabass/internal/types"
)

type Promoter struct {
    tracker      tracker.Tracker
    modelRouting ModelRouting
    logger       *log.Logger
}

func NewPromoter(t tracker.Tracker, routing ModelRouting) *Promoter {
    return &Promoter{tracker: t, modelRouting: routing}
}

func (p *Promoter) PromoteWave(ctx context.Context, wave Wave, allIssues map[string]types.Issue) ([]string, error) {
    lm, hasLM := p.tracker.(tracker.LabelManager)
    if !hasLM {
        // Tracker doesn't support labels — skip silently
        return wave.Issues, nil
    }

    var promoted []string
    for _, issueID := range wave.Issues {
        issue := allIssues[issueID]
        label := p.modelRouting.DefaultLabel
        if label == "" {
            label = "agent-ready"
        }
        if p.isHeavy(issue) {
            heavy := p.modelRouting.HeavyLabel
            if heavy == "" {
                heavy = "agent-ready-heavy"
            }
            label = heavy
        }
        if err := lm.AddLabel(ctx, issueID, label); err != nil {
            return promoted, err
        }
        promoted = append(promoted, issueID)
    }
    return promoted, nil
}

func (p *Promoter) DemoteIssue(ctx context.Context, issueID string) error {
    lm, ok := p.tracker.(tracker.LabelManager)
    if !ok {
        return nil
    }
    _ = lm.RemoveLabel(ctx, issueID, p.modelRouting.DefaultLabel)
    _ = lm.RemoveLabel(ctx, issueID, p.modelRouting.HeavyLabel)
    return nil
}

func (p *Promoter) ResolveModel(issue types.Issue) string {
    for _, rule := range p.modelRouting.Rules {
        for _, ruleLabel := range rule.Labels {
            for _, issueLabel := range issue.Labels {
                if ruleLabel == issueLabel {
                    return rule.Model
                }
            }
        }
    }
    if p.modelRouting.DefaultModel != "" {
        return p.modelRouting.DefaultModel
    }
    return ""
}

func (p *Promoter) isHeavy(issue types.Issue) bool {
    for _, rule := range p.modelRouting.Rules {
        if !rule.Heavy {
            continue
        }
        for _, ruleLabel := range rule.Labels {
            for _, issueLabel := range issue.Labels {
                if ruleLabel == issueLabel {
                    return true
                }
            }
        }
    }
    return false
}
```

- [ ] **Step 4: Run tests**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestPromoter -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /home/booster/tools/contrabass
git add internal/wave/promoter.go internal/wave/promoter_test.go
git commit -m "feat(wave): add Promoter with label management and model routing"
```

---

### Task 6: Stall detector

**Files:**
- Create: `internal/wave/stall.go`
- Create: `internal/wave/stall_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/wave/stall_test.go
package wave

import (
    "testing"
    "time"

    "github.com/junhoyeo/contrabass/internal/types"
    "github.com/stretchr/testify/assert"
)

func TestStallDetector_CheckIssue_Continue(t *testing.T) {
    d := NewStallDetector(StallConfig{MaxAgeMinutes: 45, MaxRetries: 3})
    info := RunInfo{StartTime: time.Now(), Phase: types.StreamingTurn, Attempt: 1}
    assert.Equal(t, Continue, d.CheckIssue(info))
}

func TestStallDetector_CheckIssue_Escalate(t *testing.T) {
    d := NewStallDetector(StallConfig{MaxAgeMinutes: 45, MaxRetries: 3})
    info := RunInfo{StartTime: time.Now(), Phase: types.Failed, Attempt: 4}
    assert.Equal(t, Escalate, d.CheckIssue(info))
}

func TestStallDetector_CheckIssue_Retry(t *testing.T) {
    d := NewStallDetector(StallConfig{MaxAgeMinutes: 45, MaxRetries: 3})
    info := RunInfo{StartTime: time.Now(), Phase: types.Failed, Attempt: 2}
    assert.Equal(t, Retry, d.CheckIssue(info))
}
```

- [ ] **Step 2: Implement**

```go
// internal/wave/stall.go
package wave

import (
    "time"

    "github.com/junhoyeo/contrabass/internal/types"
)

type StallAction int

const (
    Continue  StallAction = iota
    Retry
    Escalate
)

// RunInfo is a wave-package-owned abstraction to avoid importing orchestrator's runEntry.
type RunInfo struct {
    StartTime   time.Time
    LastEventAt time.Time
    Phase       types.RunPhase
    Attempt     int
}

type StallDetector struct {
    config StallConfig
}

func NewStallDetector(cfg StallConfig) *StallDetector {
    return &StallDetector{config: cfg}
}

func (d *StallDetector) CheckIssue(info RunInfo) StallAction {
    maxRetries := d.config.MaxRetries
    if maxRetries <= 0 {
        maxRetries = 3
    }

    if info.Attempt > maxRetries {
        return Escalate
    }

    if info.Phase == types.Failed || info.Phase == types.TimedOut || info.Phase == types.Stalled {
        return Retry
    }

    return Continue
}

func (d *StallDetector) CheckWave(wave Wave, running map[string]RunInfo) *Event {
    if len(running) == 0 {
        return nil
    }

    maxAge := d.config.WaveMaxAge()
    now := time.Now()
    var stalledIssues []string

    for _, issueID := range wave.Issues {
        info, ok := running[issueID]
        if !ok {
            continue
        }
        if now.Sub(info.StartTime) > maxAge {
            stalledIssues = append(stalledIssues, issueID)
        }
    }

    if len(stalledIssues) > 0 {
        return &Event{
            Timestamp: now,
            Type:      WaveEventStallDetected,
            Issues:    stalledIssues,
        }
    }
    return nil
}
```

- [ ] **Step 3: Run tests**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestStallDetector -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
cd /home/booster/tools/contrabass
git add internal/wave/stall.go internal/wave/stall_test.go
git commit -m "feat(wave): add unified StallDetector with issue and wave level checks"
```

---

### Task 7: Wave Manager (FilterDispatchable, OnIssueCompleted, Refresh)

**Files:**
- Create: `internal/wave/manager.go`
- Create: `internal/wave/manager_test.go`

- [ ] **Step 1: Write failing test — FilterDispatchable**

```go
// internal/wave/manager_test.go
package wave

import (
    "context"
    "testing"

    "github.com/junhoyeo/contrabass/internal/tracker"
    "github.com/junhoyeo/contrabass/internal/types"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestManager_FilterDispatchable_BlocksDeps(t *testing.T) {
    mock := tracker.NewMockTracker()
    mgr, err := NewManager(mock, "", nil)
    require.NoError(t, err)

    // Issue 2 depends on 1. Both are open.
    issues := []types.Issue{
        {ID: "1", State: types.Unclaimed},
        {ID: "2", State: types.Unclaimed, BlockedBy: []string{"1"}},
    }
    mgr.Refresh(issues)

    result := mgr.FilterDispatchable(context.Background(), issues)
    // Only issue 1 should be dispatchable (2 is blocked by 1 which is still open)
    assert.Len(t, result, 1)
    assert.Equal(t, "1", result[0].ID)
}

func TestManager_FilterDispatchable_DepSatisfied(t *testing.T) {
    mock := tracker.NewMockTracker()
    mgr, err := NewManager(mock, "", nil)
    require.NoError(t, err)

    // Issue 2 depends on 1. Only issue 2 is in open set (1 is closed/absent).
    issues := []types.Issue{
        {ID: "2", State: types.Unclaimed, BlockedBy: []string{"1"}},
    }
    mgr.Refresh(issues)

    result := mgr.FilterDispatchable(context.Background(), issues)
    // Issue 1 is absent from open set → satisfied → issue 2 is dispatchable
    assert.Len(t, result, 1)
    assert.Equal(t, "2", result[0].ID)
}

func TestManager_FilterDispatchable_WaveOrdered(t *testing.T) {
    mock := tracker.NewMockTracker()
    mgr, err := NewManager(mock, "", nil)
    require.NoError(t, err)

    // Diamond: 1 → 2,3 → 4. All independent (no open deps).
    // Only 1 is in open set, rest have deps absent (satisfied).
    issues := []types.Issue{
        {ID: "4", State: types.Unclaimed, BlockedBy: []string{"2", "3"}},
        {ID: "1", State: types.Unclaimed},
        {ID: "3", State: types.Unclaimed, BlockedBy: []string{"1"}},
        {ID: "2", State: types.Unclaimed, BlockedBy: []string{"1"}},
    }
    mgr.Refresh(issues)

    result := mgr.FilterDispatchable(context.Background(), issues)
    // Only issue 1 is dispatchable (2,3 blocked by 1; 4 blocked by 2,3)
    require.Len(t, result, 1)
    assert.Equal(t, "1", result[0].ID)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestManager -v`
Expected: FAIL

- [ ] **Step 3: Implement Manager**

```go
// internal/wave/manager.go
package wave

import (
    "context"
    "sort"
    "sync"
    "time"

    "github.com/charmbracelet/log"
    "github.com/junhoyeo/contrabass/internal/tracker"
    "github.com/junhoyeo/contrabass/internal/types"
)

// PromotionResult delivered asynchronously after verification.
type PromotionResult struct {
    Promoted []string
    Err      error
}

type mergedCacheEntry struct {
    merged  bool
    checked time.Time
}

type Manager struct {
    tracker    tracker.Tracker
    pipeline   *Pipeline
    eventLog   *EventLog
    stall      *StallDetector
    promoter   *Promoter
    logger     *log.Logger
    configPath string

    mu          sync.RWMutex
    openSet     map[string]bool
    mergedCache map[string]mergedCacheEntry
}

func NewManager(t tracker.Tracker, configPath string, logger *log.Logger) (*Manager, error) {
    var cfg *WaveConfig
    if configPath != "" {
        var err error
        cfg, err = ParseConfig(configPath)
        if err != nil {
            return nil, err
        }
    }

    routing := ModelRouting{DefaultLabel: "agent-ready", HeavyLabel: "agent-ready-heavy"}
    stallCfg := StallConfig{MaxAgeMinutes: 45, WaveMaxAgeMinutes: 180, MaxRetries: 3}
    if cfg != nil {
        routing = cfg.ModelRouting
        stallCfg = cfg.StallDetection
    }

    m := &Manager{
        tracker:     t,
        configPath:  configPath,
        promoter:    NewPromoter(t, routing),
        stall:       NewStallDetector(stallCfg),
        mergedCache: make(map[string]mergedCacheEntry),
    }

    if cfg != nil {
        m.pipeline = &Pipeline{Config: cfg}
    }

    return m, nil
}

func (m *Manager) Refresh(issues []types.Issue) error {
    dag, err := BuildDAG(issues)
    if err != nil {
        return err
    }

    m.mu.Lock()
    defer m.mu.Unlock()

    m.openSet = make(map[string]bool, len(issues))
    for _, issue := range issues {
        m.openSet[issue.ID] = true
    }

    var cfg *WaveConfig
    if m.pipeline != nil {
        cfg = m.pipeline.Config
    }
    pipeline, err := MergeWithDAG(cfg, dag)
    if err != nil {
        return err
    }
    pipeline.Config = cfg
    m.pipeline = pipeline

    return nil
}

func (m *Manager) FilterDispatchable(ctx context.Context, issues []types.Issue) []types.Issue {
    m.mu.RLock()
    dag := m.pipeline.DAG
    openSet := m.openSet
    m.mu.RUnlock()

    if dag == nil {
        return issues
    }

    var result []types.Issue
    for _, issue := range issues {
        if issue.State != types.Unclaimed {
            continue
        }
        node, ok := dag.Nodes[issue.ID]
        if !ok {
            result = append(result, issue)
            continue
        }
        blocked := false
        for _, depID := range node.BlockedBy {
            if openSet[depID] {
                blocked = true
                break
            }
            // P3: verify closed dep was actually merged
            if m.shouldVerifyMerge(depID) {
                if pv, ok := m.tracker.(tracker.PRVerifier); ok {
                    merged := m.checkMergedCached(ctx, pv, depID)
                    if !merged {
                        blocked = true
                        break
                    }
                }
            }
        }
        if !blocked {
            result = append(result, issue)
        }
    }

    // P5: sort by wave index (ascending), then by Blocks count (descending)
    sort.Slice(result, func(i, j int) bool {
        wi := m.waveIndex(result[i].ID)
        wj := m.waveIndex(result[j].ID)
        if wi != wj {
            return wi < wj
        }
        bi := len(dag.Nodes[result[i].ID].Blocks)
        bj := len(dag.Nodes[result[j].ID].Blocks)
        return bi > bj
    })

    return result
}

func (m *Manager) OnIssueCompleted(ctx context.Context, issueID string) <-chan PromotionResult {
    ch := make(chan PromotionResult, 1)

    m.mu.Lock()
    delete(m.openSet, issueID)
    if m.pipeline != nil && m.pipeline.DAG != nil {
        if node, ok := m.pipeline.DAG.Nodes[issueID]; ok {
            node.State = types.Released // mark as done
        }
    }
    pipeline := m.pipeline
    m.mu.Unlock()

    if pipeline == nil {
        ch <- PromotionResult{}
        return ch
    }

    // Check if current wave is complete
    go func() {
        defer close(ch)
        currentWave := m.findCurrentWave()
        if currentWave == nil {
            ch <- PromotionResult{}
            return
        }

        // Check all issues in wave are done (not in open set)
        m.mu.RLock()
        allDone := true
        for _, id := range currentWave.Issues {
            if m.openSet[id] {
                allDone = false
                break
            }
        }
        m.mu.RUnlock()

        if !allDone {
            ch <- PromotionResult{}
            return
        }

        // Verification delay
        verifDelay := 30 * time.Second
        if pipeline.Config != nil && pipeline.Config.Verification.PropagationDelay > 0 {
            verifDelay = time.Duration(pipeline.Config.Verification.PropagationDelay) * time.Second
        }
        time.Sleep(verifDelay)

        // Promote next wave
        nextWave := m.findNextWave()
        if nextWave == nil {
            ch <- PromotionResult{}
            return
        }

        allIssues := make(map[string]types.Issue)
        m.mu.RLock()
        for id := range m.pipeline.DAG.Nodes {
            node := m.pipeline.DAG.Nodes[id]
            allIssues[id] = types.Issue{ID: id, Labels: node.Labels}
        }
        m.mu.RUnlock()

        promoted, err := m.promoter.PromoteWave(ctx, *nextWave, allIssues)
        ch <- PromotionResult{Promoted: promoted, Err: err}
    }()

    return ch
}

func (m *Manager) CheckIssueStall(info RunInfo) StallAction {
    return m.stall.CheckIssue(info)
}

func (m *Manager) EscalateIssue(ctx context.Context, issueID string) {
    m.promoter.DemoteIssue(ctx, issueID)
    m.tracker.PostComment(ctx, issueID,
        "⚠️ Agent failed after maximum retries. Manual intervention required.")
    if m.eventLog != nil {
        m.eventLog.Emit(Event{
            Type:    WaveEventStallDetected,
            IssueID: issueID,
        })
    }
}

func (m *Manager) waveIndex(issueID string) int {
    m.mu.RLock()
    defer m.mu.RUnlock()
    if m.pipeline == nil {
        return 0
    }
    for _, phase := range m.pipeline.Phases {
        for _, wave := range phase.Waves {
            for _, id := range wave.Issues {
                if id == issueID {
                    return wave.Index
                }
            }
        }
    }
    return 999 // not in config → low priority
}

func (m *Manager) findCurrentWave() *Wave {
    m.mu.RLock()
    defer m.mu.RUnlock()
    if m.pipeline == nil {
        return nil
    }
    for _, phase := range m.pipeline.Phases {
        for i := range phase.Waves {
            for _, id := range phase.Waves[i].Issues {
                if m.openSet[id] {
                    return &phase.Waves[i]
                }
            }
        }
    }
    return nil
}

func (m *Manager) findNextWave() *Wave {
    m.mu.RLock()
    defer m.mu.RUnlock()
    if m.pipeline == nil {
        return nil
    }
    foundCurrent := false
    for _, phase := range m.pipeline.Phases {
        for i := range phase.Waves {
            if !foundCurrent {
                allDone := true
                for _, id := range phase.Waves[i].Issues {
                    if m.openSet[id] {
                        allDone = false
                        break
                    }
                }
                if allDone {
                    foundCurrent = true
                    continue
                }
                return nil // current wave not done
            }
            return &phase.Waves[i]
        }
    }
    return nil
}

func (m *Manager) shouldVerifyMerge(depID string) bool {
    m.mu.RLock()
    defer m.mu.RUnlock()
    if entry, ok := m.mergedCache[depID]; ok {
        if entry.merged {
            return false // immutable cache hit
        }
        if time.Since(entry.checked) < 60*time.Second {
            return false // TTL not expired
        }
    }
    return true
}

func (m *Manager) checkMergedCached(ctx context.Context, pv tracker.PRVerifier, depID string) bool {
    merged, err := pv.HasMergedPR(ctx, depID)
    if err != nil {
        return true // on error, assume merged (don't block)
    }
    m.mu.Lock()
    m.mergedCache[depID] = mergedCacheEntry{merged: merged, checked: time.Now()}
    m.mu.Unlock()
    return merged
}
```

- [ ] **Step 4: Run tests**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
cd /home/booster/tools/contrabass
git add internal/wave/manager.go internal/wave/manager_test.go
git commit -m "feat(wave): add Manager with FilterDispatchable, OnIssueCompleted, wave promotion"
```

---

## Chunk 4: Orchestrator Integration + Token Optimizations

### Task 8: AgentRunner signature change (RunOptions)

**Files:**
- Modify: `internal/agent/runner.go:12` — add RunOptions, change Start signature
- Modify: ALL agent implementations (claude, codex, opencode, ohmyopencode, omc, omx, tmux_runner, teamcli, mock)
- Modify: ALL callers of agent.Start

- [ ] **Step 1: Add RunOptions type and update interface**

```go
// internal/agent/runner.go — add before AgentRunner interface

// RunOptions carries per-dispatch configuration.
type RunOptions struct {
    MaxTurns      int
    ModelOverride string
    IsRetry       bool
    PrevError     string
    PrevPhase     string
    Attempt       int
}
```

Update `Start` signature:
```go
Start(ctx context.Context, issue types.Issue, workspace string, prompt string, opts *RunOptions) (*AgentProcess, error)
```

- [ ] **Step 2: Update all agent implementations to accept *RunOptions**

For each file, change the Start method signature. Only `claude.go` uses the fields;
others accept and ignore `opts`:

Files to update (mechanical — add `opts *RunOptions` parameter):
- `internal/agent/claude.go` — use opts.MaxTurns, opts.ModelOverride
- `internal/agent/codex.go` — accept, ignore
- `internal/agent/opencode.go` — accept, ignore
- `internal/agent/ohmyopencode.go` — accept, ignore
- `internal/agent/omc.go` — accept, pass to inner
- `internal/agent/omx.go` — accept, pass to inner
- `internal/agent/tmux_runner.go` — accept, pass to inner runner.Start
- `internal/agent/teamcli.go` — accept, pass to inner
- `internal/agent/mock.go` — accept, store for test assertions

- [ ] **Step 3: Update ALL callers (complete list)**

Every call site must add `nil` as 5th arg (or real opts for orchestrator):

**Implementations (change signature):**
- `internal/agent/claude.go:71` — use opts.MaxTurns, opts.ModelOverride
- `internal/agent/codex.go:90` — accept, ignore
- `internal/agent/opencode.go:427` — accept, ignore
- `internal/agent/ohmyopencode.go:113` — accept, pass to inner (line 114)
- `internal/agent/omc.go` — accept, pass to inner
- `internal/agent/omx.go` — accept, pass to inner
- `internal/agent/tmux_runner.go:111` — accept, pass to inner (line ~130)
- `internal/agent/teamcli.go:177` — accept, pass to inner
- `internal/agent/mock.go:25` — accept, store for test assertions

**Callers (add nil or opts):**
- `internal/orchestrator/orchestrator.go:335` — will pass real opts (Task 9)
- `cmd/contrabass/team_worker.go:230` — pass nil

**Test callers (add nil):**
- `internal/orchestrator/orchestrator_test.go:327`
- `tests/e2e/fake_agent_test.go:35`
- `tests/e2e/smoke_test.go:87`

**Note:** If compilation fails after this step, run:
`grep -rn '\.Start(ctx\|\.Start(runCtx' --include="*.go" . | grep -v opts`
to find any remaining callers.

- [ ] **Step 4: Run all tests**

Run: `cd /home/booster/tools/contrabass && go test ./... -count=1`
Expected: ALL PASS (nil opts = use defaults everywhere)

- [ ] **Step 5: Commit**

```bash
cd /home/booster/tools/contrabass
git add internal/agent/ internal/orchestrator/ internal/team/ cmd/contrabass/
git commit -m "refactor(agent): extend Start with *RunOptions for token optimization"
```

---

### Task 9: Orchestrator integration (3 points + P1+P9 + P2 + P4)

**Files:**
- Modify: `internal/orchestrator/orchestrator.go` — wave field, SetWaveManager, runCycle
- Modify: `internal/orchestrator/orchestrator_runtime.go` — completeRun (success + failure paths)
- Modify: `internal/config/template.go` — retry-aware prompt bindings

- [ ] **Step 1: Add wave.Manager to Orchestrator**

```go
// internal/orchestrator/orchestrator.go — add to struct
import "github.com/junhoyeo/contrabass/internal/wave"

type Orchestrator struct {
    // ... existing ...
    wave   *wave.Manager
    waveWg sync.WaitGroup
}

func (o *Orchestrator) SetWaveManager(wm *wave.Manager) {
    o.wave = wm
}
```

- [ ] **Step 2: Integration point 1+2 — Refresh + FilterDispatchable in runCycle**

Modify `runCycle` after FetchIssues + cache, before dispatchUnclaimedIssues:

```go
// After issuesByID cache population:
if o.wave != nil {
    o.wave.Refresh(issues)
}

// Before dispatchUnclaimedIssues — filter:
dispatchable := issues
if o.wave != nil {
    dispatchable = o.wave.FilterDispatchable(ctx, issues)
}
// Pass dispatchable instead of issues to dispatchUnclaimedIssues
```

- [ ] **Step 3: Integration point 3 — success path in completeRun**

In `orchestrator_runtime.go`, inside `completeRun`, after `postCompletionPushAndPR`:

```go
if finalAttempt.Phase == types.Succeeded {
    o.postCompletionPushAndPR(ctx, entry.workspace, entry.issue)

    // Wave notification (non-blocking)
    if o.wave != nil {
        resultCh := o.wave.OnIssueCompleted(ctx, issueID)
        o.waveWg.Add(1)
        go func() {
            defer o.waveWg.Done()
            result := <-resultCh
            if result.Err != nil {
                o.logger.Warn("wave promotion failed", "issue_id", issueID, "err", result.Err)
                return
            }
            for _, id := range result.Promoted {
                o.emitEvent(OrchestratorEvent{
                    Type:      EventWavePromoted,
                    IssueID:   id,
                    Timestamp: time.Now(),
                    Data:      WavePromoted{IssueID: id},
                })
            }
        }()
    }
}
```

- [ ] **Step 4: P1+P9 — stall-retry bridge in failure path**

In `completeRun`, before `enqueueBackoffFromRunResult`:

```go
if o.wave != nil {
    action := o.wave.CheckIssueStall(wave.RunInfo{
        StartTime:   entry.attempt.StartTime,
        LastEventAt: entry.lastEventAt,
        Phase:       finalAttempt.Phase,
        Attempt:     finalAttempt.Attempt,
    })
    if action == wave.Escalate {
        o.wave.EscalateIssue(ctx, issueID)
        o.tracker.PostComment(ctx, issueID, fmt.Sprintf(
            "Agent escalated: phase=%s attempt=%d error=%q",
            finalAttempt.Phase, finalAttempt.Attempt, finalAttempt.Error))
        o.releaseIssue(ctx, issueID, types.Running, finalAttempt.Attempt)
        return
    }
}
o.enqueueBackoffFromRunResult(ctx, entry.issue, finalAttempt)
```

- [ ] **Step 5: P4 — progressive max_turns in dispatchIssue**

In `dispatchIssue`, before `agent.Start`:

```go
var opts *agent.RunOptions
if o.wave != nil {
    maxTurns := o.currentConfig().ClaudeMaxTurns()
    opts = &agent.RunOptions{
        MaxTurns:      effectiveMaxTurns(maxTurns, attemptNumber),
        ModelOverride: o.wave.ResolveModelForIssue(issue),
        IsRetry:       attemptNumber > 1,
        Attempt:       attemptNumber,
    }
    // P2: add previous error context for retries
    if attemptNumber > 1 {
        // Get from backoff entry
        for _, b := range o.backoff {
            if b.IssueID == issue.ID {
                opts.PrevError = b.Error
                break
            }
        }
    }
}
process, err := o.agent.Start(runCtx, issue, workspacePath, prompt, opts)
```

Add helper:
```go
func effectiveMaxTurns(base int, attempt int) int {
    if base <= 0 {
        base = 100
    }
    switch attempt {
    case 1:
        return base
    case 2:
        return base * 7 / 10
    default:
        return base / 2
    }
}
```

- [ ] **Step 6: P2 — retry-aware prompt context**

DO NOT change `RenderPrompt` signature (would create circular import config→agent).
Instead, build retry context in the orchestrator before rendering:

In `orchestrator.go`, in `dispatchIssue`, before `config.RenderPrompt`:

```go
// P2: inject retry context into issue description for template access
issueForPrompt := issue
if opts != nil && opts.IsRetry {
    retryBlock := fmt.Sprintf(
        "\n\n## RETRY CONTEXT — Attempt %d\n\nPrevious attempt failed: %s\nError: %s\n\nIMPORTANT: Try a DIFFERENT approach.\n",
        opts.Attempt, opts.PrevPhase, opts.PrevError)
    issueForPrompt.Description = issue.Description + retryBlock
}
prompt, err := config.RenderPrompt(cfg.PromptTemplate, issueForPrompt)
```

This approach:
- Keeps `RenderPrompt` signature unchanged (no circular import)
- No changes to `template_test.go`
- Retry context flows naturally through existing `{{ issue.description }}` template variable
- WORKFLOW.md templates work without modification

- [ ] **Step 7: Add waveWg.Wait() to gracefulShutdown**

In `gracefulShutdown`:
```go
o.waveWg.Wait()
```

- [ ] **Step 8: P6 — fail-fast pre-flight hooks in dispatchIssue**

In `dispatchIssue`, after workspace.Create, before prompt render:

```go
if hook := o.currentConfig().HookBeforeRun(); hook != "" {
    cmd := exec.CommandContext(ctx, "sh", "-c", hook)
    cmd.Dir = workspacePath
    if err := cmd.Run(); err != nil {
        logging.LogIssueEvent(o.logger, issue.ID, "preflight_failed", "hook", hook, "err", err)
        o.workspace.Cleanup(ctx, issue.ID)
        o.enqueueContinuation(issue.ID, attemptNumber, "preflight: "+err.Error())
        return
    }
}
```

- [ ] **Step 9: Run all tests**

Run: `cd /home/booster/tools/contrabass && go test ./... -count=1`
Expected: ALL PASS

- [ ] **Step 10: Commit**

```bash
cd /home/booster/tools/contrabass
git add internal/orchestrator/ internal/config/template.go
git commit -m "feat(orchestrator): integrate wave manager with token optimizations P1-P6"
```

---

## Chunk 5: Health Check + CLI + Web API

### Task 10: Health check and reconcile

**Files:**
- Create: `internal/wave/health.go`
- Create: `internal/wave/health_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/wave/health_test.go
package wave

import (
    "context"
    "testing"

    "github.com/junhoyeo/contrabass/internal/tracker"
    "github.com/junhoyeo/contrabass/internal/types"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestHealthCheck_AllGood(t *testing.T) {
    mock := tracker.NewMockTracker()
    mgr, _ := NewManager(mock, "", nil)
    issues := []types.Issue{{ID: "1", State: types.Unclaimed}}
    mgr.Refresh(issues)

    results := mgr.HealthCheck(context.Background())
    for _, r := range results {
        assert.True(t, r.OK, "check %s failed: %s", r.Name, r.Message)
    }
}
```

- [ ] **Step 2: Implement**

```go
// internal/wave/health.go
package wave

import "context"

type HealthResult struct {
    Name    string
    OK      bool
    Message string
}

func (m *Manager) HealthCheck(ctx context.Context) []HealthResult {
    var results []HealthResult

    // 1. DAG validation
    m.mu.RLock()
    dag := m.pipeline.DAG
    m.mu.RUnlock()

    if dag != nil {
        errs := dag.Validate()
        if len(errs) == 0 {
            results = append(results, HealthResult{Name: "dag", OK: true, Message: "no cycles, no missing refs"})
        } else {
            results = append(results, HealthResult{Name: "dag", OK: false, Message: errs[0].Error()})
        }
    }

    // 2. Config validation
    if m.configPath != "" {
        cfg, err := ParseConfig(m.configPath)
        if err != nil {
            results = append(results, HealthResult{Name: "config", OK: false, Message: err.Error()})
        } else if cfg != nil {
            results = append(results, HealthResult{Name: "config", OK: true, Message: "valid"})
        }
    }

    // 3. Label consistency (no agent-ready on issues not in open set)
    // This requires fetching from tracker — simplified for now
    results = append(results, HealthResult{Name: "labels", OK: true, Message: "check skipped in offline mode"})

    return results
}
```

- [ ] **Step 3: Run tests, commit**

Run: `cd /home/booster/tools/contrabass && go test ./internal/wave/ -run TestHealth -v`

```bash
git add internal/wave/health.go internal/wave/health_test.go
git commit -m "feat(wave): add HealthCheck"
```

---

### Task 11: CLI subcommands

**Files:**
- Create: `cmd/contrabass/wave_cmd.go`
- Modify: `cmd/contrabass/main.go:112` — register waveCmd

- [ ] **Step 1: Create wave subcommand with status/health/reconcile/promote**

```go
// cmd/contrabass/wave_cmd.go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/spf13/cobra"
    "github.com/junhoyeo/contrabass/internal/tracker"
    "github.com/junhoyeo/contrabass/internal/wave"
    "github.com/junhoyeo/contrabass/internal/logging"
)

var waveCmd = &cobra.Command{
    Use:   "wave",
    Short: "Wave pipeline management",
}

var waveStatusCmd = &cobra.Command{
    Use:   "status",
    Short: "Show current pipeline state",
    RunE: func(cmd *cobra.Command, args []string) error {
        configPath, _ := cmd.Flags().GetString("wave-config")
        return runWaveStatus(configPath)
    },
}

var waveHealthCmd = &cobra.Command{
    Use:   "health",
    Short: "Check pipeline integrity",
    RunE: func(cmd *cobra.Command, args []string) error {
        configPath, _ := cmd.Flags().GetString("wave-config")
        return runWaveHealth(configPath)
    },
}

var waveReconcileCmd = &cobra.Command{
    Use:   "reconcile",
    Short: "Compare wave-config with GitHub state, show drift",
    RunE: func(cmd *cobra.Command, args []string) error {
        configPath, _ := cmd.Flags().GetString("wave-config")
        apply, _ := cmd.Flags().GetBool("apply")
        return runWaveReconcile(configPath, apply)
    },
}

var wavePromoteCmd = &cobra.Command{
    Use:   "promote",
    Short: "Manually promote next wave (requires --config for tracker access)",
    RunE: func(cmd *cobra.Command, args []string) error {
        fmt.Println("Wave promote requires running orchestrator context. Use with --config.")
        return nil
    },
}

func init() {
    waveCmd.PersistentFlags().String("wave-config", "wave-config.yaml", "path to wave-config.yaml")
    waveReconcileCmd.Flags().Bool("apply", false, "apply fixes (default: dry-run)")
    waveCmd.AddCommand(waveStatusCmd, waveHealthCmd, waveReconcileCmd, wavePromoteCmd)
}

func runWaveReconcile(configPath string, apply bool) error {
    cfg, err := wave.ParseConfig(configPath)
    if err != nil {
        return fmt.Errorf("parse config: %w", err)
    }
    if cfg == nil {
        fmt.Println("No wave-config.yaml found.")
        return nil
    }

    // Collect all issue IDs from config
    var configIssues []string
    for _, phase := range cfg.Phases {
        for _, w := range phase.Waves {
            configIssues = append(configIssues, w.Issues...)
        }
    }

    fmt.Printf("Wave config: %d issues across %d phases\n", len(configIssues), len(cfg.Phases))
    if !apply {
        fmt.Println("\nDry-run mode. Use --apply to make changes.")
    }
    // Full reconcile requires GitHub tracker — for offline mode, just validate config structure
    fmt.Println("\nOffline reconcile (no tracker):")
    // Build DAG from config issues (no deps available offline)
    fmt.Printf("  Issues in config: %v\n", configIssues)
    fmt.Println("  For full reconcile with GitHub state, run with --config pointing to WORKFLOW.md")
    return nil
}

func runWaveStatus(configPath string) error {
    cfg, err := wave.ParseConfig(configPath)
    if err != nil {
        return err
    }
    if cfg == nil {
        fmt.Println("No wave-config.yaml found. Use --wave-config to specify path.")
        return nil
    }
    for _, phase := range cfg.Phases {
        fmt.Printf("\n%s\n", phase.Name)
        for i, w := range phase.Waves {
            desc := w.Description
            if desc == "" {
                desc = fmt.Sprintf("Wave %d", i+1)
            }
            fmt.Printf("  %s: %v\n", desc, w.Issues)
        }
    }
    return nil
}

func runWaveHealth(configPath string) error {
    logger := logging.NewLogger(logging.LogOptions{Prefix: "wave"})
    mgr, err := wave.NewManager(nil, configPath, logger)
    if err != nil {
        fmt.Printf("❌ Failed to create wave manager: %v\n", err)
        return err
    }
    results := mgr.HealthCheck(context.Background())
    for _, r := range results {
        status := "✅"
        if !r.OK {
            status = "❌"
        }
        fmt.Printf("%s %s: %s\n", status, r.Name, r.Message)
    }
    return nil
}
```

- [ ] **Step 2: Register waveCmd in main.go**

```go
// cmd/contrabass/main.go:112 — change:
cmd.AddCommand(teamCmd, boardCmd)
// to:
cmd.AddCommand(teamCmd, boardCmd, waveCmd)
```

- [ ] **Step 3: Wire wave manager in main.go run() function**

Add after tracker creation, before orchestrator:

```go
// Wave manager setup
var waveMgr *wave.Manager
waveConfigPath := filepath.Join(repoPath, "wave-config.yaml")
if _, statErr := os.Stat(waveConfigPath); statErr == nil || cfg.TrackerType() == "github" {
    waveCfgPath := ""
    if _, statErr := os.Stat(waveConfigPath); statErr == nil {
        waveCfgPath = waveConfigPath
    }
    waveMgr, err = wave.NewManager(trackerClient, waveCfgPath, logger)
    if err != nil {
        logger.Warn("wave manager init failed, continuing without", "err", err)
        waveMgr = nil
    }
}

// After orchestrator creation:
orch.SetWaveManager(waveMgr)
```

- [ ] **Step 4: Build and test**

Run: `cd /home/booster/tools/contrabass && go build ./cmd/contrabass/ && ./contrabass wave --help`
Expected: Shows wave subcommand help

- [ ] **Step 5: Commit**

```bash
cd /home/booster/tools/contrabass
git add cmd/contrabass/wave_cmd.go cmd/contrabass/main.go
git commit -m "feat(cli): add contrabass wave subcommand with status, health, promote"
```

---

### Task 12: Web API endpoints

**Files:**
- Modify: `internal/web/server.go` — add wave routes
- Modify: `internal/orchestrator/orchestrator.go` — expose wave status

- [ ] **Step 1: Add wave API routes to server.go**

In `newMux()`, add:
```go
mux.HandleFunc("GET /api/v1/wave/status", s.withCORS(s.handleWaveStatus))
mux.HandleFunc("GET /api/v1/wave/health", s.withCORS(s.handleWaveHealth))
mux.HandleFunc("GET /api/v1/wave/events", s.withCORS(s.handleWaveEvents))
```

Implement handlers:
```go
func (s *Server) handleWaveStatus(w http.ResponseWriter, r *http.Request) {
    // Get wave status from orchestrator
    // Return JSON with phases, waves, progress
    writeJSON(w, http.StatusOK, map[string]string{"status": "wave manager not yet exposed"})
}

func (s *Server) handleWaveHealth(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleWaveEvents(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, http.StatusOK, []interface{}{})
}
```

- [ ] **Step 2: Run tests**

Run: `cd /home/booster/tools/contrabass && go test ./internal/web/ -v`

- [ ] **Step 3: Commit**

```bash
cd /home/booster/tools/contrabass
git add internal/web/server.go
git commit -m "feat(web): add wave API endpoints /api/v1/wave/*"
```

---

## Chunk 6: Dashboard + P8 Token Tracking + CONTRACT.md

### Task 13: Dashboard React components

**Files:**
- Create: `packages/dashboard/src/components/WavePipeline.tsx`
- Create: `packages/dashboard/src/components/DependencyGraph.tsx`
- Create: `packages/dashboard/src/components/WaveTimeline.tsx`

- [ ] **Step 1: Create WavePipeline component**

Basic React component that renders phases, waves, and progress. Fetches from `/api/v1/wave/status`.

- [ ] **Step 2: Create DependencyGraph component**

Simple SVG DAG visualization with boxes + arrows. No external graph libraries.

- [ ] **Step 3: Create WaveTimeline component**

Timeline of wave events from `/api/v1/wave/events`.

- [ ] **Step 4: Build dashboard**

Run: `cd /home/booster/tools/contrabass && make build-dashboard`

- [ ] **Step 5: Commit**

```bash
cd /home/booster/tools/contrabass
git add packages/dashboard/src/components/
git commit -m "feat(dashboard): add WavePipeline, DependencyGraph, WaveTimeline components"
```

---

### Task 14: P8 — Token budget tracking

**Files:**
- Modify: `internal/wave/dag.go` — add token fields to Node
- Modify: `internal/wave/manager.go` — update on completion

- [ ] **Step 1: Extend Node with token tracking**

```go
// internal/wave/dag.go — add to Node struct
TotalTokensIn  int64
TotalTokensOut int64
Attempts       int
```

- [ ] **Step 2: Add UpdateTokens method to Manager**

```go
func (m *Manager) UpdateTokens(issueID string, tokensIn, tokensOut int64) {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.pipeline == nil || m.pipeline.DAG == nil {
        return
    }
    if node, ok := m.pipeline.DAG.Nodes[issueID]; ok {
        node.TotalTokensIn += tokensIn
        node.TotalTokensOut += tokensOut
        node.Attempts++
    }
}
```

- [ ] **Step 3: Call from completeRun**

After finalAttempt resolved:
```go
if o.wave != nil {
    o.wave.UpdateTokens(issueID, finalAttempt.TokensIn, finalAttempt.TokensOut)
}
```

- [ ] **Step 4: Run tests, commit**

```bash
cd /home/booster/tools/contrabass
git add internal/wave/ internal/orchestrator/
git commit -m "feat(wave): add token budget tracking per issue (P8)"
```

---

### Task 15: CONTRACT.md update + deprecation notice

**Files:**
- Modify: `~/tools/wave-manager/CONTRACT.md`

- [ ] **Step 1: Update CONTRACT.md**

Add sections:
- Auto-DAG mode description
- New CLI commands (`contrabass wave ...`)
- Wave events in JSONL and SSE
- Health check format
- Token optimization features
- Deprecation notice for bash wave-manager

- [ ] **Step 2: Add deprecation notice to wave-manager README**

- [ ] **Step 3: Commit**

```bash
cd /home/booster/tools/wave-manager
git add CONTRACT.md README.md
git commit -m "docs: update CONTRACT.md, deprecate bash wave-manager"
```

---

### Task 16: Final integration test + build

- [ ] **Step 1: Run full test suite**

Run: `cd /home/booster/tools/contrabass && go test ./... -count=1 -race`

- [ ] **Step 2: Build binary**

Run: `cd /home/booster/tools/contrabass && make build`

- [ ] **Step 3: Create test wave-config.yaml fixture**

```bash
mkdir -p testdata
cat > testdata/wave-config.yaml << 'EOF'
repo: test/repo
phases:
  - name: "Phase 1: Test"
    waves:
      - issues: ["1", "2"]
      - issues: ["3"]
model_routing:
  default_label: agent-ready
  heavy_label: agent-ready-heavy
  default_model: claude-sonnet-4-6
stall_detection:
  max_age_minutes: 45
  max_retries: 3
EOF
```

- [ ] **Step 4: Verify CLI**

```bash
./contrabass wave status --wave-config testdata/wave-config.yaml
./contrabass wave health --wave-config testdata/wave-config.yaml
./contrabass wave reconcile --wave-config testdata/wave-config.yaml
./contrabass --help
```

- [ ] **Step 5: Final commit**

```bash
cd /home/booster/tools/contrabass
git add testdata/ .
git commit -m "feat: wave manager integration complete — all tasks done"
```
