#!/usr/bin/env python3
"""
nous-sync.py — Analyst helper: reads Nous output, syncs to dashboard

Reads state.json, findings, and principles from both governor and repo
Nous work directories. Applies hive-specific enrichment:
  - Confidence decay (5% per week since last_validated)
  - Regime tagging (quiet/busy/surge applicability)
  - Cross-scope conflict detection
  - Recommendation generation (5+ principles at >0.8 confidence)
"""

import json
import os
import sys
import time
from datetime import datetime, timezone

NOUS_RUN_DIR = os.environ.get("NOUS_RUN_DIR", "/var/run/nous")
HIVE_METRICS_DIR = os.environ.get("HIVE_METRICS_DIR", "/var/run/hive-metrics")
GOV_STATE_DIR = os.environ.get("GOV_STATE_DIR", "/var/run/kick-governor")

PRINCIPLES_PATH = os.path.join(NOUS_RUN_DIR, "principles.json")
RECOMMENDATIONS_PATH = os.path.join(NOUS_RUN_DIR, "recommendations.json")
LEDGER_PATH = os.path.join(NOUS_RUN_DIR, "ledger.jsonl")

DECAY_RATE_PER_WEEK = 0.05
ARCHIVE_THRESHOLD = 0.2
RECOMMENDATION_THRESHOLD = 0.8
MIN_PRINCIPLES_FOR_RECOMMENDATION = 5
SECONDS_PER_WEEK = 604800


def read_json(path):
    try:
        with open(path) as f:
            return json.load(f)
    except Exception:
        return None


def write_json(path, data):
    with open(path, "w") as f:
        json.dump(data, f, indent=2)


def read_nous_state(scope):
    work_dir = os.path.join(NOUS_RUN_DIR, scope)
    state = read_json(os.path.join(work_dir, "state.json"))
    findings = read_json(os.path.join(work_dir, "findings.json"))
    nous_principles = read_json(os.path.join(work_dir, "principles.json"))
    return state, findings, nous_principles


def get_current_regime():
    try:
        with open(os.path.join(GOV_STATE_DIR, "regime")) as f:
            return f.read().strip()
    except Exception:
        return "unknown"


def apply_confidence_decay(principles):
    now = time.time()
    active = []
    archived = []

    for p in principles:
        last_validated = p.get("last_validated", p.get("created", ""))
        if last_validated:
            try:
                dt = datetime.fromisoformat(last_validated.replace("Z", "+00:00"))
                age_sec = now - dt.timestamp()
            except Exception:
                age_sec = 0
        else:
            age_sec = 0

        weeks = age_sec / SECONDS_PER_WEEK
        decay = DECAY_RATE_PER_WEEK * weeks
        original = p.get("confidence", 0.5)
        decayed = max(0.0, original - decay)
        p["confidence"] = round(decayed, 4)
        p["decay_applied"] = round(decay, 4)

        if decayed < ARCHIVE_THRESHOLD:
            p["status"] = "archived"
            archived.append(p)
        else:
            active.append(p)

    return active, archived


def merge_nous_principles(existing, nous_new, scope):
    if not nous_new:
        return existing

    existing_ids = {p.get("id") for p in existing}
    regime = get_current_regime()

    for np in nous_new:
        np["scope"] = scope
        if regime != "unknown":
            np.setdefault("regime", regime)
        np.setdefault("last_validated", datetime.now(timezone.utc).isoformat())

        if np.get("id") in existing_ids:
            for i, ep in enumerate(existing):
                if ep.get("id") == np.get("id"):
                    np["confidence"] = max(np.get("confidence", 0.5), ep.get("confidence", 0.5))
                    np["last_validated"] = datetime.now(timezone.utc).isoformat()
                    existing[i] = np
                    break
        else:
            existing.append(np)

    return existing


def detect_cross_scope_conflicts(principles):
    conflicts = []
    gov_principles = [p for p in principles if p.get("scope") == "governor"]
    repo_principles = [p for p in principles if p.get("scope") == "repo"]

    for gp in gov_principles:
        for rp in repo_principles:
            g_ctrl = gp.get("controllable", "")
            r_ctrl = rp.get("controllable", "")
            if g_ctrl and r_ctrl and g_ctrl == r_ctrl:
                g_effect = gp.get("effect_size", {})
                r_effect = rp.get("effect_size", {})
                for metric in set(g_effect.keys()) & set(r_effect.keys()):
                    g_val = g_effect[metric]
                    r_val = r_effect[metric]
                    if isinstance(g_val, (int, float)) and isinstance(r_val, (int, float)):
                        if (g_val > 0) != (r_val > 0):
                            conflicts.append({
                                "governor_principle": gp.get("id"),
                                "repo_principle": rp.get("id"),
                                "metric": metric,
                                "governor_effect": g_val,
                                "repo_effect": r_val,
                            })

    return conflicts


def generate_recommendations(principles):
    high_confidence = [p for p in principles if p.get("confidence", 0) >= RECOMMENDATION_THRESHOLD]
    if len(high_confidence) < MIN_PRINCIPLES_FOR_RECOMMENDATION:
        return None

    recs = []
    for p in high_confidence:
        ctrl = p.get("controllable")
        if not ctrl:
            continue
        recs.append({
            "var": ctrl,
            "proposed": p.get("effect_size", {}).get("recommended_value"),
            "rationale": f"{p.get('id')}: {p.get('text', '')}",
            "confidence": p.get("confidence"),
            "evidence_count": len(p.get("evidence", [])),
            "scope": p.get("scope", "governor"),
        })

    if not recs:
        return None

    return {
        "generated": datetime.now(timezone.utc).isoformat(),
        "recommendations": recs,
    }


def log_sync(scope, phases, principle_count, conflicts_count):
    entry = {
        "ts": datetime.now(timezone.utc).isoformat(),
        "type": "nous_sync",
        "scope": scope,
        "phases": phases,
        "principle_count": principle_count,
        "conflicts_detected": conflicts_count,
    }
    try:
        with open(LEDGER_PATH, "a") as f:
            f.write(json.dumps(entry) + "\n")
    except Exception:
        pass


def main():
    print("[nous-sync] starting sync")

    principles = read_json(PRINCIPLES_PATH) or []

    phases = {}
    for scope in ("governor", "repo"):
        state, findings, nous_principles = read_nous_state(scope)
        if state:
            phase = state.get("phase", "UNKNOWN")
            iteration = state.get("iteration", 0)
            phases[scope] = {"phase": phase, "iteration": iteration}
            print(f"[nous-sync] {scope}: phase={phase} iteration={iteration}")

            if phase in ("ANALYSIS", "EXTRACTION") and nous_principles:
                principles = merge_nous_principles(principles, nous_principles, scope)
                print(f"[nous-sync] merged {len(nous_principles)} principles from {scope}")
        else:
            phases[scope] = {"phase": "IDLE", "iteration": 0}

    active, archived = apply_confidence_decay(principles)
    if archived:
        print(f"[nous-sync] archived {len(archived)} low-confidence principles")

    conflicts = detect_cross_scope_conflicts(active)
    if conflicts:
        print(f"[nous-sync] WARNING: {len(conflicts)} cross-scope conflicts detected")
        for c in conflicts:
            print(f"  {c['governor_principle']} vs {c['repo_principle']} on {c['metric']}")

    write_json(PRINCIPLES_PATH, active)
    print(f"[nous-sync] wrote {len(active)} active principles")

    recs = generate_recommendations(active)
    if recs:
        write_json(RECOMMENDATIONS_PATH, recs)
        print(f"[nous-sync] generated {len(recs['recommendations'])} recommendations")
    elif os.path.exists(RECOMMENDATIONS_PATH):
        os.remove(RECOMMENDATIONS_PATH)

    total_scope = "both" if all(phases.get(s, {}).get("phase") != "IDLE" for s in ("governor", "repo")) else next(
        (s for s in ("governor", "repo") if phases.get(s, {}).get("phase") != "IDLE"), "none"
    )
    log_sync(total_scope, phases, len(active), len(conflicts))

    print("[nous-sync] done")


if __name__ == "__main__":
    main()
