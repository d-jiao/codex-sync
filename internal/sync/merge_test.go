package sync

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	idxA  = `{"id":"t1","thread_name":"alpha","updated_at":"2026-09-01T10:00:00.000000Z"}` + "\n"
	idxB  = `{"id":"t2","thread_name":"beta","updated_at":"2026-09-02T10:00:00.000000Z"}` + "\n"
	idxA2 = `{"id":"t1","thread_name":"alpha renamed","updated_at":"2026-09-03T10:00:00.000000Z"}` + "\n"
)

func TestIsMergeablePath(t *testing.T) {
	for p, want := range map[string]bool{"session_index.jsonl": true, "history.jsonl": true, "sessions/x.jsonl": false, "config.toml": false} {
		if got := IsMergeablePath(p); got != want {
			t.Errorf("IsMergeablePath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestMergeSessionIndexUnionKeepsLatestPerID(t *testing.T) {
	got, err := MergeJSONL(SessionIndexFile, []byte(idxA+idxB), []byte(idxA2))
	if err != nil {
		t.Fatal(err)
	}
	want := idxB + idxA2 // ascending updated_at: t2 (Sep 2) then t1 (Sep 3)
	if string(got) != want {
		t.Errorf("merge =\n%s\nwant\n%s", got, want)
	}
}

func TestMergeSessionIndexIsIdempotent(t *testing.T) {
	once, _ := MergeJSONL(SessionIndexFile, []byte(idxA), []byte(idxB))
	twice, _ := MergeJSONL(SessionIndexFile, once, []byte(idxB))
	self, _ := MergeJSONL(SessionIndexFile, once, once)
	if !bytes.Equal(once, twice) || !bytes.Equal(once, self) {
		t.Errorf("merge is not idempotent:\n%s\n%s\n%s", once, twice, self)
	}
}

func TestMergeSessionIndexKeepsUnparsableLines(t *testing.T) {
	got, _ := MergeJSONL(SessionIndexFile, []byte(idxA+"not json\n"), []byte("not json\n"+idxB))
	if !strings.HasSuffix(string(got), "not json\n") || strings.Count(string(got), "not json") != 1 {
		t.Errorf("unparsable lines must be kept once, at the end:\n%s", got)
	}
}

func TestMergeHistoryOrdersByTsAndDedupes(t *testing.T) {
	h1 := `{"session_id":"s","ts":1700000002,"text":"second"}` + "\n"
	h2 := `{"session_id":"s","ts":1700000001,"text":"first"}` + "\n"
	got, err := MergeJSONL(HistoryFile, []byte(h1), []byte(h2+h1))
	if err != nil {
		t.Fatal(err)
	}
	if want := h2 + h1; string(got) != want {
		t.Errorf("merge =\n%s\nwant\n%s", got, want)
	}
}

func TestMergeHistoryLinesWithoutTsComeFirst(t *testing.T) {
	noTS := `{"session_id":"s","text":"legacy"}` + "\n"
	h := `{"session_id":"s","ts":1700000001,"text":"first"}` + "\n"
	got, _ := MergeJSONL(HistoryFile, []byte(h), []byte(noTS))
	if want := noTS + h; string(got) != want {
		t.Errorf("merge =\n%s\nwant\n%s", got, want)
	}
}

func TestMergeJSONLRejectsOtherPaths(t *testing.T) {
	if _, err := MergeJSONL("sessions/x.jsonl", nil, nil); err == nil {
		t.Error("expected an error for a non-mergeable path")
	}
}

func TestHashBytesMatchesHashFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := HashFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := hashBytes([]byte("abc")); got != fromFile {
		t.Errorf("hashBytes = %s, HashFile = %s", got, fromFile)
	}
}

func TestWriteFileAtomicCreatesParentAndSetsPerm(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b", "f.jsonl")
	if err := writeFileAtomic(p, []byte("x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("perm = %o, want 600", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(p)); len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

func TestMergeHistoryKeepsUnparsableLinesAtEnd(t *testing.T) {
	h := `{"session_id":"s","ts":1700000001,"text":"first"}` + "\n"
	got, err := MergeJSONL(HistoryFile, []byte("garbage\n"+h), []byte("garbage\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := h + "garbage\n"; string(got) != want {
		t.Errorf("merge =\n%s\nwant\n%s", got, want)
	}
}
