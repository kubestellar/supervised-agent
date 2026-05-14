package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// KnowledgeAPI provides a unified interface for dashboard endpoints to query
// across all configured wiki layers.
type KnowledgeAPI struct {
	layers        []layerClient
	config        KnowledgeConfig
	promoter      *Promoter
	subscriptions []Subscription
	vaults        []*FileStore
	logger        *slog.Logger
}

// Subscription represents a user-added wiki endpoint.
type Subscription struct {
	URL   string    `json:"url"`
	Layer LayerType `json:"layer"`
	Name  string    `json:"name"`
	Added time.Time `json:"added"`
}

// NewKnowledgeAPI creates a dashboard-facing API from the full knowledge config.
func NewKnowledgeAPI(layers []LayerConfig, config KnowledgeConfig, logger *slog.Logger) *KnowledgeAPI {
	var clients []layerClient
	for _, l := range layers {
		endpoint := l.Endpoint()
		if endpoint == "" {
			continue
		}
		clients = append(clients, layerClient{
			layerType: l.Type,
			client:    NewClient(endpoint, logger),
		})
	}

	promoter := NewPromoter(layers, config.Curator, logger)

	return &KnowledgeAPI{
		layers:   clients,
		config:   config,
		promoter: promoter,
		logger:   logger,
	}
}

// LayerStatus describes the health of a single wiki layer.
type LayerStatus struct {
	Type    LayerType `json:"type"`
	URL     string    `json:"url"`
	Healthy bool      `json:"healthy"`
	Pages   int       `json:"pages,omitempty"`
}

// SearchAll queries all reachable layers and returns tagged results.
func (k *KnowledgeAPI) SearchAll(ctx context.Context, query string, typeFilter string, limit int) []Fact {
	var all []Fact
	for _, lc := range k.layers {
		results, err := lc.client.Search(ctx, query, typeFilter, limit)
		if err != nil {
			k.logger.Warn("knowledge search failed", "layer", lc.layerType, "error", err)
			continue
		}
		for _, r := range results {
			all = append(all, Fact{
				Slug:       r.Slug,
				Title:      r.Title,
				Type:       FactType(r.Type),
				Body:       r.Snippet,
				Confidence: r.Confidence,
				Status:     r.Status,
				Tags:       r.Tags,
				Layer:      lc.layerType,
			})
		}
	}
	return all
}

// LayerFacts returns facts from a specific layer.
func (k *KnowledgeAPI) LayerFacts(ctx context.Context, layer LayerType, typeFilter string) []Fact {
	for _, lc := range k.layers {
		if lc.layerType != layer {
			continue
		}
		results, err := lc.client.ListPages(ctx, typeFilter)
		if err != nil {
			k.logger.Warn("layer list failed", "layer", layer, "error", err)
			return nil
		}
		facts := make([]Fact, len(results))
		for i, r := range results {
			facts[i] = Fact{
				Slug:       r.Slug,
				Title:      r.Title,
				Type:       FactType(r.Type),
				Body:       r.Snippet,
				Confidence: r.Confidence,
				Status:     r.Status,
				Tags:       r.Tags,
				Layer:      layer,
			}
		}
		return facts
	}
	return nil
}

// ReadFact returns a single fact from the first layer that has it.
func (k *KnowledgeAPI) ReadFact(ctx context.Context, slug string) (*Fact, error) {
	for _, lc := range k.layers {
		page, err := lc.client.ReadPage(ctx, slug)
		if err != nil {
			continue
		}
		return &Fact{
			Slug:       page.Slug,
			Title:      page.Title,
			Type:       FactType(page.Type),
			Body:       page.Body,
			Confidence: page.Confidence,
			Status:     page.Status,
			Tags:       page.Tags,
			Layer:      lc.layerType,
		}, nil
	}
	return nil, nil
}

// Health checks all configured layers and returns their status.
func (k *KnowledgeAPI) Health(ctx context.Context) []LayerStatus {
	statuses := make([]LayerStatus, len(k.layers))
	for i, lc := range k.layers {
		statuses[i] = LayerStatus{
			Type:    lc.layerType,
			URL:     lc.client.baseURL,
			Healthy: lc.client.Healthy(ctx),
		}
		if statuses[i].Healthy {
			stats, err := lc.client.Stats(ctx)
			if err == nil {
				statuses[i].Pages = stats.TotalPages
			}
		}
	}
	return statuses
}

// Stats returns aggregate stats across all layers.
func (k *KnowledgeAPI) Stats(ctx context.Context) map[string]interface{} {
	result := map[string]interface{}{
		"enabled":      k.config.Enabled,
		"engine":       k.config.Engine,
		"layers_count": len(k.layers),
	}

	layerStats := make([]map[string]interface{}, 0, len(k.layers))
	for _, lc := range k.layers {
		ls := map[string]interface{}{
			"type":    lc.layerType,
			"url":     lc.client.baseURL,
			"healthy": false,
		}
		stats, err := lc.client.Stats(ctx)
		if err == nil {
			ls["healthy"] = true
			ls["total_pages"] = stats.TotalPages
			ls["by_type"] = stats.ByType
			ls["by_status"] = stats.ByStatus
			ls["stale"] = stats.Stale
			ls["orphaned"] = stats.Orphaned
		}
		layerStats = append(layerStats, ls)
	}
	result["layers"] = layerStats

	return result
}

// CreateFactRequest is the payload for creating a new fact.
type CreateFactRequest struct {
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Type       string   `json:"type"`
	Tags       []string `json:"tags"`
	Layer      string   `json:"layer"`
	Confidence float64  `json:"confidence"`
}

// CreateFact ingests a new fact into the specified layer.
func (k *KnowledgeAPI) CreateFact(ctx context.Context, req CreateFactRequest) error {
	layer := LayerType(req.Layer)
	client := k.clientForLayer(layer)
	if client == nil {
		return fmt.Errorf("layer %s has no configured endpoint", req.Layer)
	}

	fact := ExtractedFact{
		Title:      req.Title,
		Body:       req.Body,
		Type:       FactType(req.Type),
		Confidence: req.Confidence,
		Tags:       req.Tags,
		SourcePR:   "manual",
		SourceDate: time.Now(),
	}

	if err := client.IngestFacts(ctx, []ExtractedFact{fact}); err != nil {
		return fmt.Errorf("ingesting fact: %w", err)
	}

	k.logger.Info("fact created", "title", req.Title, "layer", req.Layer, "type", req.Type)
	return nil
}

// UpdateFactRequest is the payload for updating an existing fact.
type UpdateFactRequest struct {
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Type       string   `json:"type"`
	Tags       []string `json:"tags"`
	Status     string   `json:"status"`
	Confidence float64  `json:"confidence"`
}

// UpdateFact modifies an existing fact in the specified layer.
func (k *KnowledgeAPI) UpdateFact(ctx context.Context, layer LayerType, slug string, req UpdateFactRequest) error {
	client := k.clientForLayer(layer)
	if client == nil {
		return fmt.Errorf("layer %s has no configured endpoint", layer)
	}

	update := pageUpdateRequest{
		Title:      req.Title,
		Body:       req.Body,
		Type:       req.Type,
		Confidence: req.Confidence,
		Tags:       req.Tags,
		Status:     req.Status,
	}

	if err := client.UpdatePage(ctx, slug, update); err != nil {
		return fmt.Errorf("updating fact %s: %w", slug, err)
	}

	k.logger.Info("fact updated", "slug", slug, "layer", layer)
	return nil
}

// DeleteFact removes a fact from the specified layer.
func (k *KnowledgeAPI) DeleteFact(ctx context.Context, layer LayerType, slug string) error {
	client := k.clientForLayer(layer)
	if client == nil {
		return fmt.Errorf("layer %s has no configured endpoint", layer)
	}

	if err := client.DeletePage(ctx, slug); err != nil {
		return fmt.Errorf("deleting fact %s: %w", slug, err)
	}

	k.logger.Info("fact deleted", "slug", slug, "layer", layer)
	return nil
}

// PromoteFact promotes a fact from one layer to another (upward only).
func (k *KnowledgeAPI) PromoteFact(ctx context.Context, req PromoteRequest) PromoteResult {
	return k.promoter.Promote(ctx, req)
}

// ImportFacts parses markdown or JSON content and ingests extracted facts.
func (k *KnowledgeAPI) ImportFacts(ctx context.Context, layer LayerType, content string, format string) (int, error) {
	client := k.clientForLayer(layer)
	if client == nil {
		return 0, fmt.Errorf("layer %s has no configured endpoint", layer)
	}

	var facts []ExtractedFact

	switch format {
	case "json":
		if err := parseJSONFacts(content, &facts); err != nil {
			return 0, fmt.Errorf("parsing JSON: %w", err)
		}
	case "markdown", "md":
		facts = parseMarkdownFacts(content)
	default:
		facts = parseMarkdownFacts(content)
	}

	if len(facts) == 0 {
		return 0, nil
	}

	if err := client.IngestFacts(ctx, facts); err != nil {
		return 0, fmt.Errorf("ingesting imported facts: %w", err)
	}

	k.logger.Info("facts imported", "count", len(facts), "layer", layer, "format", format)
	return len(facts), nil
}

// Subscriptions returns the current list of subscribed wiki endpoints.
func (k *KnowledgeAPI) Subscriptions() []Subscription {
	subs := make([]Subscription, len(k.subscriptions))
	copy(subs, k.subscriptions)
	return subs
}

// AddSubscription adds a new wiki endpoint and connects a client for it.
func (k *KnowledgeAPI) AddSubscription(sub Subscription) error {
	for _, existing := range k.subscriptions {
		if existing.URL == sub.URL {
			return fmt.Errorf("subscription already exists: %s", sub.URL)
		}
	}

	sub.Added = time.Now()
	k.subscriptions = append(k.subscriptions, sub)

	k.layers = append(k.layers, layerClient{
		layerType: sub.Layer,
		client:    NewClient(sub.URL, k.logger),
	})

	k.logger.Info("subscription added", "url", sub.URL, "layer", sub.Layer, "name", sub.Name)
	return nil
}

// RemoveSubscription disconnects a wiki endpoint by URL.
func (k *KnowledgeAPI) RemoveSubscription(url string) error {
	found := false
	newSubs := make([]Subscription, 0, len(k.subscriptions))
	for _, s := range k.subscriptions {
		if s.URL == url {
			found = true
			continue
		}
		newSubs = append(newSubs, s)
	}
	if !found {
		return fmt.Errorf("subscription not found: %s", url)
	}
	k.subscriptions = newSubs

	newLayers := make([]layerClient, 0, len(k.layers))
	for _, lc := range k.layers {
		if lc.client.baseURL == url {
			continue
		}
		newLayers = append(newLayers, lc)
	}
	k.layers = newLayers

	k.logger.Info("subscription removed", "url", url)
	return nil
}

// VaultInfo describes a connected Obsidian/file-based vault for the dashboard.
type VaultInfo struct {
	Name       string         `json:"name"`
	RootDir    string         `json:"root_dir"`
	Pages      int            `json:"pages"`
	LastIndexed time.Time     `json:"last_indexed"`
	TagCounts  map[string]int `json:"tag_counts,omitempty"`
}

// ConnectVault adds a file-based vault (Obsidian, MindStudio export, or any
// directory of markdown files) as a knowledge source.
func (k *KnowledgeAPI) ConnectVault(rootDir string, name string) error {
	for _, v := range k.vaults {
		if v.RootDir() == rootDir {
			return fmt.Errorf("vault already connected: %s", rootDir)
		}
	}

	store, err := NewFileStore(rootDir, name, k.logger)
	if err != nil {
		return fmt.Errorf("connecting vault: %w", err)
	}

	k.vaults = append(k.vaults, store)
	k.logger.Info("vault connected", "name", name, "dir", rootDir, "pages", store.Stats().TotalPages)
	return nil
}

// DisconnectVault removes a file-based vault by root directory.
func (k *KnowledgeAPI) DisconnectVault(rootDir string) error {
	found := false
	newVaults := make([]*FileStore, 0, len(k.vaults))
	for _, v := range k.vaults {
		if v.RootDir() == rootDir {
			found = true
			continue
		}
		newVaults = append(newVaults, v)
	}
	if !found {
		return fmt.Errorf("vault not found: %s", rootDir)
	}
	k.vaults = newVaults
	k.logger.Info("vault disconnected", "dir", rootDir)
	return nil
}

// Vaults returns info about all connected file-based vaults.
func (k *KnowledgeAPI) Vaults() []VaultInfo {
	infos := make([]VaultInfo, len(k.vaults))
	for i, v := range k.vaults {
		stats := v.Stats()
		infos[i] = VaultInfo{
			Name:        stats.Name,
			RootDir:     stats.RootDir,
			Pages:       stats.TotalPages,
			LastIndexed: stats.LastIndexed,
			TagCounts:   stats.TagCounts,
		}
	}
	return infos
}

// ReindexVault forces a re-scan of a specific vault.
func (k *KnowledgeAPI) ReindexVault(rootDir string) error {
	for _, v := range k.vaults {
		if v.RootDir() == rootDir {
			v.Reindex()
			return nil
		}
	}
	return fmt.Errorf("vault not found: %s", rootDir)
}

// SearchAllWithVaults queries both wiki layers and file-based vaults.
func (k *KnowledgeAPI) SearchAllWithVaults(ctx context.Context, query string, typeFilter string, limit int) []Fact {
	results := k.SearchAll(ctx, query, typeFilter, limit)

	for _, v := range k.vaults {
		vaultResults := v.Search(query, limit)
		results = append(results, vaultResults...)
	}

	return results
}

// VaultFacts returns all facts from a specific vault by name.
func (k *KnowledgeAPI) VaultFacts(name string) []Fact {
	for _, v := range k.vaults {
		if v.Name() == name {
			return v.ListPages("")
		}
	}
	return nil
}

// VaultFact reads a single fact from any connected vault.
func (k *KnowledgeAPI) VaultFact(slug string) (*Fact, error) {
	for _, v := range k.vaults {
		fact, err := v.ReadPage(slug)
		if err == nil {
			return fact, nil
		}
	}
	return nil, fmt.Errorf("fact not found in any vault: %s", slug)
}

// Layers returns the configured layer types for the frontend.
func (k *KnowledgeAPI) Layers() []LayerType {
	seen := make(map[LayerType]bool)
	var result []LayerType
	for _, lc := range k.layers {
		if !seen[lc.layerType] {
			seen[lc.layerType] = true
			result = append(result, lc.layerType)
		}
	}
	return result
}

func (k *KnowledgeAPI) clientForLayer(layer LayerType) *Client {
	for _, lc := range k.layers {
		if lc.layerType == layer {
			return lc.client
		}
	}
	return nil
}

func parseJSONFacts(content string, facts *[]ExtractedFact) error {
	return json.Unmarshal([]byte(content), facts)
}

func parseMarkdownFacts(content string) []ExtractedFact {
	var facts []ExtractedFact
	lines := strings.Split(content, "\n")

	var current *ExtractedFact
	var bodyLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") {
			if current != nil && current.Title != "" {
				current.Body = strings.TrimSpace(strings.Join(bodyLines, "\n"))
				facts = append(facts, *current)
			}
			title := strings.TrimLeft(trimmed, "# ")
			current = &ExtractedFact{
				Title:      title,
				Type:       FactPattern,
				Confidence: 0.6,
				SourcePR:   "import",
				SourceDate: time.Now(),
			}
			bodyLines = nil
			continue
		}

		if strings.HasPrefix(trimmed, "- **") && strings.Contains(trimmed, "**") {
			if current != nil && current.Title != "" {
				current.Body = strings.TrimSpace(strings.Join(bodyLines, "\n"))
				facts = append(facts, *current)
			}
			title := trimmed[4:]
			if idx := strings.Index(title, "**"); idx > 0 {
				body := strings.TrimSpace(title[idx+2:])
				title = title[:idx]
				current = &ExtractedFact{
					Title:      title,
					Body:       body,
					Type:       FactPattern,
					Confidence: 0.6,
					SourcePR:   "import",
					SourceDate: time.Now(),
				}
				bodyLines = nil
			}
			continue
		}

		if current != nil && trimmed != "" {
			bodyLines = append(bodyLines, trimmed)
		}
	}

	if current != nil && current.Title != "" {
		current.Body = strings.TrimSpace(strings.Join(bodyLines, "\n"))
		facts = append(facts, *current)
	}

	return facts
}
