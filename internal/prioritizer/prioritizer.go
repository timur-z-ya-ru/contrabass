package prioritizer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/charmbracelet/log"

	"github.com/junhoyeo/contrabass/internal/logging"
	"github.com/junhoyeo/contrabass/internal/types"
)

const (
	defaultModel        = "sonnet"
	maxDescriptionChars = 500
)

// Config configures the AI prioritizer.
type Config struct {
	Enabled    bool   `yaml:"enabled"`
	Model      string `yaml:"model"`
	BinaryPath string `yaml:"binary_path"`
}

// Prioritizer evaluates issues via Claude Code CLI and assigns execution priority.
type Prioritizer struct {
	model      string
	binaryPath string
	logger     *log.Logger

	mu           sync.Mutex
	lastPoolHash string
	priorities   map[string]int // issueID → priority (1 = do first)
}

// New creates a new Prioritizer. Returns nil if disabled.
func New(cfg Config, logger *log.Logger) *Prioritizer {
	if !cfg.Enabled {
		return nil
	}

	model := cfg.Model
	if model == "" {
		model = defaultModel
	}

	binaryPath := cfg.BinaryPath
	if binaryPath == "" {
		binaryPath = "claude"
	}

	if logger == nil {
		logger = logging.NewLogger(logging.LogOptions{Prefix: "prioritizer"})
	}

	return &Prioritizer{
		model:      model,
		binaryPath: binaryPath,
		logger:     logger,
		priorities: make(map[string]int),
	}
}

// Prioritize evaluates unclaimed issues, assigns Priority, and returns them
// sorted by priority (lowest number first). Issues with manually set priority
// (Priority > 0) keep their value. Re-evaluates only when the pool changes.
func (p *Prioritizer) Prioritize(ctx context.Context, issues []types.Issue) []types.Issue {
	if p == nil || len(issues) == 0 {
		return issues
	}

	// Separate unclaimed issues that need prioritization.
	unclaimed := make([]types.Issue, 0, len(issues))
	other := make([]types.Issue, 0)
	for _, issue := range issues {
		if issue.State == types.Unclaimed {
			unclaimed = append(unclaimed, issue)
		} else {
			other = append(other, issue)
		}
	}

	if len(unclaimed) <= 1 {
		return issues
	}

	// Check if pool changed.
	poolHash := computePoolHash(unclaimed)

	p.mu.Lock()
	needsEval := poolHash != p.lastPoolHash
	cachedPriorities := make(map[string]int, len(p.priorities))
	for k, v := range p.priorities {
		cachedPriorities[k] = v
	}
	p.mu.Unlock()

	priorities := cachedPriorities

	if needsEval {
		newPriorities, err := p.evaluate(ctx, unclaimed)
		if err != nil {
			p.logger.Warn("prioritization failed, using cached/default order", "err", err)
		} else {
			priorities = newPriorities
			p.mu.Lock()
			p.lastPoolHash = poolHash
			p.priorities = newPriorities
			p.mu.Unlock()

			p.logger.Info("priorities re-evaluated",
				"issues", len(unclaimed),
				"hash", poolHash[:12],
			)
		}
	}

	// Apply priorities to unclaimed issues.
	for i := range unclaimed {
		if unclaimed[i].Priority > 0 {
			// Manual priority — keep it.
			continue
		}
		if pri, ok := priorities[unclaimed[i].ID]; ok {
			unclaimed[i].Priority = pri
		}
	}

	// Sort: lower priority number first, 0 (unset) goes last.
	sort.SliceStable(unclaimed, func(i, j int) bool {
		pi, pj := unclaimed[i].Priority, unclaimed[j].Priority
		if pi == 0 && pj == 0 {
			return false
		}
		if pi == 0 {
			return false
		}
		if pj == 0 {
			return true
		}
		return pi < pj
	})

	// Rebuild the full list: unclaimed (sorted) + other (unchanged).
	result := make([]types.Issue, 0, len(issues))
	result = append(result, unclaimed...)
	result = append(result, other...)
	return result
}

// evaluate calls Claude Code CLI to assign priorities to the given issues.
func (p *Prioritizer) evaluate(ctx context.Context, issues []types.Issue) (map[string]int, error) {
	prompt := buildPrompt(issues)

	output, err := p.runClaude(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("claude CLI: %w", err)
	}

	priorities, err := parseResponse(output, issues)
	if err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return priorities, nil
}

// runClaude invokes `claude -p` in print mode and returns the text output.
func (p *Prioritizer) runClaude(ctx context.Context, prompt string) (string, error) {
	args := []string{
		"-p", prompt,
		"--model", p.model,
		"--output-format", "text",
	}

	cmd := exec.CommandContext(ctx, p.binaryPath, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude exited with error: %w (stderr: %s)", err, truncate(stderr.String(), 300))
	}

	result := strings.TrimSpace(stdout.String())
	if result == "" {
		return "", fmt.Errorf("empty response from claude (stderr: %s)", truncate(stderr.String(), 300))
	}

	return result, nil
}

// --- Prompt & parsing ---

func buildPrompt(issues []types.Issue) string {
	var sb strings.Builder

	sb.WriteString(`You are a development task prioritizer for a software project.

Given a list of issues (feature requests, bugs, tasks), analyze their content and determine the optimal execution order.

Consider:
1. Logical dependencies — if task B requires the result of task A, A must come first
2. Foundation first — infrastructure, setup, and data model tasks before features that use them
3. Blocking potential — tasks that unblock multiple other tasks get higher priority
4. Sequential constraints — some tasks must be done in a specific order

Return ONLY a JSON object mapping issue IDs to priority numbers.
Priority 1 = do first, 2 = do second, etc.
Every issue must get a unique priority number.

Issues:
`)

	for _, issue := range issues {
		desc := truncate(issue.Description, maxDescriptionChars)
		sb.WriteString(fmt.Sprintf("\n--- Issue ID: %s ---\n", issue.ID))
		sb.WriteString(fmt.Sprintf("Title: %s\n", issue.Title))
		if desc != "" {
			sb.WriteString(fmt.Sprintf("Description: %s\n", desc))
		}
		if len(issue.Labels) > 0 {
			sb.WriteString(fmt.Sprintf("Labels: %s\n", strings.Join(issue.Labels, ", ")))
		}
	}

	sb.WriteString(`
Respond with ONLY valid JSON. Example: {"42": 1, "17": 2, "5": 3}
`)

	return sb.String()
}

func parseResponse(text string, issues []types.Issue) (map[string]int, error) {
	// Extract JSON from response (may be wrapped in markdown code block).
	jsonStr := extractJSON(text)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON found in response: %s", truncate(text, 200))
	}

	// Parse as map[string]interface{} because JSON numbers may be float64.
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w (text: %s)", err, truncate(jsonStr, 200))
	}

	// Build valid issue ID set.
	validIDs := make(map[string]bool, len(issues))
	for _, issue := range issues {
		validIDs[issue.ID] = true
	}

	priorities := make(map[string]int, len(raw))
	for id, val := range raw {
		if !validIDs[id] {
			continue
		}
		switch v := val.(type) {
		case float64:
			priorities[id] = int(v)
		case json.Number:
			n, err := v.Int64()
			if err == nil {
				priorities[id] = int(n)
			}
		}
	}

	if len(priorities) == 0 {
		return nil, fmt.Errorf("no valid priorities parsed from response")
	}

	return priorities, nil
}

func extractJSON(text string) string {
	// Try to find JSON in markdown code block.
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(text[start:], "```"); end >= 0 {
			return strings.TrimSpace(text[start : start+end])
		}
	}
	if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + len("```")
		if end := strings.Index(text[start:], "```"); end >= 0 {
			candidate := strings.TrimSpace(text[start : start+end])
			if strings.HasPrefix(candidate, "{") {
				return candidate
			}
		}
	}

	// Try the whole text as JSON.
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		return trimmed
	}

	// Find first { ... last }.
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}

	return ""
}

// --- Helpers ---

func computePoolHash(issues []types.Issue) string {
	ids := make([]string, len(issues))
	for i, issue := range issues {
		ids[i] = issue.ID + ":" + issue.Title
	}
	sort.Strings(ids)

	h := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	return fmt.Sprintf("%x", h)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
