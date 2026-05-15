package logging

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewAndWrite(t *testing.T) {
	dir := t.TempDir()
	l, err := New(Config{Dir: dir, FilePrefix: "app"}, Options{Service: "svc", Env: "test"})
	if err != nil {
		t.Fatalf("New err=%v", err)
	}
	defer l.Close()

	if _, err := l.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write err=%v", err)
	}
	l.Info("x")
	if appName("svc", "test") != "svc:test" {
		t.Fatal("unexpected appName")
	}

	files, _ := filepath.Glob(filepath.Join(dir, "app-*.log"))
	if len(files) == 0 {
		t.Fatalf("expected log file in %s", dir)
	}
}

func TestHelpers(t *testing.T) {
	now := time.Now().UTC()
	if !sameHour(now, now.Add(20*time.Minute)) {
		t.Fatal("expected same hour")
	}
	if sameHour(now, now.Add(2*time.Hour)) {
		t.Fatal("expected different hour")
	}
	PanicLogger(nil, "x")
	_ = os.Setenv("DUMMY", "1")
}
