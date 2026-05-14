package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sandboxd-o/orchestrator/types"

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
	const q = `
CREATE TABLE IF NOT EXISTS nodes (
  name TEXT PRIMARY KEY,
  ip TEXT NOT NULL,
  port INTEGER NOT NULL,
  source TEXT NOT NULL,
  state TEXT NOT NULL,
  success_streak INTEGER NOT NULL DEFAULT 0,
  failure_streak INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  last_heartbeat_at TEXT,
  capacity_cpu_milli INTEGER NOT NULL DEFAULT 0,
  capacity_memory_bytes INTEGER NOT NULL DEFAULT 0,
  allocatable_cpu_milli INTEGER NOT NULL DEFAULT 0,
  allocatable_memory_bytes INTEGER NOT NULL DEFAULT 0,
  used_cpu_milli INTEGER NOT NULL DEFAULT 0,
  used_memory_bytes INTEGER NOT NULL DEFAULT 0,
  available_cpu_milli INTEGER NOT NULL DEFAULT 0,
  available_memory_bytes INTEGER NOT NULL DEFAULT 0,
  max_alloc_percent INTEGER NOT NULL DEFAULT 0,
  resource_updated_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
`
	if _, err := db.Exec(q); err != nil {
		return err
	}

	alter := []string{
		`ALTER TABLE nodes ADD COLUMN capacity_cpu_milli INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN capacity_memory_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN allocatable_cpu_milli INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN allocatable_memory_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN used_cpu_milli INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN used_memory_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN available_cpu_milli INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN available_memory_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN max_alloc_percent INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN resource_updated_at TEXT`,
	}
	for _, stmt := range alter {
		if _, err := db.Exec(stmt); err != nil && !isDuplicateColumnErr(err) {
			return err
		}
	}

	return nil
}

func (r *SQLiteNodeRepo) UpsertNode(ctx context.Context, name, ip string, port int, source string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const q = `
INSERT INTO nodes(name, ip, port, source, state, success_streak, failure_streak, last_error, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 0, 0, '', ?, ?)
ON CONFLICT(name) DO UPDATE SET
  ip=excluded.ip,
  port=excluded.port,
  source=excluded.source,
  updated_at=excluded.updated_at;
`
	_, err := r.db.ExecContext(ctx, q, name, ip, port, source, string(types.NodeStateUnknown), now, now)
	return err
}

func (r *SQLiteNodeRepo) DeleteNode(ctx context.Context, name string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM nodes WHERE name=?`, name)
	return err
}

func (r *SQLiteNodeRepo) GetNode(ctx context.Context, name string) (*types.Node, error) {
	const q = `SELECT name, ip, port, source, state, success_streak, failure_streak, last_error, last_heartbeat_at, capacity_cpu_milli, capacity_memory_bytes, allocatable_cpu_milli, allocatable_memory_bytes, used_cpu_milli, used_memory_bytes, available_cpu_milli, available_memory_bytes, max_alloc_percent, resource_updated_at, created_at, updated_at FROM nodes WHERE name=?`
	return scanOne(r.db.QueryRowContext(ctx, q, name))
}

func (r *SQLiteNodeRepo) ListNodes(ctx context.Context) ([]types.Node, error) {
	const q = `SELECT name, ip, port, source, state, success_streak, failure_streak, last_error, last_heartbeat_at, capacity_cpu_milli, capacity_memory_bytes, allocatable_cpu_milli, allocatable_memory_bytes, used_cpu_milli, used_memory_bytes, available_cpu_milli, available_memory_bytes, max_alloc_percent, resource_updated_at, created_at, updated_at FROM nodes ORDER BY name ASC`
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

		n.SandboxdBaseURL = fmt.Sprintf("http://%s:%d", n.IP, n.Port)
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

	const q = `UPDATE nodes SET state=?, success_streak=?, failure_streak=?, last_error=?, last_heartbeat_at=?, updated_at=? WHERE name=?`
	_, err := r.db.ExecContext(ctx, q, string(state), successStreak, failureStreak, lastError, beat, updated, name)
	return err
}

func (r *SQLiteNodeRepo) UpdateNodeResources(ctx context.Context, name string, res types.NodeResources) error {
	updated := time.Now().UTC().Format(time.RFC3339Nano)
	var resUpdated any
	if res.UpdatedAt != nil {
		resUpdated = res.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}

	const q = `UPDATE nodes SET
capacity_cpu_milli=?,
capacity_memory_bytes=?,
allocatable_cpu_milli=?,
allocatable_memory_bytes=?,
used_cpu_milli=?,
used_memory_bytes=?,
available_cpu_milli=?,
available_memory_bytes=?,
max_alloc_percent=?,
resource_updated_at=?,
updated_at=?
WHERE name=?`
	_, err := r.db.ExecContext(ctx, q,
		res.CapacityCPUMilli,
		res.CapacityMemoryBytes,
		res.AllocatableCPUMilli,
		res.AllocatableMemory,
		res.UsedCPUMilli,
		res.UsedMemoryBytes,
		res.AvailableCPUMilli,
		res.AvailableMemory,
		res.MaxAllocPercent,
		resUpdated,
		updated,
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

	n.SandboxdBaseURL = fmt.Sprintf("http://%s:%d", n.IP, n.Port)
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

func isDuplicateColumnErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate column name")
}
