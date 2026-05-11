package dashboard

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubestellar/hive/v2/pkg/github"
	"github.com/kubestellar/hive/v2/pkg/governor"
)

// newTestServer returns a Server wired with a discarding logger for tests.
func newTestServer() *Server {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewServer(0, logger)
}

// newTestMux wires the three API handlers onto a fresh ServeMux so we can
// exercise them without calling Start() (which calls ListenAndServe).
func newTestMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/events", s.handleSSE)
	return mux
}

// ---- helper: build a minimal StatusPayload -----------------------------------

func minimalPayload() *StatusPayload {
	return &StatusPayload{
		Governor: governor.State{
			Mode:        governor.ModeIdle,
			QueueIssues: 2,
			QueuePRs:    1,
			Cadences:    map[string]governor.AgentCadence{},
			LastKick:    map[string]time.Time{},
		},
		Actionable: &github.ActionableResult{
			GeneratedAt: time.Now(),
		},
		Agents: map[string]AgentStatus{
			"scanner": {Name: "scanner", State: "running", Backend: "claude", Model: "sonnet"},
		},
		Tokens: &TokenSummary{
			TotalTokens:  1000,
			ByAgent:      map[string]int64{"scanner": 1000},
			ByModel:      map[string]int64{"sonnet": 1000},
			SessionCount: 1,
		},
	}
}

// =============================================================================
// NewServer
// =============================================================================

func TestNewServer_DefaultFields(t *testing.T) {
	s := newTestServer()
	if s.port != 0 {
		t.Errorf("expected port 0, got %d", s.port)
	}
	if s.sseClients == nil {
		t.Error("sseClients map should be initialised")
	}
	if s.status != nil {
		t.Error("status should be nil on fresh server")
	}
	if s.logger == nil {
		t.Error("logger should not be nil")
	}
}

// =============================================================================
// handleHealth
// =============================================================================

func TestHandleHealth_StatusCode(t *testing.T) {
	s := newTestServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	s.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
}

func TestHandleHealth_ContentType(t *testing.T) {
	s := newTestServer()
	rec := httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
}

func TestHandleHealth_Body(t *testing.T) {
	s := newTestServer()
	rec := httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	var payload map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if payload["status"] != "ok" {
		t.Errorf(`expected {"status":"ok"}, got %v`, payload)
	}
}

// =============================================================================
// handleStatus — nil status
// =============================================================================

func TestHandleStatus_NilStatus_StatusCode(t *testing.T) {
	s := newTestServer()
	rec := httptest.NewRecorder()
	s.handleStatus(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestHandleStatus_NilStatus_ReturnsInitializing(t *testing.T) {
	s := newTestServer()
	rec := httptest.NewRecorder()
	s.handleStatus(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	var payload map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if payload["status"] != "initializing" {
		t.Errorf(`expected "initializing", got %q`, payload["status"])
	}
}

func TestHandleStatus_NilStatus_ContentType(t *testing.T) {
	s := newTestServer()
	rec := httptest.NewRecorder()
	s.handleStatus(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %q", ct)
	}
}

// =============================================================================
// handleStatus — after UpdateStatus
// =============================================================================

func TestHandleStatus_AfterUpdate_ReturnsPayload(t *testing.T) {
	s := newTestServer()
	s.UpdateStatus(minimalPayload())

	rec := httptest.NewRecorder()
	s.handleStatus(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	var payload StatusPayload
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if payload.Governor.Mode != governor.ModeIdle {
		t.Errorf("unexpected governor mode: %q", payload.Governor.Mode)
	}
	if _, ok := payload.Agents["scanner"]; !ok {
		t.Error("expected scanner agent in payload")
	}
}

func TestHandleStatus_AfterUpdate_DoesNotReturnInitializing(t *testing.T) {
	s := newTestServer()
	s.UpdateStatus(minimalPayload())

	rec := httptest.NewRecorder()
	s.handleStatus(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	body := rec.Body.String()
	if strings.Contains(body, `"initializing"`) {
		t.Errorf("should not contain 'initializing' after UpdateStatus, got: %s", body)
	}
}

// =============================================================================
// UpdateStatus
// =============================================================================

func TestUpdateStatus_SetsTimestamp(t *testing.T) {
	s := newTestServer()
	before := time.Now()
	p := minimalPayload()
	p.Timestamp = time.Time{} // zero it out to confirm UpdateStatus sets it
	s.UpdateStatus(p)
	after := time.Now()

	s.statusMu.RLock()
	ts := s.status.Timestamp
	s.statusMu.RUnlock()

	if ts.Before(before) || ts.After(after) {
		t.Errorf("timestamp %v not within [%v, %v]", ts, before, after)
	}
}

func TestUpdateStatus_StoresStatus(t *testing.T) {
	s := newTestServer()
	p := minimalPayload()
	s.UpdateStatus(p)

	s.statusMu.RLock()
	stored := s.status
	s.statusMu.RUnlock()

	if stored == nil {
		t.Fatal("stored status is nil")
	}
	if stored.Governor.QueueIssues != 2 {
		t.Errorf("expected QueueIssues=2, got %d", stored.Governor.QueueIssues)
	}
}

func TestUpdateStatus_OverwritesPreviousStatus(t *testing.T) {
	s := newTestServer()
	p1 := minimalPayload()
	p1.Governor.QueueIssues = 5
	s.UpdateStatus(p1)

	p2 := minimalPayload()
	p2.Governor.QueueIssues = 99
	s.UpdateStatus(p2)

	s.statusMu.RLock()
	qi := s.status.Governor.QueueIssues
	s.statusMu.RUnlock()

	if qi != 99 {
		t.Errorf("expected QueueIssues=99 after second update, got %d", qi)
	}
}

func TestUpdateStatus_BroadcastsToClients(t *testing.T) {
	s := newTestServer()

	// Manually register a buffered client channel.
	ch := make(chan []byte, 4)
	s.sseMu.Lock()
	s.sseClients[ch] = struct{}{}
	s.sseMu.Unlock()

	s.UpdateStatus(minimalPayload())

	select {
	case data := <-ch:
		var payload StatusPayload
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if payload.Governor.Mode != governor.ModeIdle {
			t.Errorf("unexpected mode in broadcast: %q", payload.Governor.Mode)
		}
	case <-time.After(time.Second):
		t.Error("timed out waiting for broadcast")
	}
}

// =============================================================================
// handleSSE — headers
// =============================================================================

// sseTestServer starts a real httptest.Server (needed for Flusher support).
func sseTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	s := newTestServer()
	mux := newTestMux(s)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, s
}

func TestHandleSSE_Headers(t *testing.T) {
	ts, _ := sseTestServer(t)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	// We only read the headers — cancel immediately.
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil && !strings.Contains(err.Error(), "context deadline exceeded") {
		// A timeout after headers arrive is fine.
		t.Fatalf("request error: %v", err)
	}
	if resp == nil {
		t.Fatal("no response")
	}
	defer resp.Body.Close()

	checks := map[string]string{
		"Content-Type":                "text/event-stream",
		"Cache-Control":               "no-cache",
		"Connection":                  "keep-alive",
		"Access-Control-Allow-Origin": "*",
	}
	for header, want := range checks {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("header %q: want %q, got %q", header, want, got)
		}
	}
}

func TestHandleSSE_StatusCode(t *testing.T) {
	ts, _ := sseTestServer(t)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(ts.URL + "/api/events")
	if err != nil && resp == nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// =============================================================================
// handleSSE — retry line
// =============================================================================

func TestHandleSSE_SendsRetryLine(t *testing.T) {
	ts, _ := sseTestServer(t)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(ts.URL + "/api/events")
	if err != nil && resp == nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	deadline := time.After(2 * time.Second)
	for {
		done := make(chan bool, 1)
		go func() { done <- scanner.Scan() }()
		select {
		case ok := <-done:
			if !ok {
				t.Fatal("SSE stream closed before retry line")
			}
			line := scanner.Text()
			if strings.HasPrefix(line, "retry:") {
				// Check value
				if !strings.Contains(line, "3000") {
					t.Errorf("unexpected retry value: %q", line)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for retry line")
		}
	}
}

// =============================================================================
// handleSSE — initial snapshot when status exists
// =============================================================================

func TestHandleSSE_InitialSnapshot_WhenStatusExists(t *testing.T) {
	ts, s := sseTestServer(t)

	// Pre-populate status before connecting the SSE client.
	s.UpdateStatus(minimalPayload())

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(ts.URL + "/api/events")
	if err != nil && resp == nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	deadline := time.After(3 * time.Second)
	for {
		done := make(chan bool, 1)
		go func() { done <- scanner.Scan() }()
		select {
		case ok := <-done:
			if !ok {
				t.Fatal("stream closed before snapshot")
			}
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				jsonPart := strings.TrimPrefix(line, "data: ")
				var payload StatusPayload
				if err := json.Unmarshal([]byte(jsonPart), &payload); err != nil {
					t.Fatalf("unmarshal error: %v — line: %q", err, line)
				}
				if payload.Governor.Mode != governor.ModeIdle {
					t.Errorf("unexpected mode in snapshot: %q", payload.Governor.Mode)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for initial snapshot")
		}
	}
}

func TestHandleSSE_NoInitialSnapshot_WhenStatusNil(t *testing.T) {
	ts, _ := sseTestServer(t)

	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(ts.URL + "/api/events")
	if err != nil && resp == nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	// Read lines for up to 400 ms; none should be a data line.
	deadline := time.After(400 * time.Millisecond)
	for {
		done := make(chan bool, 1)
		go func() { done <- scanner.Scan() }()
		select {
		case ok := <-done:
			if !ok {
				return // stream closed — fine
			}
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				t.Errorf("unexpected data line when status is nil: %q", line)
			}
		case <-deadline:
			return // passed — no spurious data line
		}
	}
}

// =============================================================================
// handleSSE — receives broadcast updates
// =============================================================================

func TestHandleSSE_ReceivesBroadcast(t *testing.T) {
	ts, s := sseTestServer(t)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(ts.URL + "/api/events")
	if err != nil && resp == nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	// Give the handler time to register the client channel and flush the retry
	// line before we broadcast.
	time.Sleep(100 * time.Millisecond)

	p := minimalPayload()
	p.Governor.QueuePRs = 42
	s.UpdateStatus(p)

	scanner := bufio.NewScanner(resp.Body)
	deadline := time.After(3 * time.Second)
	for {
		done := make(chan bool, 1)
		go func() { done <- scanner.Scan() }()
		select {
		case ok := <-done:
			if !ok {
				t.Fatal("stream closed before broadcast")
			}
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				jsonPart := strings.TrimPrefix(line, "data: ")
				var payload StatusPayload
				if err := json.Unmarshal([]byte(jsonPart), &payload); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if payload.Governor.QueuePRs == 42 {
					return // success
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for broadcast data event")
		}
	}
}

// =============================================================================
// handleSSE — client cleanup on disconnect
// =============================================================================

func TestHandleSSE_ClientCleanupOnDisconnect(t *testing.T) {
	ts, s := sseTestServer(t)

	// Verify count before
	s.sseMu.Lock()
	before := len(s.sseClients)
	s.sseMu.Unlock()

	// Connect and immediately close.
	resp, err := http.Get(ts.URL + "/api/events")
	if err != nil && resp == nil {
		t.Fatalf("request error: %v", err)
	}
	resp.Body.Close() // triggers disconnect

	// Give the defer in handleSSE time to run.
	time.Sleep(150 * time.Millisecond)

	s.sseMu.Lock()
	after := len(s.sseClients)
	s.sseMu.Unlock()

	if after != before {
		t.Errorf("expected %d clients after disconnect, got %d", before, after)
	}
}

// =============================================================================
// broadcast — multiple clients
// =============================================================================

func TestBroadcast_MultipleClients(t *testing.T) {
	s := newTestServer()

	const numClients = 5
	channels := make([]chan []byte, numClients)
	for i := range channels {
		ch := make(chan []byte, 4)
		channels[i] = ch
		s.sseMu.Lock()
		s.sseClients[ch] = struct{}{}
		s.sseMu.Unlock()
	}

	p := minimalPayload()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	s.broadcast(data)

	for i, ch := range channels {
		select {
		case got := <-ch:
			if string(got) != string(data) {
				t.Errorf("client %d: unexpected payload", i)
			}
		case <-time.After(time.Second):
			t.Errorf("client %d: timed out waiting for broadcast", i)
		}
	}
}

// =============================================================================
// broadcast — drops event for slow (full-channel) client
// =============================================================================

func TestBroadcast_DropsEventForSlowClient(t *testing.T) {
	s := newTestServer()

	// A channel with capacity 1, already full — simulates a slow client.
	full := make(chan []byte, 1)
	full <- []byte("old-data")

	// A fast client with room.
	fast := make(chan []byte, 4)

	s.sseMu.Lock()
	s.sseClients[full] = struct{}{}
	s.sseClients[fast] = struct{}{}
	s.sseMu.Unlock()

	data, _ := json.Marshal(minimalPayload())
	s.broadcast(data)

	// fast client should receive.
	select {
	case got := <-fast:
		if string(got) != string(data) {
			t.Error("fast client: unexpected payload")
		}
	case <-time.After(time.Second):
		t.Error("fast client: timed out")
	}

	// full channel should still have only the old item — the broadcast was dropped.
	if len(full) != 1 {
		t.Errorf("slow client channel length: want 1, got %d", len(full))
	}
	old := <-full
	if string(old) != "old-data" {
		t.Errorf("slow client: expected old-data, got %q", old)
	}
}

// =============================================================================
// broadcast — no clients (should not panic)
// =============================================================================

func TestBroadcast_NoClients(t *testing.T) {
	s := newTestServer()
	data, _ := json.Marshal(minimalPayload())
	// Must not panic.
	s.broadcast(data)
}

// =============================================================================
// Concurrency: UpdateStatus + handleStatus race
// =============================================================================

func TestConcurrency_UpdateAndReadStatus(t *testing.T) {
	s := newTestServer()

	var wg sync.WaitGroup
	const workers = 20

	// Writers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.UpdateStatus(minimalPayload())
		}()
	}

	// Readers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			s.handleStatus(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
		}()
	}

	wg.Wait()
}

// =============================================================================
// Concurrency: broadcast with concurrent client add/remove
// =============================================================================

func TestConcurrency_BroadcastWithClientChurn(t *testing.T) {
	s := newTestServer()
	data, _ := json.Marshal(minimalPayload())

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := make(chan []byte, 4)
			s.sseMu.Lock()
			s.sseClients[ch] = struct{}{}
			s.sseMu.Unlock()

			s.broadcast(data)

			s.sseMu.Lock()
			delete(s.sseClients, ch)
			s.sseMu.Unlock()
		}()
	}
	wg.Wait()
}

// =============================================================================
// SSE — multiple sequential broadcasts
// =============================================================================

func TestHandleSSE_MultipleSequentialBroadcasts(t *testing.T) {
	ts, s := sseTestServer(t)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(ts.URL + "/api/events")
	if err != nil && resp == nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	// Wait for handler to register.
	time.Sleep(100 * time.Millisecond)

	const broadcasts = 3
	for i := 0; i < broadcasts; i++ {
		p := minimalPayload()
		p.Governor.QueueIssues = i + 1
		s.UpdateStatus(p)
	}

	scanner := bufio.NewScanner(resp.Body)
	seen := 0
	deadline := time.After(4 * time.Second)
	for seen < broadcasts {
		done := make(chan bool, 1)
		go func() { done <- scanner.Scan() }()
		select {
		case ok := <-done:
			if !ok {
				t.Fatalf("stream closed after %d broadcasts", seen)
			}
			if strings.HasPrefix(scanner.Text(), "data:") {
				seen++
			}
		case <-deadline:
			t.Fatalf("timed out after receiving %d/%d broadcasts", seen, broadcasts)
		}
	}
}

// =============================================================================
// handleSSE — non-flusher response writer returns 500
// =============================================================================

// plainResponseWriter implements http.ResponseWriter but intentionally does NOT
// implement http.Flusher, so handleSSE falls into its "streaming unsupported" branch.
type plainResponseWriter struct {
	headers http.Header
	code    int
	body    strings.Builder
}

func newPlainResponseWriter() *plainResponseWriter {
	return &plainResponseWriter{headers: make(http.Header)}
}

func (w *plainResponseWriter) Header() http.Header         { return w.headers }
func (w *plainResponseWriter) WriteHeader(code int)        { w.code = code }
func (w *plainResponseWriter) Write(b []byte) (int, error) { return w.body.Write(b) }

func TestHandleSSE_NonFlusher_Returns500(t *testing.T) {
	s := newTestServer()
	w := newPlainResponseWriter()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	s.handleSSE(w, req)

	if w.code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.code)
	}
}

// =============================================================================
// Start — exercises the mux wiring and static-file embedding paths
// =============================================================================

// freePort returns an available TCP port on localhost.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// TestStart_ServesEndpoints verifies that Start() correctly wires handlers
// (mux setup, fs.Sub for static files) by running it on a free port in a
// goroutine and making real HTTP requests.  The goroutine is intentionally
// leaked — ListenAndServe only returns when the process exits or we close the
// listener, neither of which we do here.
func TestStart_ServesEndpoints(t *testing.T) {
	port := freePort(t)
	s := NewServer(port, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	// Run Start in a background goroutine; it blocks until the server dies.
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start()
	}()

	// Poll until the server is accepting connections (up to 2 s).
	addr := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: time.Second}
	var resp *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, err := client.Get(addr + "/api/health")
		if err == nil {
			resp = r
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if resp == nil {
		t.Fatal("server did not start within 2 s")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("health: expected 200, got %d", resp.StatusCode)
	}

	// Confirm /api/status also works (exercises the mux wiring).
	resp2, err := client.Get(addr + "/api/status")
	if err != nil {
		t.Fatalf("status request: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status: expected 200, got %d", resp2.StatusCode)
	}

	// Confirm static content is served (exercises fs.Sub + FileServer).
	resp3, err := client.Get(addr + "/")
	if err != nil {
		t.Fatalf("static request: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Errorf("static index: expected 200, got %d", resp3.StatusCode)
	}
}
