package config

import (
	"bufio"
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
	Knowledge     KnowledgeConfig              `yaml:"knowledge"`
	HiveID        string                       `yaml:"hive_id"`
}

type KnowledgeConfig struct {
	Enabled bool                `yaml:"enabled"`
	Engine  string              `yaml:"engine"`
	Layers  []KnowledgeLayer    `yaml:"layers"`
	Vaults  []VaultConfig       `yaml:"vaults"`
	Curator KnowledgeCurator    `yaml:"curator"`
	Primer  KnowledgePrimer     `yaml:"primer"`
}

// VaultConfig describes a file-based Obsidian vault to auto-connect on startup.
type VaultConfig struct {
	Name      string `yaml:"name"`
	Path      string `yaml:"path"`
	AutoIndex bool   `yaml:"auto_index"`
	GitSync   bool   `yaml:"git_sync"`
}

type KnowledgeLayer struct {
	Type   string `yaml:"type"`
	Path   string `yaml:"path,omitempty"`
	URL    string `yaml:"url,omitempty"`
	Shared bool   `yaml:"shared"`
}

type KnowledgeCurator struct {
	Schedule             string   `yaml:"schedule"`
	ExtractFrom          []string `yaml:"extract_from"`
	AutoPromoteThreshold float64  `yaml:"auto_promote_threshold"`
}

type KnowledgePrimer struct {
	MaxFacts      int      `yaml:"max_facts"`
	Priority      []string `yaml:"priority"`
	MergeStrategy string   `yaml:"merge_strategy"`
}

type ProjectConfig struct {
	Org         string   `yaml:"org"`
	Name        string   `yaml:"name"`
	Repos       []string `yaml:"repos"`
	AIAuthor    string   `yaml:"ai_author"`
	PrimaryRepo string   `yaml:"primary_repo"`
}

type PoliciesConfig struct {
	Repo         string        `yaml:"repo"`
	Branch       string        `yaml:"branch"`
	Path         string        `yaml:"path"`
	PollInterval time.Duration `yaml:"poll_interval"`
	LocalDir     string        `yaml:"local_dir"`
}

type AgentConfig struct {
	Backend         string `yaml:"backend"`
	Model           string `yaml:"model"`
	BeadsDir        string `yaml:"beads_dir"`
	Enabled         bool   `yaml:"enabled"`
	ClearOnKick     bool   `yaml:"clear_on_kick"`
	CLIPinned       bool   `yaml:"cli_pinned"`
	StaleTimeout    int    `yaml:"stale_timeout"`
	RestartStrategy string `yaml:"restart_strategy"`
	LaunchCmd       string `yaml:"launch_cmd"`
	DisplayName     string `yaml:"display_name"`
	Description     string `yaml:"description"`
	// clearOnKickSet tracks whether YAML explicitly set clear_on_kick to false
	clearOnKickSet bool
}

func (a *AgentConfig) UnmarshalYAML(value *yaml.Node) error {
	type plain AgentConfig
	if err := value.Decode((*plain)(a)); err != nil {
		return err
	}
	// Check if clear_on_kick was explicitly present in YAML
	for i := 0; i < len(value.Content)-1; i += 2 {
		if value.Content[i].Value == "clear_on_kick" {
			a.clearOnKickSet = true
			break
		}
	}
	return nil
}

type GovernorConfig struct {
	Modes         map[string]ModeConfig `yaml:"modes"`
	EvalIntervalS int                   `yaml:"eval_interval_s"`
	Labels        LabelsConfig          `yaml:"labels"`
	Sensing       SensingConfig         `yaml:"sensing"`
	Health        HealthConfig          `yaml:"health"`
	Budget        BudgetConfig          `yaml:"budget"`
	Logging       LoggingConfig         `yaml:"logging"`
}

type LoggingConfig struct {
	Dir        string `yaml:"dir"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxAgeDays int    `yaml:"max_age_days"`
	MaxBackups int    `yaml:"max_backups"`
	Compress   bool   `yaml:"compress"`
	Level      string `yaml:"level"`
}

type LabelsConfig struct {
	Exempt []string `yaml:"exempt"`
}

type SensingConfig struct {
	GHRatePatterns     []string `yaml:"gh_rate_patterns"`
	CLIExcludePatterns []string `yaml:"cli_exclude_patterns"`
	LoginPatterns      []string `yaml:"login_patterns"`
	TTLSeconds         int      `yaml:"ttl_seconds"`
	PullbackSeconds    int      `yaml:"pullback_seconds"`
}

type HealthConfig struct {
	HealthcheckInterval int  `yaml:"healthcheck_interval"`
	RestartCooldown     int  `yaml:"restart_cooldown"`
	ModelLock           bool `yaml:"model_lock"`
}

type BudgetConfig struct {
	TotalTokens int64 `yaml:"total_tokens"`
	PeriodDays  int   `yaml:"period_days"`
	CriticalPct int   `yaml:"critical_pct"`
}

type ModeConfig struct {
	Threshold int               `yaml:"threshold"`
	Cadences  map[string]string `yaml:"cadences"`
}

// UnmarshalYAML implements custom unmarshaling for ModeConfig.
// The YAML format has threshold and agent cadences as sibling keys:
//
//	idle:
//	  threshold: 0
//	  scanner: 15m
//	  ci-maintainer: 15m
//
// This method separates "threshold" into the Threshold field and collects
// all other keys into the Cadences map.
func (m *ModeConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw map[string]string
	if err := value.Decode(&raw); err != nil {
		return err
	}

	m.Cadences = make(map[string]string)

	const thresholdKey = "threshold"
	if v, ok := raw[thresholdKey]; ok {
		var t int
		if _, err := fmt.Sscanf(v, "%d", &t); err != nil {
			return fmt.Errorf("invalid threshold value %q: %w", v, err)
		}
		m.Threshold = t
	}

	for k, v := range raw {
		if k == thresholdKey {
			continue
		}
		m.Cadences[k] = v
	}

	return nil
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
	Webhook   string `yaml:"webhook"`
	BotToken  string `yaml:"bot_token"`
	ChannelID string `yaml:"channel_id"`
}

type DashboardConfig struct {
	Port               int    `yaml:"port"`
	SnapshotDir        string `yaml:"snapshot_dir"`
	AuthToken          string `yaml:"auth_token"`
	AgentPollIntervalS int    `yaml:"agent_poll_interval_s"`
}

type DataConfig struct {
	MetricsDir          string `yaml:"metrics_dir"`
	LogsDir             string `yaml:"logs_dir"`
	ClaudeSessionsDir   string `yaml:"claude_sessions_dir"`
	CopilotSessionsDir  string `yaml:"copilot_sessions_dir"`
}

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// Load reads hive.yaml, then applies config.env overrides if present.
// Precedence: hive.yaml < config.env < explicit env vars (via ${} interpolation).
func Load(path string) (*Config, error) {
	return LoadWithOverrides(path, "")
}

// LoadWithOverrides reads hive.yaml and applies a config.env override file.
// If envPath is empty, it looks for config.env next to hive.yaml, then at
// /etc/hive/config.env. Pass "-" to skip config.env entirely.
func LoadWithOverrides(path, envPath string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	expanded := expandEnvVars(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	if envPath != "-" {
		if envPath == "" {
			envPath = findConfigEnv(path)
		}
		if envPath != "" {
			if err := cfg.applyConfigEnv(envPath); err != nil {
				return nil, fmt.Errorf("applying config.env %s: %w", envPath, err)
			}
		}
	}

	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// findConfigEnv returns the path to a config.env file, or "" if none found.
func findConfigEnv(yamlPath string) string {
	candidates := []string{
		strings.TrimSuffix(yamlPath, "hive.yaml") + "config.env",
		"/etc/hive/config.env",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// ParseEnvFile reads a flat KEY=VALUE file (# comments, blank lines skipped).
func ParseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		result[key] = val
	}
	return result, scanner.Err()
}

// applyConfigEnv merges flat KEY=VALUE overrides into the loaded config.
func (c *Config) applyConfigEnv(path string) error {
	env, err := ParseEnvFile(path)
	if err != nil {
		return err
	}

	if v, ok := env["PROJECT_ORG"]; ok {
		c.Project.Org = v
	}
	if v, ok := env["PROJECT_REPOS"]; ok {
		c.Project.Repos = strings.Fields(v)
	}
	if v, ok := env["PROJECT_AI_AUTHOR"]; ok {
		c.Project.AIAuthor = v
	}
	if v, ok := env["PROJECT_PRIMARY_REPO"]; ok {
		c.Project.PrimaryRepo = v
	}
	if v, ok := env["AGENTS_ENABLED"]; ok {
		for _, name := range strings.Fields(v) {
			if agent, exists := c.Agents[name]; exists {
				agent.Enabled = true
				c.Agents[name] = agent
			}
		}
	}
	if v, ok := env["DASHBOARD_PORT"]; ok {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil && port > 0 {
			c.Dashboard.Port = port
		}
	}
	if v, ok := env["DASHBOARD_AUTH_TOKEN"]; ok {
		c.Dashboard.AuthToken = v
	}

	return nil
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
	defaultDashboardPort          = 3002
	defaultAgentPollIntervalS     = 10
	defaultEvalIntervalS          = 300
	defaultPollIntervalMins       = 5
	defaultKnowledgeMaxFacts      = 25
	defaultKnowledgeEngine        = "llm-wiki"
	defaultCuratorSchedule        = "daily"
	defaultPromoteThreshold       = 0.9
	defaultSensingTTLSeconds      = 900
	defaultSensingPullbackSeconds = 900
	defaultHealthcheckIntervalS   = 300
	defaultRestartCooldownS       = 60
	defaultBudgetPeriodDays       = 7
	defaultBudgetCriticalPct      = 90
	defaultLogMaxSizeMB           = 50
	defaultLogMaxAgeDays          = 7
	defaultLogMaxBackups          = 10
	defaultLogLevel               = "info"
)

func (c *Config) applyDefaults() {
	if c.Dashboard.Port == 0 {
		c.Dashboard.Port = defaultDashboardPort
	}
	if c.Dashboard.AgentPollIntervalS == 0 {
		c.Dashboard.AgentPollIntervalS = defaultAgentPollIntervalS
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
	if c.Data.ClaudeSessionsDir == "" {
		c.Data.ClaudeSessionsDir = "/data/home/.claude/projects"
	}
	if c.Data.CopilotSessionsDir == "" {
		c.Data.CopilotSessionsDir = "/data/home/.copilot/session-state"
	}
	for name, agent := range c.Agents {
		if agent.BeadsDir == "" {
			agent.BeadsDir = fmt.Sprintf("/data/beads/%s", name)
		}
		if !agent.Enabled {
			agent.Enabled = true
		}
		if !agent.clearOnKickSet {
			agent.ClearOnKick = true
		}
		c.Agents[name] = agent
	}

	if len(c.Governor.Labels.Exempt) == 0 {
		c.Governor.Labels.Exempt = []string{
			"nightly-tests", "LFX", "do-not-merge", "meta-tracker",
			"auto-qa-tuning-report", "hold", "adopters",
			"changes-requested", "waiting-on-author",
		}
	}
	if len(c.Governor.Sensing.GHRatePatterns) == 0 {
		c.Governor.Sensing.GHRatePatterns = []string{
			"API rate limit exceeded",
			"secondary rate limit",
			"403.*rate limit",
			"You have exceeded a secondary rate",
			"retry-after:[[:space:]]*[0-9]",
			"gh: Resource not accessible",
			"abuse detection mechanism",
		}
	}
	if len(c.Governor.Sensing.CLIExcludePatterns) == 0 {
		c.Governor.Sensing.CLIExcludePatterns = []string{
			"You.re out of extra usage",
			"out of extra usage",
			"extra usage.*resets",
			"resets [0-9]+(:[0-9]+)?[aApP][mM]",
		}
	}
	if len(c.Governor.Sensing.LoginPatterns) == 0 {
		c.Governor.Sensing.LoginPatterns = []string{
			"please log in",
			"authentication required",
			"not logged in",
			"login required",
			"session expired",
			"token expired",
			"unauthorized.*401",
			"gh auth login",
			"claude login",
			"copilot auth",
		}
	}
	if c.Governor.Sensing.TTLSeconds == 0 {
		c.Governor.Sensing.TTLSeconds = defaultSensingTTLSeconds
	}
	if c.Governor.Sensing.PullbackSeconds == 0 {
		c.Governor.Sensing.PullbackSeconds = defaultSensingPullbackSeconds
	}
	if c.Governor.Health.HealthcheckInterval == 0 {
		c.Governor.Health.HealthcheckInterval = defaultHealthcheckIntervalS
	}
	if c.Governor.Health.RestartCooldown == 0 {
		c.Governor.Health.RestartCooldown = defaultRestartCooldownS
	}
	if c.Governor.Budget.PeriodDays == 0 {
		c.Governor.Budget.PeriodDays = defaultBudgetPeriodDays
	}
	if c.Governor.Budget.CriticalPct == 0 {
		c.Governor.Budget.CriticalPct = defaultBudgetCriticalPct
	}
	if c.Governor.Logging.Dir == "" {
		c.Governor.Logging.Dir = c.Data.LogsDir
	}
	if c.Governor.Logging.MaxSizeMB == 0 {
		c.Governor.Logging.MaxSizeMB = defaultLogMaxSizeMB
	}
	if c.Governor.Logging.MaxAgeDays == 0 {
		c.Governor.Logging.MaxAgeDays = defaultLogMaxAgeDays
	}
	if c.Governor.Logging.MaxBackups == 0 {
		c.Governor.Logging.MaxBackups = defaultLogMaxBackups
	}
	if !c.Governor.Logging.Compress {
		c.Governor.Logging.Compress = true
	}
	if c.Governor.Logging.Level == "" {
		c.Governor.Logging.Level = defaultLogLevel
	}

	if c.Knowledge.Enabled {
		if c.Knowledge.Engine == "" {
			c.Knowledge.Engine = defaultKnowledgeEngine
		}
		if c.Knowledge.Primer.MaxFacts == 0 {
			c.Knowledge.Primer.MaxFacts = defaultKnowledgeMaxFacts
		}
		if c.Knowledge.Primer.MergeStrategy == "" {
			c.Knowledge.Primer.MergeStrategy = "precedence"
		}
		if len(c.Knowledge.Primer.Priority) == 0 {
			c.Knowledge.Primer.Priority = []string{"regression", "gotcha", "test_scaffold", "pattern", "decision"}
		}
		if c.Knowledge.Curator.Schedule == "" {
			c.Knowledge.Curator.Schedule = defaultCuratorSchedule
		}
		if c.Knowledge.Curator.AutoPromoteThreshold == 0 {
			c.Knowledge.Curator.AutoPromoteThreshold = defaultPromoteThreshold
		}
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
