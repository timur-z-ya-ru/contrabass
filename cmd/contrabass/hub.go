package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/junhoyeo/contrabass/internal/logging"
	"github.com/junhoyeo/contrabass/internal/multiproject"
)

var hubCmd = &cobra.Command{
	Use:   "hub",
	Short: "Run multi-project orchestrator",
	Long: `Hub mode manages multiple projects from a single process.
Each project runs its own orchestrator with per-project concurrency,
while the hub enforces a global limit on active projects.`,
	RunE: runHub,
}

func init() {
	hubCmd.Flags().String("config", "", "path to hub YAML config (required)")
	_ = hubCmd.MarkFlagRequired("config")
}

func runHub(cmd *cobra.Command, args []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")

	cfg, err := multiproject.ParseHubConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("loading hub config: %w", err)
	}

	logger := logging.NewLogger(logging.LogOptions{
		Level:  parseLogLevel(cfg.LogLevel),
		Output: cfg.LogFile,
		Prefix: "contrabass-hub",
	})

	logger.Info("hub config loaded",
		"projects", len(cfg.Projects),
		"max_active_projects", cfg.MaxActiveProjects,
		"log_file", cfg.LogFile,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-ctx.Done():
		case sig := <-signalChan:
			logger.Info("received signal, shutting down", "signal", sig)
			cancel()
		}
	}()
	defer signal.Stop(signalChan)

	hub := multiproject.NewHub(cfg, logger)
	return hub.Run(ctx)
}

// parseLogLevel is defined in main.go — this file uses it.
// No redefinition needed since both are in package main.
