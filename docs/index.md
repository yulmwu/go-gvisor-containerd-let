---
title: Home
nav_order: 1
---

Base URL: `http://localhost:8080`

## Health

- `GET /healthz`
- Success: `200 OK`

**Response**

```json
{
    "ok": true
}
```

## Sandbox APIs

### Create Sandbox

- `POST /v1/sandboxes`
- Body: `CreateSandboxRequest`
- Success: `202 Accepted` (async provisioning)
- Failure: `400 Bad Request`

**Request**

```json
{
    "id": "sbx-http-demo",
    "egress": true,
    "ports": [
        {
            "hostPort": 30080,
            "containerPort": 8080,
            "protocol": "tcp"
        }
    ],
    "containers": [
        {
            "name": "web",
            "image": "python:3.12-alpine",
            "args": [
                "sh",
                "-c",
                "mkdir -p /tmp/www && echo sandbox-http > /tmp/www/index.html && cd /tmp/www && python -m http.server 8080"
            ],
            "env": [],
            "workDir": "",
            "resource": {
                "cpu": "250m",
                "memory": "256Mi"
            }
        },
        {
            "name": "worker",
            "image": "alpine:3.20",
            "args": ["sh", "-c", "while true; do sleep 60; done"],
            "env": [],
            "workDir": "",
            "resource": {
                "cpu": "100m",
                "memory": "128Mi"
            }
        }
    ]
}
```

**Response (Accepted)**

```json
{
    "sandbox": {
        "id": "sbx-http-demo",
        "phase": "creating",
        "namespace": "sandbox-demo",
        "ip": "",
        "subnetCIDR": "10.89.0.0/16",
        "bridgeName": "sbx-br0",
        "egress": true,
        "ports": [
            {
                "hostPort": 30080,
                "containerPort": 8080,
                "protocol": "tcp"
            }
        ],
        "containers": {
            "web": {
                "id": "sbx-http-demo-web",
                "name": "web",
                "phase": "creating",
                "image": "python:3.12-alpine",
                "args": [
                    "sh",
                    "-c",
                    "mkdir -p /tmp/www && echo sandbox-http > /tmp/www/index.html && cd /tmp/www && python -m http.server 8080"
                ],
                "resource": {
                    "cpu": "250m",
                    "memory": "256Mi"
                },
                "snapshotKey": "",
                "taskPID": 0,
                "runtime": "runc"
            },
            "worker": {
                "id": "sbx-http-demo-worker",
                "name": "worker",
                "phase": "creating",
                "image": "alpine:3.20",
                "args": ["sh", "-c", "while true; do sleep 60; done"],
                "resource": {
                    "cpu": "100m",
                    "memory": "128Mi"
                },
                "snapshotKey": "",
                "taskPID": 0,
                "runtime": "runc"
            }
        },
        "cniConfPath": "/etc/cni/net.d/20-sbxnet.conflist",
        "createdAt": "2026-05-08T11:00:00Z",
        "updatedAt": "2026-05-08T11:00:00Z"
    },
    "external_ip": "203.0.113.10"
}
```

**Error Examples**

```json
{
    "error": "container web: cpu is required"
}
```

```json
{
    "error": "insufficient cpu allocatable: requested=2000m used=7000m limit=7200m (90% of host)"
}
```

```json
{
    "error": "insufficient memory allocatable: requested=17179869184 used=34359738368 limit=42949672960 (90% of host)"
}
```

```json
{
    "error": "insufficient ip addresses in subnet 10.89.0.0/16: in_use=65534 usable=65534"
}
```

```json
{
    "error": "host port already in use: 30080/tcp (sandbox sbx-old)"
}
```

### List Sandboxes (Cursor Pagination)

- `GET /v1/sandboxes?cursor=<sandbox_id>&limit=<n>`
- Default `limit`: `20`
- Max `limit`: `200`
- Success: `200 OK`

**Response**

```json
{
    "items": [
        {
            "id": "sbx-http-demo",
            "phase": "running",
            "namespace": "sandbox-demo",
            "ip": "10.89.0.18",
            "subnetCIDR": "10.89.0.0/16",
            "bridgeName": "sbx-br0",
            "egress": true,
            "ports": [
                {
                    "hostPort": 30080,
                    "containerPort": 8080,
                    "protocol": "tcp"
                }
            ],
            "containers": {
                "web": {
                    "id": "sbx-http-demo-web",
                    "name": "web",
                    "phase": "running",
                    "image": "docker.io/library/python:3.12-alpine",
                    "resource": {
                        "cpu": "250m",
                        "memory": "256Mi"
                    },
                    "snapshotKey": "sbx-http-demo-web-snapshot",
                    "taskPID": 12345,
                    "runtime": "runc",
                    "taskStatus": "running"
                }
            },
            "cniConfPath": "/etc/cni/net.d/20-sbxnet.conflist",
            "createdAt": "2026-05-08T11:00:00Z",
            "updatedAt": "2026-05-08T11:00:12Z"
        }
    ],
    "next_cursor": "sbx-http-demo",
    "external_ip": "203.0.113.10"
}
```

### Get Sandbox

- `GET /v1/sandboxes/{id}`
- Success: `200 OK`
- Failure:
    - `404 Not Found` (state file not found)
    - `500 Internal Server Error` (store/runtime refresh error)

**Response**

```json
{
    "sandbox": {
        "id": "sbx-http-demo",
        "phase": "running",
        "namespace": "sandbox-demo",
        "ip": "10.89.0.18",
        "subnetCIDR": "10.89.0.0/16",
        "bridgeName": "sbx-br0",
        "egress": true,
        "ports": [
            {
                "hostPort": 30080,
                "containerPort": 8080,
                "protocol": "tcp"
            }
        ],
        "containers": {
            "web": {
                "id": "sbx-http-demo-web",
                "name": "web",
                "phase": "running",
                "image": "docker.io/library/python:3.12-alpine",
                "args": [
                    "sh",
                    "-c",
                    "mkdir -p /tmp/www && echo sandbox-http > /tmp/www/index.html && cd /tmp/www && python -m http.server 8080"
                ],
                "resource": {
                    "cpu": "250m",
                    "memory": "256Mi"
                },
                "snapshotKey": "sbx-http-demo-web-snapshot",
                "taskPID": 12345,
                "runtime": "runc",
                "taskStatus": "running"
            },
            "worker": {
                "id": "sbx-http-demo-worker",
                "name": "worker",
                "phase": "running",
                "image": "docker.io/library/alpine:3.20",
                "args": ["sh", "-c", "while true; do sleep 60; done"],
                "resource": {
                    "cpu": "100m",
                    "memory": "128Mi"
                },
                "snapshotKey": "sbx-http-demo-worker-snapshot",
                "taskPID": 12346,
                "runtime": "runc",
                "taskStatus": "running"
            }
        },
        "cniConfPath": "/etc/cni/net.d/20-sbxnet.conflist",
        "createdAt": "2026-05-08T11:00:00Z",
        "updatedAt": "2026-05-08T11:00:12Z"
    },
    "external_ip": "203.0.113.10"
}
```

### Container Logs (Cursor Pagination)

- `GET /v1/sandboxes/{id}/containers/{name}/logs?cursor=<byte_offset>&limit=<line_count>`
- Default `limit`: `100`
- Max `limit`: `1000`
- Success: `200 OK`
- Failure:
    - `404 Not Found` (sandbox/container does not exist and log file is missing)
    - `400 Bad Request` (invalid cursor/limit)
    - `500 Internal Server Error` (log file read I/O failure)

Note:
- If the container exists but the log file is not created yet, the API returns `200` with `lines: []`.

**Response**

```json
{
    "sandbox_id": "sbx-http-demo",
    "container": "web",
    "logs": {
        "lines": [
            "Serving HTTP on 0.0.0.0 port 8080 (http://0.0.0.0:8080/) ...",
            "10.0.0.5 - - [08/May/2026 11:03:02] \"GET / HTTP/1.1\" 200 -"
        ],
        "next_cursor": "154",
        "has_more": false
    },
    "external_ip": "203.0.113.10"
}
```

### Delete Sandbox

- `DELETE /v1/sandboxes/{id}`
- Success: `200 OK`

**Response**

```json
{
    "id": "sbx-http-demo",
    "phase": "deleted",
    "external_ip": "203.0.113.10"
}
```

### Manual Reconcile

- `POST /v1/reconcile`
- Success: `200 OK`
- Failure: `500 Internal Server Error`

**Response**

```json
{
    "ok": true,
    "external_ip": "203.0.113.10"
}
```

## Resource Field Format

### `containers[].resource.cpu`

- millicore format: `250m`, `500m`, `1000m`
- core format: `0.25`, `0.5`, `1`, `2`

### `containers[].resource.memory`

- Binary units: `128Mi`, `1Gi`
- Decimal units: `128M`, `1G`
- Raw bytes as numeric string are also accepted

## Common Error Response

```json
{
    "error": "error message"
}
```
