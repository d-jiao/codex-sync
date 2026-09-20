package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSafeLocalPathRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "sessions", "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "afile"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		"../outside.txt",
		"sessions/../../outside.txt",
		"sessions/link/escaped.jsonl",
		"afile/under-a-file.txt",
		"",
		".",
	} {
		if _, err := safeLocalPath(root, rel); err == nil {
			t.Errorf("safeLocalPath(%q) = nil error, want rejection", rel)
		}
	}

	got, err := safeLocalPath(root, "sessions/ok.jsonl")
	if err != nil {
		t.Fatalf("safeLocalPath rejected a legitimate path: %v", err)
	}
	if want := filepath.Join(root, "sessions", "ok.jsonl"); got != want {
		t.Errorf("safeLocalPath = %q, want %q", got, want)
	}
}

func TestWriteLocalFileAtomicWillNotWriteThroughASymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "escape.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := writeLocalFileAtomic(root, "escape.txt", []byte("overwritten"), 0600)
	if err == nil {
		t.Fatal("expected writeLocalFileAtomic to refuse a symlinked destination")
	}
	if got, _ := os.ReadFile(target); string(got) != "original" {
		t.Errorf("file outside the root was modified: %q", got)
	}
}

func TestWriteLocalFileAtomicReplacesContentInPlace(t *testing.T) {
	root := t.TempDir()
	if err := writeLocalFileAtomic(root, "sessions/a.jsonl", []byte("one"), 0600); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeLocalFileAtomic(root, "sessions/a.jsonl", []byte("two"), 0600); err != nil {
		t.Fatalf("second write: %v", err)
	}

	full := filepath.Join(root, "sessions", "a.jsonl")
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "two" {
		t.Errorf("content = %q, want two", data)
	}
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}

	// No temp files may be left behind for the next scan to pick up.
	entries, err := os.ReadDir(filepath.Dir(full))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("leftover files in the destination directory: %v", names)
	}
}

func TestUniqueConflictPathDoesNotCollideWithinOneSecond(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

	first, err := uniqueConflictPath(root, "sessions/a.jsonl", at)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, ".conflict.") {
		t.Fatalf("expected a .conflict. sidecar name, got %q", first)
	}
	if err := writeLocalFileAtomic(root, first, []byte("remote one"), 0600); err != nil {
		t.Fatal(err)
	}

	second, err := uniqueConflictPath(root, "sessions/a.jsonl", at)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("second sidecar reused the name %q", first)
	}
	if err := writeLocalFileAtomic(root, second, []byte("remote two"), 0600); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(first))); string(got) != "remote one" {
		t.Errorf("the first sidecar was overwritten: %q", got)
	}
}

// A symlinked directory inside the Codex home must not let a remote object
// land outside it. Push already skips symlinks when scanning, so pull refuses
// them too.
func TestPullRefusesToWriteThroughASymlinkedDirectory(t *testing.T) {
	env := setupTestEnv(t)
	outside := t.TempDir()

	if err := os.MkdirAll(filepath.Join(env.claudeDir, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(env.claudeDir, "sessions", "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	seedRemote(t, env, "sessions/link/rollout.jsonl", `{"payload":"remote"}`)

	result, err := env.syncer.Pull(t.Context())
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(result.Downloaded) != 0 {
		t.Errorf("expected no downloads, got %v", result.Downloaded)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected one error naming the symlink, got %v", result.Errors)
	}
	if !strings.Contains(result.Errors[0].Error(), "symlink") {
		t.Errorf("error should name the symlink: %v", result.Errors[0])
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("pull wrote outside the Codex home: %d entries", len(entries))
	}
}
