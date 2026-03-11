package team

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// FileLock provides cross-process advisory locking using flock(2).
// It is safe for concurrent use across OS processes accessing the same file.
type FileLock struct {
	path string
	file *os.File
}

// NewFileLock creates a lock backed by the given file path.
// The lock file is created if it doesn't exist.
func NewFileLock(path string) *FileLock {
	return &FileLock{path: path + ".lock"}
}

// Lock acquires an exclusive lock. Blocks until available.
func (l *FileLock) Lock() error {
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open lock file %s: %w", l.path, err)
	}

	l.file = f
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		l.file = nil
		return fmt.Errorf("flock %s: %w", l.path, err)
	}

	return nil
}

// Unlock releases the lock.
func (l *FileLock) Unlock() error {
	if l.file == nil {
		return nil
	}

	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		_ = l.file.Close()
		l.file = nil
		return fmt.Errorf("funlock %s: %w", l.path, err)
	}

	err := l.file.Close()
	l.file = nil
	if err != nil {
		return fmt.Errorf("close lock file %s: %w", l.path, err)
	}

	return nil
}
