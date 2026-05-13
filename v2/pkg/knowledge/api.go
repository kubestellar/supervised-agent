package knowledge

import (
	"context"
	"log/slog"
)

// KnowledgeAPI provides a unified interface for dashboard endpoints to query
// across all configured wiki layers.
type KnowledgeAPI struct {
	layers []layerClient
	config KnowledgeConfig
	logger *slog.Logger
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

	return &KnowledgeAPI{
		layers: clients,
		config: config,
		logger: logger,
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
