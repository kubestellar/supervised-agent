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
	GeneratedAt   time.Time          `json:"generated_at"`
	Issues        IssueResult        `json:"issues"`
	PRs           PRResult           `json:"prs"`
	Hold          HoldResult         `json:"hold"`
	Clusters      []IssueCluster     `json:"clusters,omitempty"`
	TotalByRepo   map[string]RepoCounts `json:"total_by_repo,omitempty"`
}

type RepoCounts struct {
	Issues int `json:"issues"`
	PRs    int `json:"prs"`
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

var holdSubstrings = []string{"hold", "on-hold", "hold/review"}
var exemptPrefixes = []string{"LFX", "do-not-merge"}
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
	totalByRepo := make(map[string]RepoCounts)

	for _, repo := range c.repos {
		issues, held, issueTotal, err := c.fetchIssues(ctx, repo, now)
		if err != nil {
			c.logger.Warn("failed to fetch issues", "repo", repo, "error", err)
			continue
		}
		allIssues = append(allIssues, issues...)
		holdItems = append(holdItems, held...)

		prs, heldPRs, prTotal, err := c.fetchPRs(ctx, repo)
		if err != nil {
			c.logger.Warn("failed to fetch PRs", "repo", repo, "error", err)
			continue
		}
		allPRs = append(allPRs, prs...)
		holdItems = append(holdItems, heldPRs...)

		totalByRepo[repo] = RepoCounts{Issues: issueTotal, PRs: prTotal}
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
	result.TotalByRepo = totalByRepo

	return result, nil
}

func (c *Client) fetchIssues(ctx context.Context, repo string, now time.Time) (actionable []Issue, held []HoldItem, totalIssues int, err error) {
	opts := &gh.IssueListByRepoOptions{
		State:       "open",
		ListOptions: gh.ListOptions{PerPage: 100},
	}

	issues, _, err := c.client.Issues.ListByRepo(ctx, c.org, repo, opts)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("listing issues for %s/%s: %w", c.org, repo, err)
	}

	for _, issue := range issues {
		if issue.IsPullRequest() {
			continue
		}

		totalIssues++
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

	return actionable, held, totalIssues, nil
}

func (c *Client) fetchPRs(ctx context.Context, repo string) (actionable []PullRequest, held []HoldItem, totalPRs int, err error) {
	opts := &gh.PullRequestListOptions{
		State:       "open",
		ListOptions: gh.ListOptions{PerPage: 100},
	}

	prs, _, err := c.client.PullRequests.List(ctx, c.org, repo, opts)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("listing PRs for %s/%s: %w", c.org, repo, err)
	}

	for _, pr := range prs {
		totalPRs++
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

		if isExempt(labels) {
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

	return actionable, held, totalPRs, nil
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

type RateLimitInfo struct {
	Core    RateLimitEntry `json:"core"`
	Search  RateLimitEntry `json:"search"`
	GraphQL RateLimitEntry `json:"graphql"`
}

type RateLimitEntry struct {
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	Reset     time.Time `json:"reset"`
}

func (c *Client) RateLimits(ctx context.Context) (*RateLimitInfo, error) {
	limits, _, err := c.client.RateLimit.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching rate limits: %w", err)
	}

	info := &RateLimitInfo{}
	if limits.Core != nil {
		info.Core = RateLimitEntry{
			Limit:     limits.Core.Limit,
			Remaining: limits.Core.Remaining,
			Reset:     limits.Core.Reset.Time,
		}
	}
	if limits.Search != nil {
		info.Search = RateLimitEntry{
			Limit:     limits.Search.Limit,
			Remaining: limits.Search.Remaining,
			Reset:     limits.Search.Reset.Time,
		}
	}
	if limits.GraphQL != nil {
		info.GraphQL = RateLimitEntry{
			Limit:     limits.GraphQL.Limit,
			Remaining: limits.GraphQL.Remaining,
			Reset:     limits.GraphQL.Reset.Time,
		}
	}

	return info, nil
}

func (c *Client) LatestCommitHash(ctx context.Context, owner, repo, branch string) (string, error) {
	ref, _, err := c.client.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("fetching ref %s/%s@%s: %w", owner, repo, branch, err)
	}
	return ref.GetObject().GetSHA(), nil
}

func (c *Client) GetRepo(ctx context.Context, owner, repo string) (*gh.Repository, *gh.Response, error) {
	return c.client.Repositories.Get(ctx, owner, repo)
}

func (c *Client) GetContributorCount(ctx context.Context, owner, repo string) (int, error) {
	const perPage = 100
	opts := &gh.ListContributorsOptions{
		ListOptions: gh.ListOptions{PerPage: perPage},
	}
	var total int
	for {
		contribs, resp, err := c.client.Repositories.ListContributors(ctx, owner, repo, opts)
		if err != nil {
			return 0, fmt.Errorf("listing contributors for %s/%s: %w", owner, repo, err)
		}
		total += len(contribs)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return total, nil
}

func (c *Client) GetFileContent(ctx context.Context, owner, repo, path string) (string, error) {
	fc, _, _, err := c.client.Repositories.GetContents(ctx, owner, repo, path, nil)
	if err != nil {
		return "", fmt.Errorf("getting file %s/%s/%s: %w", owner, repo, path, err)
	}
	if fc == nil {
		return "", fmt.Errorf("file %s/%s/%s is a directory", owner, repo, path)
	}
	content, err := fc.GetContent()
	if err != nil {
		return "", fmt.Errorf("decoding %s/%s/%s: %w", owner, repo, path, err)
	}
	return content, nil
}

func (c *Client) CountOutreachPRs(ctx context.Context) (open, merged int, err error) {
	for _, repo := range c.repos {
		opts := &gh.PullRequestListOptions{
			State:       "all",
			ListOptions: gh.ListOptions{PerPage: perPage},
		}
		prs, _, err := c.client.PullRequests.List(ctx, c.org, repo, opts)
		if err != nil {
			continue
		}
		for _, pr := range prs {
			labels := extractLabels(pr.Labels)
			isOutreach := false
			for _, l := range labels {
				if strings.Contains(strings.ToLower(l), "outreach") {
					isOutreach = true
					break
				}
			}
			if !isOutreach {
				continue
			}
			if pr.GetState() == "open" {
				open++
			} else if pr.GetMerged() {
				merged++
			}
		}
	}
	return open, merged, nil
}

const perPage = 100
