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

## Node Status (Unified Heartbeat + Resources)

- `GET /v1/node/status`
- Success: `200 OK`

**Response**

```json
{
    "ok": true,
    "resources": {
        "capacity_cpu_milli": 8000,
        "capacity_memory_bytes": 33554432000,
        "allocatable_cpu_milli": 7200,
        "allocatable_memory_bytes": 30198988800,
        "used_cpu_milli": 500,
        "used_memory_bytes": 536870912,
        "available_cpu_milli": 6700,
        "available_memory_bytes": 29662117888,
        "max_alloc_percent": 90,
        "updated_at": "2026-05-14T01:00:00Z"
    }
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
        "namespace": "k8s.io",
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
                "taskPID": 0,
                "runtime": "runsc"
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
                "taskPID": 0,
                "runtime": "runsc"
            }
        },
        "cniConfPath": "/etc/cni/sandboxd.d/20-sbxnet.conflist",
        "createdAt": "2026-05-08T11:00:00Z",
        "updatedAt": "2026-05-08T11:00:00Z"
    },
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
            "namespace": "k8s.io",
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
                    "taskPID": 12345,
                    "runtime": "runsc",
                    "taskStatus": "running"
                }
            },
            "cniConfPath": "/etc/cni/sandboxd.d/20-sbxnet.conflist",
            "createdAt": "2026-05-08T11:00:00Z",
            "updatedAt": "2026-05-08T11:00:12Z"
        }
    ],
    "next_cursor": "sbx-http-demo",
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
        "namespace": "k8s.io",
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
                "taskPID": 12345,
                "runtime": "runsc",
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
                "taskPID": 12346,
                "runtime": "runsc",
                "taskStatus": "running"
            }
        },
        "cniConfPath": "/etc/cni/sandboxd.d/20-sbxnet.conflist",
        "createdAt": "2026-05-08T11:00:00Z",
        "updatedAt": "2026-05-08T11:00:12Z"
    },
}
```

### Batch Sandbox Status 

- `POST /v1/sandboxes/statuses`
- Purpose: return minimal status for orchestrator synchronization with low payload.
- Max IDs per request: `200`

**Request**

```json
{
    "ids": ["sbx-a", "sbx-b"]
}
```

**Response**

```json
{
    "items": [
        {
            "id": "sbx-a",
            "phase": "running",
            "error": "",
            "unhealthy_containers": []
        },
        {
            "id": "sbx-b",
            "phase": "error",
            "error": "pull image \"docker.io/library/nginx:bad\": ...",
            "unhealthy_containers": [
                {
                    "name": "web",
                    "phase": "error",
                    "error": "container not found",
                    "task_status": "not_found"
                }
            ]
        }
    ],
    "missing": ["sbx-missing"]
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

## Logging Environment Variables

- `SANDBOX_LOG_DIR` (optional; when set, JSON logs are also written to rotating hourly files)
- `SANDBOX_LOG_FILE_PREFIX` (default: `sandboxd`)
- `APP_ENV` (optional; added to `app` log field, e.g. `sandboxd:prod`)

# Example Create Payload (Wordpress)

```json
{
    "id": "sbx-wordpress-demo",
    "egress": true,
    "ports": [
        {
            "hostPort": 30080,
            "containerPort": 80,
            "protocol": "tcp"
        }
    ],
    "containers": [
        {
            "name": "wordpress",
            "image": "wordpress:6.9.4-php8.3-apache",
            "args": [
                "sh",
                "-c",
                "for i in $(seq 1 90); do php -r '$s=@fsockopen(\"127.0.0.1\",3306,$e,$es,1); if($s){fclose($s); exit(0);} exit(1);' && break; sleep 2; done; exec docker-entrypoint.sh apache2-foreground"
            ],
            "env": [
                "WORDPRESS_DB_HOST=127.0.0.1:3306",
                "WORDPRESS_DB_USER=wordpress",
                "WORDPRESS_DB_PASSWORD=wordpress-pass",
                "WORDPRESS_DB_NAME=wordpress"
            ],
            "workDir": "",
            "resource": {
                "cpu": "200m",
                "memory": "512Mi"
            }
        },
        {
            "name": "mysql",
            "image": "mysql:8.4",
            "args": [],
            "env": [
                "MYSQL_DATABASE=wordpress",
                "MYSQL_USER=wordpress",
                "MYSQL_PASSWORD=wordpress-pass",
                "MYSQL_ROOT_PASSWORD=root-pass"
            ],
            "workDir": "",
            "resource": {
                "cpu": "200m",
                "memory": "768Mi"
            }
        }
    ]
}
```
