package multiproject

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/errgroup"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/logging"
	"github.com/junhoyeo/contrabass/internal/orchestrator"
// 	"github.com/junhoyeo/contrabass/internal/prioritizer"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/workspace"
)

// Hub manages multiple project orchestrators with a shared dispatch guard.
type Hub struct {
	config *HubConfig
	guard  *ProjectGuard
	logger *log.Logger
}

// NewHub creates a multi-project hub.
func NewHub(cfg *HubConfig, logger *log.Logger) *Hub {
	return &Hub{
		config: cfg,
		guard:  NewProjectGuard(cfg.MaxActiveProjects),
		logger: logger,
	}
}

// Run starts all enabled project orchestrators and blocks until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) error {
	enabledProjects := make([]ProjectEntry, 0)
	for _, p := range h.config.Projects {
		if !p.IsEnabled() {
			h.logger.Info("project disabled, skipping", "path", p.Path)
			continue
		}
		if _, err := os.Stat(p.ConfigPath()); err != nil {
			h.logger.Warn("project config not found, skipping", "path", p.ConfigPath(), "err", err)
			continue
		}
		enabledProjects = append(enabledProjects, p)
	}

	if len(enabledProjects) == 0 {
		return fmt.Errorf("no enabled projects with valid configs found")
	}

	h.logger.Info("hub starting",
		"projects", len(enabledProjects),
		"max_active_projects", h.config.MaxActiveProjects,
	)

	g, gCtx := errgroup.WithContext(ctx)

	var cleanupMu sync.Mutex
	var cleanups []func()

	for _, proj := range enabledProjects {
		proj := proj // capture
		g.Go(func() error {
			cleanup, err := h.runProject(gCtx, proj)
			if cleanup != nil {
				cleanupMu.Lock()
				cleanups = append(cleanups, cleanup)
				cleanupMu.Unlock()
			}
			if err != nil {
				h.logger.Error("project orchestrator failed",
					"project", filepath.Base(proj.Path),
					"err", err,
				)
			}
			return nil // Don't kill other projects on one failure.
		})
	}

	// Periodic status log.
	g.Go(func() error {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-gCtx.Done():
				return nil
			case <-ticker.C:
				snap := h.guard.Snapshot()
				h.logger.Info("hub status",
					"active_projects", h.guard.ActiveProjects(),
					"max_active_projects", h.config.MaxActiveProjects,
					"details", fmt.Sprintf("%v", snap),
				)
			}
		}
	})

	err := g.Wait()

	// Cleanup all runners.
	cleanupMu.Lock()
	for _, fn := range cleanups {
		fn()
	}
	cleanupMu.Unlock()

	return err
}

// runProject creates and runs a single project's orchestrator.
func (h *Hub) runProject(ctx context.Context, proj ProjectEntry) (cleanup func(), retErr error) {
	projectName := filepath.Base(proj.Path)

	// Parse project config.
	cfg, err := config.ParseWorkflow(proj.ConfigPath())
	if err != nil {
		return nil, fmt.Errorf("parsing %s config: %w", projectName, err)
	}

	// Create config watcher.
	watcher, err := config.NewWatcher(proj.ConfigPath())
	if err != nil {
		return nil, fmt.Errorf("creating %s config watcher: %w", projectName, err)
	}

	go func() {
		if watchErr := watcher.Watch(ctx); watchErr != nil {
			h.logger.Error("config watcher failed", "project", projectName, "err", watchErr)
		}
	}()

	// Create tracker.
	trackerClient, err := h.createTracker(cfg, projectName)
	if err != nil {
		watcher.Stop()
		return nil, fmt.Errorf("creating %s tracker: %w", projectName, err)
	}

	// Create workspace manager.
	workspaceMgr := workspace.NewManager(proj.Path)

	// Create agent runner.
	agentRunner, err := h.createRunner(cfg, projectName)
	if err != nil {
		watcher.Stop()
		return nil, fmt.Errorf("creating %s agent runner: %w", projectName, err)
	}

	cleanup = func() {
		agentRunner.Close()
		watcher.Stop()
	}

	// Create project logger.
	projectLogger := logging.NewLogger(logging.LogOptions{
		Level:  h.logger.GetLevel(),
		Output: filepath.Join(proj.Path, "contrabass.log"),
		Prefix: "contrabass",
	})

	// Create orchestrator.
	orch := orchestrator.NewOrchestrator(trackerClient, workspaceMgr, agentRunner, watcher, projectLogger)

	// Session logger: structured JSONL log for post-hoc analysis.
	sessionLogPath := filepath.Join(proj.Path, "contrabass-runs.jsonl")
	if sl := logging.NewSessionLogger(sessionLogPath); sl != nil {
		// orch.SetSessionLogger(sl) // TODO: upstream broken
		origCleanup := cleanup
		cleanup = func() {
			sl.Close()
			origCleanup()
		}
	}

	// Set cross-project dispatch guard.
	// orch.SetDispatchGuard(projectName, h.guard.CanDispatch)

	// Set prioritizer if enabled.
	// TODO: upstream broken — PrioritizerEnabled/Model/BinaryPath not in config
	// if pri := prioritizer.New(prioritizer.Config{
	// 	Enabled:    cfg.PrioritizerEnabled(),
	// 	Model:      cfg.PrioritizerModel(),
	// 	BinaryPath: cfg.PrioritizerBinaryPath(),
	// }, projectLogger); pri != nil {
	// 	orch.SetPrioritizer(pri)
	// }

	h.logger.Info("starting project orchestrator",
		"project", projectName,
		"max_concurrency", cfg.MaxConcurrency(),
		"agent_type", cfg.AgentType(),
	)

	// Drain events (headless mode).
	go func() {
		for event := range orch.Events() {
			h.logger.Info("event",
				"project", projectName,
				"type", event.Type.String(),
				"issue_id", event.IssueID,
			)
			// Keep guard updated with actual running counts.
			h.guard.Update(projectName, orch.RunningCount())
		}
	}()

	return cleanup, orch.Run(ctx)
}

// createTracker builds a tracker client from a project's workflow config.
func (h *Hub) createTracker(cfg *config.WorkflowConfig, projectName string) (tracker.Tracker, error) {
	switch cfg.TrackerType() {
	case "github":
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			token = cfg.GitHubToken()
		}
		owner := os.Getenv("GITHUB_OWNER")
		if owner == "" {
			owner = cfg.GitHubOwner()
		}
		repo := os.Getenv("GITHUB_REPO")
		if repo == "" {
			repo = cfg.GitHubRepo()
		}
		return tracker.NewGitHubClient(tracker.GitHubConfig{
			APIToken: token,
			Owner:    owner,
			Repo:     repo,
			Labels:   cfg.GitHubLabels(),
			Assignee: cfg.GitHubAssignee(),
			Endpoint: cfg.GitHubEndpoint(),
		})
	case "internal", "local":
		return tracker.NewLocalTracker(tracker.LocalConfig{
			BoardDir:    cfg.LocalBoardDir(),
			IssuePrefix: cfg.LocalIssuePrefix(),
			Actor:       cfg.GitHubAssignee(),
		}), nil
	default:
		return nil, fmt.Errorf("unsupported tracker type for hub: %q", cfg.TrackerType())
	}
}

// createRunner builds an agent runner from a project's workflow config.
func (h *Hub) createRunner(cfg *config.WorkflowConfig, projectName string) (agent.AgentRunner, error) {
	switch cfg.AgentType() {
	case "omc":
		omcBin := os.Getenv("OMC_BINARY")
		if omcBin == "" {
			omcBin = cfg.OMCBinaryPath()
		}
		omcCfg := cfg.Clone()
		omcCfg.OMC.BinaryPath = omcBin
		return agent.NewOMCRunner(omcCfg, 30*time.Second), nil
	case "codex":
		codexBin := os.Getenv("CODEX_BINARY")
		if codexBin == "" {
			codexBin = cfg.CodexBinaryPath()
		}
		return agent.NewCodexRunner(codexBin, 30*time.Second), nil
	default:
		return nil, fmt.Errorf("unsupported agent type for hub: %q (supported: omc, codex)", cfg.AgentType())
	}
}

// defaultPageSize matches the constant in tracker package.
const defaultPageSize = 100

// sanitizeProjectName creates a filesystem-safe name from a path.
func sanitizeProjectName(path string) string {
	base := filepath.Base(path)
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, base)
}
