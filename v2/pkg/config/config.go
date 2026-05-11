package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Project       ProjectConfig                `yaml:"project"`
	Policies      PoliciesConfig               `yaml:"policies"`
	Agents        map[string]AgentConfig        `yaml:"agents"`
	Governor      GovernorConfig               `yaml:"governor"`
	GitHub        GitHubConfig                 `yaml:"github"`
	Notifications NotificationsConfig          `yaml:"notifications"`
	Dashboard     DashboardConfig              `yaml:"dashboard"`
	Data          DataConfig                   `yaml:"data"`
}

type ProjectConfig struct {
	Org         string   `yaml:"org"`
	Repos       []string `yaml:"repos"`
	AIAuthor    string   `yaml:"ai_author"`
	PrimaryRepo string   `yaml:"primary_repo"`
}

type PoliciesConfig struct {
	Repo         string        `yaml:"repo"`
	Path         string        `yaml:"path"`
	PollInterval time.Duration `yaml:"poll_interval"`
	LocalDir     string        `yaml:"local_dir"`
}

type AgentConfig struct {
	Backend  string `yaml:"backend"`
	Model    string `yaml:"model"`
	BeadsDir string `yaml:"beads_dir"`
	Enabled  bool   `yaml:"enabled"`
}

type GovernorConfig struct {
	Modes          map[string]ModeConfig `yaml:"modes"`
	EvalIntervalS  int                   `yaml:"eval_interval_s"`
}

type ModeConfig struct {
	Threshold int               `yaml:"threshold"`
	Cadences  map[string]string `yaml:"cadences"`
}

type GitHubConfig struct {
	AppID          int64  `yaml:"app_id"`
	InstallationID int64  `yaml:"installation_id"`
	KeyFile        string `yaml:"key_file"`
	Token          string `yaml:"token"`
}

type NotificationsConfig struct {
	Ntfy    *NtfyConfig    `yaml:"ntfy,omitempty"`
	Slack   *SlackConfig   `yaml:"slack,omitempty"`
	Discord *DiscordConfig `yaml:"discord,omitempty"`
}

type NtfyConfig struct {
	Server string `yaml:"server"`
	Topic  string `yaml:"topic"`
}

type SlackConfig struct {
	Webhook string `yaml:"webhook"`
}

type DiscordConfig struct {
	Webhook  string `yaml:"webhook"`
	BotToken string `yaml:"bot_token"`
}

type DashboardConfig struct {
	Port        int    `yaml:"port"`
	SnapshotDir string `yaml:"snapshot_dir"`
}

type DataConfig struct {
	MetricsDir string `yaml:"metrics_dir"`
	LogsDir    string `yaml:"logs_dir"`
}

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	expanded := expandEnvVars(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

func expandEnvVars(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		varName := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		return match
	})
}

const (
	defaultDashboardPort    = 3001
	defaultEvalIntervalS    = 300
	defaultPollIntervalMins = 5
)

func (c *Config) applyDefaults() {
	if c.Dashboard.Port == 0 {
		c.Dashboard.Port = defaultDashboardPort
	}
	if c.Governor.EvalIntervalS == 0 {
		c.Governor.EvalIntervalS = defaultEvalIntervalS
	}
	if c.Policies.PollInterval == 0 {
		c.Policies.PollInterval = time.Duration(defaultPollIntervalMins) * time.Minute
	}
	if c.Data.MetricsDir == "" {
		c.Data.MetricsDir = "/data/metrics"
	}
	if c.Data.LogsDir == "" {
		c.Data.LogsDir = "/data/logs"
	}
	for name, agent := range c.Agents {
		if agent.BeadsDir == "" {
			agent.BeadsDir = fmt.Sprintf("/data/beads/%s", name)
		}
		if !agent.Enabled {
			agent.Enabled = true
		}
		c.Agents[name] = agent
	}
}

func (c *Config) validate() error {
	if c.Project.Org == "" {
		return fmt.Errorf("project.org is required")
	}
	if len(c.Project.Repos) == 0 {
		return fmt.Errorf("project.repos must have at least one repo")
	}
	if len(c.Agents) == 0 {
		return fmt.Errorf("at least one agent must be configured")
	}
	if c.GitHub.Token == "" && c.GitHub.AppID == 0 {
		return fmt.Errorf("github.token or github.app_id is required")
	}
	for name, agent := range c.Agents {
		validBackends := map[string]bool{"claude": true, "copilot": true, "gemini": true, "goose": true}
		if !validBackends[agent.Backend] {
			return fmt.Errorf("agent %s: invalid backend %q (must be claude, copilot, gemini, or goose)", name, agent.Backend)
		}
	}
	return nil
}

func (c *Config) EnabledAgents() map[string]AgentConfig {
	result := make(map[string]AgentConfig)
	for name, agent := range c.Agents {
		if agent.Enabled {
			result[name] = agent
		}
	}
	return result
}
