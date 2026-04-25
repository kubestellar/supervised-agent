#!/usr/bin/env python3
"""Token usage aggregator for Hive agents.

Scans Claude CLI and Copilot CLI session files to produce
per-agent, per-window token usage summaries.

Output: JSON with per-agent totals and burn-rate estimates.
"""

import json
import os
import glob
import sys
from datetime import datetime, timezone, timedelta

# --- Config ---
CLAUDE_SESSIONS = os.path.expanduser(
    "~/.claude/projects/-home-dev-kubestellar-console"
)
COPILOT_SESSIONS = os.path.expanduser("~/.copilot/session-state")

# Time windows
NOW = datetime.now(timezone.utc)
WINDOWS = {
    "1h": NOW - timedelta(hours=1),
    "5h": NOW - timedelta(hours=5),   # rolling session window
    "24h": NOW - timedelta(hours=24),
    "7d": NOW - timedelta(days=7),    # rolling weekly cap
    "30d": NOW - timedelta(days=30),
}

# Claude Max 20x ($200/mo) limits — rolling windows, not fixed resets
# 5-hour rolling session window: ~200-800 messages (model-dependent)
# 7-day rolling weekly cap: ~240-480 "compute hours"
# Token equivalents (Opus 4.6, estimated from observed usage):
#   5h window: ~1.5M output tokens (conservative for Opus)
#   7d weekly: ~25M output tokens (conservative for Opus)
# Adjust these if you observe actual throttling — they'll be too low or high
FIVE_HOUR_OUTPUT_LIMIT = 1_500_000   # 5h rolling window
WEEKLY_OUTPUT_LIMIT = 25_000_000      # 7d rolling cap


def parse_iso(ts_str):
    """Parse ISO timestamp string to datetime."""
    try:
        # Handle both 'Z' suffix and '+00:00'
        ts_str = ts_str.replace("Z", "+00:00")
        return datetime.fromisoformat(ts_str)
    except (ValueError, AttributeError):
        return None


def scan_claude_sessions():
    """Scan Claude CLI session JSONL files for token usage."""
    results = []
    pattern = os.path.join(CLAUDE_SESSIONS, "*.jsonl")
    for fpath in glob.glob(pattern):
        # Skip files not modified in last 30 days
        mtime = os.path.getmtime(fpath)
        if NOW.timestamp() - mtime > 30 * 86400:
            continue
        try:
            with open(fpath) as f:
                for line in f:
                    try:
                        d = json.loads(line)
                        if d.get("type") != "assistant":
                            continue
                        ts = parse_iso(d.get("timestamp", ""))
                        if not ts:
                            continue
                        u = d.get("message", {}).get("usage", {})
                        if not u:
                            continue
                        results.append({
                            "ts": ts,
                            "source": "claude",
                            "model": d.get("message", {}).get("model", "?"),
                            "input": u.get("input_tokens", 0),
                            "output": u.get("output_tokens", 0),
                            "cache_read": u.get("cache_read_input_tokens", 0),
                            "cache_write": u.get("cache_creation_input_tokens", 0),
                        })
                    except json.JSONDecodeError:
                        continue
        except (IOError, OSError):
            continue

    # Also scan subagent files
    sub_pattern = os.path.join(CLAUDE_SESSIONS, "*/subagents/*.jsonl")
    for fpath in glob.glob(sub_pattern):
        mtime = os.path.getmtime(fpath)
        if NOW.timestamp() - mtime > 30 * 86400:
            continue
        try:
            with open(fpath) as f:
                for line in f:
                    try:
                        d = json.loads(line)
                        if d.get("type") != "assistant":
                            continue
                        ts = parse_iso(d.get("timestamp", ""))
                        if not ts:
                            continue
                        u = d.get("message", {}).get("usage", {})
                        if not u:
                            continue
                        results.append({
                            "ts": ts,
                            "source": "claude-sub",
                            "model": d.get("message", {}).get("model", "?"),
                            "input": u.get("input_tokens", 0),
                            "output": u.get("output_tokens", 0),
                            "cache_read": u.get("cache_read_input_tokens", 0),
                            "cache_write": u.get("cache_creation_input_tokens", 0),
                        })
                    except json.JSONDecodeError:
                        continue
        except (IOError, OSError):
            continue

    return results


def scan_copilot_sessions():
    """Scan Copilot CLI session event files for token usage."""
    results = []
    pattern = os.path.join(COPILOT_SESSIONS, "*/events.jsonl")
    for fpath in glob.glob(pattern):
        mtime = os.path.getmtime(fpath)
        if NOW.timestamp() - mtime > 30 * 86400:
            continue
        try:
            with open(fpath) as f:
                for line in f:
                    try:
                        d = json.loads(line)
                        if d.get("type") != "assistant.message":
                            continue
                        ts = parse_iso(d.get("timestamp", ""))
                        if not ts:
                            continue
                        out_tokens = d.get("data", {}).get("outputTokens", 0)
                        if not out_tokens:
                            continue
                        results.append({
                            "ts": ts,
                            "source": "copilot",
                            "model": "copilot",
                            "input": 0,  # Copilot doesn't expose input tokens
                            "output": out_tokens,
                            "cache_read": 0,
                            "cache_write": 0,
                        })
                    except json.JSONDecodeError:
                        continue
        except (IOError, OSError):
            continue
    return results


def aggregate(records):
    """Aggregate token usage by time window."""
    windows = {}
    for name, cutoff in WINDOWS.items():
        subset = [r for r in records if r["ts"] >= cutoff]
        total_in = sum(r["input"] for r in subset)
        total_out = sum(r["output"] for r in subset)
        total_cache_r = sum(r["cache_read"] for r in subset)
        total_cache_w = sum(r["cache_write"] for r in subset)
        messages = len(subset)

        # Model breakdown
        models = {}
        for r in subset:
            m = r["model"]
            if m not in models:
                models[m] = {"input": 0, "output": 0, "messages": 0}
            models[m]["input"] += r["input"]
            models[m]["output"] += r["output"]
            models[m]["messages"] += 1

        windows[name] = {
            "input_tokens": total_in,
            "output_tokens": total_out,
            "cache_read_tokens": total_cache_r,
            "cache_write_tokens": total_cache_w,
            "total_tokens": total_in + total_out + total_cache_r + total_cache_w,
            "messages": messages,
            "models": models,
        }

    return windows


def compute_burn_rate(windows):
    """Compute burn rate and time-to-limit estimates using rolling windows."""
    w5h = windows.get("5h", {})
    w7d = windows.get("7d", {})
    w1h = windows.get("1h", {})

    out_5h = w5h.get("output_tokens", 0)
    out_7d = w7d.get("output_tokens", 0)
    out_1h = w1h.get("output_tokens", 0)

    # Hourly burn rate (based on last hour)
    hourly_rate = out_1h

    # 5h rolling window usage
    session_pct = round(out_5h / FIVE_HOUR_OUTPUT_LIMIT * 100, 1) if FIVE_HOUR_OUTPUT_LIMIT else 0
    session_remaining = FIVE_HOUR_OUTPUT_LIMIT - out_5h
    session_hours_left = round(session_remaining / hourly_rate, 1) if hourly_rate > 0 else 999

    # 7d rolling weekly cap usage
    weekly_pct = round(out_7d / WEEKLY_OUTPUT_LIMIT * 100, 1) if WEEKLY_OUTPUT_LIMIT else 0
    weekly_remaining = WEEKLY_OUTPUT_LIMIT - out_7d
    weekly_hours_left = round(weekly_remaining / hourly_rate, 1) if hourly_rate > 0 else 999

    # Risk level — worst of session and weekly
    max_pct = max(session_pct, weekly_pct)
    if max_pct >= 90:
        risk = "CRITICAL"
    elif max_pct >= 70:
        risk = "HIGH"
    elif max_pct >= 50:
        risk = "MODERATE"
    else:
        risk = "LOW"

    return {
        "hourly_burn_rate": hourly_rate,
        "session_5h": {
            "used": out_5h,
            "limit": FIVE_HOUR_OUTPUT_LIMIT,
            "pct": session_pct,
            "hours_left": session_hours_left,
        },
        "weekly_7d": {
            "used": out_7d,
            "limit": WEEKLY_OUTPUT_LIMIT,
            "pct": weekly_pct,
            "hours_left": weekly_hours_left,
        },
        "risk": risk,
    }


def format_tokens(n):
    """Human-readable token count."""
    if n >= 1_000_000:
        return f"{n / 1_000_000:.1f}M"
    if n >= 1_000:
        return f"{n / 1_000:.1f}K"
    return str(n)


def main():
    records = scan_claude_sessions() + scan_copilot_sessions()
    windows = aggregate(records)
    burn = compute_burn_rate(windows)

    output = {
        "timestamp": NOW.isoformat(),
        "windows": windows,
        "burn_rate": burn,
    }

    if "--json" in sys.argv:
        print(json.dumps(output, indent=2, default=str))
    else:
        # Human-readable dashboard
        print("═══ TOKEN USAGE (Max 20x) ═══")
        print()
        for name in ["1h", "5h", "24h", "7d", "30d"]:
            w = windows.get(name, {})
            out = format_tokens(w.get("output_tokens", 0))
            inp = format_tokens(w.get("input_tokens", 0))
            cache = format_tokens(w.get("cache_read_tokens", 0))
            msgs = w.get("messages", 0)
            print(f"  {name:>4s}:  out={out:>7s}  in={inp:>7s}  cache={cache:>7s}  msgs={msgs}")

        print()
        risk_colors = {
            "LOW": "🟢", "MODERATE": "🟡", "HIGH": "🟠", "CRITICAL": "🔴"
        }
        icon = risk_colors.get(burn["risk"], "⚪")
        s = burn["session_5h"]
        wk = burn["weekly_7d"]
        print(f"  Burn rate:    {format_tokens(burn['hourly_burn_rate'])}/hr")
        print(f"  5h window:   {format_tokens(s['used'])} / {format_tokens(s['limit'])} ({s['pct']}%) — {s['hours_left']}h left")
        print(f"  7d weekly:   {format_tokens(wk['used'])} / {format_tokens(wk['limit'])} ({wk['pct']}%) — {wk['hours_left']}h left")
        print(f"  Risk:        {icon} {burn['risk']}")


if __name__ == "__main__":
    main()
