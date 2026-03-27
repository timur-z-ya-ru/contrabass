package wave

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

const mergedCacheTTL = 60 * time.Second
const verificationDelay = 2 * time.Second

// PromotionResult holds the result of a wave promotion attempt.
type PromotionResult struct {
	Promoted []string
	Err      error
}

// mergedCacheEntry caches the result of a HasMergedPR check.
type mergedCacheEntry struct {
	merged  bool
	checked time.Time
}

// Manager is the main orchestration object for the wave pipeline.
type Manager struct {
	tracker     tracker.Tracker
	pipeline    *Pipeline
	eventLog    *EventLog
	stall       *StallDetector
	promoter    *Promoter
	logger      *log.Logger
	configPath  string
	mu          sync.RWMutex
	openSet     map[string]bool
	mergedCache map[string]mergedCacheEntry
}

// NewManager creates a new Manager. If configPath is provided, the wave config is
// parsed from that file. If tracker is nil the manager works in offline mode.
// If logger is nil a default logger is created.
func NewManager(t tracker.Tracker, configPath string, logger *log.Logger) (*Manager, error) {
	if logger == nil {
		logger = log.Default()
	}

	var cfg *WaveConfig
	if configPath != "" {
		parsed, err := ParseConfig(configPath)
		if err != nil {
			return nil, err
		}
		cfg = parsed
	}

	var routing ModelRouting
	var stallCfg StallConfig
	if cfg != nil {
		routing = cfg.ModelRouting
		stallCfg = cfg.StallDetection
	}

	m := &Manager{
		tracker:     t,
		eventLog:    nil, // optional; set by caller if needed
		stall:       NewStallDetector(stallCfg),
		promoter:    NewPromoter(t, routing),
		logger:      logger,
		configPath:  configPath,
		openSet:     make(map[string]bool),
		mergedCache: make(map[string]mergedCacheEntry),
	}

	// Build an empty pipeline with the config (no issues yet — lazy via Refresh).
	emptyDAG := &DAG{Nodes: make(map[string]*Node)}
	pipeline, err := MergeWithDAG(cfg, emptyDAG)
	if err != nil {
		return nil, err
	}
	m.pipeline = pipeline

	return m, nil
}

// Refresh rebuilds the DAG from the given issues, updates openSet, and
// regenerates the pipeline via MergeWithDAG.
func (m *Manager) Refresh(issues []types.Issue) error {
	dag, err := BuildDAG(issues)
	if err != nil {
		return err
	}

	var cfg *WaveConfig
	if m.configPath != "" {
		cfg, err = ParseConfig(m.configPath)
		if err != nil {
			return err
		}
	}

	pipeline, err := MergeWithDAG(cfg, dag)
	if err != nil {
		return err
	}

	open := make(map[string]bool, len(issues))
	for _, iss := range issues {
		open[iss.ID] = true
	}

	m.mu.Lock()
	m.pipeline = pipeline
	m.openSet = open
	m.mu.Unlock()

	return nil
}

// FilterDispatchable returns only the issues that are ready to be dispatched:
//   - State == Unclaimed
//   - All BlockedBy deps are absent from openSet (absent = already closed/done)
//   - If tracker implements PRVerifier, closed deps are verified to have a merged PR
//
// Results are sorted by wave index ascending, then by Blocks count descending.
func (m *Manager) FilterDispatchable(ctx context.Context, issues []types.Issue) []types.Issue {
	m.mu.RLock()
	openSet := m.openSet
	pipeline := m.pipeline
	m.mu.RUnlock()

	pv, hasPV := m.tracker.(tracker.PRVerifier)

	// Build a map for O(1) lookup.
	issueMap := make(map[string]types.Issue, len(issues))
	for _, iss := range issues {
		issueMap[iss.ID] = iss
	}

	var result []types.Issue
	for _, iss := range issues {
		if iss.State != types.Unclaimed {
			continue
		}

		dispatchable := true
		for _, depID := range iss.BlockedBy {
			if openSet[depID] {
				// Dep is still open → blocked.
				dispatchable = false
				break
			}
			// Dep is absent (closed). If PRVerifier is available, confirm merged.
			if hasPV && m.shouldVerifyMerge(depID) {
				if !m.checkMergedCached(ctx, pv, depID) {
					dispatchable = false
					break
				}
			}
		}

		if dispatchable {
			result = append(result, iss)
		}
	}

	// Sort: wave index ascending, then Blocks count descending.
	sort.SliceStable(result, func(i, j int) bool {
		wi := waveIndexFromPipeline(pipeline, result[i].ID)
		wj := waveIndexFromPipeline(pipeline, result[j].ID)
		if wi != wj {
			return wi < wj
		}
		// Ties: more Blocks = higher priority.
		bi := blocksCount(pipeline, result[i].ID)
		bj := blocksCount(pipeline, result[j].ID)
		return bi > bj
	})

	return result
}

// OnIssueCompleted marks issueID as done in openSet. If the current wave is
// fully completed, it spawns a goroutine to promote the next wave after a
// short verification delay. Returns a channel that receives one PromotionResult.
func (m *Manager) OnIssueCompleted(ctx context.Context, issueID string) <-chan PromotionResult {
	ch := make(chan PromotionResult, 1)

	m.mu.Lock()
	delete(m.openSet, issueID)
	pipeline := m.pipeline
	m.mu.Unlock()

	currentWave := findCurrentWave(pipeline)
	if currentWave == nil {
		ch <- PromotionResult{}
		return ch
	}

	// Check if all issues in the current wave are done (absent from openSet).
	m.mu.RLock()
	openSet := m.openSet
	m.mu.RUnlock()

	allDone := true
	for _, id := range currentWave.Issues {
		if openSet[id] {
			allDone = false
			break
		}
	}

	if !allDone {
		ch <- PromotionResult{}
		return ch
	}

	nextWave := findNextWave(pipeline)
	if nextWave == nil {
		ch <- PromotionResult{}
		return ch
	}

	// Build allIssues map for PromoteWave.
	allIssues := buildAllIssuesMap(pipeline)

	go func() {
		select {
		case <-ctx.Done():
			ch <- PromotionResult{Err: ctx.Err()}
			return
		case <-time.After(verificationDelay):
		}

		promoted, err := m.promoter.PromoteWave(ctx, *nextWave, allIssues)
		ch <- PromotionResult{Promoted: promoted, Err: err}
	}()

	return ch
}

// CheckIssueStall delegates stall detection to the StallDetector.
func (m *Manager) CheckIssueStall(info RunInfo) StallAction {
	return m.stall.CheckIssue(info)
}

// EscalateIssue demotes the issue's labels, posts a comment, and emits an event.
func (m *Manager) EscalateIssue(ctx context.Context, issueID string) {
	if m.promoter != nil {
		if err := m.promoter.DemoteIssue(ctx, issueID); err != nil {
			m.logger.Printf("wave.Manager: DemoteIssue %q: %v", issueID, err)
		}
	}

	if m.tracker != nil {
		if err := m.tracker.PostComment(ctx, issueID, "Escalated: stall detected, human review required."); err != nil {
			m.logger.Printf("wave.Manager: PostComment %q: %v", issueID, err)
		}
	}

	if m.eventLog != nil {
		ev := Event{
			Timestamp: time.Now(),
			Type:      WaveEventStallDetected,
			IssueID:   issueID,
		}
		if err := m.eventLog.Emit(ev); err != nil {
			m.logger.Printf("wave.Manager: eventLog.Emit: %v", err)
		}
	}
}

// UpdateTokens updates the token accounting fields on the Node for issueID.
func (m *Manager) UpdateTokens(issueID string, tokensIn, tokensOut int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.pipeline == nil || m.pipeline.DAG == nil {
		return
	}
	node, ok := m.pipeline.DAG.Nodes[issueID]
	if !ok {
		return
	}
	node.TotalTokensIn += tokensIn
	node.TotalTokensOut += tokensOut
	node.Attempts++
}

// --- helper methods ---

// waveIndex returns the wave index for issueID in the pipeline, or -1 if not found.
func (m *Manager) waveIndex(issueID string) int {
	m.mu.RLock()
	p := m.pipeline
	m.mu.RUnlock()
	return waveIndexFromPipeline(p, issueID)
}

// findCurrentWave returns the first wave that still has open issues.
func (m *Manager) findCurrentWave() *Wave {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return findCurrentWave(m.pipeline)
}

// findNextWave returns the wave after the current wave.
func (m *Manager) findNextWave() *Wave {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return findNextWave(m.pipeline)
}

// shouldVerifyMerge returns true when PRVerifier is available and the dep is closed.
func (m *Manager) shouldVerifyMerge(depID string) bool {
	_, hasPV := m.tracker.(tracker.PRVerifier)
	if !hasPV {
		return false
	}
	m.mu.RLock()
	inOpen := m.openSet[depID]
	m.mu.RUnlock()
	return !inOpen
}

// checkMergedCached checks if depID has a merged PR, using a cache with TTL.
// Merged=true entries are cached immutably; merged=false entries expire after 60s.
func (m *Manager) checkMergedCached(ctx context.Context, pv tracker.PRVerifier, depID string) bool {
	m.mu.RLock()
	entry, ok := m.mergedCache[depID]
	m.mu.RUnlock()

	if ok {
		if entry.merged {
			return true // immutable positive cache
		}
		if time.Since(entry.checked) < mergedCacheTTL {
			return false // still within negative TTL
		}
	}

	merged, err := pv.HasMergedPR(ctx, depID)
	if err != nil {
		m.logger.Printf("wave.Manager: HasMergedPR %q: %v", depID, err)
		return false
	}

	m.mu.Lock()
	m.mergedCache[depID] = mergedCacheEntry{merged: merged, checked: time.Now()}
	m.mu.Unlock()

	return merged
}

// --- package-level helpers (no receiver, used by methods) ---

func waveIndexFromPipeline(p *Pipeline, issueID string) int {
	if p == nil {
		return -1
	}
	for _, phase := range p.Phases {
		for _, w := range phase.Waves {
			for _, id := range w.Issues {
				if id == issueID {
					return w.Index
				}
			}
		}
	}
	return -1
}

func blocksCount(p *Pipeline, issueID string) int {
	if p == nil || p.DAG == nil {
		return 0
	}
	node, ok := p.DAG.Nodes[issueID]
	if !ok {
		return 0
	}
	return len(node.Blocks)
}

// findCurrentWave returns the first wave that contains at least one issue still
// present in the pipeline (we can't access openSet here; caller handles that).
// We return the lowest-index wave across all phases.
func findCurrentWave(p *Pipeline) *Wave {
	if p == nil {
		return nil
	}
	for i := range p.Phases {
		for j := range p.Phases[i].Waves {
			return &p.Phases[i].Waves[j]
		}
	}
	return nil
}

// findNextWave returns the wave that follows the first wave in the pipeline.
func findNextWave(p *Pipeline) *Wave {
	if p == nil {
		return nil
	}
	for i := range p.Phases {
		waves := p.Phases[i].Waves
		if len(waves) > 1 {
			return &waves[1]
		}
		// Check next phase.
		if i+1 < len(p.Phases) && len(p.Phases[i+1].Waves) > 0 {
			return &p.Phases[i+1].Waves[0]
		}
	}
	return nil
}

// buildAllIssuesMap builds a map[id]Issue from the pipeline's DAG nodes.
// Used to pass to PromoteWave which needs a full issue map.
func buildAllIssuesMap(p *Pipeline) map[string]types.Issue {
	if p == nil || p.DAG == nil {
		return nil
	}
	m := make(map[string]types.Issue, len(p.DAG.Nodes))
	for id, node := range p.DAG.Nodes {
		m[id] = types.Issue{
			ID:     id,
			Labels: append([]string(nil), node.Labels...),
			State:  node.State,
		}
	}
	return m
}
