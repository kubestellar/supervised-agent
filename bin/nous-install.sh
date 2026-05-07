#!/usr/bin/env bash
set -euo pipefail

NOUS_DIR="${NOUS_DIR:-/opt/nous}"
NOUS_RUN_DIR="${NOUS_RUN_DIR:-/var/run/nous}"
NOUS_REPO="https://github.com/AI-native-Systems-Research/agentic-strategy-evolution"

if [ -d "$NOUS_DIR/.git" ]; then
  echo "Nous already installed at $NOUS_DIR — pulling latest"
  cd "$NOUS_DIR" && git pull --ff-only
else
  echo "Cloning Nous framework to $NOUS_DIR"
  git clone "$NOUS_REPO" "$NOUS_DIR"
fi

cd "$NOUS_DIR"
pip3 install -e . 2>&1

mkdir -p "$NOUS_RUN_DIR"/{governor,repo,snapshots}
chown -R dev:dev "$NOUS_RUN_DIR" "$NOUS_DIR"

echo "Verifying installation…"
python3 -c "from run_campaign import run_campaign; print('Nous framework installed OK')"
