#!/bin/bash
# Blue-green deploy for Hive v2.
# Builds the new image, starts it alongside the old container,
# waits for the healthcheck to pass, then swaps traffic via nginx reload.
#
# Usage: ./blue-green-deploy.sh [--skip-build]

set -euo pipefail

COMPOSE_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$COMPOSE_DIR"

HEALTH_URL="http://localhost:3001/api/health"
HEALTH_INTERVAL_S=5
HEALTH_TIMEOUT_S=180

SKIP_BUILD=false
if [ "${1:-}" = "--skip-build" ]; then
    SKIP_BUILD=true
fi

log() { echo "[deploy] $(date '+%H:%M:%S') $*"; }

# ── 1. Build new image ──────────────────────────────────────────────
if [ "$SKIP_BUILD" = false ]; then
    log "Building new image..."
    docker compose build hive
fi

# ── 2. Determine active slot ────────────────────────────────────────
# The active container is whichever one nginx currently points at.
# On first run (no gateway), we just do a normal start.
GATEWAY_RUNNING=$(docker ps -q -f name=hive-gateway 2>/dev/null || true)

if [ -z "$GATEWAY_RUNNING" ]; then
    log "No gateway found — first-time setup, starting everything..."
    docker compose up -d
    log "Waiting for hive to become healthy..."
    elapsed=0
    while [ "$elapsed" -lt "$HEALTH_TIMEOUT_S" ]; do
        if curl -sf "$HEALTH_URL" >/dev/null 2>/dev/null; then
            log "Hive is healthy. Deploy complete."
            exit 0
        fi
        sleep "$HEALTH_INTERVAL_S"
        elapsed=$((elapsed + HEALTH_INTERVAL_S))
        log "Waiting... (${elapsed}s / ${HEALTH_TIMEOUT_S}s)"
    done
    log "ERROR: Hive failed to become healthy within ${HEALTH_TIMEOUT_S}s"
    exit 1
fi

# ── 3. Start new container alongside old one ────────────────────────
# We use docker run directly to avoid compose tearing down the old one.
log "Starting new container as hive-next..."

# Get the image name compose would use
IMAGE=$(docker compose config --images | grep hive | head -1)

# Collect env vars from compose
ENV_ARGS=""
for var in HIVE_GITHUB_TOKEN HIVE_DASHBOARD_TOKEN NTFY_SERVER NTFY_TOPIC; do
    val="${!var:-}"
    ENV_ARGS="$ENV_ARGS -e $var=$val"
done

# Get the data volume name
DATA_VOL=$(docker inspect hive --format '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}' 2>/dev/null || echo "v2_hive-data")

# Get the hive.yaml and secrets paths from the running container
HIVE_YAML=$(docker inspect hive --format '{{range .Mounts}}{{if eq .Destination "/etc/hive/hive.yaml"}}{{.Source}}{{end}}{{end}}' 2>/dev/null)
SECRETS_DIR=$(docker inspect hive --format '{{range .Mounts}}{{if eq .Destination "/secrets"}}{{.Source}}{{end}}{{end}}' 2>/dev/null)

docker rm -f hive-next 2>/dev/null || true

docker run -d \
    --name hive-next \
    --network "$(docker inspect hive-gateway --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}{{end}}' | head -1)" \
    -v "${HIVE_YAML}:/etc/hive/hive.yaml:ro" \
    -v "${DATA_VOL}:/data" \
    -v "${SECRETS_DIR}:/secrets:ro" \
    $ENV_ARGS \
    --health-cmd "curl -sf http://localhost:3001/api/health" \
    --health-interval 10s \
    --health-timeout 5s \
    --health-retries 3 \
    --health-start-period 120s \
    "$IMAGE"

# ── 4. Wait for hive-next to become healthy ─────────────────────────
log "Waiting for hive-next to become healthy..."
elapsed=0
while [ "$elapsed" -lt "$HEALTH_TIMEOUT_S" ]; do
    HEALTH=$(docker inspect hive-next --format '{{.State.Health.Status}}' 2>/dev/null || echo "unknown")
    if [ "$HEALTH" = "healthy" ]; then
        log "hive-next is healthy!"
        break
    fi
    if [ "$HEALTH" = "unhealthy" ]; then
        log "ERROR: hive-next is unhealthy. Aborting deploy."
        docker logs hive-next --tail 20
        docker rm -f hive-next 2>/dev/null || true
        exit 1
    fi
    sleep "$HEALTH_INTERVAL_S"
    elapsed=$((elapsed + HEALTH_INTERVAL_S))
    log "Waiting... (${elapsed}s / ${HEALTH_TIMEOUT_S}s) status=${HEALTH}"
done

if [ "$elapsed" -ge "$HEALTH_TIMEOUT_S" ]; then
    log "ERROR: hive-next did not become healthy within ${HEALTH_TIMEOUT_S}s. Aborting."
    docker logs hive-next --tail 20
    docker rm -f hive-next 2>/dev/null || true
    exit 1
fi

# ── 5. Swap traffic: point nginx at hive-next ───────────────────────
log "Swapping traffic to hive-next..."

# Update nginx upstream to point at hive-next
NGINX_CONF="/tmp/nginx-swap.conf"
sed 's/server hive:/server hive-next:/g' deploy/nginx.conf > "$NGINX_CONF"
docker cp "$NGINX_CONF" hive-gateway:/etc/nginx/nginx.conf
rm -f "$NGINX_CONF"

# Reload nginx (zero-downtime)
docker exec hive-gateway nginx -s reload
log "Traffic switched to hive-next."

# ── 6. Stop old container, rename new one ────────────────────────────
sleep 2
log "Stopping old hive container..."
docker stop hive 2>/dev/null || true
docker rm hive 2>/dev/null || true

log "Renaming hive-next → hive..."
docker rename hive-next hive

# Restore nginx config to use "hive" name
docker cp deploy/nginx.conf hive-gateway:/etc/nginx/nginx.conf
docker exec hive-gateway nginx -s reload

log "Deploy complete. New version is live."
