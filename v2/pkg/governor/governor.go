package governor

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kubestellar/hive/v2/pkg/config"
)

type Mode string

const (
	ModeSurge Mode = "SURGE"
	ModeBusy  Mode = "BUSY"
	ModeQuiet Mode = "QUIET"
	ModeIdle  Mode = "IDLE"
)

type AgentCadence struct {
	Agent    string
	Interval time.Duration
	Paused   bool
}

type State struct {
	Mode             Mode
	QueueIssues      int
	QueuePRs         int
	QueueHold        int
	Cadences         map[string]AgentCadence
	LastKick         map[string]time.Time
	LastEval         time.Time
	SLAViolations    int
}

type Governor struct {
	cfg    config.GovernorConfig
	agents map[string]config.AgentConfig
	state  State
	mu     sync.RWMutex
	logger *slog.Logger
}

func New(cfg config.GovernorConfig, agents map[string]config.AgentConfig, logger *slog.Logger) *Governor {
	return &Governor{
		cfg:    cfg,
		agents: agents,
		state: State{
			Mode:     ModeIdle,
			Cadences: make(map[string]AgentCadence),
			LastKick: make(map[string]time.Time),
		},
		logger: logger,
	}
}

func (g *Governor) Evaluate(queueIssues, queuePRs, queueHold, slaViolations int) []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.state.QueueIssues = queueIssues
	g.state.QueuePRs = queuePRs
	g.state.QueueHold = queueHold
	g.state.SLAViolations = slaViolations
	g.state.LastEval = time.Now()

	newMode := g.computeMode(queueIssues)
	if newMode != g.state.Mode {
		g.logger.Info("governor mode change",
			"from", g.state.Mode,
			"to", newMode,
			"issues", queueIssues,
			"prs", queuePRs,
		)
		g.state.Mode = newMode
	}

	g.updateCadences()

	return g.agentsDueForKick()
}

func (g *Governor) computeMode(queueDepth int) Mode {
	type modeEntry struct {
		name      Mode
		threshold int
	}

	entries := []modeEntry{
		{ModeSurge, g.thresholdFor("surge")},
		{ModeBusy, g.thresholdFor("busy")},
		{ModeQuiet, g.thresholdFor("quiet")},
	}

	for _, e := range entries {
		if queueDepth > e.threshold {
			return e.name
		}
	}
	return ModeIdle
}

func (g *Governor) thresholdFor(modeName string) int {
	if mode, ok := g.cfg.Modes[modeName]; ok {
		return mode.Threshold
	}
	switch modeName {
	case "surge":
		return 20
	case "busy":
		return 10
	case "quiet":
		return 2
	default:
		return 0
	}
}

func (g *Governor) updateCadences() {
	modeName := modeToConfigKey(g.state.Mode)
	modeConfig, ok := g.cfg.Modes[modeName]
	if !ok {
		return
	}

	for agentName := range g.agents {
		cadenceStr, ok := modeConfig.Cadences[agentName]
		if !ok {
			continue
		}

		if cadenceStr == "pause" || cadenceStr == "paused" {
			g.state.Cadences[agentName] = AgentCadence{
				Agent:  agentName,
				Paused: true,
			}
			continue
		}

		dur, err := time.ParseDuration(cadenceStr)
		if err != nil {
			g.logger.Warn("invalid cadence duration",
				"agent", agentName,
				"mode", g.state.Mode,
				"value", cadenceStr,
				"error", err,
			)
			continue
		}

		g.state.Cadences[agentName] = AgentCadence{
			Agent:    agentName,
			Interval: dur,
		}
	}
}

func (g *Governor) agentsDueForKick() []string {
	now := time.Now()
	var due []string

	for agentName, cadence := range g.state.Cadences {
		if cadence.Paused {
			continue
		}
		if cadence.Interval == 0 {
			continue
		}

		lastKick, kicked := g.state.LastKick[agentName]
		if !kicked || now.Sub(lastKick) >= cadence.Interval {
			due = append(due, agentName)
		}
	}

	return due
}

func (g *Governor) RecordKick(agentName string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state.LastKick[agentName] = time.Now()
}

func (g *Governor) GetState() State {
	g.mu.RLock()
	defer g.mu.RUnlock()
	// Return a copy
	cadences := make(map[string]AgentCadence, len(g.state.Cadences))
	for k, v := range g.state.Cadences {
		cadences[k] = v
	}
	lastKick := make(map[string]time.Time, len(g.state.LastKick))
	for k, v := range g.state.LastKick {
		lastKick[k] = v
	}
	return State{
		Mode:          g.state.Mode,
		QueueIssues:   g.state.QueueIssues,
		QueuePRs:      g.state.QueuePRs,
		QueueHold:     g.state.QueueHold,
		Cadences:      cadences,
		LastKick:      lastKick,
		LastEval:      g.state.LastEval,
		SLAViolations: g.state.SLAViolations,
	}
}

func modeToConfigKey(m Mode) string {
	switch m {
	case ModeSurge:
		return "surge"
	case ModeBusy:
		return "busy"
	case ModeQuiet:
		return "quiet"
	case ModeIdle:
		return "idle"
	default:
		return "idle"
	}
}

func (g *Governor) FormatStatus() string {
	s := g.GetState()
	return fmt.Sprintf("mode=%s issues=%d prs=%d hold=%d sla_violations=%d",
		s.Mode, s.QueueIssues, s.QueuePRs, s.QueueHold, s.SLAViolations)
}
