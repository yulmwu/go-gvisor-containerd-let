package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"sandboxd-o/sandboxd-orch/config"
	"sandboxd-o/sandboxd-orch/types"
)

func TestBootstrapNodes(t *testing.T) {
	cfg := config.Config{
		SQLitePath:               filepath.Join(t.TempDir(), "orch.db"),
		ProbeTimeout:             50 * time.Millisecond,
		HeartbeatInterval:        time.Second,
		ResourceSyncInterval:     time.Second,
		ResourcePersistMinInt:    time.Second,
		ResourcePersistMaxInt:    time.Minute,
		ReadySuccessThreshold:    2,
		NotReadyFailureThreshold: 2,
		Bootstrap: types.APIServerConfig{Nodes: []types.StaticNode{
			{Name: "n1", IP: "127.0.0.1", Port: 8081},
			{Name: "bad", IP: "not-ip", Port: 8081},
		}},
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.BootstrapNodes(context.Background()); err != nil {
		t.Fatalf("BootstrapNodes err=%v", err)
	}

	list, err := s.ListNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(list) != 1 || list[0].Name != "n1" {
		t.Fatalf("unexpected nodes: %+v", list)
	}
}
