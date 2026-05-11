package agent

import (
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
)

type AgentProcess struct {
	Name      string
	Config    config.AgentConfig
	State     ProcessState
	PID       int
	StartedAt *time.Time
	LastKick  *time.Time
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stderr    io.ReadCloser
	cancel    context.CancelFunc
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
			Name:   name,
			Config: cfg,
			State:  StateStopped,
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

	binary, err := backendBinary(agent.Config.Backend)
	if err != nil {
		return err
	}

	agentCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(agentCtx, binary)
	cmd.Env = append(os.Environ(), agentEnvVars(agent)...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("creating stdin pipe for %s: %w", name, err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("creating stdout pipe for %s: %w", name, err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("creating stderr pipe for %s: %w", name, err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("starting agent %s: %w", name, err)
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

	m.logger.Info("agent started", "name", name, "pid", agent.PID, "backend", agent.Config.Backend)

	go m.watchProcess(name, cmd, agentCtx)

	return nil
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

	if _, err := fmt.Fprintf(agent.stdin, "%s\n", message); err != nil {
		return fmt.Errorf("sending kick to %s: %w", name, err)
	}

	now := time.Now()
	agent.LastKick = &now
	m.logger.Info("kick sent", "name", name, "message_len", len(message))

	return nil
}

func (m *Manager) GetStatus(name string) (*AgentProcess, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[name]
	if !ok {
		return nil, fmt.Errorf("agent %s not found", name)
	}
	return agent, nil
}

func (m *Manager) AllStatuses() map[string]*AgentProcess {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*AgentProcess, len(m.agents))
	for k, v := range m.agents {
		result[k] = v
	}
	return result
}

func (m *Manager) watchProcess(name string, cmd *exec.Cmd, ctx context.Context) {
	err := cmd.Wait()

	m.mu.Lock()
	agent, ok := m.agents[name]
	if ok {
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
	return []string{
		fmt.Sprintf("HIVE_AGENT=%s", agent.Name),
		fmt.Sprintf("HIVE_BACKEND=%s", agent.Config.Backend),
		fmt.Sprintf("HIVE_MODEL=%s", agent.Config.Model),
	}
}
