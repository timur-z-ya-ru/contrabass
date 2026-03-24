package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

// ClaudeRunner runs Claude Code CLI in --print mode with stream-json output.
type ClaudeRunner struct {
	binaryPath string
	model      string
	maxTurns   int
	timeout    time.Duration

	mu    sync.Mutex
	procs map[int]*claudeProcess
}

type claudeProcess struct {
	cmd    *exec.Cmd
	done   chan error
	cancel context.CancelFunc

	doneOnce sync.Once
}

func (p *claudeProcess) finish(err error) {
	p.doneOnce.Do(func() {
		p.done <- err
		close(p.done)
	})
}

type ClaudeConfig struct {
	BinaryPath string
	Model      string
	MaxTurns   int
	Timeout    time.Duration
}

func NewClaudeRunner(cfg ClaudeConfig) *ClaudeRunner {
	if cfg.BinaryPath == "" {
		cfg.BinaryPath = "claude"
	}
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 50
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &ClaudeRunner{
		binaryPath: cfg.BinaryPath,
		model:      cfg.Model,
		maxTurns:   cfg.MaxTurns,
		timeout:    cfg.Timeout,
		procs:      make(map[int]*claudeProcess),
	}
}

func (r *ClaudeRunner) Start(ctx context.Context, issue types.Issue, workspace string, prompt string) (*AgentProcess, error) {
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--no-session-persistence",
		"--dangerously-skip-permissions",
		"--disable-slash-commands",
		"--settings", `{"enabledPlugins":{},"hooks":{}}`,
	}

	if r.model != "" {
		args = append(args, "--model", r.model)
	}
	if r.maxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(r.maxTurns))
	}

	// Write prompt to temp file to avoid arg length limits
	promptFile, err := os.CreateTemp("", "contrabass-claude-prompt-*.md")
	if err != nil {
		return nil, fmt.Errorf("create prompt file: %w", err)
	}
	if _, err := promptFile.WriteString(prompt); err != nil {
		_ = os.Remove(promptFile.Name())
		return nil, fmt.Errorf("write prompt file: %w", err)
	}
	_ = promptFile.Close()

	// Read prompt from file as argument
	promptContent, err := os.ReadFile(promptFile.Name())
	if err != nil {
		_ = os.Remove(promptFile.Name())
		return nil, fmt.Errorf("read prompt file: %w", err)
	}
	_ = os.Remove(promptFile.Name())
	args = append(args, string(promptContent))

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, r.binaryPath, args...)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(),
		"DISABLE_OMC=true",
		"OMC_SKIP_HOOKS=*",
		"DISABLE_SUPERPOWERS=true",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	// Discard stderr to prevent blocking
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start claude process: %w", err)
	}

	proc := &claudeProcess{
		cmd:    cmd,
		done:   make(chan error, 1),
		cancel: cancel,
	}

	r.mu.Lock()
	r.procs[cmd.Process.Pid] = proc
	r.mu.Unlock()

	events := make(chan types.AgentEvent, 128)
	sessionID := fmt.Sprintf("claude-%s-%d", issue.ID, cmd.Process.Pid)

	go r.streamEventsAndWait(proc, stdout, events)

	return &AgentProcess{
		PID:       cmd.Process.Pid,
		SessionID: sessionID,
		Events:    events,
		Done:      proc.done,
	}, nil
}

func (r *ClaudeRunner) Stop(proc *AgentProcess) error {
	if proc == nil {
		return errors.New("process is nil")
	}

	r.mu.Lock()
	state, ok := r.procs[proc.PID]
	r.mu.Unlock()

	if !ok {
		return fmt.Errorf("claude process already stopped: pid %d", proc.PID)
	}

	if state.cmd.Process != nil {
		if err := state.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
			_ = state.cmd.Process.Kill()
		}
	}

	select {
	case <-state.done:
		return nil
	case <-time.After(r.timeout):
		if state.cmd.Process != nil {
			_ = state.cmd.Process.Kill()
		}
		select {
		case <-state.done:
			return nil
		case <-time.After(5 * time.Second):
			return fmt.Errorf("claude process kill timeout: pid %d", proc.PID)
		}
	}
}

func (r *ClaudeRunner) Close() error { return nil }

func (r *ClaudeRunner) streamEventsAndWait(proc *claudeProcess, stdout io.ReadCloser, events chan types.AgentEvent) {
	defer close(events)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		event := r.mapClaudeEvent(msg)
		if event != nil {
			select {
			case events <- *event:
			default:
			}
		}
	}

	waitErr := proc.cmd.Wait()
	if waitErr != nil {
		proc.finish(waitErr)
	} else {
		proc.finish(nil)
	}

	r.mu.Lock()
	if proc.cmd.Process != nil {
		delete(r.procs, proc.cmd.Process.Pid)
	}
	r.mu.Unlock()
}

// mapClaudeEvent maps Claude Code CLI stream-json events to Contrabass agent events.
func (r *ClaudeRunner) mapClaudeEvent(msg map[string]interface{}) *types.AgentEvent {
	msgType, _ := msg["type"].(string)
	now := time.Now()

	switch msgType {
	case "system":
		subtype, _ := msg["subtype"].(string)
		switch subtype {
		case "init":
			return &types.AgentEvent{Type: "session.status", Data: msg, Timestamp: now}
		default:
			return nil // Skip hooks and other system events
		}

	case "assistant":
		// Check if assistant message contains tool_use content blocks
		if message, ok := msg["message"].(map[string]interface{}); ok {
			if content, ok := message["content"].([]interface{}); ok {
				for _, block := range content {
					if b, ok := block.(map[string]interface{}); ok {
						if b["type"] == "tool_use" {
							return &types.AgentEvent{Type: "item/started", Data: msg, Timestamp: now}
						}
					}
				}
			}
		}
		return &types.AgentEvent{Type: "turn/started", Data: msg, Timestamp: now}

	case "user":
		// User messages contain tool_result blocks
		return &types.AgentEvent{Type: "item/completed", Data: msg, Timestamp: now}

	case "result":
		subtype, _ := msg["subtype"].(string)
		isError, _ := msg["is_error"].(bool)
		if subtype == "success" && !isError {
			return &types.AgentEvent{Type: "task/completed", Data: msg, Timestamp: now}
		}
		return &types.AgentEvent{
			Type: "protocol/error",
			Data: map[string]interface{}{
				"error":   fmt.Sprintf("claude result: %s", subtype),
				"details": msg,
			},
			Timestamp: now,
		}

	default:
		return nil
	}
}

func init() {
	// Ensure ClaudeRunner implements AgentRunner at compile time.
	var _ AgentRunner = (*ClaudeRunner)(nil)
}

// claudeConfigFromWorkflow extracts Claude runner config from workflow config values.
func ClaudeConfigFromStrings(binaryPath, model string, maxTurns int) ClaudeConfig {
	return ClaudeConfig{
		BinaryPath: binaryPath,
		Model:      model,
		MaxTurns:   maxTurns,
	}
}
