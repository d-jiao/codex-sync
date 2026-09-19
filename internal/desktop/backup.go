package desktop

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Backup copies every top-level SQLite database of the Codex home (with WAL and
// SHM files), the desktop catalog and the session index into a new timestamped
// directory under root and returns its path. The engine run touches more than
// state_5.sqlite — a different engine version may migrate any of them — so all
// are kept. Files that do not exist are skipped.
func Backup(baseDir, root string) (string, error) {
	dest, err := newBackupDir(root)
	if err != nil {
		return "", err
	}
	var files []string
	for _, pattern := range []string{"*.sqlite*", "sqlite/codex-dev.db*"} {
		matches, err := filepath.Glob(filepath.Join(baseDir, filepath.FromSlash(pattern)))
		if err != nil {
			return "", err
		}
		files = append(files, matches...)
	}
	files = append(files, filepath.Join(baseDir, "session_index.jsonl"))
	for _, src := range files {
		rel, err := filepath.Rel(baseDir, src)
		if err != nil {
			return "", err
		}
		if err := copyFile(src, filepath.Join(dest, rel)); err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return dest, nil
}

// newBackupDir creates root/db-backup-<timestamp>[-N]/sqlite, never reusing a
// directory from an earlier run in the same second.
func newBackupDir(root string) (string, error) {
	stamp := time.Now().Format("20060102-150405")
	for n := 0; ; n++ {
		dest := filepath.Join(root, "db-backup-"+stamp)
		if n > 0 {
			dest += fmt.Sprintf("-%d", n)
		}
		if err := os.Mkdir(dest, 0o700); err != nil {
			if os.IsExist(err) {
				continue
			}
			if err := os.MkdirAll(root, 0o700); err != nil {
				return "", err
			}
			if err := os.Mkdir(dest, 0o700); err != nil {
				return "", err
			}
		}
		if err := os.Mkdir(filepath.Join(dest, "sqlite"), 0o700); err != nil {
			return "", err
		}
		return dest, nil
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
