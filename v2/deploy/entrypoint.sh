#!/bin/sh
set -e

export HIVE_API_PORT="${HIVE_API_PORT:-3002}"
export HIVE_PROXY_PORT="${HIVE_PROXY_PORT:-3001}"
export HIVE_STATIC_DIR="${HIVE_STATIC_DIR:-/opt/hive/proxy/public}"

echo "[entrypoint] Starting Go binary on :${HIVE_API_PORT}"
hive "$@" &
HIVE_PID=$!

sleep 1

echo "[entrypoint] Starting Node.js proxy on :${HIVE_PROXY_PORT} → :${HIVE_API_PORT}"
cd /opt/hive/proxy && node server.js &
PROXY_PID=$!

cleanup() {
  echo "[entrypoint] Shutting down..."
  kill "$PROXY_PID" 2>/dev/null || true
  kill "$HIVE_PID" 2>/dev/null || true
  wait "$HIVE_PID" 2>/dev/null || true
  wait "$PROXY_PID" 2>/dev/null || true
}
trap cleanup INT TERM

wait "$HIVE_PID"
