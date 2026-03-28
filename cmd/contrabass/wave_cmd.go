package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/wave"
)

var waveCmd = &cobra.Command{
	Use:   "wave",
	Short: "Manage wave pipeline",
	Long:  "Inspect and control the wave pipeline configuration and status",
}

var waveStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show wave pipeline phases and waves",
	RunE:  runWaveStatus,
}

var waveHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Run health checks on the wave pipeline",
	RunE:  runWaveHealth,
}

var waveReconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Reconcile wave pipeline with current issue state",
	RunE:  runWaveReconcile,
}

var wavePromoteCmd = &cobra.Command{
	Use:   "promote",
	Short: "Promote the next wave by applying agent-ready labels",
	RunE:  runWavePromote,
}

func init() {
	waveStatusCmd.Flags().String("config", "wave-config.yaml", "path to wave-config.yaml")
	waveHealthCmd.Flags().String("config", "wave-config.yaml", "path to wave-config.yaml")
	waveReconcileCmd.Flags().String("config", "wave-config.yaml", "path to wave-config.yaml")
	waveReconcileCmd.Flags().Bool("apply", false, "apply changes (default: dry-run)")
	wavePromoteCmd.Flags().String("config", "wave-config.yaml", "path to wave-config.yaml")
	wavePromoteCmd.Flags().Bool("force", false, "force promotion even if wave is not complete")

	waveCmd.AddCommand(waveStatusCmd, waveHealthCmd, waveReconcileCmd, wavePromoteCmd)
}

func runWaveStatus(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")

	cfg, err := wave.ParseConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("parsing wave config: %w", err)
	}
	if cfg == nil {
		fmt.Println("No wave-config.yaml found. Running in auto-DAG mode.")
		return nil
	}

	fmt.Printf("Repo: %s\n", cfg.Repo)
	fmt.Printf("Phases: %d\n\n", len(cfg.Phases))
	for i, phase := range cfg.Phases {
		fmt.Printf("Phase %d: %s", i+1, phase.Name)
		if phase.Milestone != "" {
			fmt.Printf(" (milestone: %s)", phase.Milestone)
		}
		fmt.Println()
		for j, w := range phase.Waves {
			desc := w.Description
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("  Wave %d: %s — %d issue(s)\n", j, desc, len(w.Issues))
			for _, id := range w.Issues {
				fmt.Printf("    - %s\n", id)
			}
		}
	}
	return nil
}

func runWaveHealth(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	mgr, err := wave.NewManager(nil, cfgPath, log.Default())
	if err != nil {
		return fmt.Errorf("creating wave manager: %w", err)
	}

	results := mgr.HealthCheck(context.Background())
	allOK := true
	for _, r := range results {
		status := "OK"
		if !r.OK {
			status = "FAIL"
			allOK = false
		}
		fmt.Printf("[%s] %s: %s\n", status, r.Name, r.Message)
	}

	if !allOK {
		return fmt.Errorf("one or more health checks failed")
	}
	return nil
}

func runWaveReconcile(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	apply, _ := cmd.Flags().GetBool("apply")

	cfg, err := wave.ParseConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if cfg == nil {
		fmt.Println("No wave-config.yaml found.")
		return nil
	}

	if len(cfg.Phases) == 0 || len(cfg.Phases[0].Waves) == 0 {
		fmt.Println("No phases/waves defined in config.")
		return nil
	}

	wave0 := cfg.Phases[0].Waves[0]
	defaultLabel := cfg.ModelRouting.DefaultLabel
	if defaultLabel == "" {
		defaultLabel = "agent-ready"
	}

	fmt.Printf("Wave config: %s\n", cfg.Repo)
	fmt.Printf("Wave 0 issues: %v\n", wave0.Issues)
	fmt.Printf("Label to apply: %s\n", defaultLabel)

	if !apply {
		fmt.Println("\nDry-run mode. Actions that would be taken:")
		for _, issueID := range wave0.Issues {
			fmt.Printf("  - Add label %q to issue #%s\n", defaultLabel, issueID)
		}
		fmt.Println("\nRun with --apply to execute.")
		return nil
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN not set — required for --apply")
	}

	parts := strings.SplitN(cfg.Repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format in config: %q (expected owner/repo)", cfg.Repo)
	}

	githubClient, err := tracker.NewGitHubClient(tracker.GitHubConfig{
		APIToken: token,
		Owner:    parts[0],
		Repo:     parts[1],
	})
	if err != nil {
		return fmt.Errorf("creating github client: %w", err)
	}

	lm, ok := tracker.Tracker(githubClient).(tracker.LabelManager)
	if !ok {
		return fmt.Errorf("github client does not support label management")
	}

	ctx := context.Background()
	for _, issueID := range wave0.Issues {
		fmt.Printf("  Adding label %q to issue #%s... ", defaultLabel, issueID)
		if err := lm.AddLabel(ctx, issueID, defaultLabel); err != nil {
			fmt.Printf("FAILED: %v\n", err)
		} else {
			fmt.Printf("OK\n")
		}
	}

	fmt.Println("\nReconcile complete.")
	return nil
}

func runWavePromote(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	force, _ := cmd.Flags().GetBool("force")

	cfg, err := wave.ParseConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if cfg == nil {
		fmt.Println("No wave-config.yaml found.")
		return nil
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN not set")
	}

	parts := strings.SplitN(cfg.Repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format: %q", cfg.Repo)
	}

	githubClient, err := tracker.NewGitHubClient(tracker.GitHubConfig{
		APIToken: token,
		Owner:    parts[0],
		Repo:     parts[1],
	})
	if err != nil {
		return err
	}

	mgr, err := wave.NewManager(githubClient, cfgPath, nil)
	if err != nil {
		return err
	}

	ctx := context.Background()
	issues, err := githubClient.FetchIssues(ctx)
	if err != nil {
		return fmt.Errorf("fetch issues: %w", err)
	}
	if err := mgr.Refresh(issues); err != nil {
		return fmt.Errorf("refresh: %w", err)
	}

	if force {
		promoted, err := mgr.ForcePromoteNext(ctx, issues)
		if err != nil {
			return fmt.Errorf("force promote: %w", err)
		}
		if len(promoted) == 0 {
			fmt.Println("No waves to promote.")
		} else {
			fmt.Printf("Force-promoted %d issues: %v\n", len(promoted), promoted)
		}
	} else {
		if err := mgr.AutoPromoteIfNeeded(ctx, issues); err != nil {
			return fmt.Errorf("promote: %w", err)
		}
	}

	fmt.Println("Wave promotion complete.")
	return nil
}
