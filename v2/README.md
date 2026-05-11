# Hive v2

Single Go binary that replaces ~34 shell scripts, the Node.js dashboard, and the external `bd` binary. Run it in a container against your own GitHub repos.

## Quick Start

```bash
# 1. Create a config file (see hive.yaml.example for all options)
cp hive.yaml.example hive.yaml
# Edit hive.yaml with your org, repos, and agent config

# 2. Set your GitHub token
export HIVE_GITHUB_TOKEN=ghp_...

# 3. Run with Docker
docker compose up -d

# 4. Open the dashboard
open http://localhost:3001
```

## Run Without Docker

```bash
# Build
go build -o hive ./cmd/hive

# Run
./hive --config ./hive.yaml
```

## What It Does

Hive is an AI agent orchestrator for GitHub repositories. It:

- **Enumerates** open issues and PRs across your repos via the GitHub API
- **Classifies** issues by complexity (Simple/Medium/Complex) and routes to the right AI model
- **Governs** agent cadence adaptively — SURGE/BUSY/QUIET/IDLE modes based on queue depth
- **Dispatches** work to AI agents (Claude, Copilot, Gemini, Goose) as child processes
- **Tracks** work items in an embedded ledger (beads)
- **Notifies** via ntfy, Slack, or Discord on SLA violations
- **Serves** a live dashboard with SSE updates at `:3001`

## Configuration

All config lives in a single `hive.yaml` file. Environment variables are interpolated via `${VAR}` syntax. See `hive.yaml.example` for the full reference.

Agent policies (CLAUDE.md files) can be managed via a git config repo that's cloned on startup and polled for changes.

## Architecture

```
cmd/hive/main.go          Entry point — wires all packages, runs governor loop
pkg/config/               Single YAML config with env var interpolation
pkg/governor/             Adaptive scheduler (SURGE/BUSY/QUIET/IDLE modes)
pkg/scheduler/            Builds kick messages per agent from classified work
pkg/github/               GitHub API client — issues, PRs, hold/exempt filtering
pkg/classify/             Issue classifier — tier, model recommendation, lane
pkg/beads/                Embedded work item ledger (JSON-backed CRUD)
pkg/agent/                Process manager — spawns CLI agents as child processes
pkg/dashboard/            HTTP + SSE server with embedded static UI
pkg/tokens/               Session file parser for token usage tracking
pkg/notify/               Pluggable notifications (ntfy, Slack, Discord)
pkg/policies/             Config repo watcher — git clone + poll + hot reload
pkg/discord/              Discord bot (optional, responds to !hive commands)
pkg/snapshot/             Static dashboard snapshot builder
```

## Container Volumes

| Mount | Purpose |
|---|---|
| `/etc/hive/hive.yaml` | Config file (read-only) |
| `/data` | Metrics, beads, logs, snapshots (persistent) |
| `/secrets` | GitHub App key, API tokens (read-only) |

## Agent CLIs

The container needs the agent CLIs installed. Options:
1. Multi-stage Docker build that includes them
2. Volume-mount from host
3. Sidecar containers per agent
