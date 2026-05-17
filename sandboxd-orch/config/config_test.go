package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_DefaultAndYaml(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "apiserver.yaml")
	raw := "listenAddress: ':18082'\nnodes:\n  - name: n1\n    ip: 127.0.0.1\n    port: 18080\n"
	if err := os.WriteFile(cfgFile, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ORCH_CONFIG_PATH", cfgFile)
	t.Setenv("ORCH_SQLITE_PATH", filepath.Join(dir, "orch.db"))
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load err=%v", err)
	}

	if cfg.HTTPAddr != ":18082" {
		t.Fatalf("HTTPAddr=%q", cfg.HTTPAddr)
	}

	if len(cfg.Bootstrap.Nodes) != 1 || cfg.Bootstrap.Nodes[0].Name != "n1" {
		t.Fatalf("unexpected bootstrap nodes: %+v", cfg.Bootstrap.Nodes)
	}
}

func TestLoadBootstrap_MissingAndInvalid(t *testing.T) {
	b, err := loadBootstrap(filepath.Join(t.TempDir(), "no.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}

	if b.ListenAddress != "" || len(b.Nodes) != 0 {
		t.Fatalf("unexpected bootstrap: %+v", b)
	}

	invalid := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(invalid, []byte("::invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := loadBootstrap(invalid); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoad_SandboxOpTimeout(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "apiserver.yaml")
	if err := os.WriteFile(cfgFile, []byte("listenAddress: ':18082'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ORCH_CONFIG_PATH", cfgFile)
	t.Setenv("ORCH_SQLITE_PATH", filepath.Join(dir, "orch.db"))
	t.Setenv("ORCH_SANDBOX_OP_TIMEOUT", "45s")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load err=%v", err)
	}

	if cfg.SandboxOpTimeout != 45*time.Second {
		t.Fatalf("SandboxOpTimeout=%s", cfg.SandboxOpTimeout)
	}

	t.Setenv("ORCH_SANDBOX_OP_TIMEOUT", "0s")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load err=%v", err)
	}

	if cfg.SandboxOpTimeout != 60*time.Second {
		t.Fatalf("SandboxOpTimeout default=%s", cfg.SandboxOpTimeout)
	}
}

func TestLoad_StatusSyncDefaultsAndOverride(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "apiserver.yaml")
	if err := os.WriteFile(cfgFile, []byte("listenAddress: ':18082'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ORCH_CONFIG_PATH", cfgFile)
	t.Setenv("ORCH_SQLITE_PATH", filepath.Join(dir, "orch.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load err=%v", err)
	}

	if cfg.StatusSyncInterval != 20*time.Second {
		t.Fatalf("StatusSyncInterval=%s", cfg.StatusSyncInterval)
	}

	if cfg.StatusSyncTimeout != 5*time.Second {
		t.Fatalf("StatusSyncTimeout=%s", cfg.StatusSyncTimeout)
	}

	if cfg.StatusSyncBatchSize != 50 {
		t.Fatalf("StatusSyncBatchSize=%d", cfg.StatusSyncBatchSize)
	}

	if cfg.StatusSyncMaxParallel != 4 {
		t.Fatalf("StatusSyncMaxParallel=%d", cfg.StatusSyncMaxParallel)
	}

	t.Setenv("ORCH_STATUS_SYNC_INTERVAL", "45s")
	t.Setenv("ORCH_STATUS_SYNC_TIMEOUT", "9s")
	t.Setenv("ORCH_STATUS_SYNC_BATCH_SIZE", "7")
	t.Setenv("ORCH_STATUS_SYNC_MAX_PARALLEL", "3")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load err=%v", err)
	}

	if cfg.StatusSyncInterval != 45*time.Second {
		t.Fatalf("StatusSyncInterval override=%s", cfg.StatusSyncInterval)
	}

	if cfg.StatusSyncBatchSize != 7 {
		t.Fatalf("StatusSyncBatchSize override=%d", cfg.StatusSyncBatchSize)
	}

	if cfg.StatusSyncTimeout != 9*time.Second {
		t.Fatalf("StatusSyncTimeout override=%s", cfg.StatusSyncTimeout)
	}

	if cfg.StatusSyncMaxParallel != 3 {
		t.Fatalf("StatusSyncMaxParallel override=%d", cfg.StatusSyncMaxParallel)
	}
}

func TestLoad_StatusSyncFallbackOnInvalidValues(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "apiserver.yaml")
	if err := os.WriteFile(cfgFile, []byte("listenAddress: ':18082'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ORCH_CONFIG_PATH", cfgFile)
	t.Setenv("ORCH_SQLITE_PATH", filepath.Join(dir, "orch.db"))
	t.Setenv("ORCH_STATUS_SYNC_INTERVAL", "0s")
	t.Setenv("ORCH_STATUS_SYNC_TIMEOUT", "0s")
	t.Setenv("ORCH_STATUS_SYNC_BATCH_SIZE", "0")
	t.Setenv("ORCH_STATUS_SYNC_MAX_PARALLEL", "0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load err=%v", err)
	}

	if cfg.StatusSyncInterval != 20*time.Second {
		t.Fatalf("StatusSyncInterval fallback=%s", cfg.StatusSyncInterval)
	}
	if cfg.StatusSyncTimeout != 5*time.Second {
		t.Fatalf("StatusSyncTimeout fallback=%s", cfg.StatusSyncTimeout)
	}
	if cfg.StatusSyncBatchSize != 50 {
		t.Fatalf("StatusSyncBatchSize fallback=%d", cfg.StatusSyncBatchSize)
	}
	if cfg.StatusSyncMaxParallel != 4 {
		t.Fatalf("StatusSyncMaxParallel fallback=%d", cfg.StatusSyncMaxParallel)
	}
}
