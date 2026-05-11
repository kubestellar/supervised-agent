package tokens

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	TotalTokens  int64  `json:"total_tokens"`
	Messages     int    `json:"messages"`
}

type AggregateSummary struct {
	TotalTokens  int64                     `json:"total_tokens"`
	ByAgent      map[string]int64          `json:"by_agent"`
	ByModel      map[string]int64          `json:"by_model"`
	Sessions     []SessionSummary          `json:"sessions"`
	SessionCount int                       `json:"session_count"`
}

func CollectFromDir(sessionsDir string, agentDetector func(firstMsg string) string) (*AggregateSummary, error) {
	pattern := filepath.Join(sessionsDir, "*.jsonl")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("globbing session files: %w", err)
	}

	agg := &AggregateSummary{
		ByAgent: make(map[string]int64),
		ByModel: make(map[string]int64),
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
		agg.ByAgent[summary.Agent] += summary.TotalTokens
		agg.ByModel[summary.Model] += summary.TotalTokens
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

		summary.InputTokens += entry.InputTokens + entry.CacheCreation + entry.CacheRead
		summary.OutputTokens += entry.OutputTokens

		if entry.Role == "user" || entry.Role == "assistant" {
			summary.Messages++
		}
	}

	summary.TotalTokens = summary.InputTokens + summary.OutputTokens

	if agentDetector != nil && firstUserMsg != "" {
		summary.Agent = agentDetector(firstUserMsg)
	}

	return summary, nil
}

func DefaultAgentDetector(firstMsg string) string {
	lower := strings.ToLower(firstMsg)
	agents := map[string][]string{
		"scanner":    {"scanner", "triage", "issue", "bug"},
		"reviewer":   {"reviewer", "review", "ci", "coverage", "ga4"},
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
