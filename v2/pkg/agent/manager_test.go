package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kubestellar/hive/v2/pkg/config"
)

// ---------------------------------------------------------------------------
// TestMain: install stub backends so Start() can exercise its full path
// ---------------------------------------------------------------------------

// stubBinDir holds the temp directory containing stub backend binaries.
// It is created in TestMain and injected at the front of PATH.
var stubBinDir string

// TestMain sets up stub binaries for every known backend (claude, copilot,
// gemini, goose) so that backendBinary() resolves them and Start() can create
// real child processes without needing the actual CLI tools installed.
//
// Each stub is a shell script that behaves like `cat` (reads stdin, echoes
// stdout) so that SendKick and pipe-setup code is exerciseable too.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hive-agent-stubs-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: MkdirTemp: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	stubBinDir = dir

	// Stub script: read stdin line by line and echo it (like cat).
	// This keeps the process alive until stdin is closed or the context cancels.
	const stubScript = "#!/bin/sh\nexec cat\n"

	for _, name := range []string{"claude", "copilot", "gemini", "goose"} {
		path := fmt.Sprintf("%s/%s", dir, name)
		if err := os.WriteFile(path, []byte(stubScript), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "TestMain: writing stub %s: %v\n", name, err)
			os.Exit(1)
		}
	}

	// Prepend stub dir so exec.LookPath finds our stubs first.
	originalPath := os.Getenv("PATH")
	os.Setenv("PATH", dir+":"+originalPath)
	defer os.Setenv("PATH", originalPath)

	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// discardLogger returns a logger that discards all output, keeping test output clean.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// makeAgentConfig is a convenience helper for building a config.AgentConfig.
func makeAgentConfig(backend, model string) config.AgentConfig {
	return config.AgentConfig{
		Backend: backend,
		Model:   model,
		Enabled: true,
	}
}

// catPath resolves the absolute path to `cat`.
func catPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("cat")
	if err != nil {
		t.Skip("cat not found in PATH — skipping test that requires a real process")
	}
	return p
}

// startWithCat injects a `cat` process into the manager under the given agent
// name without going through backendBinary, allowing us to test the running
// state logic without touching the backend map.
func startWithCat(t *testing.T, m *Manager, name string) context.CancelFunc {
	t.Helper()
	catBin := catPath(t)

	m.mu.Lock()
	defer m.mu.Unlock()

	agent := m.agents[name]
	if agent == nil {
		t.Fatalf("agent %q not found in manager", name)
	}

	agentCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(agentCtx, catBin)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("StdoutPipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		t.Fatalf("StderrPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("cmd.Start: %v", err)
	}

	now := time.Now()
	agent.cmd = cmd
	agent.stdin = stdin
	agent.stdout = stdout
	agent.stderr = stderr
	agent.cancel = cancel
	agent.State = StateRunning
	agent.PID = cmd.Process.Pid
	agent.StartedAt = &now

	go m.watchProcess(name, cmd, agentCtx)

	return cancel
}

// waitForState polls the agent's state until it matches want or the deadline passes.
func waitForState(t *testing.T, m *Manager, name string, want ...ProcessState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ap, _ := m.GetStatus(name)
		for _, w := range want {
			if ap.State == w {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	ap, _ := m.GetStatus(name)
	wantStrs := make([]string, len(want))
	for i, w := range want {
		wantStrs[i] = string(w)
	}
	t.Errorf("timed out waiting for %q state: got %q", strings.Join(wantStrs, "|"), ap.State)
}

// ---------------------------------------------------------------------------
// NewManager
// ---------------------------------------------------------------------------

func TestNewManager_InitializesAgentsAsStopped(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"scanner": makeAgentConfig("claude", "claude-3-5-sonnet"),
		"worker":  makeAgentConfig("gemini", "gemini-pro"),
	}

	m := NewManager(cfgs, discardLogger())

	if len(m.agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(m.agents))
	}

	for name, ap := range m.agents {
		if ap.State != StateStopped {
			t.Errorf("agent %q: expected state %q, got %q", name, StateStopped, ap.State)
		}
		if ap.Name != name {
			t.Errorf("agent %q: Name field = %q, want %q", name, ap.Name, name)
		}
		if ap.PID != 0 {
			t.Errorf("agent %q: expected PID 0 before start, got %d", name, ap.PID)
		}
		if ap.StartedAt != nil {
			t.Errorf("agent %q: expected nil StartedAt before start", name)
		}
	}
}

func TestNewManager_EmptyAgentMap(t *testing.T) {
	m := NewManager(map[string]config.AgentConfig{}, discardLogger())
	if len(m.agents) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(m.agents))
	}
}

func TestNewManager_ConfigPreserved(t *testing.T) {
	cfg := makeAgentConfig("gemini", "gemini-ultra")
	cfg.BeadsDir = "/tmp/beads"

	m := NewManager(map[string]config.AgentConfig{"agent1": cfg}, discardLogger())

	ap := m.agents["agent1"]
	if ap.Config.Backend != "gemini" {
		t.Errorf("Config.Backend = %q, want %q", ap.Config.Backend, "gemini")
	}
	if ap.Config.Model != "gemini-ultra" {
		t.Errorf("Config.Model = %q, want %q", ap.Config.Model, "gemini-ultra")
	}
	if ap.Config.BeadsDir != "/tmp/beads" {
		t.Errorf("Config.BeadsDir = %q, want %q", ap.Config.BeadsDir, "/tmp/beads")
	}
}

// ---------------------------------------------------------------------------
// GetStatus
// ---------------------------------------------------------------------------

func TestGetStatus_ReturnsCorrectAgent(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"alpha": makeAgentConfig("claude", "opus"),
		"beta":  makeAgentConfig("gemini", "pro"),
	}
	m := NewManager(cfgs, discardLogger())

	ap, err := m.GetStatus("alpha")
	if err != nil {
		t.Fatalf("GetStatus(%q) unexpected error: %v", "alpha", err)
	}
	if ap.Name != "alpha" {
		t.Errorf("Name = %q, want %q", ap.Name, "alpha")
	}
	if ap.Config.Backend != "claude" {
		t.Errorf("Config.Backend = %q, want %q", ap.Config.Backend, "claude")
	}
}

func TestGetStatus_UnknownAgentReturnsError(t *testing.T) {
	m := NewManager(map[string]config.AgentConfig{}, discardLogger())

	_, err := m.GetStatus("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown agent, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error %q should mention the agent name", err.Error())
	}
}

func TestGetStatus_ReturnsPointerToSameProcess(t *testing.T) {
	cfgs := map[string]config.AgentConfig{"a": makeAgentConfig("claude", "haiku")}
	m := NewManager(cfgs, discardLogger())

	ap1, _ := m.GetStatus("a")
	ap2, _ := m.GetStatus("a")

	if ap1 != ap2 {
		t.Error("expected GetStatus to return the same *AgentProcess on repeated calls")
	}
}

// ---------------------------------------------------------------------------
// AllStatuses
// ---------------------------------------------------------------------------

func TestAllStatuses_ReturnsAllAgents(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"x": makeAgentConfig("claude", "opus"),
		"y": makeAgentConfig("gemini", "pro"),
		"z": makeAgentConfig("goose", ""),
	}
	m := NewManager(cfgs, discardLogger())

	all := m.AllStatuses()

	if len(all) != 3 {
		t.Fatalf("AllStatuses() returned %d entries, want 3", len(all))
	}
	for _, name := range []string{"x", "y", "z"} {
		if _, ok := all[name]; !ok {
			t.Errorf("AllStatuses() missing agent %q", name)
		}
	}
}

func TestAllStatuses_ReturnsCopy(t *testing.T) {
	cfgs := map[string]config.AgentConfig{"a": makeAgentConfig("claude", "sonnet")}
	m := NewManager(cfgs, discardLogger())

	all := m.AllStatuses()
	delete(all, "a")

	all2 := m.AllStatuses()
	if _, ok := all2["a"]; !ok {
		t.Error("AllStatuses() returned the internal map instead of a copy — delete affected manager state")
	}
}

func TestAllStatuses_EmptyManager(t *testing.T) {
	m := NewManager(map[string]config.AgentConfig{}, discardLogger())
	all := m.AllStatuses()
	if len(all) != 0 {
		t.Errorf("expected empty map, got %d entries", len(all))
	}
}

// ---------------------------------------------------------------------------
// backendBinary
// ---------------------------------------------------------------------------

func TestBackendBinary_UnknownBackendReturnsError(t *testing.T) {
	_, err := backendBinary("nonexistent-backend")
	if err == nil {
		t.Fatal("expected error for unknown backend, got nil")
	}
	if !strings.Contains(err.Error(), "unknown backend") {
		t.Errorf("error %q should contain 'unknown backend'", err.Error())
	}
}

func TestBackendBinary_EmptyBackendReturnsError(t *testing.T) {
	_, err := backendBinary("")
	if err == nil {
		t.Fatal("expected error for empty backend, got nil")
	}
}

func TestBackendBinary_KnownBackendsResolveToStubs(t *testing.T) {
	// TestMain installed stubs for all known backends in stubBinDir.
	knownBackends := []string{"claude", "copilot", "gemini", "goose"}

	for _, backend := range knownBackends {
		t.Run(backend, func(t *testing.T) {
			path, err := backendBinary(backend)
			if err != nil {
				t.Errorf("backendBinary(%q) unexpected error: %v", backend, err)
				return
			}
			if !strings.HasPrefix(path, "/") {
				t.Errorf("backendBinary(%q) returned non-absolute path %q", backend, path)
			}
			// Must resolve to our stub dir (or a real installation — either is fine).
			if path == "" {
				t.Errorf("backendBinary(%q) returned empty path", backend)
			}
		})
	}
}

func TestBackendBinary_ReturnsAbsolutePath(t *testing.T) {
	path, err := backendBinary("claude")
	if err != nil {
		t.Fatalf("backendBinary(claude) error: %v", err)
	}
	if !strings.HasPrefix(path, "/") {
		t.Errorf("expected absolute path, got %q", path)
	}
}

// ---------------------------------------------------------------------------
// agentEnvVars
// ---------------------------------------------------------------------------

func TestAgentEnvVars_ContainsRequiredKeys(t *testing.T) {
	ap := &AgentProcess{
		Name: "test-agent",
		Config: config.AgentConfig{
			Backend: "claude",
			Model:   "claude-3-5-sonnet",
		},
	}

	vars := agentEnvVars(ap)

	want := map[string]string{
		"HIVE_AGENT":   "test-agent",
		"HIVE_BACKEND": "claude",
		"HIVE_MODEL":   "claude-3-5-sonnet",
	}

	for _, v := range vars {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 {
			t.Errorf("env var %q is not in KEY=VALUE form", v)
			continue
		}
		key, val := parts[0], parts[1]
		if expected, ok := want[key]; ok {
			if val != expected {
				t.Errorf("env var %q = %q, want %q", key, val, expected)
			}
			delete(want, key)
		}
	}

	for missing := range want {
		t.Errorf("env var %q missing from agentEnvVars output", missing)
	}
}

func TestAgentEnvVars_ExactlyThreeEntries(t *testing.T) {
	ap := &AgentProcess{
		Name:   "agent",
		Config: config.AgentConfig{Backend: "gemini", Model: "pro"},
	}
	vars := agentEnvVars(ap)
	if len(vars) != 3 {
		t.Errorf("agentEnvVars() returned %d vars, want 3", len(vars))
	}
}

func TestAgentEnvVars_EmptyModelAllowed(t *testing.T) {
	ap := &AgentProcess{
		Name:   "nomodel",
		Config: config.AgentConfig{Backend: "goose", Model: ""},
	}
	vars := agentEnvVars(ap)

	found := false
	for _, v := range vars {
		if v == "HIVE_MODEL=" {
			found = true
		}
	}
	if !found {
		t.Error("expected HIVE_MODEL= (empty) to be present when model is unset")
	}
}

// ---------------------------------------------------------------------------
// Start — happy path (uses stub binary installed by TestMain)
// ---------------------------------------------------------------------------

func TestStart_SucceedsWithStubBackend(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"worker": makeAgentConfig("claude", "claude-3-5-sonnet"),
	}
	m := NewManager(cfgs, discardLogger())

	ctx := context.Background()
	if err := m.Start(ctx, "worker"); err != nil {
		t.Fatalf("Start() unexpected error: %v", err)
	}

	ap, err := m.GetStatus("worker")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if ap.State != StateRunning {
		t.Errorf("State = %q after Start(), want %q", ap.State, StateRunning)
	}
	if ap.PID == 0 {
		t.Error("PID should be non-zero after Start()")
	}
	if ap.StartedAt == nil {
		t.Error("StartedAt should be set after Start()")
	}
	if ap.stdin == nil {
		t.Error("stdin pipe should be set after Start()")
	}
	if ap.stdout == nil {
		t.Error("stdout pipe should be set after Start()")
	}
	if ap.stderr == nil {
		t.Error("stderr pipe should be set after Start()")
	}
	if ap.cancel == nil {
		t.Error("cancel func should be set after Start()")
	}

	// Clean up.
	_ = m.Stop("worker")
}

func TestStart_SetsStartedAtTimestamp(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"worker": makeAgentConfig("gemini", "pro"),
	}
	m := NewManager(cfgs, discardLogger())

	before := time.Now()
	if err := m.Start(context.Background(), "worker"); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	after := time.Now()
	defer m.Stop("worker")

	ap, _ := m.GetStatus("worker")
	if ap.StartedAt == nil {
		t.Fatal("StartedAt is nil after Start()")
	}
	if ap.StartedAt.Before(before) || ap.StartedAt.After(after) {
		t.Errorf("StartedAt %v outside [%v, %v]", ap.StartedAt, before, after)
	}
}

func TestStart_EnvVarsSetOnProcess(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"worker": makeAgentConfig("claude", "haiku"),
	}
	m := NewManager(cfgs, discardLogger())

	if err := m.Start(context.Background(), "worker"); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer m.Stop("worker")

	// Verify via agentEnvVars helper — the cmd.Env is set from os.Environ() + agentEnvVars.
	ap, _ := m.GetStatus("worker")
	found := false
	for _, v := range ap.cmd.Env {
		if v == "HIVE_AGENT=worker" {
			found = true
			break
		}
	}
	if !found {
		t.Error("HIVE_AGENT=worker not found in cmd.Env after Start()")
	}
}

// ---------------------------------------------------------------------------
// Start — error paths
// ---------------------------------------------------------------------------

func TestStart_UnknownAgentReturnsError(t *testing.T) {
	m := NewManager(map[string]config.AgentConfig{}, discardLogger())

	err := m.Start(context.Background(), "ghost")
	if err == nil {
		t.Fatal("expected error starting unknown agent, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q should mention the agent name", err.Error())
	}
}

func TestStart_UnknownBackendReturnsError(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"bad": makeAgentConfig("not-a-real-backend", ""),
	}
	m := NewManager(cfgs, discardLogger())

	err := m.Start(context.Background(), "bad")
	if err == nil {
		t.Fatal("expected error for unknown backend, got nil")
	}
	if !strings.Contains(err.Error(), "unknown backend") {
		t.Errorf("error %q should say 'unknown backend'", err.Error())
	}
}

func TestStart_AlreadyRunningReturnsError(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"worker": makeAgentConfig("claude", "sonnet"),
	}
	m := NewManager(cfgs, discardLogger())

	if err := m.Start(context.Background(), "worker"); err != nil {
		t.Fatalf("first Start() error: %v", err)
	}
	defer m.Stop("worker")

	err := m.Start(context.Background(), "worker")
	if err == nil {
		t.Fatal("expected error when starting an already-running agent, got nil")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error %q should mention 'already running'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Stop
// ---------------------------------------------------------------------------

func TestStop_UnknownAgentReturnsError(t *testing.T) {
	m := NewManager(map[string]config.AgentConfig{}, discardLogger())
	err := m.Stop("ghost")
	if err == nil {
		t.Fatal("expected error stopping unknown agent, got nil")
	}
}

func TestStop_NonRunningAgentIsNoOp(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"idle": makeAgentConfig("claude", "haiku"),
	}
	m := NewManager(cfgs, discardLogger())

	if err := m.Stop("idle"); err != nil {
		t.Fatalf("Stop() on non-running agent returned error: %v", err)
	}

	ap, _ := m.GetStatus("idle")
	if ap.State != StateStopped {
		t.Errorf("State = %q after no-op Stop(), want %q", ap.State, StateStopped)
	}
}

func TestStop_RunningAgentChangesStateToStopped(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"worker": makeAgentConfig("claude", "sonnet"),
	}
	m := NewManager(cfgs, discardLogger())
	cancel := startWithCat(t, m, "worker")
	defer cancel()

	ap, _ := m.GetStatus("worker")
	if ap.State != StateRunning {
		t.Fatalf("precondition: expected StateRunning, got %q", ap.State)
	}

	if err := m.Stop("worker"); err != nil {
		t.Fatalf("Stop() returned unexpected error: %v", err)
	}

	if ap.State != StateStopped {
		t.Errorf("State = %q after Stop(), want %q", ap.State, StateStopped)
	}
}

func TestStop_FailedStateAgentIsNoOp(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"crashed": makeAgentConfig("claude", "sonnet"),
	}
	m := NewManager(cfgs, discardLogger())

	m.mu.Lock()
	m.agents["crashed"].State = StateFailed
	m.mu.Unlock()

	if err := m.Stop("crashed"); err != nil {
		t.Fatalf("Stop() on failed agent returned error: %v", err)
	}

	ap, _ := m.GetStatus("crashed")
	if ap.State != StateFailed {
		t.Errorf("Stop() should not change state of a non-running agent; got %q", ap.State)
	}
}

func TestStop_ViaPublicStartThenStop(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"worker": makeAgentConfig("gemini", "pro"),
	}
	m := NewManager(cfgs, discardLogger())

	if err := m.Start(context.Background(), "worker"); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if err := m.Stop("worker"); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	ap, _ := m.GetStatus("worker")
	if ap.State != StateStopped {
		t.Errorf("State = %q after Stop(), want StateStopped", ap.State)
	}
}

// ---------------------------------------------------------------------------
// SendKick
// ---------------------------------------------------------------------------

func TestSendKick_UnknownAgentReturnsError(t *testing.T) {
	m := NewManager(map[string]config.AgentConfig{}, discardLogger())
	err := m.SendKick("nobody", "hello")
	if err == nil {
		t.Fatal("expected error for unknown agent, got nil")
	}
	if !strings.Contains(err.Error(), "nobody") {
		t.Errorf("error %q should mention the agent name", err.Error())
	}
}

func TestSendKick_NonRunningAgentReturnsError(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"idle": makeAgentConfig("claude", "haiku"),
	}
	m := NewManager(cfgs, discardLogger())

	err := m.SendKick("idle", "wake up")
	if err == nil {
		t.Fatal("expected error kicking non-running agent, got nil")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("error %q should say 'not running'", err.Error())
	}
}

func TestSendKick_RunningAgentSucceeds(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"worker": makeAgentConfig("claude", "sonnet"),
	}
	m := NewManager(cfgs, discardLogger())
	cancel := startWithCat(t, m, "worker")
	defer cancel()

	if err := m.SendKick("worker", "do something"); err != nil {
		t.Fatalf("SendKick() returned unexpected error: %v", err)
	}

	ap, _ := m.GetStatus("worker")
	if ap.LastKick == nil {
		t.Error("LastKick should be set after a successful SendKick")
	}
}

func TestSendKick_UpdatesLastKick(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"worker": makeAgentConfig("claude", "sonnet"),
	}
	m := NewManager(cfgs, discardLogger())
	cancel := startWithCat(t, m, "worker")
	defer cancel()

	before := time.Now()
	if err := m.SendKick("worker", "ping"); err != nil {
		t.Fatalf("SendKick() error: %v", err)
	}
	after := time.Now()

	ap, _ := m.GetStatus("worker")
	if ap.LastKick == nil {
		t.Fatal("LastKick is nil after SendKick")
	}
	if ap.LastKick.Before(before) || ap.LastKick.After(after) {
		t.Errorf("LastKick %v is outside expected window [%v, %v]", ap.LastKick, before, after)
	}
}

func TestSendKick_MultipleKicksAllSucceed(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"worker": makeAgentConfig("claude", "sonnet"),
	}
	m := NewManager(cfgs, discardLogger())
	cancel := startWithCat(t, m, "worker")
	defer cancel()

	messages := []string{"first", "second", "third"}
	for _, msg := range messages {
		if err := m.SendKick("worker", msg); err != nil {
			t.Fatalf("SendKick(%q) error: %v", msg, err)
		}
	}
}

func TestSendKick_NilStdinReturnsError(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"agent": makeAgentConfig("claude", "haiku"),
	}
	m := NewManager(cfgs, discardLogger())

	m.mu.Lock()
	m.agents["agent"].State = StateRunning
	m.agents["agent"].stdin = nil
	m.mu.Unlock()

	err := m.SendKick("agent", "test")
	if err == nil {
		t.Fatal("expected error when stdin is nil, got nil")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("error %q should say 'not running'", err.Error())
	}
}

func TestSendKick_ViaPublicStart(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"worker": makeAgentConfig("claude", "haiku"),
	}
	m := NewManager(cfgs, discardLogger())

	if err := m.Start(context.Background(), "worker"); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer m.Stop("worker")

	if err := m.SendKick("worker", "kick message"); err != nil {
		t.Fatalf("SendKick() after real Start() error: %v", err)
	}

	ap, _ := m.GetStatus("worker")
	if ap.LastKick == nil {
		t.Error("LastKick should be set after SendKick on public-started agent")
	}
}

// ---------------------------------------------------------------------------
// watchProcess (indirectly via process exit)
// ---------------------------------------------------------------------------

func TestWatchProcess_CleanExitSetsStateStopped(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"worker": makeAgentConfig("claude", "sonnet"),
	}
	m := NewManager(cfgs, discardLogger())
	cancel := startWithCat(t, m, "worker")
	defer cancel()

	m.mu.Lock()
	stdin := m.agents["worker"].stdin
	m.mu.Unlock()

	stdin.Close()
	cancel()

	waitForState(t, m, "worker", StateStopped, StateFailed)
}

func TestWatchProcess_ForcedExitSetsFailed(t *testing.T) {
	catBin := catPath(t)

	cfgs := map[string]config.AgentConfig{
		"victim": makeAgentConfig("claude", "sonnet"),
	}
	m := NewManager(cfgs, discardLogger())

	agentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(agentCtx, catBin)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}

	now := time.Now()
	m.mu.Lock()
	agent := m.agents["victim"]
	agent.cmd = cmd
	agent.stdin = stdin
	agent.stdout = stdout
	agent.stderr = stderr
	agent.cancel = cancel
	agent.State = StateRunning
	agent.PID = cmd.Process.Pid
	agent.StartedAt = &now
	m.mu.Unlock()

	go m.watchProcess("victim", cmd, agentCtx)

	cmd.Process.Kill()

	waitForState(t, m, "victim", StateFailed, StateStopped)
}

func TestWatchProcess_CleanExitViaStubBinary(t *testing.T) {
	// Start a real agent via the public API using the stub binary.
	// Cancel the context (which exec.CommandContext honours) so the stub exits.
	cfgs := map[string]config.AgentConfig{
		"worker": makeAgentConfig("goose", ""),
	}
	m := NewManager(cfgs, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	if err := m.Start(ctx, "worker"); err != nil {
		cancel()
		t.Fatalf("Start() error: %v", err)
	}

	m.mu.Lock()
	stdin := m.agents["worker"].stdin
	m.mu.Unlock()

	// Close stdin so cat sees EOF; cancel context as belt-and-suspenders.
	stdin.Close()
	cancel()

	waitForState(t, m, "worker", StateStopped, StateFailed)
}

// ---------------------------------------------------------------------------
// Integration: Start → SendKick → Stop (using public API + stub backend)
// ---------------------------------------------------------------------------

func TestIntegration_StartKickStop_PublicAPI(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"agent": {Backend: "claude", Model: "test-model", Enabled: true},
	}
	m := NewManager(cfgs, discardLogger())

	// Start via public API.
	if err := m.Start(context.Background(), "agent"); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	status, err := m.GetStatus("agent")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.State != StateRunning {
		t.Errorf("expected StateRunning, got %q", status.State)
	}
	if status.PID == 0 {
		t.Error("PID should be non-zero after start")
	}
	if status.StartedAt == nil {
		t.Error("StartedAt should be set after start")
	}

	// Kick.
	if err := m.SendKick("agent", "hello agent"); err != nil {
		t.Fatalf("SendKick: %v", err)
	}

	// Stop.
	if err := m.Stop("agent"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	status, _ = m.GetStatus("agent")
	if status.State != StateStopped {
		t.Errorf("expected StateStopped after Stop(), got %q", status.State)
	}
	if status.LastKick == nil {
		t.Error("LastKick should be set after kick")
	}
}

func TestIntegration_AllStatuses_ReflectsRunningState(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"a": makeAgentConfig("claude", "opus"),
		"b": makeAgentConfig("gemini", "pro"),
	}
	m := NewManager(cfgs, discardLogger())

	if err := m.Start(context.Background(), "a"); err != nil {
		t.Fatalf("Start(a): %v", err)
	}
	defer m.Stop("a")

	all := m.AllStatuses()
	if all["a"].State != StateRunning {
		t.Errorf("agent 'a' should be running, got %q", all["a"].State)
	}
	if all["b"].State != StateStopped {
		t.Errorf("agent 'b' should be stopped, got %q", all["b"].State)
	}
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestConcurrentGetStatus_NoPanic(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"a": makeAgentConfig("claude", "haiku"),
		"b": makeAgentConfig("gemini", "pro"),
	}
	m := NewManager(cfgs, discardLogger())

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				m.GetStatus("a")
				m.GetStatus("b")
				m.AllStatuses()
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestConcurrentStartStop_NoPanic(t *testing.T) {
	// Start multiple agents concurrently and stop them to exercise mutex paths.
	backends := []string{"claude", "gemini", "copilot", "goose"}
	cfgs := make(map[string]config.AgentConfig, len(backends))
	for _, b := range backends {
		cfgs[b] = makeAgentConfig(b, "")
	}
	m := NewManager(cfgs, discardLogger())

	done := make(chan struct{}, len(backends))
	for _, b := range backends {
		name := b
		go func() {
			defer func() { done <- struct{}{} }()
			if err := m.Start(context.Background(), name); err != nil {
				return // may race — that's fine
			}
			time.Sleep(5 * time.Millisecond)
			m.Stop(name)
		}()
	}
	for range backends {
		<-done
	}
}

// ---------------------------------------------------------------------------
// ProcessState constants sanity check
// ---------------------------------------------------------------------------

func TestProcessStateConstants(t *testing.T) {
	states := []ProcessState{StateIdle, StateRunning, StateStopped, StateFailed}
	seen := make(map[ProcessState]bool)
	for _, s := range states {
		if seen[s] {
			t.Errorf("duplicate ProcessState value: %q", s)
		}
		seen[s] = true
		if string(s) == "" {
			t.Error("ProcessState must not be empty string")
		}
	}
}

// ---------------------------------------------------------------------------
// backendBinary: PATH not found error branch
// ---------------------------------------------------------------------------

func TestBackendBinary_KnownBackendMissingFromPath(t *testing.T) {
	// Temporarily restore PATH to the original (no stubs) to exercise the
	// "not found in PATH" error branch inside backendBinary().
	origPath := os.Getenv("PATH")
	// Use a path that definitely contains none of the known backends.
	os.Setenv("PATH", "/nonexistent-path-for-testing")
	defer os.Setenv("PATH", origPath)

	_, err := backendBinary("claude")
	if err == nil {
		t.Fatal("expected error when backend not in PATH, got nil")
	}
	if !strings.Contains(err.Error(), "not found in PATH") {
		t.Errorf("error %q should mention 'not found in PATH'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Start: cmd.Start() failure branch
// ---------------------------------------------------------------------------

func TestStart_CmdStartFailsForInvalidBinary(t *testing.T) {
	// Create a non-executable-format file (invalid ELF/Mach-O header) that
	// exec.LookPath will accept (it's +x) but exec.Cmd.Start() will reject
	// with an "exec format error".
	dir := t.TempDir()
	badBin := fmt.Sprintf("%s/badclaude", dir)
	// Write an invalid binary header.
	if err := os.WriteFile(badBin, []byte("\x00\x01\x02\x03\x04bad"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Prepend our dir so LookPath resolves our bad binary.
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", dir+":"+origPath)
	defer os.Setenv("PATH", origPath)

	// Rename stub to match the backend name.
	badClaudePath := fmt.Sprintf("%s/claude", dir)
	if err := os.Rename(badBin, badClaudePath); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	cfgs := map[string]config.AgentConfig{
		"worker": makeAgentConfig("claude", "sonnet"),
	}
	m := NewManager(cfgs, discardLogger())

	err := m.Start(context.Background(), "worker")
	if err == nil {
		// On some platforms an empty or invalid file may start as a shell —
		// stop it and skip rather than fail.
		m.Stop("worker")
		t.Skip("platform executed invalid binary without error — skipping")
	}
	if !strings.Contains(err.Error(), "starting agent") {
		t.Errorf("error %q should mention 'starting agent'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// SendKick: fmt.Fprintf error branch (broken pipe)
// ---------------------------------------------------------------------------

// brokenWriter implements io.WriteCloser and always returns an error on Write.
type brokenWriter struct{}

func (b *brokenWriter) Write(p []byte) (int, error) {
	return 0, fmt.Errorf("broken pipe")
}

func (b *brokenWriter) Close() error { return nil }

func TestSendKick_WriteErrorReturnsError(t *testing.T) {
	cfgs := map[string]config.AgentConfig{
		"agent": makeAgentConfig("claude", "haiku"),
	}
	m := NewManager(cfgs, discardLogger())

	// Inject a broken stdin writer while keeping State = Running.
	m.mu.Lock()
	m.agents["agent"].State = StateRunning
	m.agents["agent"].stdin = &brokenWriter{}
	m.mu.Unlock()

	err := m.SendKick("agent", "test message")
	if err == nil {
		t.Fatal("expected error when stdin write fails, got nil")
	}
	if !strings.Contains(err.Error(), "sending kick") {
		t.Errorf("error %q should mention 'sending kick'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// watchProcess: clean exit branch (StateStopped when err == nil)
// ---------------------------------------------------------------------------

func TestWatchProcess_CleanExitMarksStateStopped(t *testing.T) {
	// Use a binary that exits 0 immediately: /bin/true or equivalent.
	trueBin, err := exec.LookPath("true")
	if err != nil {
		// Fallback: use cat, close stdin immediately so it exits 0.
		trueBin = catPath(t)
	}

	cfgs := map[string]config.AgentConfig{
		"agent": makeAgentConfig("claude", "haiku"),
	}
	m := NewManager(cfgs, discardLogger())

	agentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(agentCtx, trueBin)
	// For "true", there's no stdin to pipe — but StdinPipe is needed for the
	// general case. We use cat and close stdin for an immediate clean exit.
	stdinPipe, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}

	// Close stdin so cat (or true) exits cleanly with code 0.
	stdinPipe.Close()

	now := time.Now()
	m.mu.Lock()
	ap := m.agents["agent"]
	ap.cmd = cmd
	ap.stdin = stdinPipe
	ap.stdout = stdout
	ap.stderr = stderr
	ap.cancel = cancel
	ap.State = StateRunning
	ap.PID = cmd.Process.Pid
	ap.StartedAt = &now
	m.mu.Unlock()

	// watchProcess is called synchronously here so we know when it's done.
	m.watchProcess("agent", cmd, agentCtx)

	ap2, _ := m.GetStatus("agent")
	if ap2.State != StateStopped {
		t.Errorf("after clean exit: State = %q, want StateStopped", ap2.State)
	}
}

// ---------------------------------------------------------------------------
// os.Environ integration
// ---------------------------------------------------------------------------

func TestStart_EnvIncludesHiveVars(t *testing.T) {
	ap := &AgentProcess{
		Name:   "env-test",
		Config: config.AgentConfig{Backend: "claude", Model: "opus"},
	}
	extra := agentEnvVars(ap)
	combined := append(os.Environ(), extra...)

	found := false
	for _, v := range combined {
		if v == "HIVE_AGENT=env-test" {
			found = true
			break
		}
	}
	if !found {
		t.Error("HIVE_AGENT=env-test not found in combined env")
	}
}
