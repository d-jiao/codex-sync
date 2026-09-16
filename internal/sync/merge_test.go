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

// mergeBothWays merges (local, remote) and (remote, local) and fails unless
// both devices would end up with identical bytes.
func mergeBothWays(t *testing.T, relPath, a, b string) string {
	t.Helper()
	ab, err := MergeJSONL(relPath, []byte(a), []byte(b))
	if err != nil {
		t.Fatal(err)
	}
	ba, err := MergeJSONL(relPath, []byte(b), []byte(a))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ab, ba) {
		t.Fatalf("merge depends on arrival order:\n(a,b) =\n%s\n(b,a) =\n%s", ab, ba)
	}
	return string(ab)
}

func TestMergeSessionIndexTieBreaksOnBytesNotArrivalOrder(t *testing.T) {
	// Same id, same updated_at, different content: neither is "later", so the
	// byte-wise greater line wins on both devices.
	lo := `{"id":"t1","thread_name":"alpha","updated_at":"2026-09-01T10:00:00.000000Z"}` + "\n"
	hi := `{"id":"t1","thread_name":"omega","updated_at":"2026-09-01T10:00:00.000000Z"}` + "\n"
	if got := mergeBothWays(t, SessionIndexFile, lo, hi); got != hi {
		t.Errorf("merge =\n%s\nwant\n%s", got, hi)
	}
}

func TestMergeSessionIndexOrdersEqualTimesByID(t *testing.T) {
	// Equal instants written differently compare equal as times; the output
	// order must then come from the id, not from which device merged.
	t2 := `{"id":"t2","thread_name":"beta","updated_at":"2026-09-01T10:00:00Z"}` + "\n"
	t1 := `{"id":"t1","thread_name":"alpha","updated_at":"2026-09-01T10:00:00.000000Z"}` + "\n"
	if got := mergeBothWays(t, SessionIndexFile, t2, t1); got != t1+t2 {
		t.Errorf("merge =\n%s\nwant\n%s", got, t1+t2)
	}
}

func TestMergeHistoryTieBreaksOnBytesNotArrivalOrder(t *testing.T) {
	a := `{"session_id":"s","ts":1700000001,"text":"zeta"}` + "\n"
	b := `{"session_id":"s","ts":1700000001,"text":"alpha"}` + "\n"
	if got := mergeBothWays(t, HistoryFile, a, b); got != b+a {
		t.Errorf("merge =\n%s\nwant\n%s", got, b+a)
	}
}

func TestMergeUnparsableLinesConvergeOnBothDevices(t *testing.T) {
	h := `{"session_id":"s","ts":1700000001,"text":"first"}` + "\n"
	got := mergeBothWays(t, HistoryFile, h+"garbage-b\n", "garbage-a\n")
	if want := h + "garbage-a\ngarbage-b\n"; got != want {
		t.Errorf("merge =\n%s\nwant\n%s", got, want)
	}
	idx := mergeBothWays(t, SessionIndexFile, idxA+"not json 2\n", "not json 1\n"+idxA)
	if want := idxA + "not json 1\nnot json 2\n"; idx != want {
		t.Errorf("merge =\n%s\nwant\n%s", idx, want)
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
