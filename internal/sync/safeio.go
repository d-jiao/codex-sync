package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxConflictSidecars bounds the search for an unused sidecar name. Hitting it
// means something is generating conflicts in a loop, which is worth an error
// rather than an unbounded scan.
const maxConflictSidecars = 1000

// safeLocalPath resolves rel underneath root and refuses anything that could
// leave the tree: absolute paths, ".." escapes, a component that is not a
// directory, and any symlink among the components — including the destination
// itself. Push already skips symlinks when scanning (GetLocalFiles), so pull
// writing through one would be both inconsistent and a way for a planted link
// to redirect remote content outside the Codex home.
func safeLocalPath(root, rel string) (string, error) {
	cleanRoot := filepath.Clean(root)
	rel = filepath.FromSlash(rel)
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("refusing to write %q: not a relative path inside %s", rel, cleanRoot)
	}

	full := filepath.Clean(filepath.Join(cleanRoot, rel))
	if full == cleanRoot || !strings.HasPrefix(full, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to write outside %s: %s", cleanRoot, rel)
	}

	suffix, err := filepath.Rel(cleanRoot, full)
	if err != nil {
		return "", fmt.Errorf("refusing to write outside %s: %s", cleanRoot, rel)
	}

	parts := strings.Split(suffix, string(filepath.Separator))
	current := cleanRoot
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			// Everything below this point will be created by us.
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to inspect %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to write through symlink %s", current)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("refusing to write under %s: not a directory", current)
		}
		if i == len(parts)-1 && info.IsDir() {
			return "", fmt.Errorf("refusing to replace directory %s with a file", current)
		}
	}

	return full, nil
}

// writeLocalFileAtomic writes data to rel under root through a temp file in the
// destination directory, so a reader never observes a half-written file and an
// interrupted pull leaves the previous content in place. The path is validated
// before and after directory creation.
func writeLocalFileAtomic(root, rel string, data []byte, perm os.FileMode) error {
	full, err := safeLocalPath(root, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	// Re-validate: MkdirAll follows symlinks, so confirm nothing was swapped
	// underneath us between the first check and the write.
	if _, err := safeLocalPath(root, rel); err != nil {
		return err
	}
	return writeFileAtomic(full, data, perm)
}

// uniqueConflictPath returns the relative path for a `<rel>.conflict.<stamp>`
// sidecar that does not exist yet. Two conflicts on the same file within one
// second would otherwise share a name and the second would erase the first.
func uniqueConflictPath(root, rel string, at time.Time) (string, error) {
	base := rel + ".conflict." + at.Format("20060102-150405")
	for i := 0; i < maxConflictSidecars; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		full, err := safeLocalPath(root, candidate)
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(full); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", fmt.Errorf("failed to inspect %s: %w", full, err)
		}
	}
	return "", fmt.Errorf("too many conflict sidecars for %s; resolve them with 'codex-sync conflicts'", rel)
}
