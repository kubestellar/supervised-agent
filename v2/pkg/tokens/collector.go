package tokens

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type SessionEntry struct {
	Type          string `json:"type"`
	Model         string `json:"model,omitempty"`
	CacheCreation int64  `json:"cache_creation,omitempty"`
	CacheRead     int64  `json:"cache_read,omitempty"`
	InputTokens   int64  `json:"input_tokens,omitempty"`
	OutputTokens  int64  `json:"output_tokens,omitempty"`
	Message       string `json:"message,omitempty"`
	Role          string `json:"role,omitempty"`
}

type SessionSummary struct {
	SessionID    string `json:"session_id"`
	Agent        string `json:"agent"`
	Model        string `json:"model"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CacheRead    int64  `json:"cache_read"`
	CacheCreate  int64  `json:"cache_create"`
	TotalTokens  int64  `json:"total_tokens"`
	Messages     int    `json:"messages"`
	LastActive   int64  `json:"last_active,omitempty"`
}

// AgentModelBucket holds per-agent or per-model token breakdown.
type AgentModelBucket struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cache_read"`
	CacheCreate int64 `json:"cache_create"`
	Messages    int   `json:"messages"`
	Sessions    int   `json:"sessions"`
}

type AggregateSummary struct {
	TotalTokens    int64                        `json:"total_tokens"`
	TotalInput     int64                        `json:"total_input"`
	TotalOutput    int64                        `json:"total_output"`
	TotalCacheRead int64                        `json:"total_cache_read"`
	TotalCacheCreate int64                      `json:"total_cache_create"`
	TotalMessages  int                          `json:"total_messages"`
	ByAgent        map[string]int64             `json:"by_agent"`
	ByModel        map[string]int64             `json:"by_model"`
	ByAgentDetail  map[string]*AgentModelBucket `json:"by_agent_detail"`
	ByModelDetail  map[string]*AgentModelBucket `json:"by_model_detail"`
	Sessions       []SessionSummary             `json:"sessions"`
	SessionCount   int                          `json:"session_count"`
}

const defaultScanInterval = 30 * time.Second

type Collector struct {
	sessionsDir        string
	claudeSessionsDir  string
	detector           func(string) string
	logger             *slog.Logger
	mu                 sync.RWMutex
	latest             *AggregateSummary
	issueCosts         map[string]int64
	scanInterval       time.Duration
}

func NewCollector(sessionsDir string, logger *slog.Logger) *Collector {
	return &Collector{
		sessionsDir:  sessionsDir,
		detector:     DefaultAgentDetector,
		logger:       logger,
		issueCosts:   make(map[string]int64),
		scanInterval: defaultScanInterval,
	}
}

// SetClaudeSessionsDir configures the collector to also scan Claude Code's
// native session files. The path is the Claude projects directory, typically
// ~/.claude/projects (or /root/.claude/projects in containers).
func (c *Collector) SetClaudeSessionsDir(dir string) {
	c.claudeSessionsDir = dir
}

func (c *Collector) Start(stop <-chan struct{}) {
	c.scan()
	ticker := time.NewTicker(c.scanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			c.scan()
		}
	}
}

func (c *Collector) scan() {
	agg, err := CollectFromDir(c.sessionsDir, c.detector)
	if err != nil {
		c.logger.Warn("token scan failed", "error", err)
		return
	}

	// Merge Claude Code native session data if configured
	if c.claudeSessionsDir != "" {
		claudeAgg, err := ScanClaudeSessionsWithPathDetection(c.claudeSessionsDir)
		if err != nil {
			c.logger.Warn("claude session scan failed", "error", err)
		} else if claudeAgg != nil && claudeAgg.SessionCount > 0 {
			MergeAggregates(agg, claudeAgg)
			c.logger.Info("merged claude sessions",
				"claude_sessions", claudeAgg.SessionCount,
				"total_sessions", agg.SessionCount,
			)
		}
	}

	c.mu.Lock()
	c.latest = agg
	c.mu.Unlock()
}

func (c *Collector) Summary() *AggregateSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest
}

func (c *Collector) IssueCosts() map[string]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make(map[string]int64, len(c.issueCosts))
	for k, v := range c.issueCosts {
		result[k] = v
	}
	return result
}

func CollectFromDir(sessionsDir string, agentDetector func(firstMsg string) string) (*AggregateSummary, error) {
	pattern := filepath.Join(sessionsDir, "*.jsonl")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("globbing session files: %w", err)
	}

	agg := &AggregateSummary{
		ByAgent:       make(map[string]int64),
		ByModel:       make(map[string]int64),
		ByAgentDetail: make(map[string]*AgentModelBucket),
		ByModelDetail: make(map[string]*AgentModelBucket),
	}

	for _, file := range files {
		summary, err := parseSessionFile(file, agentDetector)
		if err != nil {
			continue
		}
		if summary.TotalTokens == 0 {
			continue
		}

		agg.Sessions = append(agg.Sessions, *summary)
		agg.TotalTokens += summary.TotalTokens
		agg.TotalInput += summary.InputTokens
		agg.TotalOutput += summary.OutputTokens
		agg.TotalCacheRead += summary.CacheRead
		agg.TotalCacheCreate += summary.CacheCreate
		agg.TotalMessages += summary.Messages
		agg.ByAgent[summary.Agent] += summary.TotalTokens
		agg.ByModel[summary.Model] += summary.TotalTokens

		// Per-agent detail
		ab, ok := agg.ByAgentDetail[summary.Agent]
		if !ok {
			ab = &AgentModelBucket{}
			agg.ByAgentDetail[summary.Agent] = ab
		}
		ab.Input += summary.InputTokens
		ab.Output += summary.OutputTokens
		ab.CacheRead += summary.CacheRead
		ab.CacheCreate += summary.CacheCreate
		ab.Messages += summary.Messages
		ab.Sessions++

		// Per-model detail
		mb, ok := agg.ByModelDetail[summary.Model]
		if !ok {
			mb = &AgentModelBucket{}
			agg.ByModelDetail[summary.Model] = mb
		}
		mb.Input += summary.InputTokens
		mb.Output += summary.OutputTokens
		mb.CacheRead += summary.CacheRead
		mb.CacheCreate += summary.CacheCreate
		mb.Messages += summary.Messages
		mb.Sessions++
	}

	agg.SessionCount = len(agg.Sessions)
	return agg, nil
}

func parseSessionFile(path string, agentDetector func(string) string) (*SessionSummary, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	summary := &SessionSummary{
		SessionID: sessionID,
		Agent:     "unknown",
	}

	scanner := bufio.NewScanner(f)
	const maxScanBufSize = 10 * 1024 * 1024
	scanner.Buffer(make([]byte, 0, maxScanBufSize), maxScanBufSize)

	firstUserMsg := ""
	var lastTimestamp int64
	for scanner.Scan() {
		var entry SessionEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}

		if entry.Role == "user" && firstUserMsg == "" {
			firstUserMsg = entry.Message
		}

		if entry.Model != "" && summary.Model == "" {
			summary.Model = entry.Model
		}

		summary.InputTokens += entry.InputTokens
		summary.OutputTokens += entry.OutputTokens
		summary.CacheRead += entry.CacheRead
		summary.CacheCreate += entry.CacheCreation

		if entry.Role == "user" || entry.Role == "assistant" {
			summary.Messages++
			lastTimestamp = time.Now().UnixMilli()
		}
	}

	summary.TotalTokens = summary.InputTokens + summary.OutputTokens + summary.CacheRead + summary.CacheCreate
	summary.LastActive = lastTimestamp

	if agentDetector != nil && firstUserMsg != "" {
		summary.Agent = agentDetector(firstUserMsg)
	}

	return summary, nil
}

func DefaultAgentDetector(firstMsg string) string {
	lower := strings.ToLower(firstMsg)
	agents := map[string][]string{
		"scanner":    {"scanner", "triage", "issue", "bug"},
		"ci-maintainer":   {"ci-maintainer", "review", "ci", "coverage", "ga4"},
		"architect":  {"architect", "rfc", "refactor"},
		"outreach":   {"outreach", "adopters", "community"},
		"supervisor": {"supervisor", "sweep", "monitor"},
		"sec-check":  {"security", "sec-check", "vulnerability"},
	}

	for agent, keywords := range agents {
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				return agent
			}
		}
	}
	return "unknown"
}
