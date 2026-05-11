package github

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	gh "github.com/google/go-github/v72/github"
)

type Client struct {
	client *gh.Client
	org    string
	repos  []string
	logger *slog.Logger
}

type Issue struct {
	Repo           string    `json:"repo"`
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	Author         string    `json:"author"`
	Labels         []string  `json:"labels"`
	Assignees      []string  `json:"assignees"`
	CreatedAt      time.Time `json:"created_at"`
	AgeMinutes     int       `json:"age_minutes"`
	IsTracker      bool      `json:"is_tracker"`
	ComplexityTier string    `json:"complexity_tier,omitempty"`
	ModelRec       string    `json:"model_recommendation,omitempty"`
	Lane           string    `json:"lane,omitempty"`
}

type PullRequest struct {
	Repo      string    `json:"repo"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Author    string    `json:"author"`
	Labels    []string  `json:"labels"`
	Draft     bool      `json:"draft"`
	CreatedAt time.Time `json:"created_at"`
	URL       string    `json:"url"`
	Mergeable bool      `json:"mergeable"`
}

type ActionableResult struct {
	GeneratedAt   time.Time     `json:"generated_at"`
	Issues        IssueResult   `json:"issues"`
	PRs           PRResult      `json:"prs"`
	Hold          HoldResult    `json:"hold"`
	Clusters      []IssueCluster `json:"clusters,omitempty"`
}

type IssueResult struct {
	Count         int     `json:"count"`
	Items         []Issue `json:"items"`
	SLAViolations int     `json:"sla_violations"`
}

type PRResult struct {
	Count int           `json:"count"`
	Items []PullRequest `json:"items"`
}

type HoldResult struct {
	Issues int         `json:"issues"`
	PRs    int         `json:"prs"`
	Total  int         `json:"total"`
	Items  []HoldItem  `json:"items"`
}

type HoldItem struct {
	Number int    `json:"number"`
	Repo   string `json:"repo"`
	Title  string `json:"title"`
	Type   string `json:"type"`
}

type IssueCluster struct {
	Key    string  `json:"key"`
	Count  int     `json:"count"`
	Issues []Issue `json:"issues"`
}

var holdSubstrings = []string{"hold", "on-hold", "hold/review", "do-not-merge"}
var exemptPrefixes = []string{"LFX"}
var exemptFiles = []string{"ADOPTERS.md", "ADOPTERS.MD"}

const slaThresholdMinutes = 30

func NewClient(token string, org string, repos []string, logger *slog.Logger) *Client {
	client := gh.NewClient(nil).WithAuthToken(token)
	return &Client{
		client: client,
		org:    org,
		repos:  repos,
		logger: logger,
	}
}

func (c *Client) EnumerateActionable(ctx context.Context) (*ActionableResult, error) {
	now := time.Now()
	result := &ActionableResult{
		GeneratedAt: now,
	}

	var allIssues []Issue
	var allPRs []PullRequest
	var holdItems []HoldItem

	for _, repo := range c.repos {
		issues, held, err := c.fetchIssues(ctx, repo, now)
		if err != nil {
			c.logger.Warn("failed to fetch issues", "repo", repo, "error", err)
			continue
		}
		allIssues = append(allIssues, issues...)
		holdItems = append(holdItems, held...)

		prs, heldPRs, err := c.fetchPRs(ctx, repo)
		if err != nil {
			c.logger.Warn("failed to fetch PRs", "repo", repo, "error", err)
			continue
		}
		allPRs = append(allPRs, prs...)
		holdItems = append(holdItems, heldPRs...)
	}

	sort.Slice(allIssues, func(i, j int) bool {
		return allIssues[i].AgeMinutes > allIssues[j].AgeMinutes
	})

	slaViolations := 0
	for _, issue := range allIssues {
		if issue.AgeMinutes > slaThresholdMinutes {
			slaViolations++
		}
	}

	holdIssueCount := 0
	holdPRCount := 0
	for _, h := range holdItems {
		if h.Type == "issue" {
			holdIssueCount++
		} else {
			holdPRCount++
		}
	}

	result.Issues = IssueResult{
		Count:         len(allIssues),
		Items:         allIssues,
		SLAViolations: slaViolations,
	}
	result.PRs = PRResult{
		Count: len(allPRs),
		Items: allPRs,
	}
	result.Hold = HoldResult{
		Issues: holdIssueCount,
		PRs:    holdPRCount,
		Total:  len(holdItems),
		Items:  holdItems,
	}

	return result, nil
}

func (c *Client) fetchIssues(ctx context.Context, repo string, now time.Time) (actionable []Issue, held []HoldItem, err error) {
	opts := &gh.IssueListByRepoOptions{
		State:       "open",
		ListOptions: gh.ListOptions{PerPage: 100},
	}

	issues, _, err := c.client.Issues.ListByRepo(ctx, c.org, repo, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("listing issues for %s/%s: %w", c.org, repo, err)
	}

	for _, issue := range issues {
		if issue.IsPullRequest() {
			continue
		}

		labels := extractLabels(issue.Labels)

		if isHeld(labels) {
			held = append(held, HoldItem{
				Number: issue.GetNumber(),
				Repo:   repo,
				Title:  issue.GetTitle(),
				Type:   "issue",
			})
			continue
		}

		if isExempt(labels) {
			continue
		}

		ageMinutes := int(now.Sub(issue.GetCreatedAt().Time).Minutes())

		actionable = append(actionable, Issue{
			Repo:       repo,
			Number:     issue.GetNumber(),
			Title:      issue.GetTitle(),
			Author:     issue.GetUser().GetLogin(),
			Labels:     labels,
			Assignees:  extractAssignees(issue.Assignees),
			CreatedAt:  issue.GetCreatedAt().Time,
			AgeMinutes: ageMinutes,
			IsTracker:  isTracker(issue.GetTitle(), labels),
		})
	}

	return actionable, held, nil
}

func (c *Client) fetchPRs(ctx context.Context, repo string) (actionable []PullRequest, held []HoldItem, err error) {
	opts := &gh.PullRequestListOptions{
		State:       "open",
		ListOptions: gh.ListOptions{PerPage: 100},
	}

	prs, _, err := c.client.PullRequests.List(ctx, c.org, repo, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("listing PRs for %s/%s: %w", c.org, repo, err)
	}

	for _, pr := range prs {
		labels := extractPRLabels(pr.Labels)

		if isHeld(labels) {
			held = append(held, HoldItem{
				Number: pr.GetNumber(),
				Repo:   repo,
				Title:  pr.GetTitle(),
				Type:   "pr",
			})
			continue
		}

		if pr.GetDraft() {
			continue
		}

		actionable = append(actionable, PullRequest{
			Repo:      repo,
			Number:    pr.GetNumber(),
			Title:     pr.GetTitle(),
			Author:    pr.GetUser().GetLogin(),
			Labels:    labels,
			Draft:     pr.GetDraft(),
			CreatedAt: pr.GetCreatedAt().Time,
			URL:       pr.GetHTMLURL(),
			Mergeable: pr.GetMergeable(),
		})
	}

	return actionable, held, nil
}

func extractLabels(labels []*gh.Label) []string {
	var result []string
	for _, l := range labels {
		result = append(result, l.GetName())
	}
	return result
}

func extractPRLabels(labels []*gh.Label) []string {
	return extractLabels(labels)
}

func extractAssignees(users []*gh.User) []string {
	var result []string
	for _, u := range users {
		result = append(result, u.GetLogin())
	}
	return result
}

func isHeld(labels []string) bool {
	for _, label := range labels {
		lower := strings.ToLower(label)
		for _, sub := range holdSubstrings {
			if strings.Contains(lower, sub) {
				return true
			}
		}
	}
	return false
}

func isExempt(labels []string) bool {
	for _, label := range labels {
		for _, prefix := range exemptPrefixes {
			if strings.HasPrefix(label, prefix) {
				return true
			}
		}
	}
	return false
}

func isTracker(title string, labels []string) bool {
	if strings.HasPrefix(title, "[Tracker]") {
		return true
	}
	for _, l := range labels {
		if l == "meta-tracker" {
			return true
		}
	}
	return false
}
