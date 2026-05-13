#!/usr/bin/env bash
set -euo pipefail

# Force-clean one sandbox when normal Delete API cannot be used.
# Scope is intentionally limited to resources tied to the provided sandbox ID.
#
# Usage:
#   sudo ./scripts/force_cleanup.sh <sandbox-id> [namespace] [containerd-socket]
#
# Example:
#   sudo ./scripts/force_cleanup.sh sbx-example-123 k8s.io /run/containerd/containerd.sock

SID="${1:-}"
NS="${2:-k8s.io}"
ADDR="${3:-/run/containerd/containerd.sock}"
CRI_ENDPOINT="unix://${ADDR}"

if [[ -z "$SID" ]]; then
  echo "usage: $0 <sandbox-id> [namespace] [containerd-socket]" >&2
  exit 1
fi

echo "[cleanup] sandbox-id=$SID namespace=$NS addr=$ADDR"

# 1) Remove CRI pod sandbox by pod name first.
if command -v crictl >/dev/null 2>&1; then
  echo "[cleanup] removing cri pod sandboxes by name"
  crictl --runtime-endpoint "$CRI_ENDPOINT" pods --name "$SID" -q 2>/dev/null | while read -r pid; do
    [[ -z "$pid" ]] && continue
    echo "  - stopp $pid"
    crictl --runtime-endpoint "$CRI_ENDPOINT" stopp "$pid" >/dev/null 2>&1 || true
    echo "  - rmp $pid"
    crictl --runtime-endpoint "$CRI_ENDPOINT" rmp "$pid" >/dev/null 2>&1 || true
  done
fi

# Fallback cleanup for any old direct-mode artifacts.
echo "[cleanup] removing legacy direct tasks/containers"
ctr -a "$ADDR" -n "$NS" tasks list 2>/dev/null | awk -v s="$SID-" 'NR>1 && index($1, s)==1 {print $1}' | while read -r id; do
  ctr -a "$ADDR" -n "$NS" tasks rm -f "$id" || true
done
ctr -a "$ADDR" -n "$NS" containers list 2>/dev/null | awk -v s="$SID-" 'NR>1 && index($1, s)==1 {print $1}' | while read -r id; do
  ctr -a "$ADDR" -n "$NS" containers rm --keep-snapshot "$id" || true
done

# 2) Resolve sandbox IPv4 from CNI result cache (if present)
CNI_RESULT="$(find "/var/lib/cni/${SID}/results" -maxdepth 1 -type f 2>/dev/null | head -n1 || true)"
SBX_IP=""
if [[ -f "$CNI_RESULT" ]]; then
  if command -v jq >/dev/null 2>&1; then
    SBX_IP="$(jq -r '.result.ips[]?.address // empty' "$CNI_RESULT" | sed -n 's#/.*##p' | head -n1 || true)"
  else
    SBX_IP="$(grep -oE '"address":"[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+"' "$CNI_RESULT" | head -n1 | sed -E 's/.*"address":"([0-9.]+)\/[0-9]+".*/\1/' || true)"
  fi
fi

if [[ -n "$SBX_IP" ]]; then
  echo "[cleanup] sandbox ip=$SBX_IP"

  # 3) Remove SANDBOX jump rules and per-sandbox chains (if present)
  FWD_CHAIN="$(iptables -S SANDBOX-FWD 2>/dev/null | awk -v ip="$SBX_IP/32" '$0 ~ ("-s " ip) {for(i=1;i<=NF;i++) if($i=="-j"){print $(i+1); exit}}' || true)"
  IN_CHAIN="$(iptables -S SANDBOX-IN 2>/dev/null | awk -v ip="$SBX_IP/32" '$0 ~ ("-s " ip) {for(i=1;i<=NF;i++) if($i=="-j"){print $(i+1); exit}}' || true)"

  echo "[cleanup] removing SANDBOX-FWD jump"
  iptables -D SANDBOX-FWD -s "${SBX_IP}/32" -j "${FWD_CHAIN:-DUMMY}" 2>/dev/null || true
  echo "[cleanup] removing SANDBOX-IN jump"
  iptables -D SANDBOX-IN -i sbx-br0 -s "${SBX_IP}/32" -j "${IN_CHAIN:-DUMMY}" 2>/dev/null || true

  if [[ -n "$FWD_CHAIN" ]]; then
    echo "[cleanup] deleting chain $FWD_CHAIN"
    iptables -F "$FWD_CHAIN" 2>/dev/null || true
    iptables -X "$FWD_CHAIN" 2>/dev/null || true
  fi
  if [[ -n "$IN_CHAIN" ]]; then
    echo "[cleanup] deleting chain $IN_CHAIN"
    iptables -F "$IN_CHAIN" 2>/dev/null || true
    iptables -X "$IN_CHAIN" 2>/dev/null || true
  fi

  # 4) Remove hostPort/NAT rules that target this sandbox IP
  echo "[cleanup] removing NAT/FORWARD rules for $SBX_IP"
  iptables-save -t nat 2>/dev/null | awk -v ip="$SBX_IP" '/^-A (PREROUTING|OUTPUT) / && $0 ~ ("--to-destination " ip ":") {sub(/^-A /, ""); print}' | \
    while read -r rule; do
      iptables -t nat -D ${rule} 2>/dev/null || true
    done

  iptables-save 2>/dev/null | awk -v ip="$SBX_IP/32" '/^-A FORWARD / && $0 ~ ("-d " ip) {sub(/^-A /, ""); print}' | \
    while read -r rule; do
      iptables -D ${rule} 2>/dev/null || true
    done
fi

# 5) CNI cache cleanup
echo "[cleanup] removing cni cache"
rm -rf "/var/lib/cni/${SID}" || true
find /var/lib/cni -maxdepth 3 -type f -name "*${SID}*" -delete 2>/dev/null || true

echo "[cleanup] done: $SID"
