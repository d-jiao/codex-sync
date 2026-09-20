package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/d-jiao/codex-sync/internal/sync"
)

func TestPrintTrashBatches(t *testing.T) {
	var buf bytes.Buffer
	printTrashBatches(&buf, nil)
	if !strings.Contains(buf.String(), "empty") {
		t.Errorf("an empty bin should say so: %q", buf.String())
	}

	buf.Reset()
	printTrashBatches(&buf, []sync.TrashBatchInfo{{
		Batch:   "20260920-141500Z",
		Files:   2,
		Size:    2048,
		Deleted: time.Date(2026, 9, 20, 14, 15, 0, 0, time.UTC),
	}})
	out := buf.String()
	if !strings.Contains(out, "20260920-141500Z") {
		t.Errorf("output should name the batch: %q", out)
	}
	if !strings.Contains(out, "2 file") {
		t.Errorf("output should count the copies: %q", out)
	}
	if !strings.Contains(out, "trash restore") {
		t.Errorf("output should say how to restore: %q", out)
	}
}

func TestPrintTrashRestore(t *testing.T) {
	var buf bytes.Buffer
	printTrashRestore(&buf, []string{"AGENTS.md"}, nil)
	out := buf.String()
	if !strings.Contains(out, "AGENTS.md") {
		t.Errorf("output should name what came back: %q", out)
	}
	if !strings.Contains(out, "pull") {
		t.Errorf("output should point at the pull that brings the file down: %q", out)
	}

	buf.Reset()
	printTrashRestore(&buf, nil, []string{"sessions/rollout-a.jsonl"})
	out = buf.String()
	if !strings.Contains(out, "sessions/rollout-a.jsonl") {
		t.Errorf("output should name what was skipped: %q", out)
	}
	if !strings.Contains(out, "newer") && !strings.Contains(out, "live") {
		t.Errorf("output should explain why it was skipped: %q", out)
	}
}
