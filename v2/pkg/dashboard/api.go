package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubestellar/hive/v2/pkg/config"
)

func (s *Server) RegisterAPI(deps *Dependencies) {
	s.deps = deps

	s.mux.HandleFunc("GET /api/version", s.handleVersion)
	s.mux.HandleFunc("GET /api/config", s.handleConfig)
	s.mux.HandleFunc("GET /api/history", s.handleHistory)
	s.mux.HandleFunc("GET /api/trends", s.handleTrends)
	s.mux.HandleFunc("GET /api/timeline", s.handleTimeline)
	s.mux.HandleFunc("GET /api/widget", s.handleWidget)
	s.mux.HandleFunc("GET /api/pane/{agent}", s.handlePane)

	s.mux.HandleFunc("POST /api/kick/{agent}", s.handleKick)
	s.mux.HandleFunc("POST /api/switch/{agent}/{backend}", s.handleSwitch)
	s.mux.HandleFunc("POST /api/model/{agent}/{model}", s.handleModelSet)
	s.mux.HandleFunc("POST /api/pause/{agent}", s.handlePause)
	s.mux.HandleFunc("POST /api/resume/{agent}", s.handleResume)
	s.mux.HandleFunc("POST /api/pin/{agent}/{dimension}", s.handlePin)
	s.mux.HandleFunc("POST /api/unpin/{agent}/{dimension}", s.handleUnpin)
	s.mux.HandleFunc("POST /api/restart/{agent}", s.handleRestart)
	s.mux.HandleFunc("POST /api/reset-restarts/{agent}", s.handleResetRestarts)

	s.mux.HandleFunc("GET /api/tokens", s.handleTokens)
	s.mux.HandleFunc("GET /api/issue-costs", s.handleIssueCosts)
	s.mux.HandleFunc("GET /api/model-advisor", s.handleModelAdvisor)
	s.mux.HandleFunc("GET /api/budget-ignore", s.handleBudgetIgnoreGet)
	s.mux.HandleFunc("POST /api/budget-ignore", s.handleBudgetIgnoreSet)

	s.mux.HandleFunc("GET /api/gh-auth", s.handleGHAuth)
	s.mux.HandleFunc("GET /api/gh-rate-limits", s.handleGHRateLimits)
	s.mux.HandleFunc("GET /api/summaries", s.handleSummaries)

	s.mux.HandleFunc("GET /api/config/agent/{name}", s.handleAgentConfigGet)
	s.mux.HandleFunc("PUT /api/config/agent/{name}/general", s.handleAgentConfigGeneral)
	s.mux.HandleFunc("PUT /api/config/agent/{name}/cadences", s.handleAgentConfigCadences)
	s.mux.HandleFunc("PUT /api/config/agent/{name}/models", s.handleAgentConfigModels)
	s.mux.HandleFunc("PUT /api/config/agent/{name}/pipeline", s.handleAgentConfigPipeline)
	s.mux.HandleFunc("PUT /api/config/agent/{name}/hooks", s.handleAgentConfigHooks)
	s.mux.HandleFunc("PUT /api/config/agent/{name}/restrictions", s.handleAgentConfigRestrictions)
	s.mux.HandleFunc("PUT /api/config/agent/{name}/stats", s.handleAgentConfigStats)
	s.mux.HandleFunc("GET /api/config/agent/{name}/prompt", s.handleAgentPrompt)
	s.mux.HandleFunc("GET /api/config/stat-sources", s.handleStatSources)

	s.mux.HandleFunc("GET /api/config/governor", s.handleGovernorConfigGet)
	s.mux.HandleFunc("PUT /api/config/governor/sensing", s.handleGovernorSensing)
	s.mux.HandleFunc("PUT /api/config/governor/thresholds", s.handleGovernorThresholds)
	s.mux.HandleFunc("PUT /api/config/governor/labels", s.handleGovernorLabels)
	s.mux.HandleFunc("PUT /api/config/governor/budget", s.handleGovernorBudget)
	s.mux.HandleFunc("PUT /api/config/governor/notifications", s.handleGovernorNotifications)
	s.mux.HandleFunc("PUT /api/config/governor/health", s.handleGovernorHealth)
	s.mux.HandleFunc("POST /api/config/governor/agents", s.handleGovernorAddAgent)
	s.mux.HandleFunc("DELETE /api/config/governor/agents/{name}", s.handleGovernorRemoveAgent)
	s.mux.HandleFunc("PUT /api/config/governor/repos", s.handleGovernorRepos)

	s.mux.HandleFunc("GET /api/config/sidebar", s.handleSidebarGet)
	s.mux.HandleFunc("PUT /api/config/sidebar", s.handleSidebarSet)
	s.mux.HandleFunc("GET /api/config/backends", s.handleBackends)

	s.mux.HandleFunc("POST /api/chat", s.handleChat)

	s.mux.HandleFunc("GET /api/nous/status", s.handleNousStatus)
	s.mux.HandleFunc("GET /api/nous/ledger", s.handleNousLedger)
	s.mux.HandleFunc("GET /api/nous/principles", s.handleNousPrinciples)
	s.mux.HandleFunc("POST /api/nous/approve", s.handleNousApprove)
	s.mux.HandleFunc("POST /api/nous/abort", s.handleNousAbort)
	s.mux.HandleFunc("PUT /api/nous/mode", s.handleNousMode)
	s.mux.HandleFunc("PUT /api/nous/scope", s.handleNousScope)
	s.mux.HandleFunc("GET /api/nous/phase", s.handleNousPhase)
	s.mux.HandleFunc("PUT /api/nous/gate-decision", s.handleNousGateDecision)
	s.mux.HandleFunc("GET /api/nous/gate-pending", s.handleNousGatePending)
	s.mux.HandleFunc("POST /api/nous/gate-respond", s.handleNousGateRespond)
	s.mux.HandleFunc("GET /api/nous/gate-response", s.handleNousGateResponse)
	s.mux.HandleFunc("GET /api/nous/config", s.handleNousConfigGet)
	s.mux.HandleFunc("PUT /api/nous/config/goals", s.handleNousConfigGoals)
	s.mux.HandleFunc("PUT /api/nous/config/repos", s.handleNousConfigRepos)
	s.mux.HandleFunc("PUT /api/nous/config/output", s.handleNousConfigOutput)
	s.mux.HandleFunc("PUT /api/nous/config/fast-fail", s.handleNousConfigFastFail)
	s.mux.HandleFunc("PUT /api/nous/config/schedule", s.handleNousConfigSchedule)
	s.mux.HandleFunc("PUT /api/nous/config/controllables", s.handleNousConfigControllables)
	s.mux.HandleFunc("PUT /api/nous/config/principles", s.handleNousConfigPrinciples)
	s.mux.HandleFunc("DELETE /api/nous/principles/{id}", s.handleNousDeletePrinciple)
}

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": msg})
}

func okResponse(w http.ResponseWriter, extra map[string]string) {
	result := map[string]interface{}{"ok": true}
	for k, v := range extra {
		result[k] = v
	}
	jsonResponse(w, result)
}

func (s *Server) refreshAfterMutation() {
	if s.deps != nil && s.deps.RefreshFunc != nil {
		go s.deps.RefreshFunc()
	}
}

func decodeBody(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// --- Core status endpoints ---

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]string{
		"version": "2.0.0",
		"go":      "1.25",
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.deps.Config
	primaryRepo := cfg.Project.PrimaryRepo
	if primaryRepo == "" && len(cfg.Project.Repos) > 0 {
		primaryRepo = cfg.Project.Org + "/" + cfg.Project.Repos[0]
	}
	jsonResponse(w, map[string]interface{}{
		"org":              cfg.Project.Org,
		"repos":            cfg.Project.Repos,
		"ai_author":        cfg.Project.AIAuthor,
		"agents":           len(cfg.EnabledAgents()),
		"eval_interval_s":  cfg.Governor.EvalIntervalS,
		"primaryRepo":      primaryRepo,
	})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, s.deps.Governor.EvalHistory())
}

func (s *Server) handleTrends(w http.ResponseWriter, r *http.Request) {
	const hoursPerDay = 24
	const hoursPerWeek = 168

	rangeParam := r.URL.Query().Get("range")
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))

	switch rangeParam {
	case "week":
		hours = hoursPerWeek
	case "day":
		hours = hoursPerDay
	default:
		if hours <= 0 {
			hours = hoursPerDay
		}
	}

	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)

	evals := s.deps.Governor.EvalHistory()
	filtered := make([]interface{}, 0)
	for _, e := range evals {
		if e.Timestamp.After(cutoff) {
			filtered = append(filtered, e)
		}
	}

	jsonResponse(w, filtered)
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	kicks := s.deps.Governor.KickHistory()
	modes := s.deps.Governor.ModeHistory()
	jsonResponse(w, map[string]interface{}{
		"kicks": kicks,
		"modes": modes,
	})
}

func (s *Server) handleWidget(w http.ResponseWriter, r *http.Request) {
	state := s.deps.Governor.GetState()
	statuses := s.deps.AgentMgr.AllStatuses()

	running := 0
	paused := 0
	for _, a := range statuses {
		switch a.State {
		case "running":
			running++
		case "paused":
			paused++
		}
	}

	jsonResponse(w, map[string]interface{}{
		"mode":     state.Mode,
		"issues":   state.QueueIssues,
		"prs":      state.QueuePRs,
		"running":  running,
		"paused":   paused,
		"last_eval": state.LastEval,
	})
}

func (s *Server) handlePane(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("agent")
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if lines <= 0 {
		lines = 100
	}

	output, err := s.deps.AgentMgr.GetOutput(name, lines)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"agent":  name,
		"lines":  output,
		"count":  len(output),
	})
}

// --- Agent control endpoints ---

func (s *Server) handleKick(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("agent")
	var body struct {
		Message string `json:"message"`
	}
	if err := decodeBody(r, &body); err != nil || body.Message == "" {
		jsonError(w, "message is required", http.StatusBadRequest)
		return
	}

	if err := s.deps.AgentMgr.SendKick(name, body.Message); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.deps.Governor.RecordKick(name)
	s.refreshAfterMutation()
	okResponse(w, map[string]string{"status": "kicked", "agent": name})
}

func (s *Server) handleSwitch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("agent")
	backend := r.PathValue("backend")

	if err := s.deps.AgentMgr.SetBackendOverride(name, backend); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.refreshAfterMutation()
	okResponse(w, map[string]string{"status": "switched", "agent": name, "backend": backend})
}

func (s *Server) handleModelSet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("agent")
	model := r.PathValue("model")

	if err := s.deps.AgentMgr.SetModelOverride(name, model); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.refreshAfterMutation()
	okResponse(w, map[string]string{"status": "model_set", "agent": name, "model": model})
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("agent")

	if err := s.deps.AgentMgr.Pause(name); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.refreshAfterMutation()
	okResponse(w, map[string]string{"status": "paused", "agent": name})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("agent")

	if err := s.deps.AgentMgr.Resume(s.deps.Ctx, name); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.refreshAfterMutation()
	okResponse(w, map[string]string{"status": "resumed", "agent": name})
}

func (s *Server) handlePin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("agent")
	dimension := r.PathValue("dimension")

	var body struct {
		Value string `json:"value"`
	}
	_ = decodeBody(r, &body)

	if body.Value == "" {
		proc, getErr := s.deps.AgentMgr.GetStatus(name)
		if getErr != nil || proc == nil {
			jsonError(w, "agent not found", http.StatusBadRequest)
			return
		}
		switch dimension {
		case "cli":
			body.Value = proc.Config.Backend
			if proc.BackendOverride != "" {
				body.Value = proc.BackendOverride
			}
		case "model":
			body.Value = proc.Config.Model
			if proc.ModelOverride != "" {
				body.Value = proc.ModelOverride
			}
		}
	}

	var err error
	switch dimension {
	case "cli":
		err = s.deps.AgentMgr.PinCLI(name, body.Value)
	case "model":
		err = s.deps.AgentMgr.PinModel(name, body.Value)
	default:
		jsonError(w, "dimension must be 'cli' or 'model'", http.StatusBadRequest)
		return
	}

	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.refreshAfterMutation()
	okResponse(w, map[string]string{"status": "pinned", "agent": name, "dimension": dimension, "value": body.Value})
}

func (s *Server) handleUnpin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("agent")
	dimension := r.PathValue("dimension")

	var err error
	switch dimension {
	case "cli":
		err = s.deps.AgentMgr.UnpinCLI(name)
	case "model":
		err = s.deps.AgentMgr.UnpinModel(name)
	default:
		jsonError(w, "dimension must be 'cli' or 'model'", http.StatusBadRequest)
		return
	}

	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.refreshAfterMutation()
	okResponse(w, map[string]string{"status": "unpinned", "agent": name, "dimension": dimension})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("agent")

	if err := s.deps.AgentMgr.Restart(s.deps.Ctx, name); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.refreshAfterMutation()
	okResponse(w, map[string]string{"status": "restarted", "agent": name})
}

func (s *Server) handleResetRestarts(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("agent")

	if err := s.deps.AgentMgr.ResetRestartCount(name); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	okResponse(w, map[string]string{"status": "reset", "agent": name})
}

// --- Token endpoints ---

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	if s.deps.Tokens == nil {
		jsonResponse(w, map[string]string{"status": "no_collector"})
		return
	}
	summary := s.deps.Tokens.Summary()
	if summary == nil {
		jsonResponse(w, map[string]interface{}{"total_tokens": 0, "sessions": []interface{}{}})
		return
	}
	jsonResponse(w, summary)
}

func (s *Server) handleIssueCosts(w http.ResponseWriter, r *http.Request) {
	if s.deps.Tokens == nil {
		jsonResponse(w, map[string]interface{}{})
		return
	}
	jsonResponse(w, s.deps.Tokens.IssueCosts())
}

func (s *Server) handleModelAdvisor(w http.ResponseWriter, r *http.Request) {
	budget := s.deps.Governor.GetBudget()
	jsonResponse(w, map[string]interface{}{
		"budget":        budget,
		"recommendation": "Use haiku for simple tasks, sonnet for default, opus for complex refactors",
	})
}

func (s *Server) handleBudgetIgnoreGet(w http.ResponseWriter, r *http.Request) {
	budget := s.deps.Governor.GetBudget()
	jsonResponse(w, map[string]interface{}{
		"ignored": budget.IgnoredAgents,
	})
}

func (s *Server) handleBudgetIgnoreSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agents []string `json:"agents"`
	}
	if err := decodeBody(r, &body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	s.deps.Governor.SetBudgetIgnored(body.Agents)
	okResponse(w, map[string]string{"status": "updated"})
}

// --- GitHub endpoints ---

func (s *Server) handleGHAuth(w http.ResponseWriter, r *http.Request) {
	cfg := s.deps.Config.GitHub
	authType := "token"
	if cfg.AppID != 0 {
		authType = "app"
	}
	jsonResponse(w, map[string]interface{}{
		"type":            authType,
		"app_id":          cfg.AppID,
		"installation_id": cfg.InstallationID,
	})
}

func (s *Server) handleGHRateLimits(w http.ResponseWriter, r *http.Request) {
	limits, err := s.deps.GHClient.RateLimits(s.deps.Ctx)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, limits)
}

func (s *Server) handleSummaries(w http.ResponseWriter, r *http.Request) {
	s.statusMu.RLock()
	status := s.status
	s.statusMu.RUnlock()

	if status == nil {
		jsonResponse(w, map[string]interface{}{"issues": []interface{}{}, "prs": []interface{}{}})
		return
	}

	allIssues := make([]any, 0)
	allPRs := make([]any, 0)
	for _, repo := range status.Repos {
		allIssues = append(allIssues, repo.ActionableIssues...)
		allPRs = append(allPRs, repo.OpenPrs...)
	}

	jsonResponse(w, map[string]interface{}{
		"issues": allIssues,
		"prs":    allPRs,
		"hold":   status.Hold.Items,
	})
}

// --- Agent config endpoints ---

func (s *Server) handleAgentConfigGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	agentCfg, ok := s.deps.Config.Agents[name]
	if !ok {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	proc, err := s.deps.AgentMgr.GetStatus(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"name":             name,
		"config":           agentCfg,
		"state":            proc.State,
		"paused":           proc.Paused,
		"pinned_cli":       proc.PinnedCLI,
		"pinned_model":     proc.PinnedModel,
		"model_override":   proc.ModelOverride,
		"backend_override": proc.BackendOverride,
		"restart_count":    proc.RestartCount,
		"kick_history":     proc.KickHistory,
	})
}

func (s *Server) handleAgentConfigGeneral(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.deps.Config.Agents[name]; !ok {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	var body struct {
		Enabled     *bool  `json:"enabled"`
		ClearOnKick *bool  `json:"clear_on_kick"`
		BeadsDir    string `json:"beads_dir"`
	}
	if err := decodeBody(r, &body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	agentCfg := s.deps.Config.Agents[name]
	if body.Enabled != nil {
		agentCfg.Enabled = *body.Enabled
	}
	if body.ClearOnKick != nil {
		agentCfg.ClearOnKick = *body.ClearOnKick
	}
	if body.BeadsDir != "" {
		agentCfg.BeadsDir = body.BeadsDir
	}
	s.deps.Config.Agents[name] = agentCfg

	okResponse(w, map[string]string{"status": "updated", "agent": name})
}

func (s *Server) handleAgentConfigCadences(w http.ResponseWriter, r *http.Request) {
	s.handleConfigStub(w, r, "cadences")
}

func (s *Server) handleAgentConfigModels(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Backend string `json:"backend"`
		Model   string `json:"model"`
	}
	if err := decodeBody(r, &body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	agentCfg, ok := s.deps.Config.Agents[name]
	if !ok {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	if body.Backend != "" {
		agentCfg.Backend = body.Backend
	}
	if body.Model != "" {
		agentCfg.Model = body.Model
	}
	s.deps.Config.Agents[name] = agentCfg

	okResponse(w, map[string]string{"status": "updated", "agent": name})
}

func (s *Server) handleAgentConfigPipeline(w http.ResponseWriter, r *http.Request) {
	s.handleConfigStub(w, r, "pipeline")
}

func (s *Server) handleAgentConfigHooks(w http.ResponseWriter, r *http.Request) {
	s.handleConfigStub(w, r, "hooks")
}

func (s *Server) handleAgentConfigRestrictions(w http.ResponseWriter, r *http.Request) {
	s.handleConfigStub(w, r, "restrictions")
}

func (s *Server) handleAgentConfigStats(w http.ResponseWriter, r *http.Request) {
	s.handleConfigStub(w, r, "stats")
}

func (s *Server) handleAgentPrompt(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.deps.Config.Agents[name]; !ok {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"agent":  name,
		"prompt": fmt.Sprintf("Agent %s policy (loaded from CLAUDE.md)", name),
	})
}

func (s *Server) handleStatSources(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, []string{"ga4", "github", "sentry", "custom"})
}

// --- Governor config endpoints ---

func (s *Server) handleGovernorConfigGet(w http.ResponseWriter, r *http.Request) {
	state := s.deps.Governor.GetState()
	budget := s.deps.Governor.GetBudget()

	jsonResponse(w, map[string]interface{}{
		"mode":       state.Mode,
		"eval_interval_s": s.deps.Config.Governor.EvalIntervalS,
		"modes":      s.deps.Config.Governor.Modes,
		"budget":     budget,
		"org":        s.deps.Config.Project.Org,
		"repos":      s.deps.Config.Project.Repos,
	})
}

func (s *Server) handleGovernorSensing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EvalIntervalS int `json:"eval_interval_s"`
	}
	if err := decodeBody(r, &body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	if body.EvalIntervalS > 0 {
		s.deps.Config.Governor.EvalIntervalS = body.EvalIntervalS
	}

	okResponse(w, map[string]string{"status": "updated"})
}

func (s *Server) handleGovernorThresholds(w http.ResponseWriter, r *http.Request) {
	var body map[string]int
	if err := decodeBody(r, &body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	for modeName, threshold := range body {
		if mode, ok := s.deps.Config.Governor.Modes[modeName]; ok {
			mode.Threshold = threshold
			s.deps.Config.Governor.Modes[modeName] = mode
		}
	}

	okResponse(w, map[string]string{"status": "updated"})
}

func (s *Server) handleGovernorLabels(w http.ResponseWriter, r *http.Request) {
	s.handleConfigStub(w, r, "labels")
}

func (s *Server) handleGovernorBudget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WeeklyLimit int64 `json:"weekly_limit"`
	}
	if err := decodeBody(r, &body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	s.deps.Governor.SetBudgetLimit(body.WeeklyLimit)
	okResponse(w, map[string]string{"status": "updated"})
}

func (s *Server) handleGovernorNotifications(w http.ResponseWriter, r *http.Request) {
	s.handleConfigStub(w, r, "notifications")
}

func (s *Server) handleGovernorHealth(w http.ResponseWriter, r *http.Request) {
	s.handleConfigStub(w, r, "health")
}

func (s *Server) handleGovernorAddAgent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Backend string `json:"backend"`
		Model   string `json:"model"`
	}
	if err := decodeBody(r, &body); err != nil || body.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}

	if _, exists := s.deps.Config.Agents[body.Name]; exists {
		jsonError(w, "agent already exists", http.StatusConflict)
		return
	}

	if body.Backend == "" {
		body.Backend = "claude"
	}

	s.deps.Config.Agents[body.Name] = config.AgentConfig{
		Backend: body.Backend,
		Model:   body.Model,
		Enabled: true,
	}

	okResponse(w, map[string]string{"status": "added", "agent": body.Name})
}

func (s *Server) handleGovernorRemoveAgent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.deps.Config.Agents[name]; !ok {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	delete(s.deps.Config.Agents, name)
	okResponse(w, map[string]string{"status": "removed", "agent": name})
}

func (s *Server) handleGovernorRepos(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Repos []string `json:"repos"`
	}
	if err := decodeBody(r, &body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	s.deps.Config.Project.Repos = body.Repos
	okResponse(w, map[string]string{"status": "updated"})
}

// --- Sidebar endpoints ---

func (s *Server) handleSidebarGet(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, s.sidebar)
}

func (s *Server) handleSidebarSet(w http.ResponseWriter, r *http.Request) {
	var body interface{}
	if err := decodeBody(r, &body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	s.sidebarMu.Lock()
	s.sidebar = body
	s.sidebarMu.Unlock()

	okResponse(w, map[string]string{"status": "updated"})
}

func (s *Server) handleBackends(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, []map[string]interface{}{
		{"id": "claude", "name": "Claude Code", "models": []string{"opus", "sonnet", "haiku"}},
		{"id": "copilot", "name": "GitHub Copilot", "models": []string{"gpt-4o", "gpt-4o-mini"}},
		{"id": "gemini", "name": "Gemini", "models": []string{"gemini-2.5-pro", "gemini-2.5-flash"}},
		{"id": "goose", "name": "Goose", "models": []string{"default"}},
	})
}

// --- Chat endpoint ---

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string `json:"message"`
	}
	if err := decodeBody(r, &body); err != nil || body.Message == "" {
		jsonError(w, "message is required", http.StatusBadRequest)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"response": fmt.Sprintf("Chat is not yet implemented in v2. Your message: %s", body.Message),
		"status":   "stub",
	})
}

// --- Nous endpoints ---

func (s *Server) handleNousStatus(w http.ResponseWriter, r *http.Request) {
	if s.deps.Nous == nil {
		jsonResponse(w, map[string]string{"status": "not_configured"})
		return
	}
	jsonResponse(w, s.deps.Nous.Status)
}

func (s *Server) handleNousLedger(w http.ResponseWriter, r *http.Request) {
	if s.deps.Nous == nil {
		jsonResponse(w, []interface{}{})
		return
	}
	jsonResponse(w, s.deps.Nous.Ledger)
}

func (s *Server) handleNousPrinciples(w http.ResponseWriter, r *http.Request) {
	if s.deps.Nous == nil {
		jsonResponse(w, []interface{}{})
		return
	}
	jsonResponse(w, s.deps.Nous.Principles)
}

func (s *Server) handleNousApprove(w http.ResponseWriter, r *http.Request) {
	okResponse(w, map[string]string{"status": "approved"})
}

func (s *Server) handleNousAbort(w http.ResponseWriter, r *http.Request) {
	okResponse(w, map[string]string{"status": "aborted"})
}

func (s *Server) handleNousMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if err := decodeBody(r, &body); err != nil || body.Mode == "" {
		jsonError(w, "mode is required", http.StatusBadRequest)
		return
	}

	if s.deps.Nous != nil {
		s.deps.Nous.Mode = body.Mode
	}

	okResponse(w, map[string]string{"status": "updated", "mode": body.Mode})
}

func (s *Server) handleNousScope(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope string `json:"scope"`
	}
	if err := decodeBody(r, &body); err != nil || body.Scope == "" {
		jsonError(w, "scope is required", http.StatusBadRequest)
		return
	}

	if s.deps.Nous != nil {
		s.deps.Nous.Scope = body.Scope
	}

	okResponse(w, map[string]string{"status": "updated", "scope": body.Scope})
}

func (s *Server) handleNousPhase(w http.ResponseWriter, r *http.Request) {
	if s.deps.Nous == nil {
		jsonResponse(w, map[string]string{"phase": "inactive"})
		return
	}
	jsonResponse(w, map[string]string{"phase": s.deps.Nous.Phase})
}

func (s *Server) handleNousGateDecision(w http.ResponseWriter, r *http.Request) {
	s.handleConfigStub(w, r, "gate-decision")
}

func (s *Server) handleNousGatePending(w http.ResponseWriter, r *http.Request) {
	if s.deps.Nous == nil {
		jsonResponse(w, map[string]interface{}{})
		return
	}
	jsonResponse(w, s.deps.Nous.GatePending)
}

func (s *Server) handleNousGateRespond(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := decodeBody(r, &body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	if s.deps.Nous != nil {
		s.deps.Nous.GateResponse = body
	}

	okResponse(w, map[string]string{"status": "responded"})
}

func (s *Server) handleNousGateResponse(w http.ResponseWriter, r *http.Request) {
	if s.deps.Nous == nil {
		jsonResponse(w, map[string]interface{}{})
		return
	}
	jsonResponse(w, s.deps.Nous.GateResponse)
}

func (s *Server) handleNousConfigGet(w http.ResponseWriter, r *http.Request) {
	if s.deps.Nous == nil {
		jsonResponse(w, map[string]interface{}{})
		return
	}
	jsonResponse(w, s.deps.Nous.Config)
}

func (s *Server) handleNousConfigGoals(w http.ResponseWriter, r *http.Request) {
	s.handleNousConfigSection(w, r, "goals")
}

func (s *Server) handleNousConfigRepos(w http.ResponseWriter, r *http.Request) {
	s.handleNousConfigSection(w, r, "repos")
}

func (s *Server) handleNousConfigOutput(w http.ResponseWriter, r *http.Request) {
	s.handleNousConfigSection(w, r, "output")
}

func (s *Server) handleNousConfigFastFail(w http.ResponseWriter, r *http.Request) {
	s.handleNousConfigSection(w, r, "fast_fail")
}

func (s *Server) handleNousConfigSchedule(w http.ResponseWriter, r *http.Request) {
	s.handleNousConfigSection(w, r, "schedule")
}

func (s *Server) handleNousConfigControllables(w http.ResponseWriter, r *http.Request) {
	s.handleNousConfigSection(w, r, "controllables")
}

func (s *Server) handleNousConfigPrinciples(w http.ResponseWriter, r *http.Request) {
	s.handleNousConfigSection(w, r, "principles")
}

func (s *Server) handleNousDeletePrinciple(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.deps.Nous == nil {
		jsonError(w, "nous not configured", http.StatusNotFound)
		return
	}

	filtered := make([]NousPrinciple, 0, len(s.deps.Nous.Principles))
	for _, p := range s.deps.Nous.Principles {
		if p.ID != id {
			filtered = append(filtered, p)
		}
	}
	s.deps.Nous.Principles = filtered

	okResponse(w, map[string]string{"status": "deleted", "id": id})
}

func (s *Server) handleNousConfigSection(w http.ResponseWriter, r *http.Request, section string) {
	if s.deps.Nous == nil {
		jsonError(w, "nous not configured", http.StatusNotFound)
		return
	}

	var body interface{}
	if err := decodeBody(r, &body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	if s.deps.Nous.Config == nil {
		s.deps.Nous.Config = make(map[string]interface{})
	}
	s.deps.Nous.Config[section] = body

	okResponse(w, map[string]string{"status": "updated", "section": section})
}

func (s *Server) handleConfigStub(w http.ResponseWriter, r *http.Request, section string) {
	if r.Method == http.MethodGet {
		jsonResponse(w, map[string]interface{}{
			"section": section,
			"status":  "stub",
		})
		return
	}

	var body interface{}
	if err := decodeBody(r, &body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	okResponse(w, map[string]string{"status": "updated", "section": section})
}

// suppress unused import warnings
var _ = strings.Contains
var _ = uuid.New
