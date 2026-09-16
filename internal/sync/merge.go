package sync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mergeable files are shared single files that every machine appends to. A
// last-writer-wins upload would silently drop the other machine's entries, so
// pull merges them (union) instead of writing a conflict sidecar (spec §6).
const (
	// SessionIndexFile holds one {id, thread_name, updated_at} object per line;
	// the entry with the latest updated_at wins per id.
	SessionIndexFile = "session_index.jsonl"
	// HistoryFile holds one {session_id, ts, text} object per line; distinct
	// lines are kept and ordered by ts.
	HistoryFile = "history.jsonl"
)

// IsMergeablePath reports whether relPath is merged on pull rather than
// downloaded or conflicted.
func IsMergeablePath(relPath string) bool {
	return relPath == SessionIndexFile || relPath == HistoryFile
}

// MergeJSONL returns the union of two copies of a mergeable file. The result is
// deterministic, idempotent and symmetric: merging a file with itself, merging
// twice, or merging the same two copies in either order yields identical bytes,
// so both devices converge. Ties (same id and equal updated_at; equal ts) are
// broken on the raw line bytes, never on which copy was local. Lines that are
// not JSON objects are preserved verbatim (once each) after the merged entries,
// in byte order.
func MergeJSONL(relPath string, local, remote []byte) ([]byte, error) {
	switch relPath {
	case SessionIndexFile:
		return mergeSessionIndex(local, remote), nil
	case HistoryFile:
		return mergeHistory(local, remote), nil
	}
	return nil, fmt.Errorf("%s is not a mergeable file", relPath)
}

type jsonlLine struct {
	raw []byte
	obj map[string]json.RawMessage // nil when the line is not a JSON object
}

func splitJSONL(data []byte) []jsonlLine {
	var lines []jsonlLine
	for _, raw := range bytes.Split(data, []byte("\n")) {
		raw = bytes.TrimRight(raw, "\r")
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		l := jsonlLine{raw: raw}
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) == nil {
			l.obj = obj
		}
		lines = append(lines, l)
	}
	return lines
}

func joinJSONL(lines [][]byte) []byte {
	if len(lines) == 0 {
		return nil
	}
	return append(bytes.Join(lines, []byte("\n")), '\n')
}

// rawString decodes a JSON string value; non-strings are returned as their raw text.
func rawString(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return string(v)
}

// laterTimestamp reports whether a is strictly later than b. RFC 3339 values
// are compared as times; anything else falls back to string comparison.
func laterTimestamp(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339Nano, a)
	tb, errB := time.Parse(time.RFC3339Nano, b)
	if errA == nil && errB == nil {
		return ta.After(tb)
	}
	return a > b
}

func mergeSessionIndex(local, remote []byte) []byte {
	type entry struct {
		raw     []byte
		updated string
	}
	byID := map[string]entry{}
	var ids []string
	var other [][]byte
	seenOther := map[string]bool{}

	for _, l := range append(splitJSONL(local), splitJSONL(remote)...) {
		if l.obj == nil || l.obj["id"] == nil {
			if key := string(l.raw); !seenOther[key] {
				seenOther[key] = true
				other = append(other, l.raw)
			}
			continue
		}
		id := rawString(l.obj["id"])
		updated := rawString(l.obj["updated_at"])
		cur, ok := byID[id]
		if !ok {
			ids = append(ids, id)
			byID[id] = entry{l.raw, updated}
			continue
		}
		// Latest updated_at wins; on an exact tie the byte-wise greater line
		// does, so the winner does not depend on which copy was local.
		if laterTimestamp(updated, cur.updated) ||
			(!laterTimestamp(cur.updated, updated) && bytes.Compare(l.raw, cur.raw) > 0) {
			byID[id] = entry{l.raw, updated}
		}
	}

	// Ascending updated_at; equal instants (however written) order by id.
	sort.Slice(ids, func(i, j int) bool {
		a, b := byID[ids[i]].updated, byID[ids[j]].updated
		if laterTimestamp(b, a) {
			return true
		}
		if laterTimestamp(a, b) {
			return false
		}
		return ids[i] < ids[j]
	})

	out := make([][]byte, 0, len(ids)+len(other))
	for _, id := range ids {
		out = append(out, byID[id].raw)
	}
	out = append(out, sortedLines(other)...)
	return joinJSONL(out)
}

// sortedLines orders distinct raw lines byte-wise so their position never
// depends on which copy they came from.
func sortedLines(lines [][]byte) [][]byte {
	sort.Slice(lines, func(i, j int) bool { return bytes.Compare(lines[i], lines[j]) < 0 })
	return lines
}

func mergeHistory(local, remote []byte) []byte {
	type entry struct {
		raw   []byte
		ts    float64
		hasTS bool
	}
	var entries []entry
	seen := map[string]bool{}
	var other [][]byte
	seenOther := map[string]bool{}

	for _, l := range append(splitJSONL(local), splitJSONL(remote)...) {
		if l.obj == nil {
			if key := string(l.raw); !seenOther[key] {
				seenOther[key] = true
				other = append(other, l.raw)
			}
			continue
		}
		key := string(l.raw)
		if seen[key] {
			continue
		}
		seen[key] = true
		e := entry{raw: l.raw}
		if l.obj["ts"] != nil {
			if v, err := strconv.ParseFloat(strings.Trim(string(l.obj["ts"]), `"`), 64); err == nil {
				e.ts, e.hasTS = v, true
			}
		}
		entries = append(entries, e)
	}

	// Ascending ts; equal ts (and lines without one) order by their bytes so
	// both devices produce the same file. Entries are distinct, so bytes
	// never tie.
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.hasTS != b.hasTS {
			return !a.hasTS // lines without a parsable ts sort first
		}
		if a.hasTS && a.ts != b.ts {
			return a.ts < b.ts
		}
		return bytes.Compare(a.raw, b.raw) < 0
	})

	out := make([][]byte, 0, len(entries)+len(other))
	for _, e := range entries {
		out = append(out, e.raw)
	}
	out = append(out, sortedLines(other)...)
	return joinJSONL(out)
}

// hashBytes returns the hex SHA-256 of data, the same format HashFile records in state.
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeFileAtomic writes data to path via a temp file in the same directory and
// a rename, so a crash never leaves a half-written file behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	return os.Rename(tmpName, path)
}
