package devport

import (
	"path/filepath"
	"testing"
)

func TestFileLockAndLockHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "svc.lock")
	lock := NewFileLock(path)
	acquired, err := lock.TryLock()
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if !acquired {
		t.Fatalf("expected first lock acquisition to succeed")
	}

	held, err := LockHeld(path)
	if err != nil {
		t.Fatalf("LockHeld: %v", err)
	}
	if !held {
		t.Fatalf("expected lock to be held")
	}

	second := NewFileLock(path)
	acquired, err = second.TryLock()
	if err != nil {
		t.Fatalf("second TryLock: %v", err)
	}
	if acquired {
		t.Fatalf("expected second lock acquisition to fail")
	}

	if err := lock.Unlock(); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	held, err = LockHeld(path)
	if err != nil {
		t.Fatalf("LockHeld after unlock: %v", err)
	}
	if held {
		t.Fatalf("expected lock to be released")
	}
}
