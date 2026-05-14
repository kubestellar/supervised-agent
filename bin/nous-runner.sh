#!/usr/bin/env bash
# nous-runner.sh — Strategist helper: gathers Hive context for experiment design.
#
# Two execution modes:
#   CLI mode (default): Collects context and prints a structured briefing to stdout.
#     The calling CLI agent (strategist) reasons about the data and writes outputs.
#   API mode: Delegates to run_campaign.py if NOUS_USE_API=true and the script exists.
#
# Outputs written by the CLI agent after reading the briefing:
#   observe  → dry-run analysis to ledger only
#   suggest  → proposal JSON + gate decision posted to dashboard
#   evolve   → governor overlay written to $OVERLAY_PATH
set -euo pipefail

NOUS_DIR="${NOUS_DIR:-/opt/nous}"
NOUS_PYTHON="${NOUS_DIR}/venv/bin/python3"
NOUS_RUN_DIR="${NOUS_RUN_DIR:-/var/run/nous}"
CAMPAIGN_PATH="${NOUS_CAMPAIGN_PATH:-/etc/hive/nous-campaign.yaml}"
HIVE_METRICS_DIR="${HIVE_METRICS_DIR:-/var/run/hive-metrics}"
GOV_STATE_DIR="${GOV_STATE_DIR:-/var/run/kick-governor}"
HIVE_CONFIG_DIR="${HIVE_CONFIG_DIR:-/etc/hive}"
OVERLAY_PATH="$HIVE_CONFIG_DIR/governor-experiment.env"
LAST_SCOPE_FILE="$NOUS_RUN_DIR/last-scope"
GATE_SCRIPT="$(dirname "$0")/nous-hive-gate.py"
CAMPAIGN_DIR="$(dirname "$CAMPAIGN_PATH")"

NOUS_USE_API="${NOUS_USE_API:-false}"

read_yaml_field() {
  python3 -c "
import yaml, sys
with open('$1') as f:
    d = yaml.safe_load(f)
keys = '$2'.split('.')
for k in keys:
    d = (d or {}).get(k, '')
print(d or '')
"
}

MODE=$(read_yaml_field "$CAMPAIGN_PATH" "campaign.mode")
SCOPE=$(read_yaml_field "$CAMPAIGN_PATH" "campaign.scope")
SCOPE="${SCOPE:-governor}"
MODE="${MODE:-observe}"

echo "[nous-runner] scope=$SCOPE mode=$MODE"

resolve_scope() {
  local scope="$1"
  if [ "$scope" = "both" ]; then
    local last
    last=$(cat "$LAST_SCOPE_FILE" 2>/dev/null || echo "repo")
    if [ "$last" = "governor" ]; then
      echo "repo"
    else
      echo "governor"
    fi
  else
    echo "$scope"
  fi
}

EFFECTIVE_SCOPE=$(resolve_scope "$SCOPE")
echo "$EFFECTIVE_SCOPE" > "$LAST_SCOPE_FILE"
echo "[nous-runner] effective scope=$EFFECTIVE_SCOPE"

EFFECTIVE_MODE="$MODE"
if [ "$EFFECTIVE_SCOPE" = "repo" ]; then
  EFFECTIVE_MODE="suggest"
  echo "[nous-runner] repo scope forces suggest mode"
fi

WORK_DIR="$NOUS_RUN_DIR/$EFFECTIVE_SCOPE"
mkdir -p "$WORK_DIR"

# --- Build hive context (shared by both modes) ---
build_hive_context() {
  python3 - "$WORK_DIR/hive-context.json" <<'PYEOF'
import json, sys, os, glob

out_path = sys.argv[1]
metrics_dir = os.environ.get("HIVE_METRICS_DIR", "/var/run/hive-metrics")
gov_dir = os.environ.get("GOV_STATE_DIR", "/var/run/kick-governor")
nous_dir = os.environ.get("NOUS_RUN_DIR", "/var/run/nous")

def read_file(p):
    try:
        with open(p) as f:
            return f.read().strip()
    except Exception:
        return None

def read_json(p):
    try:
        with open(p) as f:
            return json.load(f)
    except Exception:
        return None

def read_jsonl_tail(p, n=10):
    try:
        with open(p) as f:
            lines = f.readlines()
        return [json.loads(l) for l in lines[-n:] if l.strip()]
    except Exception:
        return []

snapshots_dir = os.path.join(nous_dir, "snapshots")
snapshot_files = sorted(glob.glob(os.path.join(snapshots_dir, "*.json")))[-20:]
snapshots = []
for sf in snapshot_files:
    s = read_json(sf)
    if s:
        snapshots.append(s)

ctx = {
    "governor_mode": read_file(os.path.join(gov_dir, "mode")),
    "queue_depth": read_file(os.path.join(gov_dir, "queue_depth")),
    "regime": read_file(os.path.join(gov_dir, "regime")),
    "mttr": read_json(os.path.join(metrics_dir, "issue_to_merge.json")),
    "tokens": read_json(os.path.join(metrics_dir, "tokens.json")),
    "kick_outcomes": read_jsonl_tail(os.path.join(metrics_dir, "kick-outcomes.jsonl")),
    "principles": read_json(os.path.join(nous_dir, "principles.json")) or [],
    "ledger_recent": read_jsonl_tail(os.path.join(nous_dir, "ledger.jsonl")),
    "recent_snapshots": snapshots,
    "recommendations": read_json(os.path.join(nous_dir, "recommendations.json")),
}

with open(out_path, "w") as f:
    json.dump(ctx, f, indent=2)
print(f"[nous-runner] wrote hive context to {out_path}", file=sys.stderr)
PYEOF
}

select_campaign_config() {
  local scope="$1"
  if [ "$scope" = "governor" ]; then
    echo "$CAMPAIGN_DIR/nous-governor-campaign.yaml"
  elif [ "$scope" = "repo" ]; then
    echo "$CAMPAIGN_DIR/nous-repo-campaign.yaml"
  else
    echo "$CAMPAIGN_DIR/nous-governor-campaign.yaml"
  fi
}

CAMPAIGN_CONFIG=$(select_campaign_config "$EFFECTIVE_SCOPE")
if [ ! -f "$CAMPAIGN_CONFIG" ]; then
  echo "[nous-runner] ERROR: campaign config not found: $CAMPAIGN_CONFIG"
  exit 1
fi

build_hive_context

# --- Decide execution mode: API or CLI ---
USE_API="false"
if [ "$NOUS_USE_API" = "true" ] && [ -x "$NOUS_PYTHON" ] && [ -f "$NOUS_DIR/run_campaign.py" ]; then
  USE_API="true"
fi

if [ "$USE_API" = "true" ]; then
  # ========== API MODE: delegate to run_campaign.py ==========
  echo "[nous-runner] using API mode (run_campaign.py)"

  AUTO_APPROVE="false"
  if [ "$EFFECTIVE_MODE" = "evolve" ] || [ "$EFFECTIVE_MODE" = "observe" ]; then
    AUTO_APPROVE="true"
  fi

  NOUS_HIVE_MODE="$EFFECTIVE_MODE" \
  NOUS_HIVE_SCOPE="$EFFECTIVE_SCOPE" \
  NOUS_GATE_SCRIPT="$GATE_SCRIPT" \
  "$NOUS_PYTHON" "$NOUS_DIR/run_campaign.py" \
    --campaign "$CAMPAIGN_CONFIG" \
    --work-dir "$WORK_DIR" \
    --max-iterations 1 \
    --timeout 1800 \
    --auto-approve "$AUTO_APPROVE" \
    --context-file "$WORK_DIR/hive-context.json" \
    2>&1 || {
      echo "[nous-runner] run_campaign.py exited with $?"
    }

  # Translate overlay for evolve mode
  if [ "$EFFECTIVE_SCOPE" = "governor" ] && [ "$EFFECTIVE_MODE" = "evolve" ]; then
    if [ -f "$WORK_DIR/state.json" ]; then
      echo "[nous-runner] translating Nous output to governor overlay"
      python3 - "$WORK_DIR" "$OVERLAY_PATH" "$CAMPAIGN_PATH" <<'PYEOF'
import json, sys, os, time, yaml

work_dir = sys.argv[1]
overlay_path = sys.argv[2]
campaign_path = sys.argv[3]

state = {}
try:
    with open(os.path.join(work_dir, "state.json")) as f:
        state = json.load(f)
except Exception:
    print("[nous-runner] no state.json found, skipping overlay")
    sys.exit(0)

phase = state.get("phase", "")
if phase not in ("EXTRACTION", "ANALYSIS"):
    print(f"[nous-runner] phase is {phase}, not ready for overlay")
    sys.exit(0)

with open(campaign_path) as f:
    campaign = yaml.safe_load(f)
controllables = {c["name"] for c in (campaign.get("campaign", {}).get("controllables", []))}

plan = state.get("plan", {})
treatments = {}
for arm in plan.get("arms", []):
    for condition in arm.get("conditions", []):
        var = condition.get("variable", "")
        val = condition.get("value", "")
        if var in controllables and val:
            treatments[var] = val

if not treatments:
    print("[nous-runner] no actionable treatments found")
    sys.exit(0)

if os.path.exists(overlay_path):
    print("[nous-runner] overlay already exists, skipping")
    sys.exit(0)

exp_id = f"nous-{int(time.time())}"
now_epoch = int(time.time())
ttl = campaign.get("campaign", {}).get("schedule", {}).get("experiment_duration_hours", 4) * 3600
fast_fail = campaign.get("campaign", {}).get("fast_fail", {})

lines = [
    "# Nous experiment overlay — generated by nous-runner.sh (API mode)",
    f"NOUS_EXPERIMENT_ID={exp_id}",
    f"NOUS_EXPERIMENT_START={now_epoch}",
    f"NOUS_EXPERIMENT_TTL_SEC={ttl}",
    f"NOUS_FAST_FAIL_QUEUE_MAX={fast_fail.get('queue_depth_max', 30)}",
    f"NOUS_FAST_FAIL_MTTR_MAX={fast_fail.get('mttr_max_minutes', 180)}",
]
for var, val in treatments.items():
    lines.append(f"{var}={val}")

with open(overlay_path, "w") as f:
    f.write("\n".join(lines) + "\n")
print(f"[nous-runner] wrote overlay: {overlay_path}")
print(f"[nous-runner] treatments: {treatments}")
PYEOF
    fi
  fi

else
  # ========== CLI MODE: emit briefing for the agent to reason about ==========
  echo "[nous-runner] using CLI mode (agent-as-LLM)"

  # Print the campaign config so the agent knows the research question and knobs
  echo ""
  echo "=== CAMPAIGN CONFIG ==="
  cat "$CAMPAIGN_CONFIG"

  # Print current hive context
  echo ""
  echo "=== HIVE CONTEXT ==="
  cat "$WORK_DIR/hive-context.json"

  # Print existing overlay status
  echo ""
  echo "=== OVERLAY STATUS ==="
  if [ -f "$OVERLAY_PATH" ]; then
    echo "ACTIVE EXPERIMENT:"
    cat "$OVERLAY_PATH"
  else
    echo "No active experiment overlay."
  fi

  # Print existing principles
  echo ""
  echo "=== PRINCIPLES ==="
  cat "$NOUS_RUN_DIR/principles.json" 2>/dev/null || echo "[]"

  # Print existing recommendations
  echo ""
  echo "=== RECOMMENDATIONS ==="
  cat "$NOUS_RUN_DIR/recommendations.json" 2>/dev/null || echo "No recommendations yet."

  # Print instructions based on mode
  echo ""
  echo "=== YOUR TASK (mode=$EFFECTIVE_MODE, scope=$EFFECTIVE_SCOPE) ==="
  case "$EFFECTIVE_MODE" in
    observe)
      cat <<'INSTRUCTIONS'
MODE: OBSERVE — Collect and analyze only. Do NOT propose or apply changes.

1. Analyze the hive context above (snapshots, metrics, kick outcomes).
2. Identify patterns: Which agents are most/least effective? Where are tokens wasted?
   What correlates with low MTTR? Are there regime transitions that hurt performance?
3. If you discover a principle (a reusable insight), write it to principles.json:
   python3 -c "
   import json, os, time
   path = '/var/run/nous/principles.json'
   principles = json.load(open(path)) if os.path.exists(path) else []
   principles.append({
       'id': 'principle-' + str(int(time.time())),
       'text': 'YOUR PRINCIPLE TEXT HERE',
       'confidence': 0.5,
       'source': 'observation',
       'evidence': 'brief evidence summary'
   })
   json.dump(principles, open(path, 'w'), indent=2)
   "
4. Log your analysis summary to the ledger (done automatically below).
5. Do NOT write to governor-experiment.env or propose parameter changes.
INSTRUCTIONS
      ;;
    suggest)
      cat <<'INSTRUCTIONS'
MODE: SUGGEST — Propose an experiment for human approval.

1. Review the hive context, principles, and recent ledger entries above.
2. If baseline data is insufficient (<672 snapshots), say so and stay in observe behavior.
3. If you have enough data, design ONE experiment:
   - State a falsifiable hypothesis
   - Pick controllable knobs from the campaign config
   - Define success/failure criteria with specific thresholds
4. Write the proposal to /var/run/nous/pending-experiment.json:
   python3 -c "
   import json, time
   proposal = {
       'id': 'exp-' + str(int(time.time())),
       'hypothesis': 'YOUR HYPOTHESIS',
       'changes': {'KNOB_NAME': 'new_value'},
       'success_criteria': 'metric > threshold for N hours',
       'failure_criteria': 'metric > max_threshold',
       'duration_hours': 4,
       'timestamp': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
   }
   json.dump(proposal, open('/var/run/nous/pending-experiment.json', 'w'), indent=2)
   "
5. Post to dashboard gate for human approval:
   python3 /tmp/hive/bin/nous-hive-gate.py 'Should we run experiment: YOUR_HYPOTHESIS?' /var/run/nous/pending-experiment.json
6. Do NOT write to governor-experiment.env directly.
INSTRUCTIONS
      ;;
    evolve)
      cat <<'INSTRUCTIONS'
MODE: EVOLVE — Design and apply an experiment autonomously.

1. Review the hive context, principles, and recent ledger entries above.
2. Check if an experiment is already active (overlay status above). If yes, evaluate it:
   - Has the TTL expired? Check NOUS_EXPERIMENT_START + NOUS_EXPERIMENT_TTL_SEC vs now.
   - Are fast-fail thresholds violated? If yes, remove the overlay and log failure.
   - Are success criteria met? If yes, log success, promote to principle, remove overlay.
3. If no active experiment, design ONE using the campaign config controllables.
4. Write the overlay to apply it:
   python3 -c "
   import time
   exp_id = 'nous-' + str(int(time.time()))
   lines = [
       '# Nous experiment overlay — generated by strategist (CLI mode)',
       f'NOUS_EXPERIMENT_ID={exp_id}',
       f'NOUS_EXPERIMENT_START={int(time.time())}',
       'NOUS_EXPERIMENT_TTL_SEC=14400',
       'NOUS_FAST_FAIL_QUEUE_MAX=30',
       'NOUS_FAST_FAIL_MTTR_MAX=180',
       # Add your treatment knobs here:
       # 'CADENCE_REVIEWER_QUIET_SEC=1800',
   ]
   with open('/etc/hive/governor-experiment.env', 'w') as f:
       f.write('\n'.join(lines) + '\n')
   "
5. Log the experiment to the ledger (done automatically below).
INSTRUCTIONS
      ;;
  esac
fi

# --- Log to ledger (both modes) ---
python3 - "$EFFECTIVE_SCOPE" "$EFFECTIVE_MODE" "$USE_API" <<'PYEOF'
import json, sys, time

scope = sys.argv[1]
mode = sys.argv[2]
backend = "api" if sys.argv[3] == "true" else "cli"
nous_dir = "/var/run/nous"
entry = {
    "ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    "scope": scope,
    "mode": mode,
    "backend": backend,
    "type": "nous_kick",
}
try:
    with open(f"{nous_dir}/ledger.jsonl", "a") as f:
        f.write(json.dumps(entry) + "\n")
except Exception:
    pass
PYEOF

echo "[nous-runner] done (scope=$EFFECTIVE_SCOPE, mode=$EFFECTIVE_MODE, backend=$([ "$USE_API" = "true" ] && echo "api" || echo "cli"))"
