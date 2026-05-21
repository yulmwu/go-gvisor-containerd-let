package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"sandboxd-o/sandboxd-orch/types"

	_ "modernc.org/sqlite"
)

var ErrExternalConflict = errors.New("external object conflict")

type NodeRepo interface {
	Close() error
	UpsertNode(ctx context.Context, id, ip string, port int, source string) error
	DeleteNode(ctx context.Context, id string) error
	GetNode(ctx context.Context, id string) (*types.Node, error)
	ListNodes(ctx context.Context) ([]types.Node, error)
	UpdateHeartbeat(ctx context.Context, id string, state types.NodeState, successStreak, failureStreak int, lastError string, beatAt *time.Time) error
	UpdateNodeResources(ctx context.Context, id string, res types.NodeResources) error
	AdjustNodeResourceUsage(ctx context.Context, id string, cpuMilliDelta, memBytesDelta int64) error
	SetNodeExternal(ctx context.Context, externalID, nodeID, external string) error
	DeleteNodeExternal(ctx context.Context, nodeID string) error
	DeleteExternal(ctx context.Context, id string) error
	ListExternals(ctx context.Context) ([]types.External, error)
	GetExternal(ctx context.Context, id string) (*types.External, error)
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
  external TEXT NOT NULL DEFAULT '(none)',
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
	const qNodeExternals = `
CREATE TABLE IF NOT EXISTS node_externals (
  external_id TEXT PRIMARY KEY,
  node_id TEXT NOT NULL UNIQUE,
  external TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (node_id) REFERENCES nodes(name) ON DELETE CASCADE
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
	if _, err := db.Exec(qNodeExternals); err != nil {
		return err
	}

	return nil
}

func (r *SQLiteNodeRepo) UpsertNode(ctx context.Context, id, ip string, port int, source string) error {
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

	_, err := r.db.ExecContext(ctx, qNode, id, ip, port, source, now, now)
	if err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, qStatus, id, string(types.NodeStateUnknown), now); err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, qRes, id, now)
	return err
}

func (r *SQLiteNodeRepo) DeleteNode(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sandbox_ports WHERE node_name=?`, id); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM node_resources WHERE name=?`, id); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM node_status WHERE name=?`, id); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM nodes WHERE name=?`, id)
	return err
}

func (r *SQLiteNodeRepo) GetNode(ctx context.Context, id string) (*types.Node, error) {
	const q = `SELECT n.name, n.ip, n.port, n.source,
COALESCE(s.state, 'Unknown'), COALESCE(s.success_streak,0), COALESCE(s.failure_streak,0), COALESCE(s.last_error,''), s.last_heartbeat_at,
COALESCE(r.capacity_cpu_milli,0), COALESCE(r.capacity_memory_bytes,0), COALESCE(r.allocatable_cpu_milli,0), COALESCE(r.allocatable_memory_bytes,0),
COALESCE(r.used_cpu_milli,0), COALESCE(r.used_memory_bytes,0), COALESCE(r.available_cpu_milli,0), COALESCE(r.available_memory_bytes,0), COALESCE(r.max_alloc_percent,0), r.resource_updated_at,
COALESCE(ne.external, r.external,'(none)'),
n.created_at, n.updated_at
FROM nodes n
LEFT JOIN node_status s ON s.name=n.name
LEFT JOIN node_resources r ON r.name=n.name
LEFT JOIN node_externals ne ON ne.node_id=n.name
WHERE n.name=?`
	return scanOne(r.db.QueryRowContext(ctx, q, id))
}

func (r *SQLiteNodeRepo) ListNodes(ctx context.Context) ([]types.Node, error) {
	const q = `SELECT n.name, n.ip, n.port, n.source,
COALESCE(s.state, 'Unknown'), COALESCE(s.success_streak,0), COALESCE(s.failure_streak,0), COALESCE(s.last_error,''), s.last_heartbeat_at,
COALESCE(r.capacity_cpu_milli,0), COALESCE(r.capacity_memory_bytes,0), COALESCE(r.allocatable_cpu_milli,0), COALESCE(r.allocatable_memory_bytes,0),
COALESCE(r.used_cpu_milli,0), COALESCE(r.used_memory_bytes,0), COALESCE(r.available_cpu_milli,0), COALESCE(r.available_memory_bytes,0), COALESCE(r.max_alloc_percent,0), r.resource_updated_at,
COALESCE(ne.external, r.external,'(none)'),
n.created_at, n.updated_at
FROM nodes n
LEFT JOIN node_status s ON s.name=n.name
LEFT JOIN node_resources r ON r.name=n.name
LEFT JOIN node_externals ne ON ne.node_id=n.name
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

func (r *SQLiteNodeRepo) UpdateHeartbeat(ctx context.Context, id string, state types.NodeState, successStreak, failureStreak int, lastError string, beatAt *time.Time) error {
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
	_, err := r.db.ExecContext(ctx, q, id, string(state), successStreak, failureStreak, lastError, beat, updated)
	return err
}

func (r *SQLiteNodeRepo) UpdateNodeResources(ctx context.Context, id string, res types.NodeResources) error {
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
external,
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
external=excluded.external,
resource_updated_at=excluded.resource_updated_at,
updated_at=excluded.updated_at`
	_, err := r.db.ExecContext(ctx, q,
		id,
		res.CapacityCPUMilli,
		res.CapacityMemoryBytes,
		res.AllocatableCPUMilli,
		res.AllocatableMemory,
		res.UsedCPUMilli,
		res.UsedMemoryBytes,
		res.AvailableCPUMilli,
		res.AvailableMemory,
		res.MaxAllocPercent,
		res.External,
		resUpdated,
		updated,
	)
	return err
}

func (r *SQLiteNodeRepo) AdjustNodeResourceUsage(ctx context.Context, id string, cpuMilliDelta, memBytesDelta int64) error {
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
		id,
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
		&n.ID, &n.IP, &n.Port, &n.Source, &state,
		&n.SuccessStreak, &n.FailureStreak, &n.LastError, &beat,
		&n.Resources.CapacityCPUMilli, &n.Resources.CapacityMemoryBytes,
		&n.Resources.AllocatableCPUMilli, &n.Resources.AllocatableMemory,
		&n.Resources.UsedCPUMilli, &n.Resources.UsedMemoryBytes,
		&n.Resources.AvailableCPUMilli, &n.Resources.AvailableMemory,
		&n.Resources.MaxAllocPercent, &resUpdated,
		&n.Resources.External,
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

func (r *SQLiteNodeRepo) SetNodeExternal(ctx context.Context, externalID, nodeID, external string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var byIDNode string
	err = tx.QueryRowContext(ctx, `SELECT node_id FROM node_externals WHERE external_id=?`, externalID).Scan(&byIDNode)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if byIDNode != "" && byIDNode != nodeID {
		return fmt.Errorf("%w: external id %s already bound to node %s", ErrExternalConflict, externalID, byIDNode)
	}

	var byNodeID string
	err = tx.QueryRowContext(ctx, `SELECT external_id FROM node_externals WHERE node_id=?`, nodeID).Scan(&byNodeID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if byNodeID != "" && byNodeID != externalID {
		return fmt.Errorf("%w: node %s already bound to external id %s", ErrExternalConflict, nodeID, byNodeID)
	}

	if byIDNode != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE node_externals SET external=?, updated_at=? WHERE external_id=?`, external, now, externalID); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `INSERT INTO node_externals(external_id, node_id, external, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, externalID, nodeID, external, now, now); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE node_resources SET external=?, updated_at=? WHERE name=?`, external, now, nodeID); err != nil {
		return err
	}

	return tx.Commit()
}

func (r *SQLiteNodeRepo) DeleteNodeExternal(ctx context.Context, nodeID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM node_externals WHERE node_id=?`, nodeID); err != nil {
		return err
	}

	_, err := r.db.ExecContext(ctx, `UPDATE node_resources SET external='(none)', updated_at=? WHERE name=?`, time.Now().UTC().Format(time.RFC3339Nano), nodeID)
	return err
}

func (r *SQLiteNodeRepo) DeleteExternal(ctx context.Context, id string) error {
	var nodeID string
	if err := r.db.QueryRowContext(ctx, `SELECT node_id FROM node_externals WHERE external_id=?`, id).Scan(&nodeID); err != nil {
		return err
	}

	if _, err := r.db.ExecContext(ctx, `DELETE FROM node_externals WHERE external_id=?`, id); err != nil {
		return err
	}

	_, err := r.db.ExecContext(ctx, `UPDATE node_resources SET external='(none)', updated_at=? WHERE name=?`, time.Now().UTC().Format(time.RFC3339Nano), nodeID)
	return err
}

func (r *SQLiteNodeRepo) ListExternals(ctx context.Context) ([]types.External, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT external_id, node_id, external, created_at, updated_at FROM node_externals ORDER BY external_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]types.External, 0)
	for rows.Next() {
		var ex types.External
		var created, updated string
		if err := rows.Scan(&ex.ID, &ex.NodeID, &ex.External, &created, &updated); err != nil {
			return nil, err
		}

		ex.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}

		ex.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, err
		}

		out = append(out, ex)
	}

	return out, rows.Err()
}

func (r *SQLiteNodeRepo) GetExternal(ctx context.Context, id string) (*types.External, error) {
	var ex types.External
	var created, updated string
	if err := r.db.QueryRowContext(ctx, `SELECT external_id, node_id, external, created_at, updated_at FROM node_externals WHERE external_id=?`, id).
		Scan(&ex.ID, &ex.NodeID, &ex.External, &created, &updated); err != nil {
		return nil, err
	}

	createdAt, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, err
	}

	updatedAt, err := time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return nil, err
	}

	ex.CreatedAt = createdAt
	ex.UpdatedAt = updatedAt
	return &ex, nil
}
