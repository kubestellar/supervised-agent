package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/kubestellar/hive/v2/pkg/github"
	"github.com/kubestellar/hive/v2/pkg/governor"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	port       int
	statusMu   sync.RWMutex
	status     *StatusPayload
	sseClients map[chan []byte]struct{}
	sseMu      sync.Mutex
	logger     *slog.Logger
}

type StatusPayload struct {
	Governor     governor.State            `json:"governor"`
	Actionable   *github.ActionableResult  `json:"actionable,omitempty"`
	Agents       map[string]AgentStatus    `json:"agents"`
	Tokens       *TokenSummary             `json:"tokens,omitempty"`
	Timestamp    time.Time                 `json:"timestamp"`
}

type AgentStatus struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	Backend   string `json:"backend"`
	Model     string `json:"model"`
	PID       int    `json:"pid,omitempty"`
	LastKick  string `json:"last_kick,omitempty"`
}

type TokenSummary struct {
	TotalTokens  int64            `json:"total_tokens"`
	ByAgent      map[string]int64 `json:"by_agent"`
	ByModel      map[string]int64 `json:"by_model"`
	SessionCount int              `json:"session_count"`
}

const sseRetryMs = 3000

func NewServer(port int, logger *slog.Logger) *Server {
	return &Server{
		port:       port,
		sseClients: make(map[chan []byte]struct{}),
		logger:     logger,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/events", s.handleSSE)

	staticContent, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("loading embedded static files: %w", err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(staticContent)))

	addr := fmt.Sprintf(":%d", s.port)
	s.logger.Info("dashboard starting", "addr", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) UpdateStatus(status *StatusPayload) {
	s.statusMu.Lock()
	status.Timestamp = time.Now()
	s.status = status
	s.statusMu.Unlock()

	data, err := json.Marshal(status)
	if err != nil {
		s.logger.Warn("failed to marshal status for SSE", "error", err)
		return
	}

	s.broadcast(data)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.statusMu.RLock()
	status := s.status
	s.statusMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if status == nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "initializing"})
		return
	}
	json.NewEncoder(w).Encode(status)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan []byte, 16)
	s.sseMu.Lock()
	s.sseClients[ch] = struct{}{}
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseClients, ch)
		s.sseMu.Unlock()
	}()

	fmt.Fprintf(w, "retry: %d\n\n", sseRetryMs)
	flusher.Flush()

	s.statusMu.RLock()
	if s.status != nil {
		data, _ := json.Marshal(s.status)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
	s.statusMu.RUnlock()

	for {
		select {
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) broadcast(data []byte) {
	s.sseMu.Lock()
	defer s.sseMu.Unlock()

	for ch := range s.sseClients {
		select {
		case ch <- data:
		default:
			// Client too slow — drop the event
		}
	}
}
