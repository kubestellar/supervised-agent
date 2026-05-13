#!/bin/sh
set -e

export TZ="${TZ:-America/New_York}"
export HIVE_API_PORT="${HIVE_API_PORT:-3002}"
export HIVE_PROXY_PORT="${HIVE_PROXY_PORT:-3001}"
export HIVE_STATIC_DIR="${HIVE_STATIC_DIR:-/opt/hive/proxy/public}"

# Seed data files from image into /data if they don't already exist
if [ -d /opt/hive/seed-data ]; then
  echo "[entrypoint] Seeding data files..."
  cp -rn /opt/hive/seed-data/* /data/ 2>/dev/null || true
fi

echo "[entrypoint] Starting Go binary on :${HIVE_API_PORT}"
hive "$@" &
HIVE_PID=$!

sleep 1

echo "[entrypoint] Starting Node.js proxy on :${HIVE_PROXY_PORT} → :${HIVE_API_PORT}"
cd /opt/hive/proxy && node server.js &
PROXY_PID=$!

TTYD_PORT="${HIVE_TTYD_PORT:-7681}"
echo "[entrypoint] Starting ttyd on :${TTYD_PORT}"
ttyd -W -a -p "${TTYD_PORT}" -t fontSize=14 -t disableLeaveAlert=true /usr/local/bin/ttyd-tmux.sh &
TTYD_PID=$!

cleanup() {
  echo "[entrypoint] Shutting down..."
  kill "$TTYD_PID" 2>/dev/null || true
  kill "$PROXY_PID" 2>/dev/null || true
  kill "$HIVE_PID" 2>/dev/null || true
  wait "$HIVE_PID" 2>/dev/null || true
  wait "$PROXY_PID" 2>/dev/null || true
}
trap cleanup INT TERM

wait "$HIVE_PID"
