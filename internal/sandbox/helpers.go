package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func (s *Service) acquireSandboxLock(sandboxID string) (func(), error) {
	if sandboxID == "" {
		return func() {}, fmt.Errorf("sandbox id is required")
	}

	lockPath := filepath.Join(s.lockDir, sandboxID+".lock")
	fd, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}, err
	}

	deadline := time.Now().Add(DefaultLockWaitTimeout)
	for {
		if err := unix.Flock(int(fd.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return func() {
				_ = unix.Flock(int(fd.Fd()), unix.LOCK_UN)
				_ = fd.Close()
			}, nil
		}

		if time.Now().After(deadline) {
			_ = fd.Close()
			return func() {}, fmt.Errorf("lock timeout for %s", sandboxID)
		}

		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Service) isSandboxLockHeld(sandboxID string) bool {
	if sandboxID == "" {
		return false
	}

	lockPath := filepath.Join(s.lockDir, sandboxID+".lock")
	fd, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false
	}
	defer fd.Close()

	if err := unix.Flock(int(fd.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return true
	}

	_ = unix.Flock(int(fd.Fd()), unix.LOCK_UN)
	return false
}
