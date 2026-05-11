package classify

import (
	"strings"

	"github.com/kubestellar/hive/v2/pkg/github"
)

type Tier string

const (
	TierSimple  Tier = "Simple"
	TierMedium  Tier = "Medium"
	TierComplex Tier = "Complex"
)

type ModelRecommendation string

const (
	ModelHaiku  ModelRecommendation = "haiku"
	ModelSonnet ModelRecommendation = "sonnet"
	ModelOpus   ModelRecommendation = "opus"
)

type Lane string

const (
	LaneScanner   Lane = "scanner"
	LaneReviewer  Lane = "reviewer"
	LaneArchitect Lane = "architect"
	LaneOutreach  Lane = "outreach"
)

type Classification struct {
	Tier       Tier                `json:"complexity_tier"`
	Model      ModelRecommendation `json:"model_recommendation"`
	Lane       Lane                `json:"lane"`
	ClusterKey string              `json:"cluster_key,omitempty"`
}

var simpleKeywords = []string{
	"typo", "i18n", "rename", "const", "label", "badge",
	"tooltip", "placeholder", "aria", "alt text",
}

var architectKeywords = []string{
	"rfc", "architecture", "refactor", "redesign", "migration",
	"breaking change", "protocol", "api design",
}

var reviewerKeywords = []string{
	"workflow-failure", "ci-failure", "nightly", "coverage",
	"regression", "ga4", "analytics",
}

var outreachKeywords = []string{
	"adopters", "outreach", "community", "engagement",
}

func Classify(issue github.Issue) Classification {
	c := Classification{
		Tier:  TierMedium,
		Model: ModelSonnet,
		Lane:  LaneScanner,
	}

	titleLower := strings.ToLower(issue.Title)
	labelsStr := strings.ToLower(strings.Join(issue.Labels, " "))

	c.Lane = classifyLane(titleLower, labelsStr)
	c.Tier = classifyTier(titleLower, labelsStr, issue.Labels)
	c.Model = tierToModel(c.Tier)
	c.ClusterKey = extractClusterKey(titleLower)

	return c
}

func classifyLane(titleLower, labelsStr string) Lane {
	for _, kw := range architectKeywords {
		if strings.Contains(titleLower, kw) || strings.Contains(labelsStr, kw) {
			return LaneArchitect
		}
	}
	for _, kw := range reviewerKeywords {
		if strings.Contains(titleLower, kw) || strings.Contains(labelsStr, kw) {
			return LaneReviewer
		}
	}
	for _, kw := range outreachKeywords {
		if strings.Contains(titleLower, kw) || strings.Contains(labelsStr, kw) {
			return LaneOutreach
		}
	}
	return LaneScanner
}

func classifyTier(titleLower, labelsStr string, labels []string) Tier {
	for _, kw := range simpleKeywords {
		if strings.Contains(titleLower, kw) {
			return TierSimple
		}
	}

	for _, l := range labels {
		if l == "auto-qa" || l == "auto-qa-finding" {
			return TierSimple
		}
	}

	if strings.Contains(labelsStr, "kind/security") || strings.Contains(labelsStr, "kind/regression") {
		return TierComplex
	}

	complexSignals := []string{"race condition", "deadlock", "memory leak", "performance", "api change"}
	for _, sig := range complexSignals {
		if strings.Contains(titleLower, sig) {
			return TierComplex
		}
	}

	return TierMedium
}

func tierToModel(t Tier) ModelRecommendation {
	switch t {
	case TierSimple:
		return ModelHaiku
	case TierComplex:
		return ModelOpus
	default:
		return ModelSonnet
	}
}

func extractClusterKey(titleLower string) string {
	prefixes := []string{
		"dashboard", "card", "sidebar", "navbar", "modal",
		"api", "webhook", "ci", "nightly", "drasi",
		"benchmark", "gpu", "mission", "studio",
	}

	for _, p := range prefixes {
		if strings.Contains(titleLower, p) {
			return p
		}
	}
	return ""
}

func ClassifyAll(issues []github.Issue) []github.Issue {
	for i := range issues {
		c := Classify(issues[i])
		issues[i].ComplexityTier = string(c.Tier)
		issues[i].ModelRec = string(c.Model)
		issues[i].Lane = string(c.Lane)
	}
	return issues
}
