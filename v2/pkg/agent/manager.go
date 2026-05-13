package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/kubestellar/hive/v2/pkg/config"
)

type ProcessState string

const (
	StateIdle     ProcessState = "idle"
	StateRunning  ProcessState = "running"
	StateStopped  ProcessState = "stopped"
	StateFailed   ProcessState = "failed"
	StatePaused   ProcessState = "paused"
)

type KickRecord struct {
	Timestamp time.Time `json:"timestamp"`
	Agent     string    `json:"agent"`
	Snippet   string    `json:"snippet"`
}

const (
	outputBufferCapacity  = 500
	kickHistoryCapacity   = 50
)

type AgentProcess struct {
	Name            string
	Config          config.AgentConfig
	State           ProcessState
	PID             int
	StartedAt       *time.Time
	LastKick        *time.Time
	Paused          bool
	PinnedCLI       string
	PinnedModel     string
	ModelOverride   string
	BackendOverride string
	RestartCount    int
	OutputBuffer    *RingBuffer
	KickHistory     []KickRecord
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	stdout          io.ReadCloser
	stderr          io.ReadCloser
	cancel          context.CancelFunc
}

type Manager struct {
	agents map[string]*AgentProcess
	mu     sync.RWMutex
	logger *slog.Logger
}

func NewManager(agents map[string]config.AgentConfig, logger *slog.Logger) *Manager {
	m := &Manager{
		agents: make(map[string]*AgentProcess),
		logger: logger,
	}

	for name, cfg := range agents {
		m.agents[name] = &AgentProcess{
			Name:         name,
			Config:       cfg,
			State:        StateStopped,
			OutputBuffer: NewRingBuffer(outputBufferCapacity),
		}
	}

	return m
}

func (m *Manager) Start(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	if agent.State == StateRunning {
		return fmt.Errorf("agent %s already running", name)
	}

	if agent.Paused {
		agent.State = StatePaused
		return nil
	}

	return m.startLocked(ctx, agent)
}

func (m *Manager) startLocked(ctx context.Context, agent *AgentProcess) error {
	backend := agent.Config.Backend
	if agent.BackendOverride != "" {
		backend = agent.BackendOverride
	}

	binary, err := backendBinary(backend)
	if err != nil {
		return err
	}

	agentCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(agentCtx, binary)
	cmd.Env = append(os.Environ(), agentEnvVars(agent)...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("creating stdin pipe for %s: %w", agent.Name, err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("creating stdout pipe for %s: %w", agent.Name, err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("creating stderr pipe for %s: %w", agent.Name, err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("starting agent %s: %w", agent.Name, err)
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

	m.logger.Info("agent started", "name", agent.Name, "pid", agent.PID, "backend", backend)

	go m.captureOutput(agent.Name, stdout, agent.OutputBuffer)
	go m.captureOutput(agent.Name, stderr, agent.OutputBuffer)
	go m.watchProcess(agent.Name, cmd, agentCtx)

	return nil
}

func (m *Manager) captureOutput(name string, r io.ReadCloser, buf *RingBuffer) {
	scanner := bufio.NewScanner(r)
	const maxOutputLineBytes = 64 * 1024
	scanner.Buffer(make([]byte, 0, maxOutputLineBytes), maxOutputLineBytes)
	for scanner.Scan() {
		buf.Write(scanner.Text())
	}
}

func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	if agent.State != StateRunning {
		return nil
	}

	if agent.cancel != nil {
		agent.cancel()
	}

	agent.State = StateStopped
	m.logger.Info("agent stopped", "name", name)

	return nil
}

func (m *Manager) SendKick(name string, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	if agent.State != StateRunning || agent.stdin == nil {
		return fmt.Errorf("agent %s not running", name)
	}

	if agent.Config.ClearOnKick {
		if _, err := fmt.Fprintf(agent.stdin, "/clear\n"); err != nil {
			return fmt.Errorf("sending /clear to %s: %w", name, err)
		}
		m.logger.Info("clear sent before kick", "name", name)
		time.Sleep(clearBeforeKickDelay)
	}

	if _, err := fmt.Fprintf(agent.stdin, "%s\n", message); err != nil {
		return fmt.Errorf("sending kick to %s: %w", name, err)
	}

	now := time.Now()
	agent.LastKick = &now

	snippet := message
	const maxSnippetLen = 120
	if len(snippet) > maxSnippetLen {
		snippet = snippet[:maxSnippetLen] + "..."
	}
	record := KickRecord{Timestamp: now, Agent: name, Snippet: snippet}
	if len(agent.KickHistory) >= kickHistoryCapacity {
		agent.KickHistory = agent.KickHistory[1:]
	}
	agent.KickHistory = append(agent.KickHistory, record)

	m.logger.Info("kick sent", "name", name, "message_len", len(message))

	return nil
}

const clearBeforeKickDelay = 2 * time.Second

func (m *Manager) GetStatus(name string) (*AgentProcess, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[name]
	if !ok {
		return nil, fmt.Errorf("agent %s not found", name)
	}
	snap := agent.snapshot()
	return &snap, nil
}

func (m *Manager) AllStatuses() map[string]*AgentProcess {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*AgentProcess, len(m.agents))
	for k, v := range m.agents {
		snap := v.snapshot()
		result[k] = &snap
	}
	return result
}

func (a *AgentProcess) snapshot() AgentProcess {
	history := make([]KickRecord, len(a.KickHistory))
	copy(history, a.KickHistory)
	return AgentProcess{
		Name:            a.Name,
		Config:          a.Config,
		State:           a.State,
		PID:             a.PID,
		StartedAt:       a.StartedAt,
		LastKick:        a.LastKick,
		Paused:          a.Paused,
		PinnedCLI:       a.PinnedCLI,
		PinnedModel:     a.PinnedModel,
		ModelOverride:   a.ModelOverride,
		BackendOverride: a.BackendOverride,
		RestartCount:    a.RestartCount,
		KickHistory:     history,
	}
}

func (m *Manager) watchProcess(name string, cmd *exec.Cmd, ctx context.Context) {
	err := cmd.Wait()

	m.mu.Lock()
	agent, ok := m.agents[name]
	if ok && agent.State == StateRunning {
		if err != nil {
			agent.State = StateFailed
			m.logger.Warn("agent process exited with error", "name", name, "error", err)
		} else {
			agent.State = StateStopped
			m.logger.Info("agent process exited cleanly", "name", name)
		}
	}
	m.mu.Unlock()
}

func backendBinary(backend string) (string, error) {
	binaries := map[string]string{
		"claude":  "claude",
		"copilot": "copilot",
		"gemini":  "gemini",
		"goose":   "goose",
	}

	binary, ok := binaries[backend]
	if !ok {
		return "", fmt.Errorf("unknown backend: %s", backend)
	}

	path, err := exec.LookPath(binary)
	if err != nil {
		return "", fmt.Errorf("backend %s not found in PATH: %w", backend, err)
	}

	return path, nil
}

func agentEnvVars(agent *AgentProcess) []string {
	model := agent.Config.Model
	if agent.ModelOverride != "" {
		model = agent.ModelOverride
	}
	backend := agent.Config.Backend
	if agent.BackendOverride != "" {
		backend = agent.BackendOverride
	}
	return []string{
		fmt.Sprintf("HIVE_AGENT=%s", agent.Name),
		fmt.Sprintf("HIVE_BACKEND=%s", backend),
		fmt.Sprintf("HIVE_MODEL=%s", model),
	}
}

func (m *Manager) Pause(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.Paused = true
	if agent.State == StateRunning && agent.cancel != nil {
		agent.cancel()
	}
	agent.State = StatePaused
	m.logger.Info("agent paused", "name", name)
	return nil
}

func (m *Manager) Resume(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.Paused = false
	if agent.State == StatePaused {
		return m.startLocked(ctx, agent)
	}
	return nil
}

func (m *Manager) Restart(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	if agent.State == StateRunning && agent.cancel != nil {
		agent.cancel()
	}

	agent.RestartCount++
	m.logger.Info("agent restarting", "name", name, "restart_count", agent.RestartCount)
	return m.startLocked(ctx, agent)
}

func (m *Manager) ResetRestartCount(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.RestartCount = 0
	return nil
}

func (m *Manager) PinCLI(name, version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.PinnedCLI = version
	m.logger.Info("agent CLI pinned", "name", name, "version", version)
	return nil
}

func (m *Manager) UnpinCLI(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.PinnedCLI = ""
	m.logger.Info("agent CLI unpinned", "name", name)
	return nil
}

func (m *Manager) PinModel(name, model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.PinnedModel = model
	agent.ModelOverride = model
	m.logger.Info("agent model pinned", "name", name, "model", model)
	return nil
}

func (m *Manager) UnpinModel(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.PinnedModel = ""
	m.logger.Info("agent model unpinned", "name", name)
	return nil
}

func (m *Manager) SetModelOverride(name, model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	if agent.PinnedModel != "" {
		return fmt.Errorf("agent %s model is pinned to %s", name, agent.PinnedModel)
	}

	agent.ModelOverride = model
	m.logger.Info("agent model override set", "name", name, "model", model)
	return nil
}

func (m *Manager) SetBackendOverride(name, backend string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[name]
	if !ok {
		return fmt.Errorf("agent %s not found", name)
	}

	agent.BackendOverride = backend
	m.logger.Info("agent backend override set", "name", name, "backend", backend)
	return nil
}

func (m *Manager) GetOutput(name string, lines int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[name]
	if !ok {
		return nil, fmt.Errorf("agent %s not found", name)
	}

	if agent.OutputBuffer == nil {
		return nil, nil
	}

	return agent.OutputBuffer.Last(lines), nil
}

func (m *Manager) IsPaused(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[name]
	if !ok {
		return false
	}
	return agent.Paused
}
