package main

import (
	"context"
	"fmt"
	"log"

	"github.com/spf13/cobra"

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
	Short: "Promote the next wave (stub)",
	RunE:  runWavePromote,
}

func init() {
	waveStatusCmd.Flags().String("config", "wave-config.yaml", "path to wave-config.yaml")
	waveReconcileCmd.Flags().String("config", "wave-config.yaml", "path to wave-config.yaml")
	waveReconcileCmd.Flags().Bool("apply", false, "apply changes (default: dry-run)")

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
	mgr, err := wave.NewManager(nil, "", log.Default())
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
		return fmt.Errorf("parsing wave config: %w", err)
	}

	mode := "dry-run"
	if apply {
		mode = "apply"
	}

	if cfg == nil {
		fmt.Printf("[%s] No wave-config.yaml found. Nothing to reconcile.\n", mode)
		return nil
	}

	fmt.Printf("[%s] Reconciling wave pipeline\n", mode)
	for _, phase := range cfg.Phases {
		for j, w := range phase.Waves {
			fmt.Printf("  Phase %q wave %d: %d issue(s)\n", phase.Name, j, len(w.Issues))
			for _, id := range w.Issues {
				fmt.Printf("    - %s\n", id)
			}
		}
	}

	if !apply {
		fmt.Println("\nRun with --apply to apply changes.")
	}
	return nil
}

func runWavePromote(_ *cobra.Command, _ []string) error {
	fmt.Println("wave promote: not yet implemented")
	return nil
}
