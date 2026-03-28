package multiproject

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// HubConfig defines the multi-project orchestrator configuration.
type HubConfig struct {
	// MaxActiveProjects limits how many projects can have running agents
	// simultaneously. A new project starts only when an active slot frees up.
	MaxActiveProjects int `yaml:"max_active_projects"`

	// LogFile is the path to the hub log file.
	LogFile string `yaml:"log_file"`

	// LogLevel sets the log verbosity (debug/info/warn/error).
	LogLevel string `yaml:"log_level"`

	// Projects lists the projects managed by the hub.
	Projects []ProjectEntry `yaml:"projects"`
}

// ProjectEntry describes a single project for the hub.
type ProjectEntry struct {
	// Path is the absolute path to the project directory.
	Path string `yaml:"path"`

	// Config is the relative path to WORKFLOW.md inside the project.
	// Defaults to "WORKFLOW.md".
	Config string `yaml:"config"`

	// Enabled allows disabling a project without removing it from the list.
	// Defaults to true when omitted.
	Enabled *bool `yaml:"enabled"`
}

// IsEnabled returns whether the project is enabled (default true).
func (p ProjectEntry) IsEnabled() bool {
	if p.Enabled == nil {
		return true
	}
	return *p.Enabled
}

// ConfigPath returns the full path to the project's WORKFLOW.md.
func (p ProjectEntry) ConfigPath() string {
	cfg := p.Config
	if cfg == "" {
		cfg = "WORKFLOW.md"
	}
	return p.Path + "/" + cfg
}

const (
	defaultMaxActiveProjects = 5
	defaultHubLogFile        = "contrabass-hub.log"
)

// ParseHubConfig reads and parses a hub YAML config file.
func ParseHubConfig(path string) (*HubConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading hub config: %w", err)
	}

	var cfg HubConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing hub config: %w", err)
	}

	if cfg.MaxActiveProjects <= 0 {
		cfg.MaxActiveProjects = defaultMaxActiveProjects
	}
	if cfg.LogFile == "" {
		cfg.LogFile = defaultHubLogFile
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	if len(cfg.Projects) == 0 {
		return nil, fmt.Errorf("hub config has no projects")
	}

	for i, p := range cfg.Projects {
		if p.Path == "" {
			return nil, fmt.Errorf("project #%d has empty path", i+1)
		}
	}

	return &cfg, nil
}
