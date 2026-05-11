package scheduler

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/kubestellar/hive/v2/pkg/config"
	"github.com/kubestellar/hive/v2/pkg/github"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newScheduler() *Scheduler {
	cfg := &config.Config{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return New(cfg, logger)
}

// makeIssue returns a github.Issue with the given fields. Remaining fields are
// zero-valued and safe to use in all scheduler paths.
func makeIssue(repo string, number int, title, lane string, ageMinutes int, labels []string, isTracker bool) github.Issue {
	return github.Issue{
		Repo:       repo,
		Number:     number,
		Title:      title,
		Lane:       lane,
		AgeMinutes: ageMinutes,
		Labels:     labels,
		IsTracker:  isTracker,
	}
}

// makePR returns a github.PullRequest with essential fields set.
func makePR(repo string, number int, title, author string) github.PullRequest {
	return github.PullRequest{
		Repo:   repo,
		Number: number,
		Title:  title,
		Author: author,
	}
}

// emptyActionable returns an ActionableResult with no issues, PRs, or holds.
func emptyActionable() *github.ActionableResult {
	return &github.ActionableResult{
		Issues: github.IssueResult{},
		PRs:    github.PRResult{},
		Hold:   github.HoldResult{},
	}
}

// actionableWithIssues returns an ActionableResult pre-populated with the
// provided issues. Count and Items are set; SLAViolations is left to the
// caller to set when needed.
func actionableWithIssues(issues []github.Issue) *github.ActionableResult {
	return &github.ActionableResult{
		Issues: github.IssueResult{
			Count: len(issues),
			Items: issues,
		},
		PRs:  github.PRResult{},
		Hold: github.HoldResult{},
	}
}

// ---------------------------------------------------------------------------
// BuildKickMessages — routing / agent combinations
// ---------------------------------------------------------------------------

func TestBuildKickMessages_EmptyAgentsDue(t *testing.T) {
	s := newScheduler()
	result := s.BuildKickMessages(emptyActionable(), nil)
	if len(result) != 0 {
		t.Errorf("expected 0 messages, got %d", len(result))
	}
}

func TestBuildKickMessages_SingleScannerAgent(t *testing.T) {
	s := newScheduler()
	actionable := emptyActionable()
	messages := s.BuildKickMessages(actionable, []string{"scanner"})
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Agent != "scanner" {
		t.Errorf("Agent = %q, want %q", messages[0].Agent, "scanner")
	}
	if !strings.Contains(messages[0].Message, "[agent:scanner]") {
		t.Errorf("scanner message missing [agent:scanner] header")
	}
}

func TestBuildKickMessages_MultipleAgents(t *testing.T) {
	s := newScheduler()
	actionable := emptyActionable()
	agents := []string{"scanner", "reviewer", "supervisor"}
	messages := s.BuildKickMessages(actionable, agents)
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
	agentSet := map[string]bool{}
	for _, m := range messages {
		agentSet[m.Agent] = true
	}
	for _, a := range agents {
		if !agentSet[a] {
			t.Errorf("missing message for agent %q", a)
		}
	}
}

func TestBuildKickMessages_GenericAgent(t *testing.T) {
	s := newScheduler()
	actionable := emptyActionable()
	messages := s.BuildKickMessages(actionable, []string{"outreach"})
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Agent != "outreach" {
		t.Errorf("Agent = %q, want %q", messages[0].Agent, "outreach")
	}
}

func TestBuildKickMessages_AgentOrderPreserved(t *testing.T) {
	s := newScheduler()
	actionable := emptyActionable()
	agents := []string{"supervisor", "scanner", "reviewer"}
	messages := s.BuildKickMessages(actionable, agents)
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
	for i, a := range agents {
		if messages[i].Agent != a {
			t.Errorf("messages[%d].Agent = %q, want %q", i, messages[i].Agent, a)
		}
	}
}

func TestBuildKickMessages_ClassificationApplied(t *testing.T) {
	// Issues with "typo" in title → Simple/haiku classification.
	// After ClassifyAll the lane will be "scanner" (typo hits simpleKeywords,
	// not a lane keyword), so scanner should see them.
	s := newScheduler()
	issues := []github.Issue{
		makeIssue("org/repo", 1, "Fix typo in README", "", 5, nil, false),
	}
	actionable := actionableWithIssues(issues)
	messages := s.BuildKickMessages(actionable, []string{"scanner"})
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	// The tier initial for Simple is "S".
	if !strings.Contains(messages[0].Message, "[S/") {
		t.Errorf("expected Simple tier marker [S/...] in scanner message, got:\n%s", messages[0].Message)
	}
}

// ---------------------------------------------------------------------------
// buildScannerMessage — format details
// ---------------------------------------------------------------------------

func TestScannerMessage_Header(t *testing.T) {
	s := newScheduler()
	msg := s.buildScannerMessage(nil, emptyActionable())
	if !strings.Contains(msg, "[agent:scanner] [KICK]") {
		t.Errorf("missing [agent:scanner] [KICK] header")
	}
	if !strings.Contains(msg, "YOUR WORK LIST") {
		t.Errorf("missing YOUR WORK LIST line")
	}
}

func TestScannerMessage_Footer(t *testing.T) {
	s := newScheduler()
	msg := s.buildScannerMessage(nil, emptyActionable())
	if !strings.Contains(msg, "NEVER run gh issue list") {
		t.Errorf("missing NEVER run gh issue list footer")
	}
	if !strings.Contains(msg, "MERGE DISCIPLINE") {
		t.Errorf("missing MERGE DISCIPLINE footer")
	}
	if !strings.Contains(msg, "WORKFLOW") {
		t.Errorf("missing WORKFLOW footer")
	}
}

func TestScannerMessage_IssueCount(t *testing.T) {
	s := newScheduler()
	issues := []github.Issue{
		makeIssue("org/a", 1, "Issue one", "scanner", 10, nil, false),
		makeIssue("org/a", 2, "Issue two", "scanner", 20, nil, false),
	}
	msg := s.buildScannerMessage(issues, emptyActionable())
	if !strings.Contains(msg, "ACTIONABLE ISSUES (2, oldest first)") {
		t.Errorf("expected issue count 2, message:\n%s", msg)
	}
}

func TestScannerMessage_IssueFormatLine(t *testing.T) {
	s := newScheduler()
	issues := []github.Issue{
		{
			Repo:           "org/repo",
			Number:         42,
			Title:          "Short title",
			Lane:           "scanner",
			AgeMinutes:     15,
			Labels:         []string{"kind/bug"},
			ComplexityTier: "Medium",
			ModelRec:       "sonnet",
			IsTracker:      false,
		},
	}
	msg := s.buildScannerMessage(issues, emptyActionable())
	// Expect:   15m org/repo#42 [M/sonnet] [kind/bug] Short title
	if !strings.Contains(msg, "15m org/repo#42") {
		t.Errorf("missing age and repo#number in message:\n%s", msg)
	}
	if !strings.Contains(msg, "[M/sonnet]") {
		t.Errorf("missing tier/model marker in message:\n%s", msg)
	}
	if !strings.Contains(msg, "[kind/bug]") {
		t.Errorf("missing label in message:\n%s", msg)
	}
	if !strings.Contains(msg, "Short title") {
		t.Errorf("missing issue title in message:\n%s", msg)
	}
}

func TestScannerMessage_TrackerMarker(t *testing.T) {
	s := newScheduler()
	issues := []github.Issue{
		{
			Repo:           "org/repo",
			Number:         99,
			Title:          "Tracker issue",
			Lane:           "scanner",
			AgeMinutes:     5,
			ComplexityTier: "Simple",
			ModelRec:       "haiku",
			IsTracker:      true,
		},
	}
	msg := s.buildScannerMessage(issues, emptyActionable())
	if !strings.Contains(msg, "[TRACKER]") {
		t.Errorf("expected [TRACKER] marker in message:\n%s", msg)
	}
}

func TestScannerMessage_NoTrackerMarkerWhenFalse(t *testing.T) {
	s := newScheduler()
	issues := []github.Issue{
		{
			Repo:       "org/repo",
			Number:     1,
			Title:      "Normal issue",
			Lane:       "scanner",
			AgeMinutes: 5,
			IsTracker:  false,
		},
	}
	msg := s.buildScannerMessage(issues, emptyActionable())
	if strings.Contains(msg, "[TRACKER]") {
		t.Errorf("unexpected [TRACKER] marker in message:\n%s", msg)
	}
}

func TestScannerMessage_TitleTruncatedAt60Chars(t *testing.T) {
	s := newScheduler()
	longTitle := strings.Repeat("x", 80)
	issues := []github.Issue{
		{
			Repo:       "org/repo",
			Number:     1,
			Title:      longTitle,
			Lane:       "scanner",
			AgeMinutes: 1,
		},
	}
	msg := s.buildScannerMessage(issues, emptyActionable())
	// The full 80-char title must not appear; the truncated 60-char prefix must.
	if strings.Contains(msg, longTitle) {
		t.Errorf("title was not truncated (80 chars still present)")
	}
	if !strings.Contains(msg, strings.Repeat("x", 60)) {
		t.Errorf("expected 60-char truncated title in message")
	}
}

func TestScannerMessage_TitleExactly60CharsNotTruncated(t *testing.T) {
	s := newScheduler()
	title60 := strings.Repeat("a", 60)
	issues := []github.Issue{
		{
			Repo:       "org/repo",
			Number:     1,
			Title:      title60,
			Lane:       "scanner",
			AgeMinutes: 1,
		},
	}
	msg := s.buildScannerMessage(issues, emptyActionable())
	if !strings.Contains(msg, title60) {
		t.Errorf("60-char title should appear unchanged")
	}
}

func TestScannerMessage_MaxIssuesPerKickTruncatesAt20(t *testing.T) {
	s := newScheduler()
	var issues []github.Issue
	for i := 1; i <= 25; i++ {
		issues = append(issues, makeIssue("org/repo", i, "issue title", "scanner", i, nil, false))
	}
	msg := s.buildScannerMessage(issues, emptyActionable())

	// Count how many "org/repo#" occurrences appear (each issue line has one).
	count := strings.Count(msg, "org/repo#")
	if count != maxIssuesPerKick {
		t.Errorf("expected %d issue lines, got %d", maxIssuesPerKick, count)
	}
}

func TestScannerMessage_PRListing(t *testing.T) {
	s := newScheduler()
	prs := []github.PullRequest{
		makePR("org/repo", 101, "Fix the thing", "alice"),
		makePR("org/repo", 102, "Another fix", "bob"),
	}
	actionable := &github.ActionableResult{
		PRs: github.PRResult{Count: 2, Items: prs},
	}
	msg := s.buildScannerMessage(nil, actionable)
	if !strings.Contains(msg, "ACTIONABLE PRs (2)") {
		t.Errorf("expected PR count 2, message:\n%s", msg)
	}
	if !strings.Contains(msg, "org/repo#101") {
		t.Errorf("missing PR #101 in message:\n%s", msg)
	}
	if !strings.Contains(msg, "@alice") {
		t.Errorf("missing PR author @alice in message:\n%s", msg)
	}
	if !strings.Contains(msg, "org/repo#102") {
		t.Errorf("missing PR #102 in message:\n%s", msg)
	}
}

func TestScannerMessage_PRTitleTruncatedAt70Chars(t *testing.T) {
	s := newScheduler()
	longPRTitle := strings.Repeat("p", 90)
	prs := []github.PullRequest{
		makePR("org/repo", 200, longPRTitle, "alice"),
	}
	actionable := &github.ActionableResult{
		PRs: github.PRResult{Count: 1, Items: prs},
	}
	msg := s.buildScannerMessage(nil, actionable)
	if strings.Contains(msg, longPRTitle) {
		t.Errorf("PR title was not truncated (90 chars still present)")
	}
	if !strings.Contains(msg, strings.Repeat("p", 70)) {
		t.Errorf("expected 70-char truncated PR title in message")
	}
}

func TestScannerMessage_SLAViolationShown(t *testing.T) {
	s := newScheduler()
	actionable := &github.ActionableResult{
		Issues: github.IssueResult{SLAViolations: 3},
		PRs:    github.PRResult{},
	}
	msg := s.buildScannerMessage(nil, actionable)
	if !strings.Contains(msg, "3 SLA VIOLATIONS") {
		t.Errorf("expected SLA violations warning, message:\n%s", msg)
	}
}

func TestScannerMessage_NoSLAViolationWhenZero(t *testing.T) {
	s := newScheduler()
	actionable := &github.ActionableResult{
		Issues: github.IssueResult{SLAViolations: 0},
	}
	msg := s.buildScannerMessage(nil, actionable)
	if strings.Contains(msg, "SLA VIOLATIONS") {
		t.Errorf("unexpected SLA violations warning when count is 0")
	}
}

func TestScannerMessage_FiltersByLane(t *testing.T) {
	s := newScheduler()
	issues := []github.Issue{
		makeIssue("org/repo", 1, "scanner issue", "scanner", 5, nil, false),
		makeIssue("org/repo", 2, "reviewer issue", "reviewer", 5, nil, false),
		makeIssue("org/repo", 3, "outreach issue", "outreach", 5, nil, false),
	}
	msg := s.buildScannerMessage(issues, emptyActionable())
	// Only scanner-lane issues (and empty-lane) should appear in the issue list.
	if !strings.Contains(msg, "org/repo#1") {
		t.Errorf("expected scanner-lane issue #1 in scanner message")
	}
	if strings.Contains(msg, "org/repo#2") {
		t.Errorf("unexpected reviewer-lane issue #2 in scanner message")
	}
	if strings.Contains(msg, "org/repo#3") {
		t.Errorf("unexpected outreach-lane issue #3 in scanner message")
	}
}

func TestScannerMessage_EmptyLaneIncluded(t *testing.T) {
	s := newScheduler()
	issues := []github.Issue{
		makeIssue("org/repo", 10, "unclassified issue", "", 5, nil, false),
	}
	msg := s.buildScannerMessage(issues, emptyActionable())
	if !strings.Contains(msg, "org/repo#10") {
		t.Errorf("expected empty-lane issue #10 to appear in scanner message")
	}
}

func TestScannerMessage_MultipleLabels(t *testing.T) {
	s := newScheduler()
	issues := []github.Issue{
		{
			Repo:           "org/repo",
			Number:         7,
			Title:          "Multi-label issue",
			Lane:           "scanner",
			AgeMinutes:     2,
			Labels:         []string{"kind/bug", "priority/high"},
			ComplexityTier: "Medium",
			ModelRec:       "sonnet",
		},
	}
	msg := s.buildScannerMessage(issues, emptyActionable())
	if !strings.Contains(msg, "kind/bug,priority/high") {
		t.Errorf("expected comma-joined labels, message:\n%s", msg)
	}
}

func TestScannerMessage_EmptyLabels(t *testing.T) {
	s := newScheduler()
	issues := []github.Issue{
		{
			Repo:           "org/repo",
			Number:         8,
			Title:          "No-label issue",
			Lane:           "scanner",
			AgeMinutes:     3,
			Labels:         nil,
			ComplexityTier: "Medium",
			ModelRec:       "sonnet",
		},
	}
	// Should not panic on nil labels.
	msg := s.buildScannerMessage(issues, emptyActionable())
	if !strings.Contains(msg, "org/repo#8") {
		t.Errorf("expected issue #8 in message")
	}
}

func TestScannerMessage_TierFirstCharUsed(t *testing.T) {
	s := newScheduler()
	cases := []struct {
		tier      string
		wantChar  string
	}{
		{"Simple", "S"},
		{"Medium", "M"},
		{"Complex", "C"},
	}
	for _, tc := range cases {
		t.Run(tc.tier, func(t *testing.T) {
			issues := []github.Issue{
				{
					Repo:           "org/repo",
					Number:         1,
					Title:          "test",
					Lane:           "scanner",
					AgeMinutes:     1,
					ComplexityTier: tc.tier,
					ModelRec:       "sonnet",
				},
			}
			msg := s.buildScannerMessage(issues, emptyActionable())
			marker := "[" + tc.wantChar + "/sonnet]"
			if !strings.Contains(msg, marker) {
				t.Errorf("expected %q marker, message:\n%s", marker, msg)
			}
		})
	}
}

func TestScannerMessage_EmptyTierHandled(t *testing.T) {
	s := newScheduler()
	issues := []github.Issue{
		{
			Repo:       "org/repo",
			Number:     5,
			Title:      "No tier issue",
			Lane:       "scanner",
			AgeMinutes: 1,
			// ComplexityTier intentionally empty
		},
	}
	// Should not panic when tier is empty string.
	msg := s.buildScannerMessage(issues, emptyActionable())
	if !strings.Contains(msg, "org/repo#5") {
		t.Errorf("expected issue #5 in message")
	}
}

// ---------------------------------------------------------------------------
// buildReviewerMessage
// ---------------------------------------------------------------------------

func TestReviewerMessage_Header(t *testing.T) {
	s := newScheduler()
	msg := s.buildReviewerMessage(emptyActionable())
	if !strings.Contains(msg, "[agent:reviewer] [KICK]") {
		t.Errorf("missing reviewer header")
	}
}

func TestReviewerMessage_ContainsQueueCounts(t *testing.T) {
	s := newScheduler()
	actionable := &github.ActionableResult{
		Issues: github.IssueResult{Count: 5},
		PRs:    github.PRResult{Count: 3},
		Hold:   github.HoldResult{Total: 2},
	}
	msg := s.buildReviewerMessage(actionable)
	if !strings.Contains(msg, "5 issues") {
		t.Errorf("missing issue count, message:\n%s", msg)
	}
	if !strings.Contains(msg, "3 PRs") {
		t.Errorf("missing PR count, message:\n%s", msg)
	}
	if !strings.Contains(msg, "2 on hold") {
		t.Errorf("missing hold count, message:\n%s", msg)
	}
}

func TestReviewerMessage_ZeroCounts(t *testing.T) {
	s := newScheduler()
	msg := s.buildReviewerMessage(emptyActionable())
	if !strings.Contains(msg, "0 issues") {
		t.Errorf("expected 0 issues in reviewer message:\n%s", msg)
	}
	if !strings.Contains(msg, "0 PRs") {
		t.Errorf("expected 0 PRs in reviewer message:\n%s", msg)
	}
}

func TestReviewerMessage_HealthCheckLine(t *testing.T) {
	s := newScheduler()
	msg := s.buildReviewerMessage(emptyActionable())
	if !strings.Contains(msg, "Post-merge health check") {
		t.Errorf("missing health check instruction line")
	}
}

// ---------------------------------------------------------------------------
// buildSupervisorMessage
// ---------------------------------------------------------------------------

func TestSupervisorMessage_Header(t *testing.T) {
	s := newScheduler()
	msg := s.buildSupervisorMessage(emptyActionable())
	if !strings.Contains(msg, "[agent:supervisor] [KICK]") {
		t.Errorf("missing supervisor header")
	}
}

func TestSupervisorMessage_ContainsQueueCounts(t *testing.T) {
	s := newScheduler()
	actionable := &github.ActionableResult{
		Issues: github.IssueResult{Count: 7, SLAViolations: 2},
		PRs:    github.PRResult{Count: 4},
		Hold:   github.HoldResult{Total: 1},
	}
	msg := s.buildSupervisorMessage(actionable)
	if !strings.Contains(msg, "7 issues") {
		t.Errorf("missing issue count, message:\n%s", msg)
	}
	if !strings.Contains(msg, "4 PRs") {
		t.Errorf("missing PR count, message:\n%s", msg)
	}
	if !strings.Contains(msg, "1 on hold") {
		t.Errorf("missing hold count, message:\n%s", msg)
	}
	if !strings.Contains(msg, "2 SLA violations") {
		t.Errorf("missing SLA violations count, message:\n%s", msg)
	}
}

func TestSupervisorMessage_SLAViolationsZero(t *testing.T) {
	s := newScheduler()
	msg := s.buildSupervisorMessage(emptyActionable())
	if !strings.Contains(msg, "0 SLA violations") {
		t.Errorf("expected 0 SLA violations in supervisor message:\n%s", msg)
	}
}

func TestSupervisorMessage_SweepInstructions(t *testing.T) {
	s := newScheduler()
	msg := s.buildSupervisorMessage(emptyActionable())
	if !strings.Contains(msg, "Sweep all agents") {
		t.Errorf("missing sweep instruction line")
	}
}

// ---------------------------------------------------------------------------
// buildGenericMessage
// ---------------------------------------------------------------------------

func TestGenericMessage_Header(t *testing.T) {
	s := newScheduler()
	msg := s.buildGenericMessage("outreach", nil, emptyActionable())
	if !strings.Contains(msg, "[agent:outreach] [KICK]") {
		t.Errorf("missing generic agent header, message:\n%s", msg)
	}
}

func TestGenericMessage_FiltersIssuesByLane(t *testing.T) {
	s := newScheduler()
	issues := []github.Issue{
		makeIssue("org/repo", 1, "outreach issue", "outreach", 5, nil, false),
		makeIssue("org/repo", 2, "scanner issue", "scanner", 5, nil, false),
	}
	msg := s.buildGenericMessage("outreach", issues, emptyActionable())
	if !strings.Contains(msg, "org/repo#1") {
		t.Errorf("expected outreach-lane issue #1 in generic message")
	}
	if strings.Contains(msg, "org/repo#2") {
		t.Errorf("unexpected scanner-lane issue #2 in generic message for outreach agent")
	}
}

func TestGenericMessage_EmptyLaneIncluded(t *testing.T) {
	s := newScheduler()
	issues := []github.Issue{
		makeIssue("org/repo", 3, "unclassified", "", 5, nil, false),
	}
	msg := s.buildGenericMessage("architect", issues, emptyActionable())
	if !strings.Contains(msg, "org/repo#3") {
		t.Errorf("expected empty-lane issue #3 in generic message")
	}
}

func TestGenericMessage_NoMatchingIssues(t *testing.T) {
	s := newScheduler()
	issues := []github.Issue{
		makeIssue("org/repo", 1, "scanner issue", "scanner", 5, nil, false),
	}
	// architect agent: only empty-lane or "architect"-lane issues match.
	msg := s.buildGenericMessage("architect", issues, emptyActionable())
	// Should not contain work items section since no matches.
	if strings.Contains(msg, "Work items") {
		t.Errorf("unexpected Work items section when no matching issues:\n%s", msg)
	}
}

func TestGenericMessage_WorkItemsSection(t *testing.T) {
	s := newScheduler()
	issues := []github.Issue{
		makeIssue("org/repo", 5, "outreach campaign", "outreach", 10, nil, false),
	}
	msg := s.buildGenericMessage("outreach", issues, emptyActionable())
	if !strings.Contains(msg, "Work items (1)") {
		t.Errorf("expected 'Work items (1)' section:\n%s", msg)
	}
	if !strings.Contains(msg, "org/repo#5") {
		t.Errorf("expected issue #5 in work items:\n%s", msg)
	}
	if !strings.Contains(msg, "outreach campaign") {
		t.Errorf("expected issue title in work items:\n%s", msg)
	}
}

func TestGenericMessage_CustomAgentName(t *testing.T) {
	s := newScheduler()
	msg := s.buildGenericMessage("my-custom-agent", nil, emptyActionable())
	if !strings.Contains(msg, "[agent:my-custom-agent]") {
		t.Errorf("expected custom agent name in header:\n%s", msg)
	}
}

// ---------------------------------------------------------------------------
// filterByLane
// ---------------------------------------------------------------------------

func TestFilterByLane_MatchingLane(t *testing.T) {
	issues := []github.Issue{
		makeIssue("org/r", 1, "a", "scanner", 0, nil, false),
		makeIssue("org/r", 2, "b", "reviewer", 0, nil, false),
	}
	result := filterByLane(issues, "scanner")
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].Number != 1 {
		t.Errorf("expected issue #1, got #%d", result[0].Number)
	}
}

func TestFilterByLane_EmptyLaneAlwaysIncluded(t *testing.T) {
	issues := []github.Issue{
		makeIssue("org/r", 1, "a", "", 0, nil, false),
		makeIssue("org/r", 2, "b", "scanner", 0, nil, false),
	}
	result := filterByLane(issues, "reviewer")
	// Issue #1 has empty lane → included. Issue #2 has lane "scanner" → excluded.
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].Number != 1 {
		t.Errorf("expected issue #1 (empty lane), got #%d", result[0].Number)
	}
}

func TestFilterByLane_BothMatchingAndEmpty(t *testing.T) {
	issues := []github.Issue{
		makeIssue("org/r", 1, "a", "scanner", 0, nil, false),
		makeIssue("org/r", 2, "b", "", 0, nil, false),
		makeIssue("org/r", 3, "c", "reviewer", 0, nil, false),
	}
	result := filterByLane(issues, "scanner")
	if len(result) != 2 {
		t.Fatalf("expected 2 results (scanner + empty), got %d", len(result))
	}
	nums := map[int]bool{}
	for _, r := range result {
		nums[r.Number] = true
	}
	if !nums[1] {
		t.Errorf("expected issue #1 (scanner lane)")
	}
	if !nums[2] {
		t.Errorf("expected issue #2 (empty lane)")
	}
	if nums[3] {
		t.Errorf("unexpected issue #3 (reviewer lane)")
	}
}

func TestFilterByLane_EmptyInput(t *testing.T) {
	result := filterByLane(nil, "scanner")
	if result != nil {
		t.Errorf("expected nil result for nil input, got %v", result)
	}
}

func TestFilterByLane_NoMatches(t *testing.T) {
	issues := []github.Issue{
		makeIssue("org/r", 1, "a", "reviewer", 0, nil, false),
	}
	result := filterByLane(issues, "scanner")
	if len(result) != 0 {
		t.Errorf("expected 0 results, got %d", len(result))
	}
}

func TestFilterByLane_AllEmpty(t *testing.T) {
	issues := []github.Issue{
		makeIssue("org/r", 1, "a", "", 0, nil, false),
		makeIssue("org/r", 2, "b", "", 0, nil, false),
	}
	result := filterByLane(issues, "scanner")
	if len(result) != 2 {
		t.Errorf("expected 2 results (both empty lane), got %d", len(result))
	}
}

func TestFilterByLane_AllMatchingLane(t *testing.T) {
	issues := []github.Issue{
		makeIssue("org/r", 1, "a", "outreach", 0, nil, false),
		makeIssue("org/r", 2, "b", "outreach", 0, nil, false),
	}
	result := filterByLane(issues, "outreach")
	if len(result) != 2 {
		t.Errorf("expected 2 results, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// Empty inputs (no issues, no PRs, no agents due)
// ---------------------------------------------------------------------------

func TestBuildKickMessages_NoIssuesNoPRs(t *testing.T) {
	s := newScheduler()
	actionable := emptyActionable()
	messages := s.BuildKickMessages(actionable, []string{"scanner", "reviewer", "supervisor"})
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages even with no issues/PRs, got %d", len(messages))
	}
}

func TestScannerMessage_NoPRs(t *testing.T) {
	s := newScheduler()
	msg := s.buildScannerMessage(nil, emptyActionable())
	if !strings.Contains(msg, "ACTIONABLE PRs (0)") {
		t.Errorf("expected ACTIONABLE PRs (0) line, message:\n%s", msg)
	}
}

func TestScannerMessage_NoIssues(t *testing.T) {
	s := newScheduler()
	msg := s.buildScannerMessage(nil, emptyActionable())
	if !strings.Contains(msg, "ACTIONABLE ISSUES (0, oldest first)") {
		t.Errorf("expected ACTIONABLE ISSUES (0, ...) line, message:\n%s", msg)
	}
}

func TestBuildKickMessages_AllAgentsDueButNoWork(t *testing.T) {
	s := newScheduler()
	agents := []string{"scanner", "reviewer", "supervisor", "outreach", "architect"}
	messages := s.BuildKickMessages(emptyActionable(), agents)
	if len(messages) != len(agents) {
		t.Errorf("expected %d messages, got %d", len(agents), len(messages))
	}
}

// ---------------------------------------------------------------------------
// New() constructor
// ---------------------------------------------------------------------------

func TestNew_ReturnsScheduler(t *testing.T) {
	cfg := &config.Config{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	s := New(cfg, logger)
	if s == nil {
		t.Fatal("New() returned nil")
	}
	if s.cfg != cfg {
		t.Error("New() did not assign cfg")
	}
	if s.logger != logger {
		t.Error("New() did not assign logger")
	}
}

// ---------------------------------------------------------------------------
// BuildKickMessages — KickMessage struct fields
// ---------------------------------------------------------------------------

func TestKickMessage_AgentAndMessageSet(t *testing.T) {
	s := newScheduler()
	messages := s.BuildKickMessages(emptyActionable(), []string{"reviewer"})
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	m := messages[0]
	if m.Agent == "" {
		t.Error("KickMessage.Agent is empty")
	}
	if m.Message == "" {
		t.Error("KickMessage.Message is empty")
	}
}
