package store

import (
	"path/filepath"
	"testing"

	"sandboxd-o/sandboxd-let/model"
)

func TestFileStore_CRUD(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore err=%v", err)
	}

	sb := &model.Sandbox{ID: "sbx-1", Namespace: "n"}
	if err := s.Save(sb); err != nil {
		t.Fatalf("Save err=%v", err)
	}

	got, err := s.Load("sbx-1")
	if err != nil {
		t.Fatalf("Load err=%v", err)
	}

	if got.ID != "sbx-1" {
		t.Fatalf("Load ID=%q", got.ID)
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("List err=%v", err)
	}

	if len(list) != 1 {
		t.Fatalf("List len=%d", len(list))
	}

	if err := s.Delete("sbx-1"); err != nil {
		t.Fatalf("Delete err=%v", err)
	}

	if _, err := s.Load("sbx-1"); err == nil {
		t.Fatal("expected load error after delete")
	}

	if _, err := NewFileStore(filepath.Join(dir, "a", "b")); err != nil {
		t.Fatalf("nested NewFileStore err=%v", err)
	}
}
