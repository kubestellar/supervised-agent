package dashboard

import (
	"sort"
	"strings"
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
		Agents:       buildAgents(agentStatuses, cfg, govState),
		Governor:     buildGovernor(govState, cfg),
		Tokens:       buildTokens(tokenCollector),
		Repos:        buildRepos(cfg, actionable),
		Beads:        buildBeads(beadStores),
		Health:       buildHealth(),
		Budget:       buildBudget(gov, tokenCollector),
		CadenceMatrix: buildCadenceMatrix(cfg, agentStatuses),
		GHRateLimits: map[string]any{"core": map[string]any{}, "alerts": []any{}, "pullbacks": []any{}},
		AgentMetrics: map[string]any{},
		Hold:         buildHold(actionable),
		IssueToMerge: map[string]any{},
	}
	return payload
}

func buildAgents(statuses map[string]*agent.AgentProcess, cfg *config.Config, govState governor.State) []FrontendAgent {
	currentMode := strings.ToLower(string(govState.Mode))

	names := make([]string, 0, len(statuses))
	for name := range statuses {
		names = append(names, name)
	}
	sort.Strings(names)

	agents := make([]FrontendAgent, 0, len(statuses))
	for _, name := range names {
		proc := statuses[name]
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
			lastKick = formatHumanTime(*proc.LastKick)
		}

		cadence := lookupCadenceForMode(name, currentMode, cfg)
		if cadence == "" {
			cadence = lookupCadence(name, cfg)
		}
		nextKick := computeNextKick(proc.LastKick, cadence)

		pinnedCli := proc.PinnedCLI != ""
		pinnedModel := proc.PinnedModel != ""

		a := FrontendAgent{
			Name:          name,
			Session:       name,
			State:         string(proc.State),
			Busy:          busy,
			Paused:        proc.Paused,
			CLI:           cli,
			Model:         model,
			Cadence:       cadence,
			PinnedCli:     pinnedCli,
			PinnedModel:   pinnedModel,
			PinnedBoth:    pinnedCli && pinnedModel,
			Pinned:        pinnedCli || pinnedModel,
			LastKick:      lastKick,
			NextKick:      nextKick,
			Restarts:      proc.RestartCount,
			GovBackend:    cli,
			GovModel:      model,
			GovCostWeight: 0,
			StatsConfig:   []any{},
		}
		agents = append(agents, a)
	}
	return agents
}

func formatHumanTime(t time.Time) string {
	local := t.Local()
	return local.Format("1/2 3:04 PM")
}

func computeNextKick(lastKick *time.Time, cadence string) string {
	if cadence == "" || cadence == "off" || cadence == "pause" {
		return ""
	}
	base := time.Now()
	if lastKick != nil {
		base = *lastKick
	}
	d := parseCadenceDuration(cadence)
	if d == 0 {
		return ""
	}
	next := base.Add(d)
	return formatHumanTime(next)
}

func parseCadenceDuration(cadence string) time.Duration {
	cadence = strings.TrimSpace(cadence)
	if cadence == "" || cadence == "off" || cadence == "pause" {
		return 0
	}
	d, err := time.ParseDuration(cadence)
	if err == nil {
		return d
	}
	// Handle shorthand like "15m", "1h", "2m" — already valid for ParseDuration
	// Handle "5min" style
	cadence = strings.Replace(cadence, "min", "m", 1)
	d, err = time.ParseDuration(cadence)
	if err == nil {
		return d
	}
	return 0
}

func lookupCadence(agentName string, cfg *config.Config) string {
	return lookupCadenceForMode(agentName, "idle", cfg)
}

func lookupCadenceForMode(agentName, modeName string, cfg *config.Config) string {
	if mode, ok := cfg.Governor.Modes[modeName]; ok {
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

	nextKick := ""
	if cfg.Governor.EvalIntervalS > 0 {
		next := time.Now().Add(time.Duration(cfg.Governor.EvalIntervalS) * time.Second)
		nextKick = formatHumanTime(next)
	}

	return FrontendGovernor{
		Active:     true,
		Mode:       strings.ToLower(string(state.Mode)),
		Issues:     state.QueueIssues,
		PRs:        state.QueuePRs,
		Thresholds: thresholds,
		NextKick:   nextKick,
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

func buildBudget(gov *governor.Governor, tokenCollector *tokens.Collector) FrontendBudget {
	budget := gov.GetBudget()

	var totalTokens int64
	var hoursElapsed float64
	if tokenCollector != nil {
		if summary := tokenCollector.Summary(); summary != nil {
			totalTokens = summary.TotalTokens
		}
	}

	used := totalTokens
	if budget.CurrentSpend > 0 {
		used = budget.CurrentSpend
	}

	fb := FrontendBudget{
		WeeklyBudget: budget.WeeklyLimit,
		Used:         used,
		LastUpdated:  time.Now().UTC().Format(time.RFC3339),
	}

	if budget.WeeklyLimit > 0 {
		const pctMultiplier = 100.0
		remaining := budget.WeeklyLimit - used
		if remaining < 0 {
			remaining = 0
		}
		fb.Remaining = remaining
		fb.PctUsed = float64(used) / float64(budget.WeeklyLimit) * pctMultiplier

		if hoursElapsed > 0 {
			burnRate := float64(used) / hoursElapsed
			fb.BurnRateHourly = burnRate
			fb.BurnRateInstant = burnRate
			const hoursPerWeek = 168.0
			fb.ProjectedWeekly = int64(burnRate * hoursPerWeek)
			fb.ProjectedPct = float64(fb.ProjectedWeekly) / float64(budget.WeeklyLimit) * pctMultiplier
			if burnRate > 0 {
				fb.HoursRemaining = float64(remaining) / burnRate
			}
		}
		fb.HoursElapsed = hoursElapsed
	}

	return fb
}

func buildCadenceMatrix(cfg *config.Config, agentStatuses map[string]*agent.AgentProcess) []FrontendCadence {
	sortedNames := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		sortedNames = append(sortedNames, name)
	}
	sort.Strings(sortedNames)

	matrix := make([]FrontendCadence, 0, len(sortedNames))
	for _, name := range sortedNames {
		entry := FrontendCadence{Agent: name}

		paused := false
		if proc, ok := agentStatuses[name]; ok && proc.Paused {
			paused = true
		}

		for modeName, mode := range cfg.Governor.Modes {
			cadence := mode.Cadences[name]
			if cadence == "" || cadence == "pause" {
				cadence = "off"
			}
			if paused {
				cadence = "paused"
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
	var holdIssues, holdPRs int
	for _, item := range actionable.Hold.Items {
		items = append(items, item)
		if item.Type == "pr" {
			holdPRs++
		} else {
			holdIssues++
		}
	}
	return FrontendHold{
		Issues: holdIssues,
		PRs:    holdPRs,
		Total:  actionable.Hold.Total,
		Items:  items,
	}
}
