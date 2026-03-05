package config

import (
	"errors"
)

const (
	defaultMaxConcurrency    = 10
	defaultPollIntervalMs    = 30_000
	defaultMaxRetryBackoffMs = 300_000
	defaultAgentTimeoutMs    = 600_000
	defaultStallTimeoutMs    = 120_000
)

var (
	ErrModelRequired      = errors.New("model is required")
	ErrProjectURLRequired = errors.New("project_url is required")
)

type WorkflowConfig struct {
	MaxConcurrencyRaw    int    `yaml:"max_concurrency"`
	PollIntervalMsRaw    int    `yaml:"poll_interval_ms"`
	MaxRetryBackoffMsRaw int    `yaml:"max_retry_backoff_ms"`
	ModelRaw             string `yaml:"model"`
	ProjectURLRaw        string `yaml:"project_url"`
	AgentTimeoutMsRaw    int    `yaml:"agent_timeout_ms"`
	StallTimeoutMsRaw    int    `yaml:"stall_timeout_ms"`
	PromptTemplate       string `yaml:"-"`
}

func (c *WorkflowConfig) MaxConcurrency() int {
	if c == nil || c.MaxConcurrencyRaw <= 0 {
		return defaultMaxConcurrency
	}
	return c.MaxConcurrencyRaw
}

func (c *WorkflowConfig) PollIntervalMs() int {
	if c == nil || c.PollIntervalMsRaw <= 0 {
		return defaultPollIntervalMs
	}
	return c.PollIntervalMsRaw
}

func (c *WorkflowConfig) MaxRetryBackoffMs() int {
	if c == nil || c.MaxRetryBackoffMsRaw <= 0 {
		return defaultMaxRetryBackoffMs
	}
	return c.MaxRetryBackoffMsRaw
}

func (c *WorkflowConfig) AgentTimeoutMs() int {
	if c == nil || c.AgentTimeoutMsRaw <= 0 {
		return defaultAgentTimeoutMs
	}
	return c.AgentTimeoutMsRaw
}

func (c *WorkflowConfig) StallTimeoutMs() int {
	if c == nil || c.StallTimeoutMsRaw <= 0 {
		return defaultStallTimeoutMs
	}
	return c.StallTimeoutMsRaw
}

func (c *WorkflowConfig) Model() (string, error) {
	if c == nil || c.ModelRaw == "" {
		return "", ErrModelRequired
	}
	return c.ModelRaw, nil
}

func (c *WorkflowConfig) ProjectURL() (string, error) {
	if c == nil || c.ProjectURLRaw == "" {
		return "", ErrProjectURLRequired
	}
	return c.ProjectURLRaw, nil
}
