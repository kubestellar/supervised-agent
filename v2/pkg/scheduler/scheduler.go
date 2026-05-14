package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kubestellar/hive/v2/pkg/classify"
	"github.com/kubestellar/hive/v2/pkg/config"
	"github.com/kubestellar/hive/v2/pkg/github"
	"github.com/kubestellar/hive/v2/pkg/knowledge"
)

type Scheduler struct {
	cfg    *config.Config
	primer *knowledge.Primer
	logger *slog.Logger
}

func New(cfg *config.Config, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		cfg:    cfg,
		logger: logger,
	}
}

// SetPrimer attaches a knowledge primer to the scheduler. When set, kick
// messages include relevant facts from the wiki layers.
func (s *Scheduler) SetPrimer(p *knowledge.Primer) {
	s.primer = p
}

type KickMessage struct {
	Agent   string
	Message string
}

func (s *Scheduler) BuildKickMessages(actionable *github.ActionableResult, agentsDue []string) []KickMessage {
	classifiedIssues := classify.ClassifyAll(actionable.Issues.Items)
	reposSection := s.buildReposSection()

	var messages []KickMessage
	for _, agentName := range agentsDue {
		msg := s.buildAgentMessage(agentName, classifiedIssues, actionable)
		if msg != "" {
			if agentName != "outreach" {
				msg += "\n" + reposSection
			}
			messages = append(messages, KickMessage{
				Agent:   agentName,
				Message: msg,
			})
		}
	}
	return messages
}

func (s *Scheduler) buildReposSection() string {
	var b strings.Builder
	b.WriteString("AUTHORIZED REPOS (you may ONLY interact with these):\n")
	org := s.cfg.Project.Org
	for _, repo := range s.cfg.Project.Repos {
		if strings.Contains(repo, "/") {
			b.WriteString(fmt.Sprintf("  %s\n", repo))
		} else {
			b.WriteString(fmt.Sprintf("  %s/%s\n", org, repo))
		}
	}
	b.WriteString("⛔ NEVER access, search, list, file issues in, or open PRs on repos not listed above.\n")
	return b.String()
}

const maxIssuesPerKick = 20

func (s *Scheduler) buildAgentMessage(agentName string, issues []github.Issue, actionable *github.ActionableResult) string {
	switch agentName {
	case "scanner":
		return s.buildScannerMessage(issues, actionable)
	case "ci-maintainer":
		return s.buildCIMaintainerMessage(actionable)
	case "supervisor":
		return s.buildSupervisorMessage(actionable)
	case "tester":
		return s.buildTesterMessage(issues, actionable)
	case "architect":
		return s.buildArchitectMessage(issues, actionable)
	case "outreach":
		return s.buildOutreachMessage(actionable)
	case "sec-check":
		return s.buildSecCheckMessage(actionable)
	default:
		return s.buildGenericMessage(agentName, issues, actionable)
	}
}

func (s *Scheduler) buildScannerMessage(issues []github.Issue, actionable *github.ActionableResult) string {
	var b strings.Builder

	b.WriteString("[agent:scanner] [KICK]\n")
	b.WriteString(fmt.Sprintf("YOUR WORK LIST (pre-filtered — hold/ADOPTERS/drafts excluded, classified):\n"))

	scannerIssues := filterByLane(issues, "scanner")

	b.WriteString(fmt.Sprintf("ACTIONABLE ISSUES (%d, oldest first):\n", len(scannerIssues)))
	shown := 0
	for _, issue := range scannerIssues {
		if shown >= maxIssuesPerKick {
			break
		}
		tier := string(issue.ComplexityTier)
		if len(tier) > 0 {
			tier = tier[:1]
		}
		tracker := ""
		if issue.IsTracker {
			tracker = " [TRACKER]"
		}
		title := issue.Title
		const maxTitleLen = 60
		if len(title) > maxTitleLen {
			title = title[:maxTitleLen]
		}
		b.WriteString(fmt.Sprintf("  %dm %s#%d [%s/%s] [%s] %s%s\n",
			issue.AgeMinutes, issue.Repo, issue.Number,
			tier, issue.ModelRec,
			strings.Join(issue.Labels, ","),
			title, tracker))
		shown++
	}

	b.WriteString(fmt.Sprintf("ACTIONABLE PRs (%d):\n", actionable.PRs.Count))
	for _, pr := range actionable.PRs.Items {
		title := pr.Title
		const maxPRTitleLen = 70
		if len(title) > maxPRTitleLen {
			title = title[:maxPRTitleLen]
		}
		b.WriteString(fmt.Sprintf("  %s#%d by @%s %s\n", pr.Repo, pr.Number, pr.Author, title))
	}

	if actionable.Issues.SLAViolations > 0 {
		b.WriteString(fmt.Sprintf("\n⚠️ %d SLA VIOLATIONS (>30 min)\n", actionable.Issues.SLAViolations))
	}

	if knowledgeSection := s.primeKnowledge(scannerIssues); knowledgeSection != "" {
		b.WriteString("\n")
		b.WriteString(knowledgeSection)
	}

	b.WriteString("\n⛔ NEVER run gh issue list, gh pr list, gh search issues — the work list above is your ONLY source.\n")
	b.WriteString("⛔ MERGE DISCIPLINE: Only merge PRs listed in MERGE-READY section. Never merge a PR you created this session.\n")
	b.WriteString("WORKFLOW: Dispatch sub-agents for each issue (Agent tool). 4-6 agents IN PARALLEL.\n")

	return b.String()
}

func (s *Scheduler) buildCIMaintainerMessage(actionable *github.ActionableResult) string {
	var b strings.Builder
	b.WriteString("[agent:ci-maintainer] [KICK]\n")
	b.WriteString("Post-merge health check. Review CI status, GA4 errors, workflow health.\n")
	b.WriteString(fmt.Sprintf("Queue: %d issues, %d PRs, %d on hold\n",
		actionable.Issues.Count, actionable.PRs.Count, actionable.Hold.Total))
	return b.String()
}

func (s *Scheduler) buildSupervisorMessage(actionable *github.ActionableResult) string {
	now := time.Now().In(time.FixedZone("EDT", -4*3600))
	var b strings.Builder
	b.WriteString("[agent:supervisor] [KICK]\n")
	b.WriteString(fmt.Sprintf("MONITORING PASS %s\n\n", now.Format("1/2 3:04 PM MST")))

	b.WriteString(s.ghAuthInstructions())
	b.WriteString(s.reposSection())

	b.WriteString("ROLE: You are the SUPERVISOR. Your job is to MONITOR other agents, NOT to fix issues yourself.\n")
	b.WriteString("⛔ NEVER work on issues directly — that is scanner's job.\n")
	b.WriteString("⛔ NEVER open PRs or commit code — that is scanner's and architect's job.\n")
	b.WriteString("⛔ NEVER merge PRs — that is scanner's job.\n")
	b.WriteString("⛔ NEVER launch background fix agents — that is scanner's job.\n\n")

	b.WriteString("YOUR RESPONSIBILITIES:\n")
	b.WriteString("  1. Check all agent tmux panes — are they working or stuck at a prompt?\n")
	b.WriteString("  2. Check if agents are idle when they should be working (queue > 0 but agent idle)\n")
	b.WriteString("  3. Report agent health: running/stuck/crashed/idle/rate-limited\n")
	b.WriteString("  4. Flag stale agents that haven't produced output in > 1 cadence cycle\n")
	b.WriteString("  5. Summarize current state: what each agent is doing, what's stuck, what needs attention\n\n")

	b.WriteString(fmt.Sprintf("Queue: %d issues, %d PRs, %d on hold, %d SLA violations\n",
		actionable.Issues.Count, actionable.PRs.Count,
		actionable.Hold.Total, actionable.Issues.SLAViolations))

	b.WriteString("\nBeads: ~/supervisor-beads\n")
	return b.String()
}

func (s *Scheduler) ghAuthInstructions() string {
	return "⚙ GH AUTH: ALWAYS prefix gh commands with: GH_TOKEN=$(cat /var/run/hive-metrics/gh-app-token.cache) gh ... — this uses the GitHub App token (15k/hr). NEVER use a PAT or hardcode tokens.\n\n"
}

func (s *Scheduler) reposSection() string {
	var b strings.Builder
	b.WriteString("AUTHORIZED REPOS (you may ONLY interact with these):\n")
	for _, repo := range s.cfg.Project.Repos {
		b.WriteString(fmt.Sprintf("  %s/%s\n", s.cfg.Project.Org, repo))
	}
	b.WriteString("⛔ NEVER access, search, list, file issues in, or open PRs on repos not listed above.\n\n")
	return b.String()
}

func (s *Scheduler) buildGenericMessage(agentName string, issues []github.Issue, actionable *github.ActionableResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[agent:%s] [KICK]\n", agentName))

	agentIssues := filterByLane(issues, agentName)
	if len(agentIssues) > 0 {
		b.WriteString(fmt.Sprintf("Work items (%d):\n", len(agentIssues)))
		for _, issue := range agentIssues {
			b.WriteString(fmt.Sprintf("  %s#%d %s\n", issue.Repo, issue.Number, issue.Title))
		}
	}

	if knowledgeSection := s.primeKnowledge(agentIssues); knowledgeSection != "" {
		b.WriteString("\n")
		b.WriteString(knowledgeSection)
	}

	return b.String()
}

const defaultCoverageTargetPct = 91.0

func (s *Scheduler) buildTesterMessage(issues []github.Issue, actionable *github.ActionableResult) string {
	var b strings.Builder

	b.WriteString("[agent:tester] [KICK]\n")
	b.WriteString("TEST STRATEGIST — build test coverage from current level toward target.\n\n")

	b.WriteString(fmt.Sprintf("COVERAGE TARGET: %.0f%%\n", defaultCoverageTargetPct))

	testerIssues := filterByLane(issues, "tester")
	if len(testerIssues) > 0 {
		b.WriteString(fmt.Sprintf("\nTEST-RELATED ISSUES (%d):\n", len(testerIssues)))
		shown := 0
		for _, issue := range testerIssues {
			if shown >= maxIssuesPerKick {
				break
			}
			title := issue.Title
			const maxTitleLen = 60
			if len(title) > maxTitleLen {
				title = title[:maxTitleLen]
			}
			b.WriteString(fmt.Sprintf("  %s#%d [%s] %s\n",
				issue.Repo, issue.Number,
				strings.Join(issue.Labels, ","),
				title))
			shown++
		}
	}

	b.WriteString("\nMATURITY-ADAPTIVE INSTRUCTIONS:\n")
	b.WriteString("  If project has NO tests or CI (Level 1-2, mode=suggest):\n")
	b.WriteString("    - Propose test scaffolding. Create stub files with TODO bodies.\n")
	b.WriteString("    - Suggest which test framework to adopt. Open draft PRs.\n")
	b.WriteString("    - Create shared test utilities (factories, fixtures, helpers).\n")
	b.WriteString("  If project has CI but coverage is below target (Level 3, mode=gate):\n")
	b.WriteString("    - Identify the highest-impact untested code paths.\n")
	b.WriteString("    - Create test PRs that raise coverage above the CI threshold.\n")
	b.WriteString("    - Focus on integration tests for critical paths.\n")
	b.WriteString("  If project has full CI + TDD markers (Level 4, mode=tdd):\n")
	b.WriteString("    - Identify modules without red-green discipline.\n")
	b.WriteString("    - Create regression tests for recent bug fixes missing them.\n")
	b.WriteString("    - Enforce test-first for new features.\n")

	if knowledgeSection := s.primeKnowledge(testerIssues); knowledgeSection != "" {
		b.WriteString("\n")
		b.WriteString(knowledgeSection)
	}

	b.WriteString("\nWORKFLOW:\n")
	b.WriteString("  1. Analyze coverage reports and identify untested modules.\n")
	b.WriteString("  2. Prioritize: regression-prone code > new features > utilities.\n")
	b.WriteString("  3. Create test PRs in batches (max 3 concurrent).\n")
	b.WriteString("  4. Each PR must include: test file, required mocks/factories, coverage delta estimate.\n")
	b.WriteString("  5. Write test_scaffold and pattern facts to the knowledge wiki for future agents.\n")
	b.WriteString("⛔ NEVER run gh issue list, gh pr list, gh search issues — the work list above is your ONLY source.\n")

	return b.String()
}

func (s *Scheduler) buildArchitectMessage(issues []github.Issue, actionable *github.ActionableResult) string {
	var b strings.Builder
	b.WriteString("[agent:architect] [KICK]\n")
	b.WriteString("Full architect pass — refactor/perf scan across all repos.\n\n")

	b.WriteString(s.ghAuthInstructions())

	architectIssues := filterByLane(issues, "architect")
	if len(architectIssues) > 0 {
		b.WriteString(fmt.Sprintf("ARCHITECTURE-RELATED ISSUES (%d):\n", len(architectIssues)))
		shown := 0
		for _, issue := range architectIssues {
			if shown >= maxIssuesPerKick {
				break
			}
			title := issue.Title
			const maxTitleLen = 60
			if len(title) > maxTitleLen {
				title = title[:maxTitleLen]
			}
			b.WriteString(fmt.Sprintf("  %s#%d [%s] %s\n",
				issue.Repo, issue.Number,
				strings.Join(issue.Labels, ","),
				title))
			shown++
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf("Queue: %d issues, %d PRs, %d on hold\n\n",
		actionable.Issues.Count, actionable.PRs.Count, actionable.Hold.Total))

	b.WriteString("YOUR RESPONSIBILITIES:\n")
	b.WriteString("  1. Scan repos for refactoring opportunities (dead code, duplication, tech debt)\n")
	b.WriteString("  2. Identify performance bottlenecks and propose improvements\n")
	b.WriteString("  3. Review architecture decisions and flag inconsistencies\n")
	b.WriteString("  4. Create RFC-style issues for large changes that need discussion\n")
	b.WriteString("  5. Open PRs for small refactors that improve maintainability\n\n")

	b.WriteString("AUTONOMY RULES:\n")
	b.WriteString("  ✅ May do without approval: refactoring PRs, perf improvements, dead code removal\n")
	b.WriteString("  ❌ Needs human approval: API changes, dependency upgrades, schema migrations\n\n")

	if knowledgeSection := s.primeKnowledge(architectIssues); knowledgeSection != "" {
		b.WriteString(knowledgeSection)
		b.WriteString("\n")
	}

	b.WriteString("Beads: ~/architect-beads\n")

	return b.String()
}

func (s *Scheduler) buildOutreachMessage(actionable *github.ActionableResult) string {
	now := time.Now().In(time.FixedZone("EDT", -4*3600))
	var b strings.Builder
	b.WriteString("[agent:outreach] [KICK]\n")
	b.WriteString(fmt.Sprintf("Full outreach pass. Time: %s\n\n", now.Format("1/2 3:04 PM MST")))

	b.WriteString(s.ghAuthInstructions())

	b.WriteString("YOUR RESPONSIBILITIES:\n")
	b.WriteString("  1. Open PRs on external repos to promote adoption (awesome-lists, adopters files, install guides)\n")
	b.WriteString("  2. Check blocked_orgs before opening new PRs — one PR per org at a time\n")
	b.WriteString("  3. Monitor open outreach PRs for review feedback and address comments\n")
	b.WriteString("  4. Track placement progress toward target\n\n")

	b.WriteString("RULES:\n")
	b.WriteString("  ⛔ NEVER re-query PR counts with gh search — use pre-computed metrics\n")
	b.WriteString("  ⛔ NEVER open a second PR on an org that already has an open outreach PR\n")
	b.WriteString("  ⛔ NEVER open PRs on repos without verifying a matching mission exists first\n")
	b.WriteString("  ✅ Check ADOPTERS.MD before proposing cold outreach to any org\n\n")

	b.WriteString("Beads: ~/outreach-beads\n")

	return b.String()
}

func (s *Scheduler) buildSecCheckMessage(actionable *github.ActionableResult) string {
	now := time.Now().In(time.FixedZone("EDT", -4*3600))
	var b strings.Builder
	b.WriteString("[agent:sec-check] [KICK]\n")
	b.WriteString(fmt.Sprintf("Security review pass. Time: %s\n\n", now.Format("1/2 3:04 PM MST")))

	b.WriteString(s.ghAuthInstructions())

	b.WriteString("YOUR RESPONSIBILITIES:\n")
	b.WriteString("  1. Scan repos for security vulnerabilities (OWASP top 10, dependency CVEs)\n")
	b.WriteString("  2. Review recent PRs for security implications\n")
	b.WriteString("  3. Check for exposed secrets, hardcoded credentials, insecure defaults\n")
	b.WriteString("  4. Verify security headers, CSP policies, and auth middleware\n")
	b.WriteString("  5. Open issues or PRs for any findings\n\n")

	b.WriteString(fmt.Sprintf("Queue: %d issues, %d PRs\n",
		actionable.Issues.Count, actionable.PRs.Count))

	return b.String()
}

func filterByLane(issues []github.Issue, lane string) []github.Issue {
	var result []github.Issue
	for _, issue := range issues {
		if issue.Lane == lane || issue.Lane == "" {
			result = append(result, issue)
		}
	}
	return result
}

const maxIssuesToPrime = 5

// primeKnowledge queries the wiki layers for facts relevant to the given issues
// and returns a formatted section for injection into the kick message.
func (s *Scheduler) primeKnowledge(issues []github.Issue) string {
	if s.primer == nil || len(issues) == 0 {
		return ""
	}

	limit := maxIssuesToPrime
	if len(issues) < limit {
		limit = len(issues)
	}

	keywords := extractKeywords(issues[:limit])
	if len(keywords) == 0 {
		return ""
	}

	primed := s.primer.Prime(context.Background(), nil, keywords)
	return primed.FormatForPrompt()
}

// extractKeywords pulls searchable terms from issue labels and titles.
func extractKeywords(issues []github.Issue) []string {
	seen := make(map[string]bool)
	var keywords []string

	for _, issue := range issues {
		for _, label := range issue.Labels {
			lower := strings.ToLower(label)
			if !seen[lower] && !isNoiseLabel(lower) {
				keywords = append(keywords, lower)
				seen[lower] = true
			}
		}

		if issue.ComplexityTier != "" {
			tier := strings.ToLower(issue.ComplexityTier)
			if !seen[tier] {
				keywords = append(keywords, tier)
				seen[tier] = true
			}
		}
	}

	return keywords
}

var noiseLabels = map[string]bool{
	"triage/accepted":   true,
	"ai-fix-requested":  true,
	"kind/bug":          true,
	"kind/feature":      true,
	"kind/task":         true,
	"good first issue":  true,
	"help wanted":       true,
	"hold":              true,
}

func isNoiseLabel(label string) bool {
	return noiseLabels[label]
}
