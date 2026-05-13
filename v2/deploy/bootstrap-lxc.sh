#!/usr/bin/env bash
set -euo pipefail

# Bootstrap script for hive-v2 architect LXC on Proxmox
# Run this INSIDE the LXC after creation.
#
# Prerequisites:
#   - Ubuntu 24.04 LXC created on Proxmox
#   - Network configured (DHCP)
#   - This script copied into the LXC
#
# Usage:
#   bash bootstrap-lxc.sh

HIVE_DIR=/opt/hive
ENV_FILE="${HIVE_DIR}/.env"

echo "=== Installing Docker ==="
apt-get update -qq
apt-get install -y -qq ca-certificates curl gnupg lsb-release git

install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc

echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo "$VERSION_CODENAME") stable" > /etc/apt/sources.list.d/docker.list

apt-get update -qq
apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin

echo "=== Cloning hive repo ==="
git clone --branch v2 --single-branch https://github.com/kubestellar/hive.git "${HIVE_DIR}"

echo "=== Creating .env file ==="
if [ ! -f "${ENV_FILE}" ]; then
  cat > "${ENV_FILE}" <<'ENVEOF'
# Fill in these values before running docker compose up
HIVE_GITHUB_TOKEN=
HIVE_DASHBOARD_TOKEN=
ANTHROPIC_API_KEY=
ENVEOF
  echo ">>> EDIT ${ENV_FILE} with your tokens before starting <<<"
else
  echo ".env already exists, skipping"
fi

echo "=== Building Docker image ==="
cd "${HIVE_DIR}"
docker compose -f v2/deploy/docker-compose.architect.yaml build

echo ""
echo "=== Setup complete ==="
echo ""
echo "Next steps:"
echo "  1. Edit ${ENV_FILE} with your tokens:"
echo "     - HIVE_GITHUB_TOKEN  (same as 4.56 hive server)"
echo "     - HIVE_DASHBOARD_TOKEN  (generate with: openssl rand -hex 32)"
echo "     - ANTHROPIC_API_KEY  (for Claude Code CLI inside container)"
echo ""
echo "  2. Start hive:"
echo "     cd ${HIVE_DIR} && docker compose --env-file .env -f v2/deploy/docker-compose.architect.yaml up -d"
echo ""
echo "  3. Check logs:"
echo "     docker logs -f hive-architect"
echo ""
echo "  4. Dashboard: http://<this-lxc-ip>:3001"
echo ""
echo "  5. Pause architect on 4.56:"
echo "     ssh dev@192.168.4.56 'touch /etc/hive/pause_architect'"
