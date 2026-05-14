---
title: Orchestrator API
nav_order: 2
---

Base URL: `http://localhost:8082`

## Health

- `GET /healthz`
- Success: `200 OK`

**Response**

```json
{
    "ok": true
}
```

## Node APIs

### Register Node

- `POST /api/v1/nodes/register`
- Success: `200 OK`
- Failure: `400 Bad Request` (validation)
- Failure: `500 Internal Server Error` (storage/internal)

**Request**

```json
{
    "name": "sandbox-node-1",
    "ip": "192.168.0.3",
    "port": 8080
}
```

**Response**

```json
{
    "node": {
        "name": "sandbox-node-1",
        "ip": "192.168.0.3",
        "port": 8080,
        "state": "Unknown",
        "source": "api",
        "success_streak": 0,
        "failure_streak": 0,
        "created_at": "2026-05-14T01:00:00Z",
        "updated_at": "2026-05-14T01:00:00Z",
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
        },
        "sandboxd_base_url": "http://192.168.0.3:8080"
    }
}
```

### List Nodes

- `GET /api/v1/nodes`
- Success: `200 OK`

**Response**

```json
{
    "items": [
        {
            "name": "sandbox-node-1",
            "ip": "192.168.0.3",
            "port": 8080,
            "state": "Ready",
            "source": "config",
            "last_error": "",
            "success_streak": 3,
            "failure_streak": 0,
            "created_at": "2026-05-14T01:00:00Z",
            "updated_at": "2026-05-14T01:02:00Z",
            "last_heartbeat": "2026-05-14T01:02:00Z",
            "resources": {
                "capacity_cpu_milli": 8000,
                "capacity_memory_bytes": 33554432000,
                "allocatable_cpu_milli": 7200,
                "allocatable_memory_bytes": 30198988800,
                "used_cpu_milli": 900,
                "used_memory_bytes": 1073741824,
                "available_cpu_milli": 6300,
                "available_memory_bytes": 29125246976,
                "max_alloc_percent": 90,
                "updated_at": "2026-05-14T01:02:00Z"
            },
            "sandboxd_base_url": "http://192.168.0.3:8080"
        }
    ]
}
```

### Get Node

- `GET /api/v1/nodes/{name}`
- Success: `200 OK`
- Failure: `404 Not Found`

**Response**

```json
{
    "node": {
        "name": "sandbox-node-1",
        "ip": "192.168.0.3",
        "port": 8080,
        "state": "Ready",
        "source": "config",
        "success_streak": 3,
        "failure_streak": 0,
        "sandboxd_base_url": "http://192.168.0.3:8080"
    }
}
```

### Delete Node

- `DELETE /api/v1/nodes/{name}`
- Success: `200 OK`
- Failure: `400 Bad Request` (validation)
- Failure: `500 Internal Server Error` (storage/internal)

**Response**

```json
{
    "deleted": "sandbox-node-1"
}
```

### Trigger Heartbeat Probe

- `POST /api/v1/nodes/{name}/heartbeat`
- Success: `200 OK`
- Failure: `404 Not Found`

**Response**

```json
{
    "node": {
        "name": "sandbox-node-1",
        "ip": "192.168.0.3",
        "port": 8080,
        "state": "Ready",
        "sandboxd_base_url": "http://192.168.0.3:8080"
    },
    "heartbeat": "ok",
    "resources": {
        "capacity_cpu_milli": 8000,
        "capacity_memory_bytes": 33554432000,
        "allocatable_cpu_milli": 7200,
        "allocatable_memory_bytes": 30198988800,
        "used_cpu_milli": 900,
        "used_memory_bytes": 1073741824,
        "available_cpu_milli": 6300,
        "available_memory_bytes": 29125246976,
        "max_alloc_percent": 90,
        "updated_at": "2026-05-14T01:02:00Z"
    },
    "heartbeat_error": "",
    "status_error": ""
}
```

## Sandboxd Proxy APIs

### List Sandboxes

- `GET /api/v1/nodes/{name}/sandboxes?cursor=<id>&limit=<n>`

### Get Sandbox

- `GET /api/v1/nodes/{name}/sandboxes/{id}`

### Create Sandbox

- `POST /api/v1/nodes/{name}/sandboxes`
- Body: same as sandboxd `CreateSandboxRequest`

### Delete Sandbox

- `DELETE /api/v1/nodes/{name}/sandboxes/{id}`

### Get Container Logs

- `GET /api/v1/nodes/{name}/sandboxes/{id}/containers/{container}/logs?cursor=<offset>&limit=<n>`

### Reconcile

- `POST /api/v1/nodes/{name}/reconcile`

## Bootstrap Config

Config file path: `ORCH_CONFIG_PATH` (default: `configs/apiserver.yaml`)

```yaml
listenAddress: ':8082'
nodes:
    - name: 'sandbox-node-1'
      ip: '127.0.0.1'
      port: 8080
```

Notes:

- Nodes in config are re-registered on every orchestrator start.
- If a config node was deleted via API, restart adds it back.
- If duplicate node names exist in config, orchestrator logs a warning and applies the last entry.

## Environment Variables

- `ORCH_HTTP_ADDR` (default: from config `listenAddress`, fallback `:8082`)
- `ORCH_CONFIG_PATH` (default: `configs/apiserver.yaml`)
- `ORCH_SQLITE_PATH` (default: `build/orchestrator.db`)
- `ORCH_HEARTBEAT_INTERVAL` (default: `10s`)
- `ORCH_NODE_PROBE_TIMEOUT` (default: `3s`)
- `ORCH_HEARTBEAT_PARALLEL` (default: `false`)
- `ORCH_HEARTBEAT_MAX_PARALLEL` (default: `4`, minimum: `1`)
- `ORCH_RESOURCE_SYNC_INTERVAL` (default: `30s`)
- `ORCH_RESOURCE_PERSIST_MIN_INTERVAL` (default: `30s`)
- `ORCH_RESOURCE_PERSIST_MAX_INTERVAL` (default: `5m`)
- `ORCH_READY_SUCCESS_THRESHOLD` (default: `2`)
- `ORCH_NOTREADY_FAILURE_THRESHOLD` (default: `2`)
- `ORCH_SHUTDOWN_TIMEOUT` (default: `5s`)
- `ORCH_LOG_DIR` (optional; when set, JSON logs are also written to rotating hourly files)
- `ORCH_LOG_FILE_PREFIX` (default: `orchestrator`)
- `APP_ENV` (optional; added to `app` log field, e.g. `orchestrator:prod`)

## Common Error Response

```json
{
    "error": "error message"
}
```
