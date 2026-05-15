package http

import (
	"path/filepath"
	"testing"
	"time"

	"sandboxd-o/orchestrator/config"
	"sandboxd-o/orchestrator/service"
	"sandboxd-o/pkg/logging"
)

func TestNewRouter(t *testing.T) {
	svc, err := service.New(config.Config{
		SQLitePath:               filepath.Join(t.TempDir(), "orch.db"),
		ProbeTimeout:             time.Second,
		HeartbeatInterval:        time.Second,
		ResourceSyncInterval:     time.Second,
		ResourcePersistMinInt:    time.Second,
		ResourcePersistMaxInt:    time.Minute,
		ReadySuccessThreshold:    2,
		NotReadyFailureThreshold: 2,
		ShutdownTimeout:          time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	lg, _ := logging.New(logging.Config{}, logging.Options{Service: "test"})
	r := NewRouter(svc, lg)
	if r == nil {
		t.Fatal("router is nil")
	}
}
