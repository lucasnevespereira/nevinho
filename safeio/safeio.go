// Package safeio provides atomic, crash safe file writes.
//
// On a long running VPS process, a kill signal or kernel panic mid write
// can leave a half written file behind. WriteFile streams to a sibling
// temp file, fsyncs, then renames over the destination. POSIX guarantees
// the rename is atomic, so a reader after a crash sees either the old
// file or the new one. Never an empty or truncated mix.
package safeio

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFile writes data to path atomically.
//
// If the destination already exists, its file mode is preserved.
// Otherwise perm is used. The parent directory is created if missing.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	mode := perm
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".write.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}
