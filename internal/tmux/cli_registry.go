package tmux

import (
	"fmt"
	"sort"
	"strings"
)

const (
	promptModeStdin = "stdin"
	promptModeFile  = "file"
	promptModeArg   = "arg"
)

type CLIConfig struct {
	AgentType    string
	BinaryPath   string
	BuildArgs    func(workspace, prompt string) []string
	Env          map[string]string
	ReadyPattern string
	PromptMode   string
}

type CLIRegistry struct {
	configs map[string]CLIConfig
}

func NewCLIRegistry() *CLIRegistry {
	r := &CLIRegistry{configs: make(map[string]CLIConfig, 5)}

	// Codex starts an app-server subprocess and receives prompts over stdin JSONL.
	mustRegister(r, CLIConfig{
		AgentType:  "codex",
		BinaryPath: "codex",
		BuildArgs: func(_, _ string) []string {
			return []string{"app-server"}
		},
		PromptMode:   promptModeStdin,
		ReadyPattern: "initialized",
	})

	// OpenCode starts an HTTP/SSE server and consumes prompt content from a file.
	mustRegister(r, CLIConfig{
		AgentType:  "opencode",
		BinaryPath: "opencode",
		BuildArgs: func(_, _ string) []string {
			return []string{"serve"}
		},
		PromptMode:   promptModeFile,
		ReadyPattern: "serve|listening",
	})

	// OMX runs its team runtime entrypoint and accepts task input via argument.
	mustRegister(r, CLIConfig{
		AgentType:  "omx",
		BinaryPath: "omx",
		BuildArgs: func(_, _ string) []string {
			return []string{"team"}
		},
		PromptMode:   promptModeArg,
		ReadyPattern: "team",
	})

	// OMC runs its team runtime entrypoint and accepts task input via argument.
	mustRegister(r, CLIConfig{
		AgentType:  "omc",
		BinaryPath: "omc",
		BuildArgs: func(_, _ string) []string {
			return []string{"team"}
		},
		PromptMode:   promptModeArg,
		ReadyPattern: "team",
	})

	// oh-my-opencode wraps OpenCode and commonly receives prompt content from file.
	mustRegister(r, CLIConfig{
		AgentType:  "oh-my-opencode",
		BinaryPath: "oh-my-opencode",
		BuildArgs: func(_, _ string) []string {
			return []string{}
		},
		PromptMode:   promptModeFile,
		ReadyPattern: "ready|serve|listening",
	})

	return r
}

func (r *CLIRegistry) Register(config CLIConfig) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}

	agentType := strings.TrimSpace(config.AgentType)
	if agentType == "" {
		return fmt.Errorf("agent type is empty")
	}
	if strings.TrimSpace(config.BinaryPath) == "" {
		return fmt.Errorf("binary path is empty for agent type %q", agentType)
	}
	if config.BuildArgs == nil {
		return fmt.Errorf("build args function is nil for agent type %q", agentType)
	}
	if !isValidPromptMode(config.PromptMode) {
		return fmt.Errorf("invalid prompt mode %q for agent type %q", config.PromptMode, agentType)
	}

	config.AgentType = agentType
	r.configs[agentType] = config
	return nil
}

func (r *CLIRegistry) Get(agentType string) (*CLIConfig, error) {
	if r == nil {
		return nil, fmt.Errorf("registry is nil")
	}

	cfg, ok := r.configs[strings.TrimSpace(agentType)]
	if !ok {
		return nil, fmt.Errorf("unknown agent type %q", agentType)
	}

	return &cfg, nil
}

func (r *CLIRegistry) List() []CLIConfig {
	if r == nil || len(r.configs) == 0 {
		return []CLIConfig{}
	}

	configs := make([]CLIConfig, 0, len(r.configs))
	for _, cfg := range r.configs {
		configs = append(configs, cfg)
	}

	sort.Slice(configs, func(i, j int) bool {
		return configs[i].AgentType < configs[j].AgentType
	})

	return configs
}

func mustRegister(r *CLIRegistry, cfg CLIConfig) {
	if err := r.Register(cfg); err != nil {
		panic(err)
	}
}

func isValidPromptMode(mode string) bool {
	switch mode {
	case promptModeStdin, promptModeFile, promptModeArg:
		return true
	default:
		return false
	}
}
