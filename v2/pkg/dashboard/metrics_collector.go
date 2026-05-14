package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	ghpkg "github.com/kubestellar/hive/v2/pkg/github"
)

const (
	metricsCollectInterval = 5 * time.Minute
	httpTimeout            = 10 * time.Second
	metricsCacheFile       = "/data/metrics/agent-metrics-cache.json"
)

const (
	adoptersRepo = "kubestellar"
)

type MetricsCollector struct {
	ghClient *ghpkg.Client
	org      string
	repo     string
	badgeURL string
	aiAuthor string
	logger   *slog.Logger
	mu       sync.RWMutex
	metrics  map[string]any
}

func NewMetricsCollector(ghClient *ghpkg.Client, org, primaryRepo, badgeURL, aiAuthor string, logger *slog.Logger) *MetricsCollector {
	mc := &MetricsCollector{
		ghClient: ghClient,
		org:      org,
		repo:     primaryRepo,
		badgeURL: badgeURL,
		aiAuthor: aiAuthor,
		logger:   logger,
		metrics:  make(map[string]any),
	}
	mc.loadFromDisk()
	return mc
}

func (mc *MetricsCollector) Start(ctx context.Context) {
	mc.collect(ctx)
	ticker := time.NewTicker(metricsCollectInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			mc.collect(ctx)
		}
	}
}

func (mc *MetricsCollector) Get() map[string]any {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	result := make(map[string]any, len(mc.metrics))
	for k, v := range mc.metrics {
		result[k] = v
	}
	return result
}

func (mc *MetricsCollector) collect(ctx context.Context) {
	metrics := make(map[string]any)

	outreach := mc.collectOutreach(ctx)
	metrics["outreach"] = outreach

	reviewer := mc.collectCoverage()
	metrics["ci-maintainer"] = reviewer

	architect := mc.collectArchitect()
	metrics["architect"] = architect

	mc.mu.Lock()
	mc.metrics = metrics
	mc.mu.Unlock()

	mc.saveToDisk(metrics)
	mc.logger.Info("agent metrics collected",
		"stars", outreach["stars"],
		"coverage", reviewer["coverage"],
	)
}

func (mc *MetricsCollector) collectOutreach(ctx context.Context) map[string]any {
	result := map[string]any{
		"stars":          0,
		"forks":          0,
		"contributors":   0,
		"adopters":       0,
		"acmm":           0,
		"outreachOpen":   0,
		"outreachMerged": 0,
	}

	if mc.ghClient == nil {
		return result
	}

	repoFull := mc.org + "/" + mc.repo
	parts := strings.SplitN(repoFull, "/", 2)
	if len(parts) != 2 {
		return result
	}

	repo, _, err := mc.ghClient.GetRepo(ctx, parts[0], parts[1])
	if err == nil && repo != nil {
		result["stars"] = repo.GetStargazersCount()
		result["forks"] = repo.GetForksCount()
	}

	contribs, err := mc.ghClient.GetContributorCount(ctx, parts[0], parts[1])
	if err == nil {
		result["contributors"] = contribs
	}

	adopters := mc.countAdopters(ctx, mc.org, adoptersRepo)
	result["adopters"] = adopters

	acmm := mc.countACMM(ctx, mc.org, adoptersRepo)
	result["acmm"] = acmm

	open, merged := mc.countOutreachPRs(ctx)
	result["outreachOpen"] = open
	result["outreachMerged"] = merged

	return result
}

func (mc *MetricsCollector) collectCoverage() map[string]any {
	const coverageTarget = 91
	result := map[string]any{
		"coverage":       0,
		"coverageTarget": coverageTarget,
	}

	if mc.badgeURL == "" {
		return result
	}

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(mc.badgeURL)
	if err != nil {
		return result
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return result
	}

	var badge struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &badge) == nil && badge.Message != "" {
		msg := strings.TrimSuffix(badge.Message, "%")
		var val int
		if _, err := fmt.Sscanf(msg, "%d", &val); err == nil {
			result["coverage"] = val
		}
	}

	return result
}

func (mc *MetricsCollector) collectArchitect() map[string]any {
	return map[string]any{
		"prs":    0,
		"closed": 0,
	}
}

func (mc *MetricsCollector) countAdopters(ctx context.Context, owner, repo string) int {
	content, err := mc.ghClient.GetFileContent(ctx, owner, repo, "ADOPTERS.md")
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && strings.HasPrefix(line, "|") && !strings.Contains(line, "---") && !strings.HasPrefix(line, "| Organization") {
			count++
		}
	}
	return count
}

func (mc *MetricsCollector) countACMM(ctx context.Context, owner, repo string) int {
	content, err := mc.ghClient.GetFileContent(ctx, owner, repo, "ADOPTERS.md")
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "acmm") || strings.Contains(line, "ACMM") {
			count++
		}
	}
	return count
}

func (mc *MetricsCollector) countOutreachPRs(ctx context.Context) (open, merged int) {
	if mc.ghClient == nil || mc.aiAuthor == "" {
		return 0, 0
	}

	openCount, err := mc.ghClient.SearchPRCount(ctx, mc.aiAuthor, mc.org, "open")
	if err != nil {
		mc.logger.Warn("failed to count open outreach PRs", "error", err)
	}

	mergedCount, err := mc.ghClient.SearchPRCount(ctx, mc.aiAuthor, mc.org, "merged")
	if err != nil {
		mc.logger.Warn("failed to count merged outreach PRs", "error", err)
	}

	return openCount, mergedCount
}

func (mc *MetricsCollector) loadFromDisk() {
	data, err := os.ReadFile(metricsCacheFile)
	if err != nil {
		return
	}
	var metrics map[string]any
	if json.Unmarshal(data, &metrics) == nil {
		mc.metrics = metrics
	}
}

func (mc *MetricsCollector) saveToDisk(metrics map[string]any) {
	data, err := json.Marshal(metrics)
	if err != nil {
		return
	}
	_ = os.MkdirAll("/data/metrics", 0o755)
	_ = os.WriteFile(metricsCacheFile, data, 0o644)
}
