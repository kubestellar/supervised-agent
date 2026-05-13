package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
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
	outputBufferCapacity = 500
	kickHistoryCapacity  = 50
	tmuxCaptureLines     = 200
	paneCaptureSleep     = 500 * time.Millisecond
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
	tmuxSession     string
	cancel          context.CancelFunc
}

type Manager struct {
	agents  map[string]*AgentProcess
	mu      sync.RWMutex
	logger  *slog.Logger
	workDir string
}

func NewManager(agents map[string]config.AgentConfig, logger *slog.Logger) *Manager {
	workDir := os.Getenv("HIVE_WORK_DIR")
	if workDir == "" {
		workDir = "/data/agents"
	}

	m := &Manager{
		agents:  make(map[string]*AgentProcess),
		logger:  logger,
		workDir: workDir,
	}

	for name, cfg := range agents {
		m.agents[name] = &AgentProcess{
			Name:         name,
			Config:       cfg,
			State:        StateStopped,
			OutputBuffer: NewRingBuffer(outputBufferCapacity),
			tmuxSession:  "hive-" + name,
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

	if err := m.ensureTmuxSession(agent); err != nil {
		return err
	}

	if agent.Paused {
		agent.State = StatePaused
		return nil
	}

	return m.launchInTmux(ctx, agent)
}

func (m *Manager) ensureTmuxSession(agent *AgentProcess) error {
	if m.tmuxSessionExists(agent.tmuxSession) {
		return nil
	}

	agentDir := m.workDir + "/" + agent.Name
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return fmt.Errorf("creating agent work dir %s: %w", agentDir, err)
	}

	cmd := exec.Command("tmux", "new-session", "-d", "-s", agent.tmuxSession, "-c", agentDir)
	cmd.Env = append(os.Environ(), agentEnvVars(agent)...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("creating tmux session for %s: %w", agent.Name, err)
	}

	m.logger.Info("tmux session created", "name", agent.Name, "session", agent.tmuxSession)
	return nil
}

func (m *Manager) tmuxSessionExists(session string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", session)
	return cmd.Run() == nil
}

func (m *Manager) launchInTmux(ctx context.Context, agent *AgentProcess) error {
	backend := agent.Config.Backend
	if agent.BackendOverride != "" {
		backend = agent.BackendOverride
	}

	binary, err := backendBinary(backend)
	if err != nil {
		agent.State = StateFailed
		m.logger.Warn("backend binary not found", "name", agent.Name, "backend", backend, "error", err)
		return nil
	}

	launchCmd := binary
	model := agent.Config.Model
	if agent.ModelOverride != "" {
		model = agent.ModelOverride
	}

	switch backend {
	case "claude":
		launchCmd = fmt.Sprintf("%s --model %s --dangerously-skip-permissions", binary, model)
	case "copilot":
		launchCmd = fmt.Sprintf("%s --model %s --allow-all", binary, model)
	case "gemini":
		launchCmd = fmt.Sprintf("%s --model %s", binary, model)
	case "goose":
		if model != "" {
			launchCmd = fmt.Sprintf("%s --model %s", binary, model)
		}
	case "bob":
		launchCmd = binary
	default:
		launchCmd = binary
	}

	envCmd := m.buildEnvPrefix(agent)
	fullCmd := envCmd + launchCmd

	cmd := exec.Command("tmux", "send-keys", "-t", agent.tmuxSession, fullCmd, "Enter")
	if err := cmd.Run(); err != nil {
		agent.State = StateFailed
		return fmt.Errorf("launching CLI in tmux for %s: %w", agent.Name, err)
	}

	now := time.Now()
	agent.State = StateRunning
	agent.StartedAt = &now
	m.logger.Info("agent launched in tmux", "name", agent.Name, "backend", backend, "session", agent.tmuxSession)

	agentCtx, cancel := context.WithCancel(ctx)
	agent.cancel = cancel
	go m.pollTmuxOutput(agent.Name, agent.tmuxSession, agent.OutputBuffer, agentCtx)

	return nil
}

func (m *Manager) buildEnvPrefix(agent *AgentProcess) string {
	vars := agentEnvVars(agent)
	if len(vars) == 0 {
		return ""
	}
	return strings.Join(vars, " ") + " "
}

func (m *Manager) pollTmuxOutput(name, session string, buf *RingBuffer, ctx context.Context) {
	const pollInterval = 3 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			output := m.captureTmuxPane(session)
			if output != "" {
				for _, line := range strings.Split(output, "\n") {
					trimmed := strings.TrimRight(line, " \t")
					if trimmed != "" {
						buf.Write(trimmed)
					}
				}
			}
		}
	}
}

func (m *Manager) captureTmuxPane(session string) string {
	cmd := exec.Command("tmux", "capture-pane", "-t", session, "-p",
		"-S", fmt.Sprintf("-%d", tmuxCaptureLines))
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
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

	cmd := exec.Command("tmux", "send-keys", "-t", agent.tmuxSession, "C-c", "")
	_ = cmd.Run()

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

	if agent.State != StateRunning {
		return fmt.Errorf("agent %s not running", name)
	}

	if !m.tmuxSessionExists(agent.tmuxSession) {
		return fmt.Errorf("tmux session %s not found", agent.tmuxSession)
	}

	if agent.Config.ClearOnKick {
		clearCmd := exec.Command("tmux", "send-keys", "-t", agent.tmuxSession, "/clear", "Enter")
		if err := clearCmd.Run(); err != nil {
			return fmt.Errorf("sending /clear to %s: %w", name, err)
		}
		m.logger.Info("clear sent before kick", "name", name)
		time.Sleep(clearBeforeKickDelay)
	}

	escaped := strings.ReplaceAll(message, "'", "'\\''")
	sendCmd := exec.Command("tmux", "send-keys", "-t", agent.tmuxSession, "-l", escaped)
	if err := sendCmd.Run(); err != nil {
		return fmt.Errorf("sending kick text to %s: %w", name, err)
	}
	enterCmd := exec.Command("tmux", "send-keys", "-t", agent.tmuxSession, "Enter")
	if err := enterCmd.Run(); err != nil {
		return fmt.Errorf("sending enter to %s: %w", name, err)
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
		tmuxSession:     a.tmuxSession,
	}
}

func backendBinary(backend string) (string, error) {
	binaries := map[string]string{
		"claude":  "claude",
		"copilot": "copilot",
		"gemini":  "gemini",
		"goose":   "goose",
		"bob":     "bob",
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
	if agent.State == StateRunning {
		cmd := exec.Command("tmux", "send-keys", "-t", agent.tmuxSession, "C-c", "")
		_ = cmd.Run()
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
		if err := m.ensureTmuxSession(agent); err != nil {
			return err
		}
		return m.launchInTmux(ctx, agent)
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

	if agent.State == StateRunning {
		cmd := exec.Command("tmux", "send-keys", "-t", agent.tmuxSession, "C-c", "")
		_ = cmd.Run()
		if agent.cancel != nil {
			agent.cancel()
		}
	}

	killCmd := exec.Command("tmux", "kill-session", "-t", agent.tmuxSession)
	_ = killCmd.Run()

	agent.RestartCount++
	m.logger.Info("agent restarting", "name", name, "restart_count", agent.RestartCount)

	if err := m.ensureTmuxSession(agent); err != nil {
		return err
	}
	return m.launchInTmux(ctx, agent)
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

	if m.tmuxSessionExists(agent.tmuxSession) {
		output := m.captureTmuxPane(agent.tmuxSession)
		if output != "" {
			allLines := strings.Split(output, "\n")
			if len(allLines) > lines {
				allLines = allLines[len(allLines)-lines:]
			}
			return allLines, nil
		}
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

func (m *Manager) TmuxSession(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[name]
	if !ok {
		return ""
	}
	return agent.tmuxSession
}
