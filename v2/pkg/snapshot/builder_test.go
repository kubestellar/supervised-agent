package snapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/kubestellar/hive/v2/pkg/dashboard"
	"github.com/kubestellar/hive/v2/pkg/governor"
)

// discardLogger returns a logger that discards all output, keeping test output clean.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 10}))
}

// minimalStatus returns a StatusPayload with sensible defaults for tests that
// do not need to inspect specific field values.
func minimalStatus() *dashboard.StatusPayload {
	return &dashboard.StatusPayload{
		Governor: governor.State{
			Mode:        governor.ModeIdle,
			QueueIssues: 0,
			QueuePRs:    0,
		},
		Agents:    map[string]dashboard.AgentStatus{},
		Timestamp: time.Now().UTC(),
	}
}

// richStatus returns a StatusPayload with several agents and non-zero queue counts.
func richStatus() *dashboard.StatusPayload {
	return &dashboard.StatusPayload{
		Governor: governor.State{
			Mode:        governor.ModeSurge,
			QueueIssues: 7,
			QueuePRs:    3,
		},
		Agents: map[string]dashboard.AgentStatus{
			"scanner": {
				Name:    "scanner",
				State:   "running",
				Backend: "anthropic",
				Model:   "claude-sonnet-4-6",
			},
			"fixer": {
				Name:    "fixer",
				State:   "idle",
				Backend: "anthropic",
				Model:   "claude-opus-4-5",
			},
		},
		Timestamp: time.Now().UTC(),
	}
}

// globJSON returns all status-*.json files in dir (excludes latest.json).
func globJSON(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "status-*.json"))
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	return matches
}

// ---- NewBuilder ---------------------------------------------------------

func TestNewBuilder_FieldsStored(t *testing.T) {
	dir := t.TempDir()
	logger := discardLogger()
	b := NewBuilder(dir, logger)

	if b.outputDir != dir {
		t.Errorf("outputDir: got %q, want %q", b.outputDir, dir)
	}
	if b.logger != logger {
		t.Error("logger not stored correctly")
	}
}

// ---- Build: directory creation ------------------------------------------

func TestBuild_CreatesOutputDirIfNotExists(t *testing.T) {
	base := t.TempDir()
	// Use a subdirectory that does not exist yet.
	dir := filepath.Join(base, "snapshots", "nested")

	b := NewBuilder(dir, discardLogger())
	if err := b.Build(minimalStatus()); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("output dir does not exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("output path is not a directory")
	}
}

// ---- Build: timestamped JSON file ---------------------------------------

func TestBuild_WritesTimestampedStatusJSON(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	status := richStatus()
	if err := b.Build(status); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	files := globJSON(t, dir)
	if len(files) != 1 {
		t.Fatalf("expected 1 status-*.json, got %d: %v", len(files), files)
	}

	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("reading status file: %v", err)
	}

	var got dashboard.StatusPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshaling status JSON: %v", err)
	}

	if got.Governor.Mode != status.Governor.Mode {
		t.Errorf("Governor.Mode: got %q, want %q", got.Governor.Mode, status.Governor.Mode)
	}
	if got.Governor.QueueIssues != status.Governor.QueueIssues {
		t.Errorf("QueueIssues: got %d, want %d", got.Governor.QueueIssues, status.Governor.QueueIssues)
	}
	if got.Governor.QueuePRs != status.Governor.QueuePRs {
		t.Errorf("QueuePRs: got %d, want %d", got.Governor.QueuePRs, status.Governor.QueuePRs)
	}
}

// ---- Build: latest.json -------------------------------------------------

func TestBuild_WritesLatestJSON(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	status := richStatus()
	if err := b.Build(status); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	latestPath := filepath.Join(dir, "latest.json")
	if _, err := os.Stat(latestPath); err != nil {
		t.Fatalf("latest.json not found: %v", err)
	}
}

func TestBuild_LatestJSONMatchesTimestampedFile(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	if err := b.Build(richStatus()); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	latestData, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err != nil {
		t.Fatalf("reading latest.json: %v", err)
	}

	files := globJSON(t, dir)
	if len(files) != 1 {
		t.Fatalf("expected 1 status-*.json, got %d", len(files))
	}

	tsData, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("reading timestamped file: %v", err)
	}

	if string(latestData) != string(tsData) {
		t.Error("latest.json content differs from timestamped status file")
	}
}

// ---- Build: index.html governor + counts --------------------------------

func TestBuild_IndexHTMLContainsGovernorMode(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	status := richStatus() // governor mode == SURGE
	if err := b.Build(status); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	html, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatalf("reading index.html: %v", err)
	}

	if !strings.Contains(string(html), "SURGE") {
		t.Errorf("index.html does not contain governor mode %q", "SURGE")
	}
}

func TestBuild_IndexHTMLContainsIssuePRCounts(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	status := richStatus() // issues=7, PRs=3
	if err := b.Build(status); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	html, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatalf("reading index.html: %v", err)
	}
	content := string(html)

	if !strings.Contains(content, "7") {
		t.Error("index.html does not contain issue count 7")
	}
	if !strings.Contains(content, "3") {
		t.Error("index.html does not contain PR count 3")
	}
}

// ---- Build: index.html agent names and states ---------------------------

func TestBuild_IndexHTMLContainsAgentNamesAndStates(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	status := richStatus() // agents: scanner(running), fixer(idle)
	if err := b.Build(status); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	html, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatalf("reading index.html: %v", err)
	}
	content := string(html)

	for agentName, agentStatus := range status.Agents {
		if !strings.Contains(content, agentName) {
			t.Errorf("index.html missing agent name %q", agentName)
		}
		if !strings.Contains(content, agentStatus.State) {
			t.Errorf("index.html missing agent state %q for agent %q", agentStatus.State, agentName)
		}
	}
}

// ---- Build: multiple calls create multiple timestamped files ------------

func TestBuild_MultipleCalls_CreateMultipleTimestampedFiles(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	// We need at least 1-second separation because the timestamp format has
	// 1-second resolution. Perform two builds and track distinct filenames
	// without sleeping by capturing them directly.
	const calls = 3
	seen := map[string]struct{}{}

	for i := 0; i < calls; i++ {
		before := globJSON(t, dir)
		if err := b.Build(minimalStatus()); err != nil {
			t.Fatalf("Build() call %d error: %v", i, err)
		}
		after := globJSON(t, dir)

		// Find the newly created file(s).
		beforeSet := map[string]struct{}{}
		for _, f := range before {
			beforeSet[f] = struct{}{}
		}
		for _, f := range after {
			if _, exists := beforeSet[f]; !exists {
				seen[f] = struct{}{}
			}
		}

		// Ensure the timestamp advances by sleeping 1 second between calls,
		// only if not the last call, so subsequent files get distinct names.
		if i < calls-1 {
			time.Sleep(time.Second)
		}
	}

	if len(seen) < calls {
		t.Errorf("expected %d distinct timestamped files, got %d", calls, len(seen))
	}
}

// ---- Build: empty agents map --------------------------------------------

func TestBuild_EmptyAgents_Succeeds(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	status := &dashboard.StatusPayload{
		Governor: governor.State{
			Mode:        governor.ModeQuiet,
			QueueIssues: 0,
			QueuePRs:    0,
		},
		Agents:    map[string]dashboard.AgentStatus{},
		Timestamp: time.Now().UTC(),
	}

	if err := b.Build(status); err != nil {
		t.Fatalf("Build() with empty agents error: %v", err)
	}

	// All three output files must still be produced.
	if _, err := os.Stat(filepath.Join(dir, "latest.json")); err != nil {
		t.Error("latest.json missing for empty agents build")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Error("index.html missing for empty agents build")
	}
	if files := globJSON(t, dir); len(files) != 1 {
		t.Errorf("expected 1 status-*.json, got %d", len(files))
	}
}

// ---- Cleanup: removes old files -----------------------------------------

func TestCleanup_RemovesOldFiles(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	// Write a fake old status file with a modification time in the past.
	oldFile := filepath.Join(dir, "status-old.json")
	if err := os.WriteFile(oldFile, []byte(`{}`), 0644); err != nil {
		t.Fatalf("writing old file: %v", err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldFile, past, past); err != nil {
		t.Fatalf("setting mtime: %v", err)
	}

	maxAge := time.Hour
	if err := b.Cleanup(maxAge); err != nil {
		t.Fatalf("Cleanup() error: %v", err)
	}

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("old file was not removed by Cleanup")
	}
}

// ---- Cleanup: preserves latest.json and index.html ----------------------

func TestCleanup_PreservesLatestJSONAndIndexHTML(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	// Write latest.json and index.html that are also "old".
	protected := []string{"latest.json", "index.html"}
	past := time.Now().Add(-2 * time.Hour)
	for _, name := range protected {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(`placeholder`), 0644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatalf("setting mtime on %s: %v", name, err)
		}
	}

	if err := b.Cleanup(time.Hour); err != nil {
		t.Fatalf("Cleanup() error: %v", err)
	}

	for _, name := range protected {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("protected file %q was removed by Cleanup", name)
		}
	}
}

// ---- Cleanup: preserves recent files ------------------------------------

func TestCleanup_PreservesRecentFiles(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	// Create a file whose mtime is very recent (well within maxAge).
	recentFile := filepath.Join(dir, "status-recent.json")
	if err := os.WriteFile(recentFile, []byte(`{}`), 0644); err != nil {
		t.Fatalf("writing recent file: %v", err)
	}
	// No Chtimes — the default mtime is now, which is within a 1-hour maxAge.

	if err := b.Cleanup(time.Hour); err != nil {
		t.Fatalf("Cleanup() error: %v", err)
	}

	if _, err := os.Stat(recentFile); err != nil {
		t.Error("recent file was incorrectly removed by Cleanup")
	}
}

// ---- Cleanup: does not error on empty directory -------------------------

func TestCleanup_EmptyDirectory_NoError(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	if err := b.Cleanup(time.Hour); err != nil {
		t.Fatalf("Cleanup() on empty dir error: %v", err)
	}
}

// ---- Cleanup: removes old but keeps recent, mixed directory -------------

func TestCleanup_MixedFiles_RemovesOldKeepsRecent(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	past := time.Now().Add(-3 * time.Hour)
	maxAge := time.Hour

	// Two old status files.
	for _, name := range []string{"status-old1.json", "status-old2.json"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(`{}`), 0644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatalf("setting mtime: %v", err)
		}
	}

	// One recent status file.
	recentPath := filepath.Join(dir, "status-recent.json")
	if err := os.WriteFile(recentPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("writing recent file: %v", err)
	}

	if err := b.Cleanup(maxAge); err != nil {
		t.Fatalf("Cleanup() error: %v", err)
	}

	// Old files must be gone.
	for _, name := range []string{"status-old1.json", "status-old2.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("old file %q was not removed", name)
		}
	}

	// Recent file must survive.
	if _, err := os.Stat(recentPath); err != nil {
		t.Error("recent file was incorrectly removed")
	}
}

// ---- Cleanup: skips subdirectories -------------------------------------

func TestCleanup_SkipsSubdirectories(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	// Create a subdirectory; Cleanup must not try to remove or recurse into it.
	subdir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("creating subdir: %v", err)
	}

	if err := b.Cleanup(time.Nanosecond); err != nil {
		t.Fatalf("Cleanup() error: %v", err)
	}

	if _, err := os.Stat(subdir); err != nil {
		t.Error("subdirectory was removed by Cleanup")
	}
}

// ---- buildIndexHTML: content checks ------------------------------------

func TestBuildIndexHTML_ContainsTimestamp(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	const ts = "2026-05-11T12-00-00Z"
	status := &dashboard.StatusPayload{
		Governor: governor.State{Mode: governor.ModeBusy, QueueIssues: 2, QueuePRs: 1},
		Agents:   map[string]dashboard.AgentStatus{},
	}
	indexPath := filepath.Join(dir, "index.html")
	if err := b.buildIndexHTML(indexPath, status, ts); err != nil {
		t.Fatalf("buildIndexHTML() error: %v", err)
	}

	html, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("reading index.html: %v", err)
	}
	if !strings.Contains(string(html), ts) {
		t.Errorf("index.html does not contain timestamp %q", ts)
	}
}

func TestBuildIndexHTML_AllGovernorModes(t *testing.T) {
	modes := []governor.Mode{
		governor.ModeSurge,
		governor.ModeBusy,
		governor.ModeQuiet,
		governor.ModeIdle,
	}

	for _, mode := range modes {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			dir := t.TempDir()
			b := NewBuilder(dir, discardLogger())

			status := &dashboard.StatusPayload{
				Governor: governor.State{Mode: mode},
				Agents:   map[string]dashboard.AgentStatus{},
			}
			indexPath := filepath.Join(dir, "index.html")
			if err := b.buildIndexHTML(indexPath, status, "ts"); err != nil {
				t.Fatalf("buildIndexHTML() error: %v", err)
			}

			html, err := os.ReadFile(indexPath)
			if err != nil {
				t.Fatalf("reading index.html: %v", err)
			}
			if !strings.Contains(string(html), string(mode)) {
				t.Errorf("index.html does not contain mode %q", mode)
			}
		})
	}
}

// ---- Build: error paths -------------------------------------------------

func TestBuild_ErrorWhenOutputDirIsFile(t *testing.T) {
	// Make the "output dir" path actually a regular file — MkdirAll will fail.
	base := t.TempDir()
	blocker := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("writing blocker file: %v", err)
	}
	// Attempt to use a child of the blocker file as outputDir: the kernel
	// will refuse MkdirAll because a component of the path is a regular file.
	b := NewBuilder(filepath.Join(blocker, "subdir"), discardLogger())
	if err := b.Build(minimalStatus()); err == nil {
		t.Error("expected error when outputDir cannot be created, got nil")
	}
}

func TestBuild_ErrorWhenStatusFileNotWritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write to read-only dirs; skipping")
	}
	dir := t.TempDir()
	// Make the output directory read-only so WriteFile fails.
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(dir, 0755) // restore so TempDir cleanup works

	b := NewBuilder(dir, discardLogger())
	if err := b.Build(minimalStatus()); err == nil {
		t.Error("expected error writing status file to read-only dir, got nil")
	}
}

// TestBuild_ErrorWhenLatestJSONIsDir simulates the os.WriteFile(latestPath, ...)
// failure by pre-creating a directory named "latest.json" inside the output dir.
// The first WriteFile (timestamped file) succeeds because the timestamped name is
// different; the second WriteFile to "latest.json" fails because it's a directory.
func TestBuild_ErrorWhenLatestJSONIsDir(t *testing.T) {
	dir := t.TempDir()

	// Create a directory named "latest.json" — WriteFile will fail on it.
	blocker := filepath.Join(dir, "latest.json")
	if err := os.MkdirAll(blocker, 0755); err != nil {
		t.Fatalf("creating blocker dir: %v", err)
	}

	b := NewBuilder(dir, discardLogger())
	err := b.Build(minimalStatus())
	if err == nil {
		t.Error("expected error when latest.json is a directory, got nil")
	}
}

// TestBuild_ErrorWhenIndexHTMLIsDir simulates the buildIndexHTML failure by
// pre-creating a directory named "index.html" inside the output dir.
// The status file and latest.json writes succeed; index.html write fails.
func TestBuild_ErrorWhenIndexHTMLIsDir(t *testing.T) {
	dir := t.TempDir()

	// Create a directory named "index.html" — WriteFile will fail on it.
	blocker := filepath.Join(dir, "index.html")
	if err := os.MkdirAll(blocker, 0755); err != nil {
		t.Fatalf("creating blocker dir: %v", err)
	}

	b := NewBuilder(dir, discardLogger())
	err := b.Build(minimalStatus())
	if err == nil {
		t.Error("expected error when index.html is a directory, got nil")
	}
}

func TestCleanup_ErrorOnUnreadableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read any dir; skipping")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(dir, 0755)

	b := NewBuilder(dir, discardLogger())
	if err := b.Cleanup(time.Hour); err == nil {
		t.Error("expected error when ReadDir fails on unreadable dir, got nil")
	}
}

// ---- Build: latest.json updated on second call --------------------------

func TestBuild_SecondCall_UpdatesLatestJSON(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(dir, discardLogger())

	// First build with issues=1.
	s1 := &dashboard.StatusPayload{
		Governor: governor.State{Mode: governor.ModeIdle, QueueIssues: 1},
		Agents:   map[string]dashboard.AgentStatus{},
		Timestamp: time.Now().UTC(),
	}
	if err := b.Build(s1); err != nil {
		t.Fatalf("first Build() error: %v", err)
	}

	time.Sleep(time.Second) // ensure distinct timestamp

	// Second build with issues=99.
	s2 := &dashboard.StatusPayload{
		Governor: governor.State{Mode: governor.ModeSurge, QueueIssues: 99},
		Agents:   map[string]dashboard.AgentStatus{},
		Timestamp: time.Now().UTC(),
	}
	if err := b.Build(s2); err != nil {
		t.Fatalf("second Build() error: %v", err)
	}

	latestData, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err != nil {
		t.Fatalf("reading latest.json: %v", err)
	}

	var got dashboard.StatusPayload
	if err := json.Unmarshal(latestData, &got); err != nil {
		t.Fatalf("unmarshaling latest.json: %v", err)
	}

	if got.Governor.QueueIssues != 99 {
		t.Errorf("latest.json QueueIssues: got %d, want 99", got.Governor.QueueIssues)
	}
}
