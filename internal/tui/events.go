package tui

import (
	"time"

	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/types"
)

type OrchestratorEventMsg struct {
	Event orchestrator.OrchestratorEvent
}

type TeamEventMsg struct {
	Event types.TeamEvent
}

type tickMsg time.Time
