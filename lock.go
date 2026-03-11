package devport

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type FileLock struct {
	path string
	file *os.File
}

func NewFileLock(path string) *FileLock {
	return &FileLock{path: path}
}

func (l *FileLock) TryLock() (bool, error) {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return false, fmt.Errorf("create lock dir: %w", err)
	}

	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if err == syscall.EWOULDBLOCK {
			return false, nil
		}
		return false, err
	}

	l.file = file
	return true, nil
}

func (l *FileLock) Unlock() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func LockHeld(path string) (bool, error) {
	lock := NewFileLock(path)
	ok, err := lock.TryLock()
	if err != nil {
		return false, err
	}
	if ok {
		if err := lock.Unlock(); err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}
