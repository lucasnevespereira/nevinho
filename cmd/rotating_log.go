package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	terminalLogMaxBytes = 8 * 1024 * 1024
	terminalLogBackups  = 3
)

// rotatingLog caps chat.log while keeping a few recent backups. log.Logger
// serializes writes, and the mutex also makes Close safe against a late write.
type rotatingLog struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
}

func openRotatingLog(path string, maxBytes int64, backups int) (*rotatingLog, error) {
	if maxBytes <= 0 || backups < 1 {
		return nil, fmt.Errorf("invalid log rotation limits")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	r := &rotatingLog{path: path, maxBytes: maxBytes, backups: backups}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingLog) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.file = f
	r.size = info.Size()
	return nil
}

func (r *rotatingLog) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return 0, os.ErrClosed
	}
	originalLen := len(p)
	if int64(len(p)) > r.maxBytes {
		// A single oversized log entry must not defeat the size cap.
		p = p[len(p)-int(r.maxBytes):]
	}
	if r.size > 0 && r.size+int64(len(p)) > r.maxBytes {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	if err != nil {
		return n, err
	}
	return originalLen, nil
}

func (r *rotatingLog) rotate() (err error) {
	file := r.file
	r.file = nil
	// Reopen the active path even if a backup operation fails, so a later
	// write can retry rotation instead of losing the logger permanently.
	defer func() {
		if openErr := r.open(); openErr != nil {
			err = errors.Join(err, openErr)
		}
	}()
	if err := file.Close(); err != nil {
		return err
	}
	for i := r.backups; i >= 1; i-- {
		src := r.path
		if i > 1 {
			src = fmt.Sprintf("%s.%d", r.path, i-1)
		}
		dst := fmt.Sprintf("%s.%d", r.path, i)
		if i == r.backups {
			if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (r *rotatingLog) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}
