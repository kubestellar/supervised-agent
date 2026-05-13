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
	case "reviewer":
		return s.buildReviewerMessage(actionable)
	case "supervisor":
		return s.buildSupervisorMessage(actionable)
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

func (s *Scheduler) buildReviewerMessage(actionable *github.ActionableResult) string {
	var b strings.Builder
	b.WriteString("[agent:reviewer] [KICK]\n")
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
