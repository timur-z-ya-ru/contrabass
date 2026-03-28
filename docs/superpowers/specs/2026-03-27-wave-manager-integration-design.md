# Wave Manager Integration into Contrabass

**Date:** 2026-03-27
**Status:** Ready for implementation (rev5 — final review fixes applied)
**Scope:** Integrate wave management (DAG, wave promotion, stall detection, health check, dashboard) into Contrabass Go binary, deprecate bash wave-manager.

---

## Context

The autonomous orchestration pipeline consists of three tools:
- **Issue Planner** (Claude skill) — decomposes projects into GitHub issues with dependency annotations
- **Wave Manager** (bash daemon, `~/tools/wave-manager/`) — manages issue progression through phases/waves, label-based activation
- **Contrabass** (Go agent runner, `~/tools/contrabass/`) — claims and executes issues via Claude Code CLI

### Problems Being Solved

1. **Contrabass ignores dependencies in GitHub mode** — `BlockedBy` field is parsed but not validated before dispatch (`orchestrator.go:dispatchUnclaimedIssues`)
2. **No feedback between components** — Contrabass doesn't tell Wave Manager about failures; Wave Manager doesn't know why issues are stuck
3. **Wave Manager is a bash script without tests** — brittle YAML parsing, GitHub API calls, state machine in shell
4. **wave-config.yaml is a manual single point of failure** — no reconciliation with actual GitHub state
5. **Duplicate stall detection** — both Wave Manager and Contrabass detect stalls independently, can conflict
6. **No end-to-end health check** — no single command to verify pipeline integrity

### Decisions Made

- **Cherry-pick from upstream PR #28** — take patterns (event system, dependency blocking, health monitoring), not code. Write own implementation adapted for wave management.
- **Auto-DAG from `Blocked by:` annotations** as default, **wave-config.yaml as optional override**
- **CONTRACT.md as source of truth** for issue body format (read by Issue Planner skill, validated by Contrabass)
- **Full scope** (not MVP): includes dashboard integration, reconcile, deprecation of bash wave-manager

---

## Architecture

### Approach: Wave as separate `internal/wave/` package + CLI subcommand

Orchestrator remains slim — calls `wave.Manager` methods at 3 integration points. Wave logic is isolated, testable, with its own CLI commands.

```
contrabass (Go binary)
├── cmd/contrabass/
│   ├── main.go          — wave.Manager injection into orchestrator
│   └── wave_cmd.go      — `contrabass wave [status|health|reconcile|promote]`
├── internal/wave/       — NEW PACKAGE
│   ├── dag.go           — DAG build, validate, topological sort → waves
│   ├── wave.go          — Wave, Phase, Pipeline structs
│   ├── config.go        — wave-config.yaml parsing, MergeWithDAG
│   ├── filter.go        — Manager: FilterDispatchable, OnIssueCompleted, Refresh
│   ├── promoter.go      — label management (agent-ready / agent-ready-heavy)
│   ├── stall.go         — unified stall detection (issue + wave level)
│   ├── health.go        — pipeline health check, reconcile
│   ├── events.go        — event types
│   ├── event_log.go     — JSONL writer + subscriber
│   └── *_test.go        — tests for each
├── internal/tracker/
│   ├── tracker.go       — +AddLabel, RemoveLabel, HasMergedPR in interface
│   └── github.go        — implementation of new methods
├── internal/orchestrator/
│   ├── orchestrator.go  — 3 integration points (Refresh, Filter, OnCompleted)
│   └── events.go        — +wave event types
├── internal/web/
│   └── server.go        — +/api/wave/* endpoints
└── packages/dashboard/
    └── src/components/  — WavePipeline, DependencyGraph, WaveTimeline
```

---

## Package `internal/wave/` — Core

### DAG

```go
// dag.go

type Node struct {
    IssueID   string
    BlockedBy []string
    Blocks    []string           // reverse edges, computed
    Labels    []string
    State     types.IssueState   // uses existing enum, not string
}

type DAG struct {
    Nodes map[string]*Node
}

// BuildDAG builds graph from issues list.
// Parses BlockedBy from types.Issue, computes reverse edges (Blocks).
func BuildDAG(issues []types.Issue) (*DAG, error)

// Validate checks: no cycles, all referenced issues exist.
func (d *DAG) Validate() []error

// ComputeWaves does topological sort and groups into waves.
// Wave 0 = issues with no dependencies.
// Wave N = issues whose all dependencies are in waves 0..N-1.
func (d *DAG) ComputeWaves() []Wave
```

### Wave, Phase, Pipeline

```go
// wave.go

type Wave struct {
    Index       int
    Issues      []string
    Description string
}

type Phase struct {
    Name      string
    Milestone string
    Epic      string
    Waves     []Wave
}

type Pipeline struct {
    Phases      []Phase
    ActivePhase int
    ActiveWave  int
    DAG         *DAG
    Config      *WaveConfig // optional override, nil = pure DAG
}
```

### WaveConfig (optional override)

```go
// config.go

type WaveConfig struct {
    Repo           string        `yaml:"repo"`
    PollInterval   int           `yaml:"poll_interval"`
    Phases         []PhaseConfig `yaml:"phases"`
    ModelRouting   ModelRouting  `yaml:"model_routing"`
    Verification   Verification  `yaml:"verification"`
    StallDetection StallConfig   `yaml:"stall_detection"`
    AutoDAG        *bool         `yaml:"auto_dag"` // default: true
}

// ParseConfig reads wave-config.yaml. Returns nil, nil if file doesn't exist (not an error).
func ParseConfig(path string) (*WaveConfig, error)

// MergeWithDAG: if config exists — grouping from config, DAG for validation.
// If config is nil — grouping from DAG.ComputeWaves().
func MergeWithDAG(cfg *WaveConfig, dag *DAG) (*Pipeline, error)
```

Format of wave-config.yaml is unchanged from bash wave-manager. Only addition: optional `auto_dag` field (default true). If `auto_dag: false` — waves taken strictly from config, no DAG computation.

---

## Orchestrator Integration

### Manager — main integration object

```go
// filter.go

type Manager struct {
    tracker    tracker.Tracker
    pipeline   *Pipeline
    eventLog   *EventLog
    stall      *StallDetector
    promoter   *Promoter
    logger     *log.Logger
    mu         sync.RWMutex
}

// NewManager creates wave manager. Does NOT require issues at construction time —
// DAG is built lazily on first Refresh() call. This matches the existing pattern
// where FetchIssues happens inside runCycle, not at orchestrator construction time.
func NewManager(tracker tracker.Tracker, configPath string, logger *log.Logger) (*Manager, error)

// FilterDispatchable returns only issues whose all dependencies are satisfied.
// "Satisfied" means: dependency issue ID is NOT in the current open issues set.
// Since FetchIssues only returns open issues (github.go filters state=open),
// a dependency that is absent from the set is either closed or doesn't exist.
// Missing refs are logged as warnings but don't block dispatch.
// Takes ctx for P3 (HasMergedPR calls on closed deps).
func (m *Manager) FilterDispatchable(ctx context.Context, issues []types.Issue) []types.Issue

// OnIssueCompleted called when issue completed successfully.
// IMPORTANT: This is non-blocking. It marks the issue as done in the DAG,
// checks wave completion, and if the wave is complete, spawns an async
// verification goroutine. Promotion results are delivered via the returned channel.
func (m *Manager) OnIssueCompleted(ctx context.Context, issueID string) <-chan PromotionResult

// PromotionResult delivered asynchronously after verification delay + HasMergedPR checks.
type PromotionResult struct {
    Promoted []string // issue IDs that got agent-ready labels
    Err      error
}

// Refresh rebuilds DAG from fresh issues (called each cycle).
func (m *Manager) Refresh(issues []types.Issue) error
```

### 3 integration points in orchestrator.go

```go
// 1. NewOrchestrator — receives optional wave.Manager
type Orchestrator struct {
    // ... existing fields ...
    wave *wave.Manager // nil = wave management disabled (backward compatible)
}

// 2. runCycle — refresh + filter
func (o *Orchestrator) runCycle(...) {
    // ... existing: FetchIssues, cache ...

    if o.wave != nil {
        o.wave.Refresh(issues)
    }

    // ... existing: dispatchReadyBackoff ...

    dispatchable := issues
    if o.wave != nil {
        dispatchable = o.wave.FilterDispatchable(ctx, issues)
    }
    o.dispatchUnclaimedIssues(ctx, ..., dispatchable, ...)
}

// 3. completeRun — AFTER resolveFinalPhase, guarded by Succeeded phase.
// NOT in handleRunSignal (signal.err == nil does NOT mean success —
// resolveFinalPhase can produce Failed/Finishing even with nil doneErr).
//
// Insertion point: orchestrator_runtime.go, inside completeRun(),
// within the `if finalAttempt.Phase == types.Succeeded` block,
// after `o.postCompletionPushAndPR(ctx, entry.workspace, entry.issue)`
// and before workspace cleanup.
func (o *Orchestrator) completeRun(ctx context.Context, issueID string, doneErr error) {
    // ... existing: resolveFinalPhase, emitEvent(AgentFinished), postCompletionPushAndPR ...

    if finalAttempt.Phase == types.Succeeded {
        o.postCompletionPushAndPR(ctx, entry.workspace, entry.issue)

        // NEW: notify wave manager (non-blocking)
        if o.wave != nil {
            resultCh := o.wave.OnIssueCompleted(ctx, issueID)
            // Read result in background — don't block the main loop
            go func() {
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

    // ... existing: cleanup, comment, releaseIssue or enqueueBackoff ...
}
```

**Backward compatible:** if `wave.Manager` is nil — orchestrator works exactly as before.

### Manager wiring in main.go

```go
// cmd/contrabass/main.go — inside run(), after tracker creation, before orchestrator creation

// Wave manager: created if wave-config.yaml exists OR tracker is GitHub
// (GitHub issues may have Blocked by: annotations → auto-DAG).
var waveMgr *wave.Manager
waveConfigPath := filepath.Join(repoPath, "wave-config.yaml")
if _, err := os.Stat(waveConfigPath); err == nil || cfg.TrackerType() == "github" {
    configPath := ""
    if _, err := os.Stat(waveConfigPath); err == nil {
        configPath = waveConfigPath
    }
    waveMgr, err = wave.NewManager(trackerClient, configPath, logger)
    if err != nil {
        logger.Warn("wave manager init failed, continuing without", "err", err)
        waveMgr = nil // degrade gracefully
    }
}

// Pass to orchestrator (nil = disabled)
orch := orchestrator.NewOrchestrator(trackerClient, workspaceMgr, agentRunner, watcher, logger)
orch.SetWaveManager(waveMgr) // setter instead of constructor param to minimize blast radius
```

`SetWaveManager` avoids changing `NewOrchestrator` constructor signature (6 callers + tests).

### Background goroutine cleanup

The goroutine spawned in `completeRun` for async wave promotion must be tracked:

```go
// Orchestrator gets a WaitGroup for background wave operations
type Orchestrator struct {
    // ... existing ...
    wave   *wave.Manager
    waveWg sync.WaitGroup  // tracks background promotion goroutines
}

// In completeRun, wave notification:
o.waveWg.Add(1)
go func() {
    defer o.waveWg.Done()
    result := <-resultCh
    // ... emit events ...
}()

// In gracefulShutdown:
o.waveWg.Wait() // wait for in-flight promotions before exit
```

---

## Label Management and Wave Promotion

### Promoter

```go
// promoter.go

type Promoter struct {
    tracker      tracker.Tracker
    modelRouting ModelRouting
}

// ModelRouting handles BOTH label assignment (agent-ready vs agent-ready-heavy)
// AND model selection (haiku/sonnet/opus). Single canonical definition.
type ModelRouting struct {
    DefaultLabel string          `yaml:"default_label"`  // "agent-ready"
    HeavyLabel   string          `yaml:"heavy_label"`    // "agent-ready-heavy"
    DefaultModel string          `yaml:"default_model"`  // "claude-sonnet-4-6"
    Rules        []RoutingRule   `yaml:"rules"`
}

type RoutingRule struct {
    Labels []string `yaml:"labels"` // e.g. ["frontend", "architecture"]
    Model  string   `yaml:"model"`  // "claude-opus-4-6" or "claude-haiku-4-5"
    Heavy  bool     `yaml:"heavy"`  // if true → agent-ready-heavy label
}

// PromoteWave adds agent-ready / agent-ready-heavy labels to all issues in wave.
// Heavy: if issue has label from HeavyLabels → agent-ready-heavy, else agent-ready.
func (p *Promoter) PromoteWave(ctx context.Context, wave Wave, allIssues map[string]types.Issue) ([]string, error)

// DemoteIssue removes agent-ready labels (on stall/retry exhausted).
func (p *Promoter) DemoteIssue(ctx context.Context, issueID string) error
```

### Tracker interface extension

```go
// tracker/tracker.go — core Tracker interface UNCHANGED to avoid breaking existing code.
// New capabilities via optional interfaces (type assertion at runtime).

type Tracker interface {
    // existing — no changes
    FetchIssues(ctx context.Context) ([]types.Issue, error)
    ClaimIssue(ctx context.Context, issueID string) error
    ReleaseIssue(ctx context.Context, issueID string) error
    UpdateIssueState(ctx context.Context, issueID string, state types.IssueState) error
    PostComment(ctx context.Context, issueID string, body string) error
}

// NEW: optional interface for label management.
// Wave promoter checks: if lm, ok := tracker.(LabelManager); ok { ... }
// This avoids breaking Linear/Local/Mock implementations.
type LabelManager interface {
    AddLabel(ctx context.Context, issueID string, label string) error
    RemoveLabel(ctx context.Context, issueID string, label string) error
}

// NEW: optional interface for PR verification.
type PRVerifier interface {
    HasMergedPR(ctx context.Context, issueID string) (bool, error)
}
```

**Implementation per tracker:**

| Tracker | LabelManager | PRVerifier | Notes |
|---------|-------------|------------|-------|
| `GitHubClient` | YES — `POST /repos/{owner}/{repo}/issues/{number}/labels` | YES — timeline API | Full support |
| `LinearClient` | NO — noop, Linear doesn't use labels for dispatch | NO — noop | Wave features degrade gracefully |
| `LocalTracker` | NO — noop | NO — noop | Local board has own dispatch mechanism |
| `MockTracker` | YES — in-memory map | YES — configurable return | Required for tests |

When `LabelManager` is not implemented, `Promoter.PromoteWave()` logs a warning
and skips label operations. Wave progress still tracked in DAG — just no GitHub labels set.
When `PRVerifier` is not implemented, wave completion skips the merged-PR check
and trusts issue closed state.

### Wave completion flow

```
Issue closed (PR merged)
  → OnIssueCompleted(issueID)
    → mark node as closed in DAG
    → check: all issues in current wave closed?
      → YES:
        → verification delay (configurable, default 30s)
        → HasMergedPR() for each issue in wave
        → PromoteWave(nextWave)
        → emit WaveCompleted + WavePromoted events
      → NO:
        → emit IssueCompleted event, continue
```

---

## Event System and JSONL Log

### Event types

Two layers: wave-internal events (for JSONL log) and orchestrator events (for TUI/SSE).

**Wave-internal events** (JSONL log, CLI queries):

```go
// internal/wave/events.go — wave package's own event type for JSONL persistence

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

// Event is the wave-internal event written to JSONL. NOT the same as OrchestratorEvent.
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

**Orchestrator events** (TUI, SSE, dashboard) — extend existing `int` iota system
with typed payload structs implementing `EventPayload` marker interface:

```go
// internal/orchestrator/events.go — add to existing iota block

const (
    // ... existing ...
    // Separate const block — NOT appended to existing iota.
    // Explicit values to avoid iota confusion.
    EventWavePromoted  EventType = 100
    EventWaveCompleted EventType = 101
    EventPhaseCompleted EventType = 102
    EventPipelineDone  EventType = 103
    EventWaveStall     EventType = 104
)

// Typed payloads implementing EventPayload marker interface

type WavePromoted struct {
    IssueID string
    Phase   int
    Wave    int
    Issues  []string // all issues in the promoted wave
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

type WaveStall struct {
    Phase   int
    Wave    int
    Issues  []string // stalled issue IDs
}
func (WaveStall) eventPayload() {}
```

### EventLog

```go
// event_log.go

type EventLog struct {
    path   string // ".contrabass/wave-events.jsonl"
    file   *os.File
    mu     sync.Mutex
    notify chan Event
}

func NewEventLog(path string) (*EventLog, error)
func (l *EventLog) Emit(event Event) error  // returns error on file I/O failure; in-memory subscribers still notified
func (l *EventLog) Subscribe() <-chan Event
func (l *EventLog) Query(since time.Time, types []EventType) ([]Event, error)
```

### Orchestrator bridge

Wave events map to existing `OrchestratorEvent` type with new event type constants. They flow through the existing hub → SSE → dashboard pipeline without changes to the event infrastructure.

JSONL file `.contrabass/wave-events.jsonl` — persistent log for debugging and CLI commands. Orchestrator event channel — in-memory for real-time UI.

---

## Unified Stall Detection

### Two levels

```go
// stall.go

type StallConfig struct {
    IssueMaxAgeMinutes int  `yaml:"max_age_minutes"`  // default 45
    WaveMaxAgeMinutes  int  `yaml:"wave_max_age_minutes"` // default 180
    MaxRetries         int  `yaml:"max_retries"`       // default 3
}

// Duration helpers for Go code:
func (c StallConfig) IssueMaxAge() time.Duration { return time.Duration(c.IssueMaxAgeMinutes) * time.Minute }
func (c StallConfig) WaveMaxAge() time.Duration  { return time.Duration(c.WaveMaxAgeMinutes) * time.Minute }

type StallAction int

const (
    Continue  StallAction = iota // ok
    Retry                        // requeue to backoff
    Escalate                     // retry exhausted, needs human
)

type StallDetector struct {
    config StallConfig
    log    *EventLog
}

// CheckIssue — checks individual issue. Called from orchestrator detectStalledRuns.
func (d *StallDetector) CheckIssue(issue types.Issue, attempt types.RunAttempt) StallAction

// RunInfo is a wave-package-owned abstraction to avoid importing orchestrator's runEntry.
// Orchestrator populates this from runEntry before calling CheckWave.
type RunInfo struct {
    StartTime   time.Time
    LastEventAt time.Time
    Phase       types.RunPhase
    Attempt     int
}

// CheckWave — checks entire wave for stall.
func (d *StallDetector) CheckWave(wave Wave, running map[string]RunInfo) *StallEvent
```

### Escalation behavior

```
Issue stall (retry exhausted):
  → DemoteIssue() — remove agent-ready label
  → PostComment() — warning comment on issue
  → Emit EventStallDetected
  → Skip issue, continue wave with remaining

Wave stall (entire wave stuck):
  → Emit EventWaveStall
  → PostComment on epic issue — wave stall summary
```

Existing `orchestrator.detectStalledRuns()` delegates to `wave.StallDetector.CheckIssue()`. Orchestrator still owns retry/backoff queue — stall detector only decides action.

If wave manager is not active (no DAG, no dependencies) — fallback to current behavior: simple timeout from WORKFLOW.md.

---

## CLI Subcommands

### `contrabass wave`

```
contrabass wave status     — current pipeline state (phases, waves, progress)
contrabass wave health     — integrity check (config, DAG, labels, issues, process)
contrabass wave reconcile  — compare config vs GitHub, show drift, offer fixes
contrabass wave promote    — manual promote next wave (--force to bypass completion check)
```

**`status` output:**
```
Phase 1: Foundation (active)
  Wave 1: completed [#2, #3]
  Wave 2: in progress [#4 running, #5 queued, #6 done]
  Wave 3: pending [#7, #8, #9]
Phase 2: Core Features
  Wave 1: pending [#21, #22, #23]

Pipeline: 3/9 complete | Active agents: 1 | Stalls: 0
```

**`health` checks:**
1. wave-config.yaml parses and is valid
2. DAG has no cycles or missing refs
3. Labels are consistent (no agent-ready on closed issues)
4. All config issues exist on GitHub
5. No stuck issues (open without PR activity beyond threshold)
6. Orchestrator process alive (PID file)

**`reconcile` output:**
```
Drift detected:
  - Issue #10 in config but CLOSED (wontfix) on GitHub → suggest: remove
  - Issue #15 on GitHub with "agent" label but NOT in config → suggest: add to wave 3
  - Issue #4 in wave 2 depends on #7 from wave 3 → ERROR: invalid ordering
Apply fixes? [y/N/--dry-run/--apply]
```

Note: `reconcile` uses `--dry-run` (default, show drift) / `--apply` (fix automatically)
flags instead of interactive prompt, so it works in automated/daemon contexts.

Wave subcommands work standalone — they read wave-config.yaml and GitHub API directly, don't require running orchestrator.

---

## Dashboard Integration

### New web API endpoints

```
GET /api/v1/wave/status  — pipeline state (JSON)
GET /api/v1/wave/events  — wave events with ?since= and ?type= filters
GET /api/v1/wave/health  — health check result
```

### SSE — wave events in existing stream

Wave events already map to `OrchestratorEvent` — they automatically flow through the existing hub → SSE → dashboard pipeline.

### React components

```
packages/dashboard/src/components/
├── WavePipeline.tsx    — phases, waves, progress bars
├── DependencyGraph.tsx — DAG visualization (SVG, boxes + arrows)
└── WaveTimeline.tsx    — wave events timeline
```

Simple SVG for DAG — no heavy graph libraries. Typical project has 10-30 issues.

---

## Deprecation of bash wave-manager

### 3-stage transition

**Stage 1 (this project):** `contrabass wave` fully covers bash wave-manager functionality. CONTRACT.md updated. Bash wave-manager marked deprecated.

**Stage 2 (after field testing):** Cron/daemon switched from `wave-manager.sh` to built-in `contrabass wave`. Bash wave-manager stopped.

**Stage 3 (2-4 weeks after Stage 2):** `~/tools/wave-manager/` archived or deleted.

### Migration table

| bash module | Go equivalent |
|-------------|---------------|
| `wave.sh` (state machine) | `internal/wave/wave.go` + `Manager.OnIssueCompleted()` |
| `config.sh` (yaml parsing) | `internal/wave/config.go` |
| `github.sh` (API wrappers) | `internal/tracker/github.go` + AddLabel/RemoveLabel/HasMergedPR |
| `stall.sh` (stall detection) | `internal/wave/stall.go` |
| `model-router.sh` (heavy labels) | `internal/wave/promoter.go` ModelRouting |
| `status.sh` (reporting) | `cmd/contrabass/wave_cmd.go` status |
| `validate.sh` (config validation) | `internal/wave/config.go` Validate() + health command |

### wave-config.yaml backward compatibility

Format unchanged. Go parser reads the same YAML as bash. Only addition: optional `auto_dag` field (default true).

---

## Cherry-pick from upstream PR #28

### What we take (patterns, not code)

| PR #28 feature | What we adopt | How we use it |
|----------------|---------------|---------------|
| Event system with filtering | `EventLog` + `Subscribe()` + `Query()` pattern | `wave/event_log.go` |
| Task dependency blocking | `BlockedBy` validation at dispatch | `FilterDispatchable()` |
| Worker health heartbeat | Age tracking pattern | `StallDetector.CheckIssue()` |
| Quarantine mechanism | Exclude after N failures | `StallAction.Escalate` → DemoteIssue |

### What we skip

| PR #28 feature | Reason |
|----------------|--------|
| Inter-worker mailbox | No use case — agents are independent |
| Dynamic worker scaling | No use case — WORKFLOW.md concurrency is sufficient |
| Optimistic concurrency control | One orchestrator, no claim competition |
| JSON-RPC layer | Go interfaces, not IPC |

No `git cherry-pick`. Own implementation adapted for wave management. Avoids merge conflicts on future upstream sync.

---

## CONTRACT.md Updates

Add to existing `~/tools/wave-manager/CONTRACT.md`:
- Auto-DAG mode description
- New CLI commands (`contrabass wave ...`)
- Wave events in JSONL and SSE
- Health check format
- Deprecation notice for bash wave-manager

CONTRACT.md remains the source of truth for issue body format. Issue Planner skill reads it before generating issues.

---

## Token Optimization Engine

All 9 optimizations integrated into the wave manager and orchestrator.

### P1+P9: Retry cap + stall-retry bridge (HIGH impact)

**Problem:** `enqueueBackoffFromRunResult` (orchestrator_runtime.go:226) retries infinitely —
only backoff delay is capped, not attempt count. The spec's `StallDetector.CheckIssue`
returning `Escalate` is a separate code path and doesn't prevent the retry.

**Solution:** Wire stall detector into the completion path. In `completeRun`, when
`finalAttempt.Phase != types.Succeeded`:

```go
// orchestrator_runtime.go — inside completeRun, failure branch
if o.wave != nil {
    action := o.wave.CheckIssueStall(entry.issue, finalAttempt)
    switch action {
    case wave.Escalate:
        // Do NOT retry. Demote + comment + release.
        // EscalateIssue handles: DemoteIssue (remove agent-ready label),
        // PostComment (escalation warning), emit EventStallDetected.
        o.wave.EscalateIssue(ctx, issueID, finalAttempt)
        // Still post the standard completion comment (existing behavior)
        o.tracker.PostComment(ctx, issueID, fmt.Sprintf(
            "Agent escalated: phase=%s attempt=%d error=%q",
            finalAttempt.Phase, finalAttempt.Attempt, finalAttempt.Error))
        o.releaseIssue(ctx, issueID, types.Running, finalAttempt.Attempt)
        return  // skip enqueueBackoffFromRunResult entirely
    case wave.Retry:
        // fall through to existing retry logic
    }
}
o.enqueueBackoffFromRunResult(ctx, entry.issue, finalAttempt)
```

Without wave manager (nil) — fallback to current unbounded behavior (backward compatible).

**Estimated savings:** 150-500k tokens/pipeline (prevents 2-5 full wasted runs per broken issue).

### P2: Retry-aware prompt enrichment (HIGH impact)

**Problem:** `RenderPrompt` (template.go) produces identical prompts on retry.
Agent repeats the same failed approach.

**Solution:** Extend Liquid template bindings with retry context. Data already exists
in `BackoffEntry.Error` and `RunAttempt.Phase`:

```go
// config/template.go — extend template data
type PromptData struct {
    Issue         types.Issue
    Attempt       int
    PrevError     string    // from BackoffEntry.Error
    PrevPhase     string    // from previous RunAttempt.Phase
    IsRetry       bool      // attempt > 1
}
```

WORKFLOW.md template gets new block:

```liquid
{% if attempt > 1 %}
## RETRY CONTEXT — Attempt {{ attempt }}

Previous attempt failed at phase: {{ prev_phase }}
Error: {{ prev_error }}

IMPORTANT: Try a DIFFERENT approach. Do NOT repeat the same steps that failed.
{% endif %}
```

**Integration:** `dispatchIssue` passes `BackoffEntry` data when dispatching from backoff queue.
For fresh dispatches (attempt=1), retry fields are empty.

**Estimated savings:** ~190k tokens/pipeline (turns ~50% of retry failures into successes).

### P3: PR merge validation at dispatch (catastrophic prevention)

**Problem:** `FilterDispatchable` checks if dependency issues are absent from open set.
But an issue can be closed-as-wontfix (no merged PR) — dependents should NOT be unblocked.

**Solution:** When `PRVerifier` is available, validate at dispatch time:

```go
// wave/filter.go — inside FilterDispatchable
func (m *Manager) FilterDispatchable(issues []types.Issue) []types.Issue {
    openSet := buildOpenSet(issues)
    var result []types.Issue

    for _, issue := range issues {
        if issue.State != types.Unclaimed { continue }
        blocked := false
        for _, depID := range m.dag.Nodes[issue.ID].BlockedBy {
            if _, isOpen := openSet[depID]; isOpen {
                blocked = true; break // dep still open
            }
            // Dep not in open set — verify it was actually merged
            if pv, ok := m.tracker.(tracker.PRVerifier); ok {
                merged, _ := pv.HasMergedPR(ctx, depID)
                if !merged {
                    blocked = true; break // closed but not merged
                }
            }
        }
        if !blocked { result = append(result, issue) }
    }
    return result
}
```

**Rate limit mitigation:** Cache `HasMergedPR` results in Manager (merged=true is immutable, never expires).
Cache `merged=false` results with 60s TTL (issue may get merged between cycles).
Cache stored in `Manager.mergedCache map[string]mergedCacheEntry`.

**Estimated savings:** 50-100k tokens per occurrence (rare but catastrophic).

### P4: Progressive max_turns (MEDIUM impact)

**Problem:** Agent gets same `max_turns: 100` on every attempt. Retries should need fewer
turns — the agent has a focused fix, not a full implementation.

**Solution:** Compute effective max_turns based on attempt:

```go
// orchestrator.go — in dispatchIssue, before agent.Start
func effectiveMaxTurns(base int, attempt int) int {
    switch attempt {
    case 1:  return base          // 100
    case 2:  return base * 7 / 10 // 70
    default: return base / 2      // 50
    }
}
```

Pass as override to `agent.Start`. Requires extending `AgentRunner.Start` to accept
a `RunOptions` struct (also used by P2 for retry context):

```go
type RunOptions struct {
    MaxTurns  int
    IsRetry   bool
    PrevError string
    PrevPhase string
}
```

**Estimated savings:** 30-50k tokens per failed retry (caps wasted turns).

### P5: Wave-ordered dispatch priority (MEDIUM impact)

**Problem:** `dispatchUnclaimedIssues` iterates in fetch order. No priority awareness.

**Solution:** After `FilterDispatchable`, sort by:
1. Wave index ascending (earlier waves first — they unblock more)
2. Within wave: number of `Blocks` edges descending (issues that unblock most go first)

```go
// wave/filter.go
func (m *Manager) FilterDispatchable(issues []types.Issue) []types.Issue {
    filtered := /* ... dependency check ... */
    sort.Slice(filtered, func(i, j int) bool {
        wi := m.waveIndex(filtered[i].ID)
        wj := m.waveIndex(filtered[j].ID)
        if wi != wj { return wi < wj }
        bi := len(m.dag.Nodes[filtered[i].ID].Blocks)
        bj := len(m.dag.Nodes[filtered[j].ID].Blocks)
        return bi > bj
    })
    return filtered
}
```

**Estimated savings:** 50-100k tokens/pipeline (critical path first → fewer stall timeouts).

### P6: Fail-fast pre-flight hooks (MEDIUM impact)

**Problem:** `HooksConfig.BeforeRun` exists in config parsing (`config.go:107-111`)
but is NOT implemented. Agent starts even if codebase is in broken state.

**Solution:** Execute `before_run` hook in workspace before `agent.Start`:

```go
// orchestrator.go — in dispatchIssue, after workspace.Create, before agent.Start
if hook := cfg.Hooks.BeforeRun; hook != "" {
    cmd := exec.CommandContext(ctx, "sh", "-c", hook)
    cmd.Dir = workspacePath
    if err := cmd.Run(); err != nil {
        // Pre-flight failed — don't count as agent failure, don't retry
        logging.LogIssueEvent(o.logger, issue.ID, "preflight_failed", "hook", hook, "err", err)
        o.workspace.Cleanup(ctx, issue.ID)
        // Re-queue with short delay (code might be fixed by another agent)
        o.enqueueContinuation(issue.ID, attemptNumber, "preflight: "+err.Error())
        return
    }
}
```

WORKFLOW.md config:
```yaml
hooks:
  before_run: "go build ./..."   # or "npm run build", "make check", etc.
```

**Key:** Pre-flight failure does NOT count as an attempt (no increment). Issue
goes to continuation queue with short delay — another agent's merge might fix the build.

**Estimated savings:** 100-200k tokens/pipeline (catches 10-20% of failures before agent runs).

### P7: Model routing by complexity (HIGH cost savings)

**Problem:** Single model for all issues. Simple tasks (docs, config) burn expensive Sonnet tokens.

**Solution:** Extend `ModelRouting` in wave-config.yaml to map labels to actual models:

```yaml
model_routing:
  default_model: "claude-sonnet-4-6"
  rules:
    - labels: [frontend, architecture, refactor]
      model: "claude-opus-4-6"       # heavy tasks
    - labels: [docs, config, chore, ci]
      model: "claude-haiku-4-5"      # light tasks
```

```go
// wave/promoter.go
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
    return p.modelRouting.DefaultModel
}
```

Model override set via `Issue.ModelOverride` (already exists in types.go:95).
Overrides the per-issue `<!-- model: X -->` comment only if not explicitly set.

**Estimated savings:** 200-400k tokens cost reduction/pipeline (haiku ~80% cheaper for 30-40% of issues).

### P8: Token budget tracking per wave (visibility)

**Problem:** Token usage tracked per-run and globally, but not per-wave or per-issue-across-retries.

**Solution:** Track cumulative tokens in DAG nodes:

```go
// wave/dag.go — extend Node
type Node struct {
    // ... existing ...
    TotalTokensIn  int64
    TotalTokensOut int64
    Attempts       int
}
```

Updated in `OnIssueCompleted` from `RunAttempt.TokensIn/Out`. Exposed in:
- `contrabass wave status` — per-wave token totals
- `/api/v1/wave/status` — JSON includes token data
- Dashboard `WavePipeline.tsx` — token cost per wave

```
Phase 1: Foundation (active) — 245k tokens
  Wave 1: ✅ completed [#2 ✅ 45k, #3 ✅ 52k]
  Wave 2: 🔄 in progress [#4 🔄 78k, #5 ⏳, #6 ✅ 70k]
```

**Estimated savings:** Indirect — enables informed decisions. "Wave 2 cost 3x more than expected" → investigate.

### RunOptions — unified dispatch config

P2 and P4 both need additional data passed to `agent.Start`. Unified via `RunOptions`:

```go
// types/types.go
type RunOptions struct {
    MaxTurns      int    // from P4: progressive max_turns
    ModelOverride string // from P7: wave-based model routing
    IsRetry       bool   // from P2: retry-aware prompts
    PrevError     string // from P2
    PrevPhase     string // from P2
    Attempt       int    // from P2
}
```

`AgentRunner.Start` signature extended (pointer for backward-compatible incremental migration):
```go
Start(ctx context.Context, issue types.Issue, workspace string, prompt string, opts *RunOptions) (*AgentProcess, error)
```

**Blast radius — all AgentRunner implementations need update:**
- `ClaudeRunner` (internal/agent/claude.go) — uses MaxTurns, ModelOverride, retry context
- `OpenCodeRunner` (internal/agent/opencode.go) — passes through MaxTurns
- `OhMyOpenCodeRunner` (internal/agent/ohmyopencode.go) — passes through
- `OMXRunner` (internal/agent/omx.go) — passes through
- `OMCRunner` (internal/agent/omc.go) — passes through
- `TmuxRunner` (internal/agent/tmux.go) — passes through to inner runner

All non-Claude runners: accept `*RunOptions`, pass relevant fields to inner agent, ignore unknown fields.
When `opts == nil` — use defaults (backward compatible with existing callers during migration).

### Token optimization summary

| # | Optimization | Where | Savings estimate |
|---|-------------|-------|-----------------|
| P1+P9 | Retry cap + stall bridge | orchestrator_runtime.go + wave/stall.go | 150-500k/pipeline |
| P2 | Retry-aware prompts | config/template.go + WORKFLOW.md | ~190k/pipeline |
| P3 | PR merge validation at dispatch | wave/filter.go | 50-100k/occurrence |
| P4 | Progressive max_turns | orchestrator.go + types | 30-50k/retry |
| P5 | Wave-ordered dispatch | wave/filter.go | 50-100k/pipeline |
| P6 | Fail-fast pre-flight hooks | orchestrator.go + config | 100-200k/pipeline |
| P7 | Model routing by complexity | wave/promoter.go + config | 200-400k cost/pipeline |
| P8 | Token budget tracking | wave/dag.go + dashboard | Visibility |

**Total estimated savings: 500k-1.5M tokens per pipeline run of 20-50 issues.**

---

## Key Principles

1. **Backward compatible** — without wave-config.yaml and `Blocked by:` annotations, orchestrator works as before
2. **Auto-DAG by default** — dependencies from issue body → topological sort → waves automatically
3. **wave-config.yaml optional override** — explicit grouping, model routing, stall thresholds
4. **Unified stall detector** — two levels (issue, wave), decision in wave package, execution in orchestrator
5. **Token-aware** — every dispatch decision considers cost: retry cap, progressive turns, model routing, fail-fast hooks

---

## Edge Cases and Failure Modes

### GitHub API failures during wave promotion

`PromoteWave` calls `AddLabel` for each issue in a wave. If some succeed and some fail:
- Track which labels were successfully applied
- Retry only the failed ones on next cycle
- Log partial promotion as warning event
- GitHub secondary rate limit (30 mutations/min): use exponential backoff between AddLabel calls

### Issue re-opened after completion

If a closed issue is re-opened (bug found in merged PR):
- `Refresh()` will see it as open again in next cycle
- DAG node state reverts to open
- Dependents that were already promoted remain promoted (can't un-promote safely)
- Emit `EventReconcileDrift` warning
- `contrabass wave health` will flag the inconsistency

### DAG changes mid-execution

If issue body is edited (Blocked by: annotations changed) while pipeline is running:
- `Refresh()` rebuilds DAG each cycle from fresh issue data
- Already-running issues are not affected (they're past the filter stage)
- Newly computed waves may differ from previous — this is expected and correct
- wave-config.yaml override is NOT hot-reloaded (requires restart or `reconcile --apply`)

### Race: promotion + dispatch timing

After `PromoteWave` adds `agent-ready` labels via GitHub API, the next `FetchIssues` may
return newly labeled issues before `Refresh()` updates the DAG. Mitigation:
- `FilterDispatchable` always checks dependencies against current DAG state
- Even if GitHub returns a newly-labeled issue, it won't pass the filter if DAG hasn't refreshed
- Since Refresh + Filter happen in the same `runCycle`, this race doesn't occur within a single cycle
- Cross-cycle race window is acceptable: worst case is a 1-cycle delay

### Partial wave completion with stalled issues

If 4 of 5 issues in a wave complete but 1 is stalled (retry exhausted):
- `StallDetector.CheckIssue` returns `Escalate`
- Issue gets demoted (agent-ready removed) + comment posted
- Wave is NOT considered complete — it's blocked by the stalled issue
- Human intervention required (fix the issue or close as wontfix)
- After human resolves it, next `Refresh()` cycle detects completion → promotes next wave

### EventLog file I/O errors

`EventLog.Emit()` returns an error (not void):
```go
func (l *EventLog) Emit(event Event) error
```
On disk full / permission error: log warning via logger, continue operating.
Events still delivered to in-memory subscribers (orchestrator channel). Only JSONL persistence is lost.

### .contrabass/ directory

Created by Contrabass at startup if it doesn't exist (existing behavior for other state files).
Wave event log goes to `.contrabass/wave-events.jsonl` relative to workspace root.

---

## Testing Strategy

### Unit tests (internal/wave/*_test.go)

| File | Tests |
|------|-------|
| `dag_test.go` | BuildDAG from issues, cycle detection, missing refs, topological sort correctness, ComputeWaves grouping |
| `config_test.go` | ParseConfig (valid, missing file, invalid YAML), MergeWithDAG (override + validation) |
| `filter_test.go` | FilterDispatchable (all deps met, some deps open, no deps, missing refs), Refresh |
| `promoter_test.go` | PromoteWave (default + heavy labels), DemoteIssue, tracker without LabelManager |
| `stall_test.go` | CheckIssue (Continue/Retry/Escalate), CheckWave |
| `health_test.go` | Health checks (all pass, label inconsistency, missing issues) |
| `event_log_test.go` | Emit + Query, Subscribe, file I/O error handling |

### Integration tests

- MockTracker implements `LabelManager` + `PRVerifier` with in-memory state
- Full cycle: create issues → build DAG → filter → simulate completion → verify promotion
- Verify orchestrator integration: wave.Manager as dependency, nil (disabled) path

Note: `StallConfig` fields and `Node.State` type are defined in their respective
sections above (Unified Stall Detection and DAG). Both use int-based types
matching existing codebase conventions.
5. **Events → JSONL + SSE** — persistent log for CLI, real-time for dashboard
6. **CONTRACT.md** — source of truth for issue format, updated via co-evolution rule
