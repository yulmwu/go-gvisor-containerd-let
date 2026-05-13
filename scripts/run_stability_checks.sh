#!/usr/bin/env bash
set -euo pipefail

API_BASE="${API_BASE:-http://127.0.0.1:8080}"
CTR_ADDR="${SANDBOX_CONTAINERD_ADDRESS:-/run/containerd/containerd.sock}"
HOST_IP="${HOST_IP:-$(hostname -I | awk '{print $1}')}"
TIMEOUT_SEC="${TIMEOUT_SEC:-60}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "[fatal] missing command: $1" >&2; exit 1; }; }
log() { echo "[check] $*"; }
pass() { echo "[pass] $*"; }
fail() { echo "[fail] $*"; FAILS+=("$*"); }

need curl
need jq
need crictl

if [[ "${EUID}" -ne 0 ]]; then
  echo "[fatal] run as root (example: sudo -E ./scripts/run_stability_checks.sh)" >&2
  exit 1
fi

SUFFIX="$(date +%s)"
SBX_A="sbx-test-a-${SUFFIX}"
SBX_B="sbx-test-b-${SUFFIX}"
SBX_C="sbx-test-c-${SUFFIX}"
SBX_M="sbx-test-multi-${SUFFIX}"
SBX_U="sbx-test-ubuntu-${SUFFIX}"
SBX_Z="sbx-test-busybox-${SUFFIX}"
SBX_N="sbx-test-nginx-${SUFFIX}"

PORT_A=31080
PORT_B=31081
PORT_M1=31082
PORT_M2=31083
PORT_N=31084

FAILS=()
CREATED_IDS=()

cleanup() {
  for id in "$SBX_A" "$SBX_B" "$SBX_C" "$SBX_M" "$SBX_U" "$SBX_Z" "$SBX_N"; do
    curl -fsS -X DELETE "${API_BASE}/v1/sandboxes/${id}" >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT

api_post() {
  local file="$1"
  curl -sS -w '\n%{http_code}' -H 'content-type: application/json' -d @"${file}" "${API_BASE}/v1/sandboxes"
}

api_get() {
  local id="$1"
  curl -sS "${API_BASE}/v1/sandboxes/${id}"
}

phase_of() {
  local id="$1"
  api_get "${id}" | jq -r '.sandbox.phase // "missing"'
}

wait_running() {
  local id="$1"
  local i
  for i in $(seq 1 "${TIMEOUT_SEC}"); do
    local ph
    ph="$(phase_of "${id}" || true)"
    if [[ "${ph}" == "running" ]]; then
      return 0
    fi
    if [[ "${ph}" == "error" || "${ph}" == "missing" ]]; then
      return 1
    fi
    sleep 1
  done
  return 1
}

wait_deleted() {
  local id="$1"
  local i
  for i in $(seq 1 "${TIMEOUT_SEC}"); do
    local code
    code="$(curl -sS -o /dev/null -w '%{http_code}' "${API_BASE}/v1/sandboxes/${id}" || true)"
    if [[ "${code}" == "404" ]]; then
      return 0
    fi
    sleep 1
  done
  return 1
}

sandbox_ip() {
  local id="$1"
  api_get "${id}" | jq -r '.sandbox.ip // ""'
}

log "health check: ${API_BASE}/healthz"
if curl -fsS "${API_BASE}/healthz" >/dev/null; then
  pass "health endpoint"
else
  fail "health endpoint"
fi

tmpdir="$(mktemp -d)"
cat > "${tmpdir}/a.json" <<JSON
{
  "id": "${SBX_A}",
  "egress": true,
  "ports": [{"hostPort": ${PORT_A}, "containerPort": 8080, "protocol": "tcp"}],
  "containers": [
    {"name":"web","image":"python:3.12-alpine","args":["sh","-c","mkdir -p /tmp/www && echo A > /tmp/www/index.html && cd /tmp/www && python -m http.server 8080"],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"128Mi"}},
    {"name":"worker","image":"alpine:3.20","args":["sh","-c","sleep infinity"],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"128Mi"}}
  ]
}
JSON

cat > "${tmpdir}/b.json" <<JSON
{
  "id": "${SBX_B}",
  "egress": false,
  "ports": [{"hostPort": ${PORT_B}, "containerPort": 8080, "protocol": "tcp"}],
  "containers": [
    {"name":"web","image":"python:3.12-alpine","args":["sh","-c","mkdir -p /tmp/www && echo B > /tmp/www/index.html && cd /tmp/www && python -m http.server 8080"],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"128Mi"}},
    {"name":"worker","image":"alpine:3.20","args":["sh","-c","sleep infinity"],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"128Mi"}}
  ]
}
JSON

cat > "${tmpdir}/one.json" <<JSON
{
  "id": "${SBX_C}",
  "egress": true,
  "ports": [],
  "containers": [
    {"name":"main","image":"alpine:3.20","args":["sh","-c","sleep infinity"],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"64Mi"}}
  ]
}
JSON

cat > "${tmpdir}/multi.json" <<JSON
{
  "id": "${SBX_M}",
  "egress": true,
  "ports": [
    {"hostPort": ${PORT_M1}, "containerPort": 8080, "protocol": "tcp"},
    {"hostPort": ${PORT_M2}, "containerPort": 8081, "protocol": "tcp"}
  ],
  "containers": [
    {"name":"web","image":"python:3.12-alpine","args":["sh","-c","mkdir -p /tmp/www && echo M8080 > /tmp/www/index.html && cd /tmp/www && python -m http.server 8080"],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"128Mi"}},
    {"name":"web2","image":"python:3.12-alpine","args":["sh","-c","mkdir -p /tmp/www2 && echo M8081 > /tmp/www2/index.html && cd /tmp/www2 && python -m http.server 8081"],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"128Mi"}}
  ]
}
JSON

cat > "${tmpdir}/invalid_dup_port.json" <<JSON
{
  "id": "sbx-invalid-dup-port-${SUFFIX}",
  "egress": true,
  "ports": [
    {"hostPort": 31100, "containerPort": 8080, "protocol": "tcp"},
    {"hostPort": 31100, "containerPort": 8081, "protocol": "tcp"}
  ],
  "containers": [{"name":"main","image":"alpine:3.20","args":["sh","-c","sleep infinity"],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"64Mi"}}]
}
JSON

cat > "${tmpdir}/invalid_protocol.json" <<JSON
{
  "id": "sbx-invalid-proto-${SUFFIX}",
  "egress": true,
  "ports": [{"hostPort": 31101, "containerPort": 8080, "protocol": "icmp"}],
  "containers": [{"name":"main","image":"alpine:3.20","args":["sh","-c","sleep infinity"],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"64Mi"}}]
}
JSON

cat > "${tmpdir}/invalid_priv_port.json" <<JSON
{
  "id": "sbx-invalid-priv-${SUFFIX}",
  "egress": true,
  "ports": [{"hostPort": 80, "containerPort": 8080, "protocol": "tcp"}],
  "containers": [{"name":"main","image":"alpine:3.20","args":["sh","-c","sleep infinity"],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"64Mi"}}]
}
JSON

cat > "${tmpdir}/invalid_missing_fields.json" <<JSON
{
  "id": "sbx-invalid-missing-${SUFFIX}",
  "egress": true,
  "ports": [],
  "containers": [{"name":"","image":"","args":[],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"64Mi"}}]
}
JSON

cat > "${tmpdir}/nginx.json" <<JSON
{
  "id": "${SBX_N}",
  "egress": true,
  "ports": [{"hostPort": ${PORT_N}, "containerPort": 80, "protocol": "tcp"}],
  "containers": [
    {"name":"web","image":"nginx:alpine","args":[],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"256Mi"}}
  ]
}
JSON

cat > "${tmpdir}/ubuntu.json" <<JSON
{
  "id": "${SBX_U}",
  "egress": true,
  "ports": [],
  "containers": [
    {"name":"main","image":"ubuntu:22.04","args":["sh","-c","mkdir -p /tmp/fs && echo ubuntu-ok >/tmp/fs/probe && sleep infinity"],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"256Mi"}}
  ]
}
JSON

cat > "${tmpdir}/busybox.json" <<JSON
{
  "id": "${SBX_Z}",
  "egress": true,
  "ports": [],
  "containers": [
    {"name":"main","image":"busybox:1.36","args":["sh","-c","mkdir -p /tmp/fs && echo busybox-ok >/tmp/fs/probe && sleep 3600"],"env":[],"workDir":"","resource":{"cpu":"500m","memory":"128Mi"}}
  ]
}
JSON

container_id_of() {
  local id="$1"
  local name="$2"
  api_get "${id}" | jq -r ".sandbox.containers[\"${name}\"].id // \"\""
}

log "validation: one-container request"
code="$(api_post "${tmpdir}/one.json" | tail -n1)"
if [[ "${code}" == "202" ]]; then pass "one-container create accepted"; else fail "one-container create accepted (got ${code})"; fi
wait_running "${SBX_C}" && pass "one-container reaches running" || fail "one-container reaches running"

log "validation: duplicate id rejection"
code="$(api_post "${tmpdir}/one.json" | tail -n1)"
if [[ "${code}" == "400" ]]; then pass "duplicate id rejected"; else fail "duplicate id rejected (got ${code})"; fi

log "validation: bad requests"
for f in invalid_dup_port invalid_protocol invalid_priv_port invalid_missing_fields; do
  code="$(api_post "${tmpdir}/${f}.json" | tail -n1)"
  if [[ "${code}" == "400" ]]; then
    pass "${f} rejected"
  else
    fail "${f} rejected (got ${code})"
  fi
done

log "create base networking sandboxes"
[[ "$(api_post "${tmpdir}/a.json" | tail -n1)" == "202" ]] && pass "create sbx-a accepted" || fail "create sbx-a accepted"
[[ "$(api_post "${tmpdir}/b.json" | tail -n1)" == "202" ]] && pass "create sbx-b accepted" || fail "create sbx-b accepted"
wait_running "${SBX_A}" && pass "sbx-a reaches running" || fail "sbx-a reaches running"
wait_running "${SBX_B}" && pass "sbx-b reaches running" || fail "sbx-b reaches running"

log "hostPort collision across sandboxes"
code="$(api_post "${tmpdir}/a.json" | tail -n1)"
if [[ "${code}" == "400" ]]; then pass "hostPort collision rejected"; else fail "hostPort collision rejected (got ${code})"; fi

log "same sandbox localhost connectivity"
CID_A_WORKER="$(container_id_of "${SBX_A}" "worker")"
if crictl --runtime-endpoint "unix://${CTR_ADDR}" exec "${CID_A_WORKER}" sh -lc 'wget -qO- http://127.0.0.1:8080 | grep -qx A'; then
  pass "same-sandbox localhost communication"
else
  fail "same-sandbox localhost communication"
fi

log "cross-sandbox communication block"
IP_B="$(sandbox_ip "${SBX_B}")"
if crictl --runtime-endpoint "unix://${CTR_ADDR}" exec "${CID_A_WORKER}" sh -lc "wget -T 2 -qO- http://${IP_B}:8080 >/dev/null"; then
  fail "cross-sandbox communication blocked"
else
  pass "cross-sandbox communication blocked"
fi

log "egress true/false DNS behavior"
if crictl --runtime-endpoint "unix://${CTR_ADDR}" exec "${CID_A_WORKER}" sh -lc 'wget -T 5 -qO- http://example.com >/dev/null'; then
  pass "egress=true outbound DNS/http"
else
  fail "egress=true outbound DNS/http"
fi
CID_B_WORKER="$(container_id_of "${SBX_B}" "worker")"
if crictl --runtime-endpoint "unix://${CTR_ADDR}" exec "${CID_B_WORKER}" sh -lc 'wget -T 3 -qO- http://example.com >/dev/null'; then
  fail "egress=false outbound blocked"
else
  pass "egress=false outbound blocked"
fi

log "host private ip hostPort access"
if curl -fsS -m 5 "http://${HOST_IP}:${PORT_A}" | grep -qx "A"; then pass "hostPort A"; else fail "hostPort A"; fi
if curl -fsS -m 5 "http://${HOST_IP}:${PORT_B}" | grep -qx "B"; then pass "hostPort B"; else fail "hostPort B"; fi

log "multi-port sandbox"
[[ "$(api_post "${tmpdir}/multi.json" | tail -n1)" == "202" ]] && pass "multi-port create accepted" || fail "multi-port create accepted"
wait_running "${SBX_M}" && pass "multi-port reaches running" || fail "multi-port reaches running"
if curl -fsS -m 5 "http://${HOST_IP}:${PORT_M1}" | grep -qx "M8080"; then pass "multi-port hostPort #1"; else fail "multi-port hostPort #1"; fi
if curl -fsS -m 5 "http://${HOST_IP}:${PORT_M2}" | grep -qx "M8081"; then pass "multi-port hostPort #2"; else fail "multi-port hostPort #2"; fi

log "phase cleanup before heavy image/fs coverage"
for id in "${SBX_A}" "${SBX_B}" "${SBX_C}" "${SBX_M}"; do
  curl -fsS -X DELETE "${API_BASE}/v1/sandboxes/${id}" >/dev/null || true
done
for id in "${SBX_A}" "${SBX_B}" "${SBX_C}" "${SBX_M}"; do
  wait_deleted "${id}" && pass "deleted ${id}" || fail "deleted ${id}"
done

log "image/fs coverage"
[[ "$(api_post "${tmpdir}/nginx.json" | tail -n1)" == "202" ]] && pass "nginx create accepted" || fail "nginx create accepted"
[[ "$(api_post "${tmpdir}/ubuntu.json" | tail -n1)" == "202" ]] && pass "ubuntu create accepted" || fail "ubuntu create accepted"
[[ "$(api_post "${tmpdir}/busybox.json" | tail -n1)" == "202" ]] && pass "busybox create accepted" || fail "busybox create accepted"
wait_running "${SBX_N}" && pass "nginx reaches running" || fail "nginx reaches running"
wait_running "${SBX_U}" && pass "ubuntu reaches running" || fail "ubuntu reaches running"
wait_running "${SBX_Z}" && pass "busybox reaches running" || fail "busybox reaches running"
if curl -fsS -m 5 "http://${HOST_IP}:${PORT_N}" >/dev/null; then pass "nginx hostPort"; else fail "nginx hostPort"; fi
CID_U_MAIN="$(container_id_of "${SBX_U}" "main")"
CID_Z_MAIN="$(container_id_of "${SBX_Z}" "main")"
if crictl --runtime-endpoint "unix://${CTR_ADDR}" exec "${CID_U_MAIN}" sh -lc 'cat /tmp/fs/probe | grep -qx ubuntu-ok'; then pass "ubuntu fs write/read"; else fail "ubuntu fs write/read"; fi
if crictl --runtime-endpoint "unix://${CTR_ADDR}" exec "${CID_Z_MAIN}" sh -lc 'cat /tmp/fs/probe | grep -qx busybox-ok'; then pass "busybox fs write/read"; else fail "busybox fs write/read"; fi

log "delete and runtime cleanup"
for id in "${SBX_A}" "${SBX_B}" "${SBX_C}" "${SBX_M}" "${SBX_U}" "${SBX_Z}" "${SBX_N}"; do
  curl -fsS -X DELETE "${API_BASE}/v1/sandboxes/${id}" >/dev/null || true
done
sleep 2

list_count="$(curl -sS "${API_BASE}/v1/sandboxes" | jq -r '.items | length')"
if [[ "${list_count}" =~ ^[0-9]+$ ]] && [[ "${list_count}" -eq 0 ]]; then
  pass "list empty after cleanup"
else
  fail "list empty after cleanup (count=${list_count})"
fi

if crictl --runtime-endpoint "unix://${CTR_ADDR}" pods --name "sbx-test-" -q | grep -q .; then
  fail "runtime containers cleanup"
else
  pass "runtime containers cleanup"
fi

if ((${#FAILS[@]} == 0)); then
  echo
  echo "[summary] ALL CHECKS PASSED"
  exit 0
fi

echo
echo "[summary] FAILED CHECKS (${#FAILS[@]})"
for f in "${FAILS[@]}"; do
  echo " - ${f}"
done
exit 1
