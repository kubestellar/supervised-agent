package github

import (
	"context"
	"strings"
	"time"

	gh "github.com/google/go-github/v72/github"
)

type HealthCheckConfig struct {
	Org   string
	Repos []string
}

const (
	workflowStatusSuccess   = 1
	workflowStatusFailure   = 0
	workflowStatusNotFound  = -1
	ciPassRateMaxPercent     = 100
	recentRunsLimit         = 10
	lookbackHours           = 48
)

var workflowChecks = []struct {
	Key      string
	Patterns []string
	Repo     string
}{
	{Key: "ci", Patterns: []string{"Build and Deploy", "CI", "build"}, Repo: "console"},
	{Key: "brew", Patterns: []string{"brew", "Homebrew"}, Repo: "homebrew-tap"},
	{Key: "helm", Patterns: []string{"Helm", "helm"}, Repo: "console"},
	{Key: "nightly", Patterns: []string{"Nightly Test"}, Repo: "console"},
	{Key: "nightlyCompliance", Patterns: []string{"Nightly Compliance", "Compliance"}},
	{Key: "nightlyDashboard", Patterns: []string{"Nightly Dashboard", "Dashboard Health"}},
	{Key: "nightlyGhaw", Patterns: []string{"gh-aw", "Nightly gh-aw"}},
	{Key: "nightlyPlaywright", Patterns: []string{"Playwright", "Cross-Browser"}},
	{Key: "nightlyRel", Patterns: []string{"Release", "Nightly Rel"}},
	{Key: "weekly", Patterns: []string{"Weekly"}, Repo: "console"},
	{Key: "weeklyRel", Patterns: []string{"Weekly Rel", "Weekly Release"}},
	{Key: "hourly", Patterns: []string{"Hourly"}, Repo: "console"},
	{Key: "deploy_vllm_d", Patterns: []string{"vllm-d", "deploy-vllm-d", "vLLM"}},
	{Key: "deploy_pok_prod", Patterns: []string{"pok-prod", "deploy-pok-prod", "PokProd"}},
}

func (c *Client) FetchWorkflowHealth(ctx context.Context) map[string]any {
	health := make(map[string]any, len(workflowChecks))

	for _, check := range workflowChecks {
		health[check.Key] = workflowStatusNotFound
	}

	cutoff := time.Now().Add(-lookbackHours * time.Hour)

	for _, repo := range c.repos {
		runs, err := c.listRecentWorkflowRuns(ctx, repo, cutoff)
		if err != nil {
			c.logger.Warn("failed to list workflow runs", "repo", repo, "error", err)
			continue
		}

		for _, check := range workflowChecks {
			if check.Repo != "" && check.Repo != repo {
				continue
			}
			if v, ok := health[check.Key]; ok {
				if vi, isInt := v.(int); isInt && vi == workflowStatusSuccess {
					continue
				}
			}

			status := matchWorkflowRuns(runs, check.Patterns)
			if status != workflowStatusNotFound {
				health[check.Key] = status
			}
		}
	}

	if ciRuns := c.countCIRuns(ctx); ciRuns >= 0 {
		health["ci"] = ciRuns
	}

	return health
}

func (c *Client) listRecentWorkflowRuns(ctx context.Context, repo string, since time.Time) ([]*gh.WorkflowRun, error) {
	opts := &gh.ListWorkflowRunsOptions{
		Created:     ">=" + since.Format("2006-01-02"),
		ListOptions: gh.ListOptions{PerPage: 50},
	}

	runs, _, err := c.client.Actions.ListRepositoryWorkflowRuns(ctx, c.org, repo, opts)
	if err != nil {
		return nil, err
	}

	return runs.WorkflowRuns, nil
}

func matchWorkflowRuns(runs []*gh.WorkflowRun, patterns []string) int {
	for _, run := range runs {
		name := run.GetName()
		for _, pattern := range patterns {
			if strings.Contains(strings.ToLower(name), strings.ToLower(pattern)) {
				if run.GetConclusion() == "success" {
					return workflowStatusSuccess
				}
				return workflowStatusFailure
			}
		}
	}
	return workflowStatusNotFound
}

func (c *Client) countCIRuns(ctx context.Context) int {
	primaryRepo := "console"
	if len(c.repos) > 0 {
		primaryRepo = c.repos[0]
	}

	cutoff := time.Now().Add(-lookbackHours * time.Hour)
	opts := &gh.ListWorkflowRunsOptions{
		Created:     ">=" + cutoff.Format("2006-01-02"),
		ListOptions: gh.ListOptions{PerPage: recentRunsLimit},
	}

	runs, _, err := c.client.Actions.ListRepositoryWorkflowRuns(ctx, c.org, primaryRepo, opts)
	if err != nil {
		return workflowStatusNotFound
	}

	total := 0
	passed := 0
	for _, run := range runs.WorkflowRuns {
		name := strings.ToLower(run.GetName())
		if strings.Contains(name, "build") || strings.Contains(name, "ci") || strings.Contains(name, "deploy") {
			total++
			if run.GetConclusion() == "success" {
				passed++
			}
		}
	}

	if total == 0 {
		return ciPassRateMaxPercent
	}
	return passed * ciPassRateMaxPercent / total
}
