package repo

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"sandboxd-o/sandboxd-orch/types"

	_ "modernc.org/sqlite"
)

type NodeRepo interface {
	Close() error
	UpsertNode(ctx context.Context, name, ip string, port int, source string) error
	DeleteNode(ctx context.Context, name string) error
	GetNode(ctx context.Context, name string) (*types.Node, error)
	ListNodes(ctx context.Context) ([]types.Node, error)
	UpdateHeartbeat(ctx context.Context, name string, state types.NodeState, successStreak, failureStreak int, lastError string, beatAt *time.Time) error
	UpdateNodeResources(ctx context.Context, name string, res types.NodeResources) error
	AdjustNodeResourceUsage(ctx context.Context, name string, cpuMilliDelta, memBytesDelta int64) error
}

type SQLiteNodeRepo struct {
	db *sql.DB
}

func NewSQLite(path string) (*SQLiteNodeRepo, error) {
	if path != "" && path != ":memory:" {
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				if dir := filepath.Dir(path); dir != "" && dir != "." {
					if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
						return nil, fmt.Errorf("create sqlite dir: %w", mkErr)
					}
				}

				f, createErr := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
				if createErr != nil {
					return nil, fmt.Errorf("create sqlite db file: %w", createErr)
				}
				_ = f.Close()
				slog.Warn("sqlite db file not found; created new file", slog.String("path", path))
			} else {
				return nil, fmt.Errorf("stat sqlite db file: %w", err)
			}
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SQLiteNodeRepo{db: db}, nil
}

func (r *SQLiteNodeRepo) Close() error {
	if r == nil || r.db == nil {
		return nil
	}

	return r.db.Close()
}

func migrate(db *sql.DB) error {
	const qNodes = `
CREATE TABLE IF NOT EXISTS nodes (
  name TEXT PRIMARY KEY,
  ip TEXT NOT NULL,
  port INTEGER NOT NULL,
  source TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
`
	const qStatus = `
CREATE TABLE IF NOT EXISTS node_status (
  name TEXT PRIMARY KEY,
  state TEXT NOT NULL,
  success_streak INTEGER NOT NULL DEFAULT 0,
  failure_streak INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  last_heartbeat_at TEXT,
  updated_at TEXT NOT NULL
);
`
	const qResources = `
CREATE TABLE IF NOT EXISTS node_resources (
  name TEXT PRIMARY KEY,
  capacity_cpu_milli INTEGER NOT NULL DEFAULT 0,
  capacity_memory_bytes INTEGER NOT NULL DEFAULT 0,
  allocatable_cpu_milli INTEGER NOT NULL DEFAULT 0,
  allocatable_memory_bytes INTEGER NOT NULL DEFAULT 0,
  used_cpu_milli INTEGER NOT NULL DEFAULT 0,
  used_memory_bytes INTEGER NOT NULL DEFAULT 0,
  available_cpu_milli INTEGER NOT NULL DEFAULT 0,
  available_memory_bytes INTEGER NOT NULL DEFAULT 0,
  max_alloc_percent INTEGER NOT NULL DEFAULT 0,
  external_ip TEXT NOT NULL DEFAULT '',
  resource_updated_at TEXT,
  updated_at TEXT NOT NULL
);
`
	const qSandboxes = `
CREATE TABLE IF NOT EXISTS sandboxes (
  id TEXT PRIMARY KEY,
  spec_json TEXT NOT NULL,
  status_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
`
	const qSandboxPorts = `
CREATE TABLE IF NOT EXISTS sandbox_ports (
  sandbox_id TEXT NOT NULL,
  node_name TEXT NOT NULL,
  host_port INTEGER NOT NULL,
  container_port INTEGER NOT NULL,
  protocol TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (node_name, host_port),
  FOREIGN KEY (sandbox_id) REFERENCES sandboxes(id) ON DELETE CASCADE
);
`
	if _, err := db.Exec(qNodes); err != nil {
		return err
	}
	if _, err := db.Exec(qStatus); err != nil {
		return err
	}
	if _, err := db.Exec(qResources); err != nil {
		return err
	}
	if _, err := db.Exec(qSandboxes); err != nil {
		return err
	}
	if _, err := db.Exec(qSandboxPorts); err != nil {
		return err
	}

	return nil
}

func (r *SQLiteNodeRepo) UpsertNode(ctx context.Context, name, ip string, port int, source string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const qNode = `
INSERT INTO nodes(name, ip, port, source, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
  ip=excluded.ip,
  port=excluded.port,
  source=excluded.source,
  updated_at=excluded.updated_at;
`
	const qStatus = `
INSERT INTO node_status(name, state, success_streak, failure_streak, last_error, updated_at)
VALUES (?, ?, 0, 0, '', ?)
ON CONFLICT(name) DO NOTHING;`
	const qRes = `
INSERT INTO node_resources(name, updated_at)
VALUES (?, ?)
ON CONFLICT(name) DO NOTHING;`

	_, err := r.db.ExecContext(ctx, qNode, name, ip, port, source, now, now)
	if err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, qStatus, name, string(types.NodeStateUnknown), now); err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, qRes, name, now)
	return err
}

func (r *SQLiteNodeRepo) DeleteNode(ctx context.Context, name string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sandbox_ports WHERE node_name=?`, name); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM node_resources WHERE name=?`, name); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM node_status WHERE name=?`, name); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM nodes WHERE name=?`, name)
	return err
}

func (r *SQLiteNodeRepo) GetNode(ctx context.Context, name string) (*types.Node, error) {
	const q = `SELECT n.name, n.ip, n.port, n.source,
COALESCE(s.state, 'Unknown'), COALESCE(s.success_streak,0), COALESCE(s.failure_streak,0), COALESCE(s.last_error,''), s.last_heartbeat_at,
COALESCE(r.capacity_cpu_milli,0), COALESCE(r.capacity_memory_bytes,0), COALESCE(r.allocatable_cpu_milli,0), COALESCE(r.allocatable_memory_bytes,0),
COALESCE(r.used_cpu_milli,0), COALESCE(r.used_memory_bytes,0), COALESCE(r.available_cpu_milli,0), COALESCE(r.available_memory_bytes,0), COALESCE(r.max_alloc_percent,0), r.resource_updated_at,
COALESCE(r.external_ip,''),
n.created_at, n.updated_at
FROM nodes n
LEFT JOIN node_status s ON s.name=n.name
LEFT JOIN node_resources r ON r.name=n.name
WHERE n.name=?`
	return scanOne(r.db.QueryRowContext(ctx, q, name))
}

func (r *SQLiteNodeRepo) ListNodes(ctx context.Context) ([]types.Node, error) {
	const q = `SELECT n.name, n.ip, n.port, n.source,
COALESCE(s.state, 'Unknown'), COALESCE(s.success_streak,0), COALESCE(s.failure_streak,0), COALESCE(s.last_error,''), s.last_heartbeat_at,
COALESCE(r.capacity_cpu_milli,0), COALESCE(r.capacity_memory_bytes,0), COALESCE(r.allocatable_cpu_milli,0), COALESCE(r.allocatable_memory_bytes,0),
COALESCE(r.used_cpu_milli,0), COALESCE(r.used_memory_bytes,0), COALESCE(r.available_cpu_milli,0), COALESCE(r.available_memory_bytes,0), COALESCE(r.max_alloc_percent,0), r.resource_updated_at,
COALESCE(r.external_ip,''),
n.created_at, n.updated_at
FROM nodes n
LEFT JOIN node_status s ON s.name=n.name
LEFT JOIN node_resources r ON r.name=n.name
ORDER BY n.name ASC`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]types.Node, 0)
	for rows.Next() {
		n, err := scanRow(rows)
		if err != nil {
			return nil, err
		}

		n.SbxletBaseURL = fmt.Sprintf("http://%s:%d", n.IP, n.Port)
		out = append(out, n)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

func (r *SQLiteNodeRepo) UpdateHeartbeat(ctx context.Context, name string, state types.NodeState, successStreak, failureStreak int, lastError string, beatAt *time.Time) error {
	updated := time.Now().UTC().Format(time.RFC3339Nano)
	var beat any
	if beatAt != nil {
		beat = beatAt.UTC().Format(time.RFC3339Nano)
	}

	const q = `INSERT INTO node_status(name, state, success_streak, failure_streak, last_error, last_heartbeat_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
state=excluded.state,
success_streak=excluded.success_streak,
failure_streak=excluded.failure_streak,
last_error=excluded.last_error,
last_heartbeat_at=excluded.last_heartbeat_at,
updated_at=excluded.updated_at`
	_, err := r.db.ExecContext(ctx, q, name, string(state), successStreak, failureStreak, lastError, beat, updated)
	return err
}

func (r *SQLiteNodeRepo) UpdateNodeResources(ctx context.Context, name string, res types.NodeResources) error {
	updated := time.Now().UTC().Format(time.RFC3339Nano)
	var resUpdated any
	if res.UpdatedAt != nil {
		resUpdated = res.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}

	const q = `INSERT INTO node_resources(
name,
capacity_cpu_milli,
capacity_memory_bytes,
allocatable_cpu_milli,
allocatable_memory_bytes,
used_cpu_milli,
used_memory_bytes,
available_cpu_milli,
available_memory_bytes,
max_alloc_percent,
external_ip,
resource_updated_at,
updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
capacity_cpu_milli=excluded.capacity_cpu_milli,
capacity_memory_bytes=excluded.capacity_memory_bytes,
allocatable_cpu_milli=excluded.allocatable_cpu_milli,
allocatable_memory_bytes=excluded.allocatable_memory_bytes,
used_cpu_milli=excluded.used_cpu_milli,
used_memory_bytes=excluded.used_memory_bytes,
available_cpu_milli=excluded.available_cpu_milli,
available_memory_bytes=excluded.available_memory_bytes,
max_alloc_percent=excluded.max_alloc_percent,
external_ip=excluded.external_ip,
resource_updated_at=excluded.resource_updated_at,
updated_at=excluded.updated_at`
	_, err := r.db.ExecContext(ctx, q,
		name,
		res.CapacityCPUMilli,
		res.CapacityMemoryBytes,
		res.AllocatableCPUMilli,
		res.AllocatableMemory,
		res.UsedCPUMilli,
		res.UsedMemoryBytes,
		res.AvailableCPUMilli,
		res.AvailableMemory,
		res.MaxAllocPercent,
		res.ExternalIP,
		resUpdated,
		updated,
	)
	return err
}

func (r *SQLiteNodeRepo) AdjustNodeResourceUsage(ctx context.Context, name string, cpuMilliDelta, memBytesDelta int64) error {
	const q = `
UPDATE node_resources
SET
  used_cpu_milli = MAX(0, used_cpu_milli + ?),
  used_memory_bytes = MAX(0, used_memory_bytes + ?),
  available_cpu_milli = MAX(0, allocatable_cpu_milli - MAX(0, used_cpu_milli + ?)),
  available_memory_bytes = MAX(0, allocatable_memory_bytes - MAX(0, used_memory_bytes + ?)),
  updated_at = ?
WHERE name = ?`
	_, err := r.db.ExecContext(
		ctx,
		q,
		cpuMilliDelta,
		memBytesDelta,
		cpuMilliDelta,
		memBytesDelta,
		time.Now().UTC().Format(time.RFC3339Nano),
		name,
	)

	return err
}

type scanner interface{ Scan(dest ...any) error }

func scanOne(s scanner) (*types.Node, error) {
	n, err := scanRowScanner(s)
	if err != nil {
		return nil, err
	}

	n.SbxletBaseURL = fmt.Sprintf("http://%s:%d", n.IP, n.Port)
	return &n, nil
}

func scanRow(rows *sql.Rows) (types.Node, error) { return scanRowScanner(rows) }

func scanRowScanner(s scanner) (types.Node, error) {
	var n types.Node
	var state string
	var created, updated string
	var beat sql.NullString
	var resUpdated sql.NullString
	if err := s.Scan(
		&n.Name, &n.IP, &n.Port, &n.Source, &state,
		&n.SuccessStreak, &n.FailureStreak, &n.LastError, &beat,
		&n.Resources.CapacityCPUMilli, &n.Resources.CapacityMemoryBytes,
		&n.Resources.AllocatableCPUMilli, &n.Resources.AllocatableMemory,
		&n.Resources.UsedCPUMilli, &n.Resources.UsedMemoryBytes,
		&n.Resources.AvailableCPUMilli, &n.Resources.AvailableMemory,
		&n.Resources.MaxAllocPercent, &resUpdated,
		&n.Resources.ExternalIP,
		&created, &updated,
	); err != nil {
		return n, err
	}
	n.State = types.NodeState(state)

	ct, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return n, err
	}

	ut, err := time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return n, err
	}

	n.CreatedAt = ct
	n.UpdatedAt = ut

	if beat.Valid && beat.String != "" {
		bt, err := time.Parse(time.RFC3339Nano, beat.String)
		if err != nil {
			return n, err
		}

		n.LastHeartbeat = &bt
	}

	if resUpdated.Valid && resUpdated.String != "" {
		rt, err := time.Parse(time.RFC3339Nano, resUpdated.String)
		if err != nil {
			return n, err
		}
		n.Resources.UpdatedAt = &rt
	}

	return n, nil
}
