package dashboard

import (
	"time"

	"github.com/kubestellar/hive/v2/pkg/agent"
	"github.com/kubestellar/hive/v2/pkg/beads"
	"github.com/kubestellar/hive/v2/pkg/config"
	"github.com/kubestellar/hive/v2/pkg/github"
	"github.com/kubestellar/hive/v2/pkg/governor"
	"github.com/kubestellar/hive/v2/pkg/tokens"
)

const defaultLookbackHours = 24

func BuildFrontendStatus(
	govState governor.State,
	actionable *github.ActionableResult,
	agentStatuses map[string]*agent.AgentProcess,
	cfg *config.Config,
	tokenCollector *tokens.Collector,
	gov *governor.Governor,
	beadStores map[string]*beads.Store,
) *StatusPayload {
	payload := &StatusPayload{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		Agents:       buildAgents(agentStatuses, cfg),
		Governor:     buildGovernor(govState, cfg),
		Tokens:       buildTokens(tokenCollector),
		Repos:        buildRepos(cfg, actionable),
		Beads:        buildBeads(beadStores),
		Health:       buildHealth(),
		Budget:       buildBudget(gov),
		CadenceMatrix: buildCadenceMatrix(cfg),
		GHRateLimits: map[string]any{"core": map[string]any{}, "alerts": []any{}, "pullbacks": []any{}},
		AgentMetrics: map[string]any{},
		Hold:         buildHold(actionable),
		IssueToMerge: map[string]any{},
	}
	return payload
}

func buildAgents(statuses map[string]*agent.AgentProcess, cfg *config.Config) []FrontendAgent {
	agents := make([]FrontendAgent, 0, len(statuses))
	for name, proc := range statuses {
		cli := proc.Config.Backend
		if proc.BackendOverride != "" {
			cli = proc.BackendOverride
		}
		model := proc.Config.Model
		if proc.ModelOverride != "" {
			model = proc.ModelOverride
		}

		busy := "idle"
		if proc.State == agent.StateRunning {
			busy = "working"
		}

		lastKick := ""
		if proc.LastKick != nil {
			lastKick = proc.LastKick.Format(time.RFC3339)
		}

		cadence := lookupCadence(name, cfg)

		a := FrontendAgent{
			Name:        name,
			State:       string(proc.State),
			Busy:        busy,
			Paused:      proc.Paused,
			CLI:         cli,
			Model:       model,
			Cadence:     cadence,
			PinnedCli:   proc.PinnedCLI != "",
			PinnedModel: proc.PinnedModel != "",
			PinnedBoth:  proc.PinnedCLI != "" && proc.PinnedModel != "",
			LastKick:    lastKick,
			Restarts:    proc.RestartCount,
		}
		agents = append(agents, a)
	}
	return agents
}

func lookupCadence(agentName string, cfg *config.Config) string {
	for _, mode := range cfg.Governor.Modes {
		if c, ok := mode.Cadences[agentName]; ok {
			return c
		}
	}
	return ""
}

func buildGovernor(state governor.State, cfg *config.Config) FrontendGovernor {
	const (
		defaultQuiet = 2
		defaultBusy  = 10
		defaultSurge = 20
	)

	thresholds := FrontendThresholds{
		Quiet: defaultQuiet,
		Busy:  defaultBusy,
		Surge: defaultSurge,
	}

	if m, ok := cfg.Governor.Modes["quiet"]; ok {
		thresholds.Quiet = m.Threshold
	}
	if m, ok := cfg.Governor.Modes["busy"]; ok {
		thresholds.Busy = m.Threshold
	}
	if m, ok := cfg.Governor.Modes["surge"]; ok {
		thresholds.Surge = m.Threshold
	}

	return FrontendGovernor{
		Active:     true,
		Mode:       string(state.Mode),
		Issues:     state.QueueIssues,
		PRs:        state.QueuePRs,
		Thresholds: thresholds,
	}
}

func buildTokens(collector *tokens.Collector) FrontendTokens {
	ft := FrontendTokens{
		LookbackHours: defaultLookbackHours,
		Totals:        FrontendTokenTotals{},
		ByAgent:       make(map[string]FrontendTokenBucket),
		ByModel:       make(map[string]FrontendTokenBucket),
	}

	if collector == nil {
		return ft
	}

	summary := collector.Summary()
	if summary == nil {
		return ft
	}

	ft.Sessions = summary.SessionCount
	ft.Totals.Input = summary.TotalTokens

	for agentName, total := range summary.ByAgent {
		ft.ByAgent[agentName] = FrontendTokenBucket{Input: total}
	}
	for modelName, total := range summary.ByModel {
		ft.ByModel[modelName] = FrontendTokenBucket{Input: total}
	}

	var totalMessages int
	for _, sess := range summary.Sessions {
		totalMessages += sess.Messages
	}
	ft.Totals.Messages = totalMessages

	return ft
}

func buildRepos(cfg *config.Config, actionable *github.ActionableResult) []FrontendRepo {
	repos := make([]FrontendRepo, 0, len(cfg.Project.Repos))

	issuesByRepo := make(map[string][]any)
	prsByRepo := make(map[string][]any)
	issueCounts := make(map[string]int)
	prCounts := make(map[string]int)

	if actionable != nil {
		for _, issue := range actionable.Issues.Items {
			repo := issue.Repo
			issueCounts[repo]++
			issuesByRepo[repo] = append(issuesByRepo[repo], issue)
		}
		for _, pr := range actionable.PRs.Items {
			repo := pr.Repo
			prCounts[repo]++
			prsByRepo[repo] = append(prsByRepo[repo], pr)
		}
	}

	for _, repoName := range cfg.Project.Repos {
		full := cfg.Project.Org + "/" + repoName
		r := FrontendRepo{
			Name:             repoName,
			Full:             full,
			Issues:           issueCounts[repoName],
			PRs:              prCounts[repoName],
			ActionableIssues: issuesByRepo[repoName],
			OpenPrs:          prsByRepo[repoName],
		}
		if r.ActionableIssues == nil {
			r.ActionableIssues = []any{}
		}
		if r.OpenPrs == nil {
			r.OpenPrs = []any{}
		}
		repos = append(repos, r)
	}

	return repos
}

func buildBeads(stores map[string]*beads.Store) FrontendBeads {
	fb := FrontendBeads{}
	for name, store := range stores {
		count := store.Count()
		if name == "supervisor" {
			fb.Supervisor = count
		} else {
			fb.Workers += count
		}
	}
	return fb
}

func buildHealth() map[string]any {
	return map[string]any{
		"ci": 100,
	}
}

func buildBudget(gov *governor.Governor) FrontendBudget {
	budget := gov.GetBudget()
	fb := FrontendBudget{
		WeeklyBudget: budget.WeeklyLimit,
		Used:         budget.CurrentSpend,
	}
	if budget.WeeklyLimit > 0 {
		const pctMultiplier = 100.0
		fb.PctUsed = float64(budget.CurrentSpend) / float64(budget.WeeklyLimit) * pctMultiplier
	}
	return fb
}

func buildCadenceMatrix(cfg *config.Config) []FrontendCadence {
	agentNames := make(map[string]bool)
	for name := range cfg.Agents {
		agentNames[name] = true
	}

	matrix := make([]FrontendCadence, 0, len(agentNames))
	for name := range agentNames {
		entry := FrontendCadence{Agent: name}
		for modeName, mode := range cfg.Governor.Modes {
			cadence := mode.Cadences[name]
			if cadence == "" {
				cadence = "off"
			}
			switch modeName {
			case "idle":
				entry.Idle = cadence
			case "quiet":
				entry.Quiet = cadence
			case "busy":
				entry.Busy = cadence
			case "surge":
				entry.Surge = cadence
			}
		}
		matrix = append(matrix, entry)
	}
	return matrix
}

func buildHold(actionable *github.ActionableResult) FrontendHold {
	if actionable == nil {
		return FrontendHold{Items: []any{}}
	}
	items := make([]any, 0, len(actionable.Hold.Items))
	for _, item := range actionable.Hold.Items {
		items = append(items, item)
	}
	return FrontendHold{
		Total: actionable.Hold.Total,
		Items: items,
	}
}
