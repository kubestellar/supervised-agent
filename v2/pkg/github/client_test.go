package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	gh "github.com/google/go-github/v72/github"
)

// newTestClient creates a Client whose internal go-github client points at the
// provided httptest server instead of api.github.com.
func newTestClient(t *testing.T, server *httptest.Server, org string, repos []string) *Client {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	c := NewClient("fake-token", org, repos, logger)
	base, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	c.client.BaseURL = base
	return c
}

// --------------------------------------------------------------------------
// JSON helpers – mirror the shapes go-github expects from the wire.
// --------------------------------------------------------------------------

type wireLabel struct {
	Name string `json:"name"`
}

type wireUser struct {
	Login string `json:"login"`
}

type wirePR struct {
	Number    int         `json:"number"`
	Title     string      `json:"title"`
	User      wireUser    `json:"user"`
	Labels    []wireLabel `json:"labels"`
	Draft     bool        `json:"draft"`
	CreatedAt string      `json:"created_at"`
	HTMLURL   string      `json:"html_url"`
	Mergeable *bool       `json:"mergeable"`
}

type wireIssue struct {
	Number    int         `json:"number"`
	Title     string      `json:"title"`
	User      wireUser    `json:"user"`
	Labels    []wireLabel `json:"labels"`
	Assignees []wireUser  `json:"assignees"`
	CreatedAt string      `json:"created_at"`
	// Setting PullRequest makes IsPullRequest() return true.
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

func boolPtr(b bool) *bool { return &b }

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

const rfc3339Ago = "2006-01-02T15:04:05Z"

func hoursAgo(h float64) string {
	return time.Now().UTC().Add(-time.Duration(float64(time.Hour) * h)).Format(time.RFC3339)
}

// --------------------------------------------------------------------------
// TestNewClient
// --------------------------------------------------------------------------

func TestNewClient(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	c := NewClient("tok", "myorg", []string{"repo1", "repo2"}, logger)
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if c.org != "myorg" {
		t.Errorf("org = %q, want %q", c.org, "myorg")
	}
	if len(c.repos) != 2 {
		t.Errorf("repos len = %d, want 2", len(c.repos))
	}
	if c.client == nil {
		t.Error("internal gh.Client is nil")
	}
	if c.logger == nil {
		t.Error("logger is nil")
	}
}

// --------------------------------------------------------------------------
// TestEnumerateActionable – main integration path
// --------------------------------------------------------------------------

// buildMux creates a ServeMux that answers issues + PRs for the given org/repo
// with the provided wire payloads.
func buildMux(t *testing.T, org, repo string, issues []wireIssue, prs []wirePR) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues", org, repo), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(mustMarshal(t, issues))
	})
	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/pulls", org, repo), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(mustMarshal(t, prs))
	})
	return mux
}

func TestEnumerateActionable_BasicCounts(t *testing.T) {
	org, repo := "testorg", "testrepo"
	issues := []wireIssue{
		{Number: 1, Title: "bug one", User: wireUser{"alice"}, Labels: []wireLabel{{Name: "bug"}}, CreatedAt: hoursAgo(2)},
		{Number: 2, Title: "bug two", User: wireUser{"bob"}, Labels: []wireLabel{{Name: "enhancement"}}, CreatedAt: hoursAgo(1)},
		{Number: 3, Title: "held issue", User: wireUser{"carol"}, Labels: []wireLabel{{Name: "hold"}}, CreatedAt: hoursAgo(1)},
		{Number: 4, Title: "exempt issue", User: wireUser{"dave"}, Labels: []wireLabel{{Name: "LFX mentorship"}}, CreatedAt: hoursAgo(1)},
		// This entry has pull_request set so it should be skipped by fetchIssues.
		// Must have a valid CreatedAt or go-github fails to parse the whole response.
		{Number: 5, Title: "a PR returned in issues", User: wireUser{"eve"}, CreatedAt: hoursAgo(1), PullRequest: &struct{}{}},
	}
	prs := []wirePR{
		{Number: 10, Title: "open pr", User: wireUser{"alice"}, CreatedAt: hoursAgo(3), Draft: false},
		{Number: 11, Title: "draft pr", User: wireUser{"bob"}, CreatedAt: hoursAgo(1), Draft: true},
		{Number: 12, Title: "held pr", User: wireUser{"carol"}, Labels: []wireLabel{{Name: "do-not-merge"}}, CreatedAt: hoursAgo(1)},
	}

	mux := buildMux(t, org, repo, issues, prs)
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, []string{repo})
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("EnumerateActionable: %v", err)
	}

	// 2 actionable issues (1 held, 1 exempt, 1 PR-in-issues skipped)
	if result.Issues.Count != 2 {
		t.Errorf("Issues.Count = %d, want 2", result.Issues.Count)
	}
	// 1 actionable PR (draft and held filtered)
	if result.PRs.Count != 1 {
		t.Errorf("PRs.Count = %d, want 1", result.PRs.Count)
	}
	// 1 held issue + 1 held PR = 2 total hold items
	if result.Hold.Total != 2 {
		t.Errorf("Hold.Total = %d, want 2", result.Hold.Total)
	}
	if result.Hold.Issues != 1 {
		t.Errorf("Hold.Issues = %d, want 1", result.Hold.Issues)
	}
	if result.Hold.PRs != 1 {
		t.Errorf("Hold.PRs = %d, want 1", result.Hold.PRs)
	}
}

func TestEnumerateActionable_SortedOldestFirst(t *testing.T) {
	org, repo := "testorg", "testrepo"
	issues := []wireIssue{
		{Number: 1, Title: "young", User: wireUser{"a"}, CreatedAt: hoursAgo(1)},
		{Number: 2, Title: "old", User: wireUser{"b"}, CreatedAt: hoursAgo(10)},
		{Number: 3, Title: "middle", User: wireUser{"c"}, CreatedAt: hoursAgo(5)},
	}

	mux := buildMux(t, org, repo, issues, nil)
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, []string{repo})
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("EnumerateActionable: %v", err)
	}
	if len(result.Issues.Items) != 3 {
		t.Fatalf("expected 3 issues, got %d", len(result.Issues.Items))
	}
	// oldest first = descending AgeMinutes
	for i := 1; i < len(result.Issues.Items); i++ {
		if result.Issues.Items[i].AgeMinutes > result.Issues.Items[i-1].AgeMinutes {
			t.Errorf("issues not sorted oldest-first at index %d: age[%d]=%d > age[%d]=%d",
				i, i, result.Issues.Items[i].AgeMinutes, i-1, result.Issues.Items[i-1].AgeMinutes)
		}
	}
}

func TestEnumerateActionable_SLAViolations(t *testing.T) {
	org, repo := "testorg", "testrepo"
	issues := []wireIssue{
		// 2 hours old — well past 30-min SLA
		{Number: 1, Title: "old", User: wireUser{"a"}, CreatedAt: hoursAgo(2)},
		// 10 minutes old — within SLA
		{Number: 2, Title: "new", User: wireUser{"b"}, CreatedAt: time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)},
	}

	mux := buildMux(t, org, repo, issues, nil)
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, []string{repo})
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("EnumerateActionable: %v", err)
	}
	if result.Issues.SLAViolations != 1 {
		t.Errorf("SLAViolations = %d, want 1", result.Issues.SLAViolations)
	}
}

func TestEnumerateActionable_EmptyRepos(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	c := NewClient("tok", "org", []string{}, logger)
	base, _ := url.Parse(server.URL + "/")
	c.client.BaseURL = base

	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Issues.Count != 0 || result.PRs.Count != 0 {
		t.Errorf("expected empty result, got issues=%d prs=%d", result.Issues.Count, result.PRs.Count)
	}
}

func TestEnumerateActionable_APIError(t *testing.T) {
	org, repo := "testorg", "failrepo"
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues", org, repo), func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	})
	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/pulls", org, repo), func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, []string{repo})
	// EnumerateActionable logs warnings but does not return an error when individual
	// repos fail — it continues to the next repo.
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No data collected — counts should be zero.
	if result.Issues.Count != 0 || result.PRs.Count != 0 {
		t.Errorf("expected zero counts on API error, got issues=%d prs=%d",
			result.Issues.Count, result.PRs.Count)
	}
}

func TestEnumerateActionable_MultipleRepos(t *testing.T) {
	org := "testorg"
	repos := []string{"repo1", "repo2"}

	mux := http.NewServeMux()
	for i, repo := range repos {
		r := repo
		num := i + 1
		mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues", org, r), func(w http.ResponseWriter, _ *http.Request) {
			issues := []wireIssue{
				{Number: num, Title: fmt.Sprintf("issue in %s", r), User: wireUser{"user"}, CreatedAt: hoursAgo(1)},
			}
			b, _ := json.Marshal(issues)
			w.Header().Set("Content-Type", "application/json")
			w.Write(b)
		})
		mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/pulls", org, r), func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
		})
	}
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, repos)
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("EnumerateActionable: %v", err)
	}
	if result.Issues.Count != 2 {
		t.Errorf("Issues.Count = %d, want 2 (one per repo)", result.Issues.Count)
	}
}

func TestEnumerateActionable_GeneratedAtSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))
	defer server.Close()

	c := newTestClient(t, server, "org", []string{"repo"})
	before := time.Now()
	result, err := c.EnumerateActionable(context.Background())
	after := time.Now()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.GeneratedAt.Before(before) || result.GeneratedAt.After(after) {
		t.Errorf("GeneratedAt %v not between %v and %v", result.GeneratedAt, before, after)
	}
}

// --------------------------------------------------------------------------
// TestFetchIssues — detailed issue-level assertions
// --------------------------------------------------------------------------

func TestFetchIssues_AssigneesAndLabels(t *testing.T) {
	org, repo := "org", "repo"
	issues := []wireIssue{
		{
			Number:    7,
			Title:     "labelled issue",
			User:      wireUser{"testuser"},
			Labels:    []wireLabel{{Name: "bug"}, {Name: "help wanted"}},
			Assignees: []wireUser{{Login: "alice"}, {Login: "bob"}},
			CreatedAt: hoursAgo(1),
		},
	}
	mux := buildMux(t, org, repo, issues, nil)
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, []string{repo})
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("EnumerateActionable: %v", err)
	}
	if result.Issues.Count != 1 {
		t.Fatalf("expected 1 issue, got %d", result.Issues.Count)
	}
	iss := result.Issues.Items[0]
	if iss.Author != "testuser" {
		t.Errorf("Author = %q, want %q", iss.Author, "testuser")
	}
	if len(iss.Labels) != 2 {
		t.Errorf("Labels len = %d, want 2", len(iss.Labels))
	}
	if len(iss.Assignees) != 2 {
		t.Errorf("Assignees len = %d, want 2", len(iss.Assignees))
	}
	if iss.Repo != repo {
		t.Errorf("Repo = %q, want %q", iss.Repo, repo)
	}
	if iss.Number != 7 {
		t.Errorf("Number = %d, want 7", iss.Number)
	}
}

func TestFetchIssues_TrackerByTitle(t *testing.T) {
	org, repo := "org", "repo"
	issues := []wireIssue{
		{Number: 1, Title: "[Tracker] big epic", User: wireUser{"u"}, CreatedAt: hoursAgo(1)},
		{Number: 2, Title: "regular issue", User: wireUser{"u"}, Labels: []wireLabel{{Name: "meta-tracker"}}, CreatedAt: hoursAgo(1)},
		{Number: 3, Title: "plain issue", User: wireUser{"u"}, CreatedAt: hoursAgo(1)},
	}
	mux := buildMux(t, org, repo, issues, nil)
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, []string{repo})
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("EnumerateActionable: %v", err)
	}
	byNum := map[int]Issue{}
	for _, iss := range result.Issues.Items {
		byNum[iss.Number] = iss
	}
	if !byNum[1].IsTracker {
		t.Error("issue 1 ([Tracker] prefix) should be IsTracker=true")
	}
	if !byNum[2].IsTracker {
		t.Error("issue 2 (meta-tracker label) should be IsTracker=true")
	}
	if byNum[3].IsTracker {
		t.Error("issue 3 (plain) should be IsTracker=false")
	}
}

// --------------------------------------------------------------------------
// TestFetchPRs — PR-specific paths
// --------------------------------------------------------------------------

func TestFetchPRs_DraftFiltered(t *testing.T) {
	org, repo := "org", "repo"
	prs := []wirePR{
		{Number: 1, Title: "real pr", User: wireUser{"u"}, Draft: false, CreatedAt: hoursAgo(1)},
		{Number: 2, Title: "draft pr", User: wireUser{"u"}, Draft: true, CreatedAt: hoursAgo(1)},
	}
	mux := buildMux(t, org, repo, nil, prs)
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, []string{repo})
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("EnumerateActionable: %v", err)
	}
	if result.PRs.Count != 1 {
		t.Errorf("PRs.Count = %d, want 1 (draft filtered)", result.PRs.Count)
	}
	if result.PRs.Items[0].Number != 1 {
		t.Errorf("expected PR #1, got #%d", result.PRs.Items[0].Number)
	}
}

func TestFetchPRs_HoldBeforeDraftCheck(t *testing.T) {
	// A held+draft PR should count as held, not just silently dropped.
	org, repo := "org", "repo"
	prs := []wirePR{
		{Number: 1, Title: "held draft", User: wireUser{"u"}, Draft: true,
			Labels: []wireLabel{{Name: "hold"}}, CreatedAt: hoursAgo(1)},
	}
	mux := buildMux(t, org, repo, nil, prs)
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, []string{repo})
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("EnumerateActionable: %v", err)
	}
	if result.Hold.PRs != 1 {
		t.Errorf("Hold.PRs = %d, want 1 (held draft should be in hold list)", result.Hold.PRs)
	}
	if result.PRs.Count != 0 {
		t.Errorf("PRs.Count = %d, want 0", result.PRs.Count)
	}
}

func TestFetchPRs_PRFields(t *testing.T) {
	org, repo := "org", "repo"
	mergeable := true
	prs := []wirePR{
		{
			Number:    42,
			Title:     "my pr",
			User:      wireUser{"prauthor"},
			Labels:    []wireLabel{{Name: "ready"}},
			Draft:     false,
			CreatedAt: hoursAgo(2),
			HTMLURL:   "https://github.com/org/repo/pull/42",
			Mergeable: &mergeable,
		},
	}
	mux := buildMux(t, org, repo, nil, prs)
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, []string{repo})
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("EnumerateActionable: %v", err)
	}
	if result.PRs.Count != 1 {
		t.Fatalf("expected 1 PR, got %d", result.PRs.Count)
	}
	pr := result.PRs.Items[0]
	if pr.Number != 42 {
		t.Errorf("PR Number = %d, want 42", pr.Number)
	}
	if pr.Author != "prauthor" {
		t.Errorf("PR Author = %q, want %q", pr.Author, "prauthor")
	}
	if pr.URL != "https://github.com/org/repo/pull/42" {
		t.Errorf("PR URL = %q", pr.URL)
	}
	if pr.Repo != repo {
		t.Errorf("PR Repo = %q, want %q", pr.Repo, repo)
	}
	if !pr.Mergeable {
		t.Error("PR Mergeable should be true")
	}
}

// --------------------------------------------------------------------------
// Helper function unit tests
// --------------------------------------------------------------------------

func TestIsHeld(t *testing.T) {
	tests := []struct {
		labels []string
		want   bool
	}{
		{[]string{"hold"}, true},
		{[]string{"on-hold"}, true},
		{[]string{"hold/review"}, true},
		{[]string{"do-not-merge"}, true},
		{[]string{"HOLD"}, true},             // case-insensitive
		{[]string{"DO-NOT-MERGE"}, true},     // case-insensitive
		{[]string{"bug", "hold"}, true},      // mixed labels
		{[]string{"bug", "enhancement"}, false},
		{[]string{}, false},
		{nil, false},
		{[]string{"holdover"}, true}, // "hold" is a substring of "holdover"
	}
	for _, tt := range tests {
		got := isHeld(tt.labels)
		if got != tt.want {
			t.Errorf("isHeld(%v) = %v, want %v", tt.labels, got, tt.want)
		}
	}
}

func TestIsExempt(t *testing.T) {
	tests := []struct {
		labels []string
		want   bool
	}{
		{[]string{"LFX mentorship"}, true},
		{[]string{"LFX"}, true},
		{[]string{"LFXsomething"}, true},
		{[]string{"bug", "LFX mentorship"}, true},
		{[]string{"bug", "enhancement"}, false},
		{[]string{}, false},
		{nil, false},
		{[]string{"lfx"}, false}, // case-sensitive prefix
	}
	for _, tt := range tests {
		got := isExempt(tt.labels)
		if got != tt.want {
			t.Errorf("isExempt(%v) = %v, want %v", tt.labels, got, tt.want)
		}
	}
}

func TestIsTracker(t *testing.T) {
	tests := []struct {
		title  string
		labels []string
		want   bool
	}{
		{"[Tracker] big epic", []string{}, true},
		{"[Tracker]", []string{}, true},
		{"regular issue", []string{"meta-tracker"}, true},
		{"regular issue", []string{"bug", "meta-tracker"}, true},
		{"regular issue", []string{"bug"}, false},
		{"regular issue", []string{}, false},
		{"", []string{}, false},
		{"Not a tracker", []string{"tracker"}, false}, // label must be exactly "meta-tracker"
	}
	for _, tt := range tests {
		got := isTracker(tt.title, tt.labels)
		if got != tt.want {
			t.Errorf("isTracker(%q, %v) = %v, want %v", tt.title, tt.labels, got, tt.want)
		}
	}
}

func TestExtractLabels(t *testing.T) {
	name1 := "bug"
	name2 := "enhancement"
	labels := []*gh.Label{
		{Name: &name1},
		{Name: &name2},
	}
	got := extractLabels(labels)
	if len(got) != 2 || got[0] != "bug" || got[1] != "enhancement" {
		t.Errorf("extractLabels = %v, want [bug enhancement]", got)
	}
}

func TestExtractLabels_Nil(t *testing.T) {
	got := extractLabels(nil)
	if got != nil && len(got) != 0 {
		t.Errorf("extractLabels(nil) = %v, want nil/empty", got)
	}
}

func TestExtractPRLabels(t *testing.T) {
	name := "wip"
	labels := []*gh.Label{{Name: &name}}
	got := extractPRLabels(labels)
	if len(got) != 1 || got[0] != "wip" {
		t.Errorf("extractPRLabels = %v, want [wip]", got)
	}
}

func TestExtractAssignees(t *testing.T) {
	login1, login2 := "alice", "bob"
	users := []*gh.User{
		{Login: &login1},
		{Login: &login2},
	}
	got := extractAssignees(users)
	if len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Errorf("extractAssignees = %v, want [alice bob]", got)
	}
}

func TestExtractAssignees_Nil(t *testing.T) {
	got := extractAssignees(nil)
	if got != nil && len(got) != 0 {
		t.Errorf("extractAssignees(nil) = %v, want nil/empty", got)
	}
}

// --------------------------------------------------------------------------
// Edge-case / corner-case tests
// --------------------------------------------------------------------------

func TestEnumerateActionable_HoldItemsHaveCorrectType(t *testing.T) {
	org, repo := "org", "repo"
	issues := []wireIssue{
		{Number: 1, Title: "held iss", User: wireUser{"u"}, Labels: []wireLabel{{Name: "on-hold"}}, CreatedAt: hoursAgo(1)},
	}
	prs := []wirePR{
		{Number: 2, Title: "held pr", User: wireUser{"u"}, Labels: []wireLabel{{Name: "hold/review"}}, CreatedAt: hoursAgo(1)},
	}
	mux := buildMux(t, org, repo, issues, prs)
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, []string{repo})
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("EnumerateActionable: %v", err)
	}
	for _, h := range result.Hold.Items {
		if h.Number == 1 && h.Type != "issue" {
			t.Errorf("hold item #1 Type = %q, want issue", h.Type)
		}
		if h.Number == 2 && h.Type != "pr" {
			t.Errorf("hold item #2 Type = %q, want pr", h.Type)
		}
	}
}

func TestEnumerateActionable_IssuesErrorContinuesToPRs(t *testing.T) {
	// Issues endpoint 500s but PRs endpoint succeeds — we should still see PRs.
	org, repo := "org", "repo"
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues", org, repo), func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/pulls", org, repo), func(w http.ResponseWriter, r *http.Request) {
		prs := []wirePR{{Number: 99, Title: "works", User: wireUser{"u"}, CreatedAt: hoursAgo(1)}}
		w.Header().Set("Content-Type", "application/json")
		w.Write(mustMarshal(t, prs))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, []string{repo})
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// When issues fetch fails, fetchIssues returns error; EnumerateActionable
	// logs and calls `continue` — so fetchPRs is NOT called for that repo.
	// Both counts should be 0.
	if result.Issues.Count != 0 {
		t.Errorf("Issues.Count = %d, want 0", result.Issues.Count)
	}
	// PRs also 0 because the continue skips fetchPRs.
	if result.PRs.Count != 0 {
		t.Errorf("PRs.Count = %d, want 0 (fetchPRs skipped after issues error)", result.PRs.Count)
	}
}

func TestEnumerateActionable_AllExemptIssues(t *testing.T) {
	org, repo := "org", "repo"
	issues := []wireIssue{
		{Number: 1, Labels: []wireLabel{{Name: "LFX mentorship"}}, User: wireUser{"u"}, CreatedAt: hoursAgo(1)},
		{Number: 2, Labels: []wireLabel{{Name: "LFX"}}, User: wireUser{"u"}, CreatedAt: hoursAgo(2)},
	}
	mux := buildMux(t, org, repo, issues, nil)
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, []string{repo})
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("EnumerateActionable: %v", err)
	}
	if result.Issues.Count != 0 {
		t.Errorf("Issues.Count = %d, want 0 (all exempt)", result.Issues.Count)
	}
	if result.Hold.Total != 0 {
		t.Errorf("Hold.Total = %d, want 0 (exempt != held)", result.Hold.Total)
	}
}

func TestEnumerateActionable_NoSLAViolationsWhenAllFresh(t *testing.T) {
	org, repo := "org", "repo"
	// 5 minutes old — within the 30-minute SLA threshold.
	issues := []wireIssue{
		{Number: 1, Title: "fresh", User: wireUser{"u"}, CreatedAt: time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)},
	}
	mux := buildMux(t, org, repo, issues, nil)
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestClient(t, server, org, []string{repo})
	result, err := c.EnumerateActionable(context.Background())
	if err != nil {
		t.Fatalf("EnumerateActionable: %v", err)
	}
	if result.Issues.SLAViolations != 0 {
		t.Errorf("SLAViolations = %d, want 0", result.Issues.SLAViolations)
	}
}

func init() {
	_ = rfc3339Ago // suppress unused-constant warning
}
