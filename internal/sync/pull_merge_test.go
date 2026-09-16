package sync

import (
	"context"
	"strings"
	"testing"
)

func TestPullMergesSessionIndexFromBothDevices(t *testing.T) {
	a := setupTestEnv(t)
	b := newPeer(t, a)
	ctx := context.Background()
	writeFile(t, a.claudeDir, "session_index.jsonl", idxA)
	writeFile(t, b.claudeDir, "session_index.jsonl", idxB)

	if _, err := a.syncer.Push(ctx); err != nil {
		t.Fatalf("A push: %v", err)
	}
	res, err := b.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("B pull: %v", err)
	}
	if len(res.Merged) != 1 || res.Merged[0] != SessionIndexFile {
		t.Fatalf("expected one merge, got %+v", res)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("mergeable file must not conflict: %v", res.Conflicts)
	}
	got := readFile(t, b.claudeDir, "session_index.jsonl")
	if !strings.Contains(got, `"t1"`) || !strings.Contains(got, `"t2"`) {
		t.Fatalf("union is missing an entry:\n%s", got)
	}

	// B's push uploads the union; A's pull merges it back.
	if _, err := b.syncer.Push(ctx); err != nil {
		t.Fatalf("B push: %v", err)
	}
	if _, err := a.syncer.Pull(ctx); err != nil {
		t.Fatalf("A pull: %v", err)
	}
	if gotA := readFile(t, a.claudeDir, "session_index.jsonl"); gotA != got {
		t.Fatalf("devices diverged:\nA:\n%s\nB:\n%s", gotA, got)
	}
}

func TestPullMergeIsIdempotentAndPushesNothingAfterwards(t *testing.T) {
	a := setupTestEnv(t)
	b := newPeer(t, a)
	ctx := context.Background()
	writeFile(t, a.claudeDir, "history.jsonl", `{"session_id":"s","ts":1700000001,"text":"first"}`+"\n")
	if _, err := a.syncer.Push(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.syncer.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, b.claudeDir, "history.jsonl")
	res, err := b.syncer.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Merged) != 0 {
		t.Errorf("second pull merged again: %v", res.Merged)
	}
	if readFile(t, b.claudeDir, "history.jsonl") != first {
		t.Error("second pull changed the file")
	}
	pushed, err := b.syncer.Push(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pushed.Uploaded) != 0 {
		t.Errorf("nothing new locally, but push uploaded %v", pushed.Uploaded)
	}
}

func TestPreviewPullReportsMerge(t *testing.T) {
	a := setupTestEnv(t)
	b := newPeer(t, a)
	ctx := context.Background()
	writeFile(t, a.claudeDir, "session_index.jsonl", idxA)
	writeFile(t, b.claudeDir, "session_index.jsonl", idxB)
	if _, err := a.syncer.Push(ctx); err != nil {
		t.Fatal(err)
	}
	preview, err := b.syncer.PreviewPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.WouldMerge) != 1 || preview.WouldMerge[0].Path != SessionIndexFile {
		t.Errorf("WouldMerge = %+v", preview.WouldMerge)
	}
	if len(preview.WouldOverwrite) != 0 || len(preview.WouldConflict) != 0 {
		t.Errorf("mergeable file classified as overwrite/conflict: %+v", preview)
	}
}
