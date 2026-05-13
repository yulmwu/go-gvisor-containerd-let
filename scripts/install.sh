#!/usr/bin/env bash
set -euo pipefail

CNI_VERSION="v1.9.0"
ARCH="amd64"
GVISOR_ARCH="x86_64"
SBX_CNI_CONF_DIR="/etc/cni/sandboxd.d"
SBX_CNI_CONF_FILE="${SBX_CNI_CONF_DIR}/20-sbxnet.conflist"

log() { echo "[install] $*"; }
need() { command -v "$1" >/dev/null 2>&1 || { echo "missing command: $1"; exit 1; }; }
die() { echo "[install] ERROR: $*" >&2; exit 1; }

preflight_checks() {
  need sudo
  need systemctl
  need curl
  need tar
  need jq
}

detect_arch() {
  local m
  m="$(uname -m)"
  case "${m}" in
    x86_64|amd64)
      ARCH="amd64"
      GVISOR_ARCH="x86_64"
      ;;
    aarch64|arm64)
      ARCH="arm64"
      GVISOR_ARCH="aarch64"
      ;;
    *)
      die "unsupported architecture: ${m}"
      ;;
  esac
  log "Detected arch: cni=${ARCH}, gvisor=${GVISOR_ARCH}"
}

install_containerd_pkg() {
  if command -v containerd >/dev/null 2>&1; then
    return
  fi
  log "Installing containerd package"
  if sudo apt-cache show containerd.io >/dev/null 2>&1; then
    sudo apt install -y containerd.io
    return
  fi
  if sudo apt-cache show containerd >/dev/null 2>&1; then
    sudo apt install -y containerd
    return
  fi
  die "neither containerd.io nor containerd package is available in apt sources"
}

install_base() {
  log "Installing base packages"
  sudo apt update
  sudo apt install -y iproute2 iptables curl jq tar ca-certificates gnupg apparmor apparmor-utils
  install_containerd_pkg

  log "Ensuring containerd service is running"
  sudo systemctl enable --now containerd
  sudo systemctl is-active containerd >/dev/null || die "containerd failed to start"
}

install_cni() {
  log "Installing CNI plugins ${CNI_VERSION}"
  sudo mkdir -p /opt/cni/bin
  curl -fsSL "https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-linux-${ARCH}-${CNI_VERSION}.tgz" | sudo tar -C /opt/cni/bin -xz

  log "Enabling bridge netfilter"
  sudo modprobe br_netfilter
  cat <<'CONF' | sudo tee /etc/sysctl.d/99-sandboxd.conf >/dev/null
net.bridge.bridge-nf-call-iptables = 1
net.bridge.bridge-nf-call-ip6tables = 1
CONF
  sudo sysctl --system >/dev/null
}

install_runsc() {
  log "Installing gVisor runsc"

  download_gvisor_asset runsc /tmp/runsc
  download_gvisor_asset containerd-shim-runsc-v1 /tmp/containerd-shim-runsc-v1

  sudo install -m 0755 /tmp/runsc /usr/local/bin/runsc
  sudo install -m 0755 /tmp/containerd-shim-runsc-v1 /usr/local/bin/containerd-shim-runsc-v1
}

download_gvisor_asset() {
  local asset="$1"
  local out="$2"
  local urls=(
    "https://storage.googleapis.com/gvisor/releases/release/latest/${GVISOR_ARCH}/${asset}"
  )

  for u in "${urls[@]}"; do
    if curl -fsSL -o "${out}" "${u}"; then
      log "Downloaded ${asset} from ${u}"
      return 0
    fi
  done

  die "failed to download ${asset}; tried known gVisor release URLs"
}

configure_containerd_for_runsc() {
  log "Writing /etc/containerd/config.toml (runsc runtime)"
  sudo mkdir -p /etc/containerd
  cat <<'TOML' | sudo tee /etc/containerd/config.toml >/dev/null
version = 2

[plugins."io.containerd.grpc.v1.cri".containerd]
  snapshotter = "overlayfs"
  default_runtime_name = "runsc"

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"

[plugins."io.containerd.grpc.v1.cri".cni]
  bin_dir = "/opt/cni/bin"
  conf_dir = "/etc/cni/sandboxd.d"
  max_conf_num = 1
TOML

  sudo systemctl restart containerd
  sudo systemctl is-active containerd >/dev/null || die "containerd restart failed after runsc config"
}

configure_network() {
  log "Writing CNI config (sbxnet)"
  sudo mkdir -p "${SBX_CNI_CONF_DIR}" /var/lib/cni/sbxnet
  if [[ -f /etc/cni/net.d/20-sbxnet.conflist && ! -f "${SBX_CNI_CONF_FILE}" ]]; then
    log "Migrating existing CNI conflist from /etc/cni/net.d to ${SBX_CNI_CONF_DIR}"
    sudo cp /etc/cni/net.d/20-sbxnet.conflist "${SBX_CNI_CONF_FILE}"
  fi
  cat <<'JSON' | sudo tee "${SBX_CNI_CONF_FILE}" >/dev/null
{
  "cniVersion": "1.0.0",
  "name": "sbxnet",
  "plugins": [
    {
      "type": "bridge",
      "bridge": "sbx-br0",
      "isGateway": true,
      "ipMasq": true,
      "hairpinMode": true,
      "ipam": {
        "type": "host-local",
        "ranges": [[{"subnet": "10.89.0.0/16", "gateway": "10.89.0.1"}]],
        "routes": [{"dst": "0.0.0.0/0"}],
        "dataDir": "/var/lib/cni/sbxnet"
      }
    },
    {"type": "loopback"}
  ]
}
JSON

  log "Ensuring iptables global chains"
  sudo iptables -N SANDBOX-FWD 2>/dev/null || true
  sudo iptables -C FORWARD -j SANDBOX-FWD 2>/dev/null || sudo iptables -I FORWARD 1 -j SANDBOX-FWD
  sudo iptables -N SANDBOX-IN 2>/dev/null || true
  sudo iptables -C INPUT -j SANDBOX-IN 2>/dev/null || sudo iptables -I INPUT 1 -j SANDBOX-IN
}

write_runtime_env() {
  log "Writing runtime settings to .env"
  local runtime_addr="/run/containerd/containerd.sock"
  if [[ -f .env ]]; then
    if grep -q '^SANDBOX_CONTAINERD_ADDRESS=' .env; then
      sed -i "s|^SANDBOX_CONTAINERD_ADDRESS=.*|SANDBOX_CONTAINERD_ADDRESS=${runtime_addr}|" .env
    else
      echo "SANDBOX_CONTAINERD_ADDRESS=${runtime_addr}" >> .env
    fi
    if grep -q '^SANDBOX_CNI_CONF_PATH=' .env; then
      sed -i "s|^SANDBOX_CNI_CONF_PATH=.*|SANDBOX_CNI_CONF_PATH=${SBX_CNI_CONF_FILE}|" .env
    else
      echo "SANDBOX_CNI_CONF_PATH=${SBX_CNI_CONF_FILE}" >> .env
    fi
    sed -i '/^SANDBOX_RUNTIME_PROFILE=/d' .env
    sed -i '/^SANDBOX_SNAPSHOTTER=/d' .env
  else
    cat > .env <<ENV
SANDBOX_CONTAINERD_ADDRESS=${runtime_addr}
SANDBOX_CNI_CONF_PATH=${SBX_CNI_CONF_FILE}
ENV
  fi
}

main() {
  preflight_checks
  detect_arch
  install_base
  install_cni
  install_runsc
  configure_containerd_for_runsc
  configure_network
  write_runtime_env

  log "Install complete"
  containerd --version
  ctr version
  runsc --version | head -n 1
  command -v containerd-shim-runsc-v1 >/dev/null
  log "Runtime: gVisor(runsc)"
}

main "$@"
