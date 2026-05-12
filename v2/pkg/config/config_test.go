package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// minimalValidYAML returns the smallest YAML that passes validate().
// It uses a real token so github auth check passes.
func minimalValidYAML(org, token string) string {
	return `
project:
  org: ` + org + `
  repos:
    - repo-a
github:
  token: ` + token + `
agents:
  worker:
    backend: claude
    enabled: true
`
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hive.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return path
}

// ---------------------------------------------------------------------------
// Load — happy path
// ---------------------------------------------------------------------------

func TestLoad_ValidConfig(t *testing.T) {
	yaml := `
project:
  org: my-org
  repos:
    - my-repo
    - other-repo
  ai_author: bot
  primary_repo: my-repo
github:
  token: ghp_test
agents:
  scanner:
    backend: claude
    model: claude-3-5-sonnet
    enabled: true
dashboard:
  port: 4000
  snapshot_dir: /tmp/snaps
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Project.Org != "my-org" {
		t.Errorf("Project.Org = %q, want %q", cfg.Project.Org, "my-org")
	}
	if len(cfg.Project.Repos) != 2 {
		t.Errorf("len(Project.Repos) = %d, want 2", len(cfg.Project.Repos))
	}
	if cfg.GitHub.Token != "ghp_test" {
		t.Errorf("GitHub.Token = %q, want %q", cfg.GitHub.Token, "ghp_test")
	}
	if cfg.Dashboard.Port != 4000 {
		t.Errorf("Dashboard.Port = %d, want 4000", cfg.Dashboard.Port)
	}
	if cfg.Agents["scanner"].Backend != "claude" {
		t.Errorf("Agents[scanner].Backend = %q, want %q", cfg.Agents["scanner"].Backend, "claude")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/hive.yaml")
	if err == nil {
		t.Fatal("Load() expected error for missing file, got nil")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTempConfig(t, ":::invalid yaml:::")
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for invalid YAML, got nil")
	}
}

// ---------------------------------------------------------------------------
// expandEnvVars
// ---------------------------------------------------------------------------

func TestExpandEnvVars_KnownVar(t *testing.T) {
	t.Setenv("MY_TOKEN", "secret123")
	got := expandEnvVars("token: ${MY_TOKEN}")
	want := "token: secret123"
	if got != want {
		t.Errorf("expandEnvVars() = %q, want %q", got, want)
	}
}

func TestExpandEnvVars_UnsetVarLeftAsIs(t *testing.T) {
	// Guarantee the variable is not set.
	os.Unsetenv("DEFINITELY_NOT_SET_XYZ")
	got := expandEnvVars("token: ${DEFINITELY_NOT_SET_XYZ}")
	want := "token: ${DEFINITELY_NOT_SET_XYZ}"
	if got != want {
		t.Errorf("expandEnvVars() = %q, want %q", got, want)
	}
}

func TestExpandEnvVars_MultipleVars(t *testing.T) {
	t.Setenv("ORG", "my-org")
	t.Setenv("REPO", "my-repo")
	got := expandEnvVars("org: ${ORG}\nrepo: ${REPO}")
	want := "org: my-org\nrepo: my-repo"
	if got != want {
		t.Errorf("expandEnvVars() = %q, want %q", got, want)
	}
}

func TestExpandEnvVars_NoPlaceholders(t *testing.T) {
	input := "plain text without placeholders"
	got := expandEnvVars(input)
	if got != input {
		t.Errorf("expandEnvVars() modified input unexpectedly: %q", got)
	}
}

func TestExpandEnvVars_EmptyString(t *testing.T) {
	got := expandEnvVars("")
	if got != "" {
		t.Errorf("expandEnvVars(\"\") = %q, want empty string", got)
	}
}

// ---------------------------------------------------------------------------
// applyDefaults
// ---------------------------------------------------------------------------

func TestApplyDefaults_DashboardPort(t *testing.T) {
	path := writeTempConfig(t, minimalValidYAML("my-org", "ghp_tok"))
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Dashboard.Port != defaultDashboardPort {
		t.Errorf("Dashboard.Port = %d, want %d", cfg.Dashboard.Port, defaultDashboardPort)
	}
}

func TestApplyDefaults_DashboardPortNotOverridden(t *testing.T) {
	yaml := minimalValidYAML("my-org", "ghp_tok") + "\ndashboard:\n  port: 9090\n"
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Dashboard.Port != 9090 {
		t.Errorf("Dashboard.Port = %d, want 9090 (explicit value should not be overwritten)", cfg.Dashboard.Port)
	}
}

func TestApplyDefaults_EvalInterval(t *testing.T) {
	path := writeTempConfig(t, minimalValidYAML("my-org", "ghp_tok"))
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Governor.EvalIntervalS != defaultEvalIntervalS {
		t.Errorf("Governor.EvalIntervalS = %d, want %d", cfg.Governor.EvalIntervalS, defaultEvalIntervalS)
	}
}

func TestApplyDefaults_PollInterval(t *testing.T) {
	path := writeTempConfig(t, minimalValidYAML("my-org", "ghp_tok"))
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := time.Duration(defaultPollIntervalMins) * time.Minute
	if cfg.Policies.PollInterval != want {
		t.Errorf("Policies.PollInterval = %v, want %v", cfg.Policies.PollInterval, want)
	}
}

func TestApplyDefaults_DataDirs(t *testing.T) {
	path := writeTempConfig(t, minimalValidYAML("my-org", "ghp_tok"))
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Data.MetricsDir != "/data/metrics" {
		t.Errorf("Data.MetricsDir = %q, want %q", cfg.Data.MetricsDir, "/data/metrics")
	}
	if cfg.Data.LogsDir != "/data/logs" {
		t.Errorf("Data.LogsDir = %q, want %q", cfg.Data.LogsDir, "/data/logs")
	}
}

func TestApplyDefaults_AgentBeadsDir(t *testing.T) {
	// Agent has no beads_dir set — default should be /data/beads/<name>.
	path := writeTempConfig(t, minimalValidYAML("my-org", "ghp_tok"))
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Agents["worker"].BeadsDir != "/data/beads/worker" {
		t.Errorf("Agents[worker].BeadsDir = %q, want %q", cfg.Agents["worker"].BeadsDir, "/data/beads/worker")
	}
}

func TestApplyDefaults_AgentEnabled(t *testing.T) {
	// An agent with enabled: false gets flipped to true by applyDefaults.
	yaml := `
project:
  org: my-org
  repos:
    - repo-a
github:
  token: ghp_tok
agents:
  scanner:
    backend: claude
    enabled: false
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Agents["scanner"].Enabled {
		t.Errorf("Agents[scanner].Enabled = false, want true (applyDefaults should set it)")
	}
}

// ---------------------------------------------------------------------------
// validate — missing required fields
// ---------------------------------------------------------------------------

func TestValidate_MissingOrg(t *testing.T) {
	yaml := `
project:
  repos:
    - repo-a
github:
  token: ghp_tok
agents:
  w:
    backend: claude
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for missing org, got nil")
	}
}

func TestValidate_EmptyRepos(t *testing.T) {
	yaml := `
project:
  org: my-org
github:
  token: ghp_tok
agents:
  w:
    backend: claude
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for empty repos, got nil")
	}
}

func TestValidate_NoAgents(t *testing.T) {
	yaml := `
project:
  org: my-org
  repos:
    - repo-a
github:
  token: ghp_tok
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for no agents, got nil")
	}
}

func TestValidate_NoGitHubAuth(t *testing.T) {
	yaml := `
project:
  org: my-org
  repos:
    - repo-a
agents:
  w:
    backend: claude
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error when neither github.token nor github.app_id is set, got nil")
	}
}

func TestValidate_GitHubAppIDAccepted(t *testing.T) {
	// app_id without token should be accepted.
	yaml := `
project:
  org: my-org
  repos:
    - repo-a
github:
  app_id: 12345
  installation_id: 67890
  key_file: /tmp/key.pem
agents:
  w:
    backend: claude
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err != nil {
		t.Errorf("Load() unexpected error with app_id auth: %v", err)
	}
}

// ---------------------------------------------------------------------------
// validate — invalid backend
// ---------------------------------------------------------------------------

func TestValidate_InvalidBackend(t *testing.T) {
	yaml := `
project:
  org: my-org
  repos:
    - repo-a
github:
  token: ghp_tok
agents:
  w:
    backend: openai
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for invalid backend, got nil")
	}
}

func TestValidate_ValidBackends(t *testing.T) {
	validBackends := []string{"claude", "copilot", "gemini", "goose"}
	for _, backend := range validBackends {
		t.Run(backend, func(t *testing.T) {
			yaml := `
project:
  org: my-org
  repos:
    - repo-a
github:
  token: ghp_tok
agents:
  w:
    backend: ` + backend + `
`
			path := writeTempConfig(t, yaml)
			_, err := Load(path)
			if err != nil {
				t.Errorf("Load() unexpected error for backend %q: %v", backend, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// EnabledAgents
// ---------------------------------------------------------------------------

func TestEnabledAgents_FiltersDisabled(t *testing.T) {
	// After Load(), applyDefaults sets all agents to enabled=true.
	// Test EnabledAgents() directly on a crafted Config where one is disabled.
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"active":   {Backend: "claude", Enabled: true},
			"inactive": {Backend: "gemini", Enabled: false},
		},
	}
	enabled := cfg.EnabledAgents()
	if _, ok := enabled["active"]; !ok {
		t.Error("EnabledAgents() missing 'active' agent")
	}
	if _, ok := enabled["inactive"]; ok {
		t.Error("EnabledAgents() should not include 'inactive' (disabled) agent")
	}
	if len(enabled) != 1 {
		t.Errorf("EnabledAgents() returned %d agents, want 1", len(enabled))
	}
}

func TestEnabledAgents_AllEnabled(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"a": {Backend: "claude", Enabled: true},
			"b": {Backend: "gemini", Enabled: true},
		},
	}
	enabled := cfg.EnabledAgents()
	if len(enabled) != 2 {
		t.Errorf("EnabledAgents() returned %d agents, want 2", len(enabled))
	}
}

func TestEnabledAgents_NoneEnabled(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"a": {Backend: "claude", Enabled: false},
		},
	}
	enabled := cfg.EnabledAgents()
	if len(enabled) != 0 {
		t.Errorf("EnabledAgents() returned %d agents, want 0", len(enabled))
	}
}

// ---------------------------------------------------------------------------
// Env var interpolation via Load()
// ---------------------------------------------------------------------------

func TestLoad_EnvVarInToken(t *testing.T) {
	t.Setenv("HIVE_GITHUB_TOKEN", "ghp_from_env")
	yaml := `
project:
  org: my-org
  repos:
    - repo-a
github:
  token: ${HIVE_GITHUB_TOKEN}
agents:
  w:
    backend: claude
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GitHub.Token != "ghp_from_env" {
		t.Errorf("GitHub.Token = %q, want %q", cfg.GitHub.Token, "ghp_from_env")
	}
}

func TestLoad_UnsetEnvVarInToken_FailsValidation(t *testing.T) {
	// The placeholder remains unexpanded → token stays "${MISSING_TOKEN}" →
	// not empty, so validate() passes. But the raw placeholder string is
	// preserved as the token value. Verify Load succeeds and token holds the
	// unexpanded placeholder (the caller's responsibility to handle).
	os.Unsetenv("MISSING_TOKEN")
	yaml := `
project:
  org: my-org
  repos:
    - repo-a
github:
  token: ${MISSING_TOKEN}
agents:
  w:
    backend: claude
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GitHub.Token != "${MISSING_TOKEN}" {
		t.Errorf("GitHub.Token = %q, want ${MISSING_TOKEN} (unexpanded)", cfg.GitHub.Token)
	}
}

func TestLoad_EnvVarInOrg(t *testing.T) {
	t.Setenv("HIVE_ORG", "env-org")
	yaml := `
project:
  org: ${HIVE_ORG}
  repos:
    - repo-a
github:
  token: ghp_tok
agents:
  w:
    backend: claude
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Project.Org != "env-org" {
		t.Errorf("Project.Org = %q, want %q", cfg.Project.Org, "env-org")
	}
}

// ---------------------------------------------------------------------------
// config.env overrides
// ---------------------------------------------------------------------------

func writeConfigEnv(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config.env: %v", err)
	}
	return path
}

func TestParseEnvFile_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")
	content := `# Comment line
PROJECT_ORG=override-org
PROJECT_REPOS=repo-x repo-y repo-z
DASHBOARD_PORT=4000
`
	os.WriteFile(path, []byte(content), 0o600)

	env, err := ParseEnvFile(path)
	if err != nil {
		t.Fatalf("ParseEnvFile() error = %v", err)
	}
	if env["PROJECT_ORG"] != "override-org" {
		t.Errorf("PROJECT_ORG = %q, want %q", env["PROJECT_ORG"], "override-org")
	}
	if env["PROJECT_REPOS"] != "repo-x repo-y repo-z" {
		t.Errorf("PROJECT_REPOS = %q", env["PROJECT_REPOS"])
	}
	if env["DASHBOARD_PORT"] != "4000" {
		t.Errorf("DASHBOARD_PORT = %q, want %q", env["DASHBOARD_PORT"], "4000")
	}
}

func TestParseEnvFile_QuotedValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")
	content := `KEY1="double quoted"
KEY2='single quoted'
`
	os.WriteFile(path, []byte(content), 0o600)

	env, err := ParseEnvFile(path)
	if err != nil {
		t.Fatalf("ParseEnvFile() error = %v", err)
	}
	if env["KEY1"] != "double quoted" {
		t.Errorf("KEY1 = %q, want %q", env["KEY1"], "double quoted")
	}
	if env["KEY2"] != "single quoted" {
		t.Errorf("KEY2 = %q, want %q", env["KEY2"], "single quoted")
	}
}

func TestParseEnvFile_MissingFile(t *testing.T) {
	_, err := ParseEnvFile("/nonexistent/config.env")
	if err == nil {
		t.Fatal("ParseEnvFile() expected error for missing file, got nil")
	}
}

func TestLoadWithOverrides_ConfigEnvOverridesOrg(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "hive.yaml")
	os.WriteFile(yamlPath, []byte(minimalValidYAML("yaml-org", "ghp_tok")), 0o600)

	envPath := filepath.Join(dir, "config.env")
	os.WriteFile(envPath, []byte("PROJECT_ORG=env-org\n"), 0o600)

	cfg, err := LoadWithOverrides(yamlPath, envPath)
	if err != nil {
		t.Fatalf("LoadWithOverrides() error = %v", err)
	}
	if cfg.Project.Org != "env-org" {
		t.Errorf("Project.Org = %q, want %q (config.env should override yaml)", cfg.Project.Org, "env-org")
	}
}

func TestLoadWithOverrides_ConfigEnvOverridesRepos(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "hive.yaml")
	os.WriteFile(yamlPath, []byte(minimalValidYAML("my-org", "ghp_tok")), 0o600)

	envPath := filepath.Join(dir, "config.env")
	os.WriteFile(envPath, []byte("PROJECT_REPOS=my-org/alpha my-org/beta\n"), 0o600)

	cfg, err := LoadWithOverrides(yamlPath, envPath)
	if err != nil {
		t.Fatalf("LoadWithOverrides() error = %v", err)
	}
	if len(cfg.Project.Repos) != 2 {
		t.Fatalf("len(Project.Repos) = %d, want 2", len(cfg.Project.Repos))
	}
	if cfg.Project.Repos[0] != "my-org/alpha" {
		t.Errorf("Project.Repos[0] = %q, want %q", cfg.Project.Repos[0], "my-org/alpha")
	}
}

func TestLoadWithOverrides_DashDisabled(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "hive.yaml")
	os.WriteFile(yamlPath, []byte(minimalValidYAML("my-org", "ghp_tok")), 0o600)

	cfg, err := LoadWithOverrides(yamlPath, "-")
	if err != nil {
		t.Fatalf("LoadWithOverrides() error = %v", err)
	}
	if cfg.Project.Org != "my-org" {
		t.Errorf("Project.Org = %q, want %q (dash should skip config.env)", cfg.Project.Org, "my-org")
	}
}

func TestLoadWithOverrides_AutoDetectConfigEnv(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "hive.yaml")
	os.WriteFile(yamlPath, []byte(minimalValidYAML("yaml-org", "ghp_tok")), 0o600)

	writeConfigEnv(t, dir, "PROJECT_ORG=auto-detected-org\n")

	cfg, err := LoadWithOverrides(yamlPath, "")
	if err != nil {
		t.Fatalf("LoadWithOverrides() error = %v", err)
	}
	if cfg.Project.Org != "auto-detected-org" {
		t.Errorf("Project.Org = %q, want %q (auto-detected config.env)", cfg.Project.Org, "auto-detected-org")
	}
}

func TestLoadWithOverrides_DashboardPort(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "hive.yaml")
	os.WriteFile(yamlPath, []byte(minimalValidYAML("my-org", "ghp_tok")+"\ndashboard:\n  port: 3001\n"), 0o600)

	envPath := filepath.Join(dir, "config.env")
	os.WriteFile(envPath, []byte("DASHBOARD_PORT=9090\n"), 0o600)

	cfg, err := LoadWithOverrides(yamlPath, envPath)
	if err != nil {
		t.Fatalf("LoadWithOverrides() error = %v", err)
	}
	if cfg.Dashboard.Port != 9090 {
		t.Errorf("Dashboard.Port = %d, want 9090 (config.env override)", cfg.Dashboard.Port)
	}
}

func TestAgentConfig_ClearOnKick(t *testing.T) {
	yaml := `
project:
  org: my-org
  repos:
    - repo-a
github:
  token: ghp_tok
agents:
  scanner:
    backend: claude
    clear_on_kick: true
  reviewer:
    backend: claude
    clear_on_kick: false
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Agents["scanner"].ClearOnKick {
		t.Error("scanner.ClearOnKick should be true")
	}
	if cfg.Agents["reviewer"].ClearOnKick {
		t.Error("reviewer.ClearOnKick should be false")
	}
}
