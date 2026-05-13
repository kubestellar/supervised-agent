#!/usr/bin/env bash
set -euo pipefail

# Run this on the Proxmox host (192.168.6.144) to create the LXC.
# Usage: bash create-lxc.sh
#
# Or from your Mac:
#   ssh root@192.168.6.144 'bash -s' < create-lxc.sh

CTID=110
HOSTNAME="hive-v2"
TEMPLATE="local:vztmpl/ubuntu-24.04-standard_24.04-2_amd64.tar.zst"
STORAGE="local-lvm"
DISK_SIZE=16
RAM_MB=4096
SWAP_MB=512
CORES=4

# Check if template exists, download if not
if ! pveam list local | grep -q "ubuntu-24.04-standard"; then
  echo "=== Downloading Ubuntu 24.04 template ==="
  pveam update
  pveam download local ubuntu-24.04-standard_24.04-2_amd64.tar.zst
fi

# Check if CTID is in use
if pct status "${CTID}" &>/dev/null; then
  echo "ERROR: CTID ${CTID} already exists. Pick a different ID."
  exit 1
fi

echo "=== Creating LXC ${CTID} (${HOSTNAME}) ==="
pct create "${CTID}" "${TEMPLATE}" \
  --hostname "${HOSTNAME}" \
  --storage "${STORAGE}" \
  --rootfs "${STORAGE}:${DISK_SIZE}" \
  --memory "${RAM_MB}" \
  --swap "${SWAP_MB}" \
  --cores "${CORES}" \
  --net0 "name=eth0,bridge=vmbr0,ip=dhcp" \
  --features "nesting=1,keyctl=1" \
  --unprivileged 0 \
  --start 1 \
  --password "changeme"

echo "=== Waiting for LXC to boot ==="
sleep 5

echo "=== LXC IP address ==="
pct exec "${CTID}" -- ip -4 addr show eth0 | grep inet || echo "(DHCP may still be pending — check in a few seconds)"

echo ""
echo "=== LXC ${CTID} created ==="
echo ""
echo "Next: copy and run the bootstrap script inside the LXC:"
echo "  pct exec ${CTID} -- bash -c 'curl -fsSL https://raw.githubusercontent.com/kubestellar/hive/v2/v2/deploy/bootstrap-lxc.sh | bash'"
echo ""
echo "Or manually:"
echo "  pct enter ${CTID}"
echo "  # then run bootstrap-lxc.sh"
