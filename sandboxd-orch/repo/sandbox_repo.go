package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"sandboxd-o/sandboxd-orch/types"
)

type SandboxRepo interface {
	CreateSandbox(ctx context.Context, sbx types.Sandbox) error
	GetSandbox(ctx context.Context, id string) (*types.Sandbox, error)
	ListSandboxes(ctx context.Context) ([]types.Sandbox, error)
	UpdateSandboxStatus(ctx context.Context, id string, st types.SandboxStatus) error
	DeleteSandbox(ctx context.Context, id string) error
	ReserveSandboxPortsAndSchedule(ctx context.Context, sandboxID, nodeName string, ports []types.SandboxPortAssign) error
	ReleaseSandboxPorts(ctx context.Context, sandboxID string) error
	NodeUsedHostPorts(ctx context.Context, nodeName string) (map[int]struct{}, error)
}

func (r *SQLiteNodeRepo) CreateSandbox(ctx context.Context, sbx types.Sandbox) error {
	now := time.Now().UTC()
	if sbx.CreatedAt.IsZero() {
		sbx.CreatedAt = now
	}
	sbx.UpdatedAt = now

	specRaw, err := json.Marshal(sbx.Spec)
	if err != nil {
		return err
	}
	stRaw, err := json.Marshal(sbx.Status)
	if err != nil {
		return err
	}

	const q = `INSERT INTO sandboxes(id, spec_json, status_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)`
	_, err = r.db.ExecContext(ctx, q, sbx.ID, string(specRaw), string(stRaw), sbx.CreatedAt.Format(time.RFC3339Nano), sbx.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (r *SQLiteNodeRepo) GetSandbox(ctx context.Context, id string) (*types.Sandbox, error) {
	const q = `SELECT id, spec_json, status_json, created_at, updated_at FROM sandboxes WHERE id=?`
	row := r.db.QueryRowContext(ctx, q, id)
	return scanSandboxRow(row)
}

func (r *SQLiteNodeRepo) ListSandboxes(ctx context.Context) ([]types.Sandbox, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, spec_json, status_json, created_at, updated_at FROM sandboxes ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]types.Sandbox, 0)
	for rows.Next() {
		sbx, err := scanSandboxRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sbx)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

func (r *SQLiteNodeRepo) UpdateSandboxStatus(ctx context.Context, id string, st types.SandboxStatus) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, `UPDATE sandboxes SET status_json=?, updated_at=? WHERE id=?`, string(raw), time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func (r *SQLiteNodeRepo) DeleteSandbox(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sandbox_ports WHERE sandbox_id=?`, id); err != nil {
		return err
	}

	_, err := r.db.ExecContext(ctx, `DELETE FROM sandboxes WHERE id=?`, id)
	return err
}

func (r *SQLiteNodeRepo) ReserveSandboxPortsAndSchedule(ctx context.Context, sandboxID, nodeName string, ports []types.SandboxPortAssign) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, p := range ports {
		var cnt int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM sandbox_ports WHERE node_name=? AND host_port=?`, nodeName, p.HostPort).Scan(&cnt); err != nil {
			return err
		}

		if cnt > 0 {
			return fmt.Errorf("host port already reserved on node %s: %d", nodeName, p.HostPort)
		}
	}

	for _, p := range ports {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sandbox_ports(sandbox_id, node_name, host_port, container_port, protocol, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			sandboxID, nodeName, p.HostPort, p.ContainerPort, p.Protocol, time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			return err
		}
	}

	sbx, err := r.getSandboxTx(ctx, tx, sandboxID)
	if err != nil {
		return err
	}
	sbx.Status.NodeName = nodeName
	sbx.Status.Phase = types.SandboxPhaseScheduled
	sbx.Status.AssignedPorts = append([]types.SandboxPortAssign(nil), ports...)

	stRaw, err := json.Marshal(sbx.Status)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE sandboxes SET status_json=?, updated_at=? WHERE id=?`, string(stRaw), time.Now().UTC().Format(time.RFC3339Nano), sandboxID); err != nil {
		return err
	}

	return tx.Commit()
}

func (r *SQLiteNodeRepo) ReleaseSandboxPorts(ctx context.Context, sandboxID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sandbox_ports WHERE sandbox_id=?`, sandboxID)
	return err
}

func (r *SQLiteNodeRepo) NodeUsedHostPorts(ctx context.Context, nodeName string) (map[int]struct{}, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT host_port FROM sandbox_ports WHERE node_name=?`, nodeName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int]struct{}{}

	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}

		out[p] = struct{}{}
	}

	return out, rows.Err()
}

func (r *SQLiteNodeRepo) getSandboxTx(ctx context.Context, tx *sql.Tx, id string) (*types.Sandbox, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, spec_json, status_json, created_at, updated_at FROM sandboxes WHERE id=?`, id)
	return scanSandboxRow(row)
}

type sandboxScanner interface{ Scan(dest ...any) error }

func scanSandboxRow(s sandboxScanner) (*types.Sandbox, error) {
	sbx, err := scanSandboxFromScanner(s)
	if err != nil {
		return nil, err
	}

	return &sbx, nil
}

func scanSandboxRows(rows *sql.Rows) (types.Sandbox, error) { return scanSandboxFromScanner(rows) }

func scanSandboxFromScanner(s sandboxScanner) (types.Sandbox, error) {
	var (
		sbx                    types.Sandbox
		specRaw, statusRaw     string
		createdRaw, updatedRaw string
	)

	if err := s.Scan(&sbx.ID, &specRaw, &statusRaw, &createdRaw, &updatedRaw); err != nil {
		return sbx, err
	}

	if err := json.Unmarshal([]byte(specRaw), &sbx.Spec); err != nil {
		return sbx, err
	}

	if err := json.Unmarshal([]byte(statusRaw), &sbx.Status); err != nil {
		return sbx, err
	}

	ct, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return sbx, err
	}

	ut, err := time.Parse(time.RFC3339Nano, updatedRaw)
	if err != nil {
		return sbx, err
	}

	sbx.CreatedAt = ct
	sbx.UpdatedAt = ut

	return sbx, nil
}
