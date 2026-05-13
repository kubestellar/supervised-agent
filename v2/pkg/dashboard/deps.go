package dashboard

import (
	"context"
	"log/slog"

	"github.com/kubestellar/hive/v2/pkg/agent"
	"github.com/kubestellar/hive/v2/pkg/config"
	"github.com/kubestellar/hive/v2/pkg/governor"
	ghpkg "github.com/kubestellar/hive/v2/pkg/github"
	"github.com/kubestellar/hive/v2/pkg/knowledge"
	"github.com/kubestellar/hive/v2/pkg/tokens"
)

type Dependencies struct {
	Config      *config.Config
	AgentMgr    *agent.Manager
	Governor    *governor.Governor
	GHClient    *ghpkg.Client
	Tokens      *tokens.Collector
	Knowledge   *knowledge.KnowledgeAPI
	Nous        *NousState
	Logger      *slog.Logger
	Ctx         context.Context
	RefreshFunc func()
}

type NousState struct {
	Mode       string                 `json:"mode"`
	Scope      string                 `json:"scope"`
	Phase      string                 `json:"phase"`
	Status     map[string]interface{} `json:"status"`
	Ledger     []map[string]interface{} `json:"ledger"`
	Principles []NousPrinciple        `json:"principles"`
	Config     map[string]interface{} `json:"config"`
	GatePending map[string]interface{} `json:"gate_pending,omitempty"`
	GateResponse map[string]interface{} `json:"gate_response,omitempty"`
}

type NousPrinciple struct {
	ID         string  `json:"id"`
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
}
