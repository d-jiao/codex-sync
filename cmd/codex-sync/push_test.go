package main

import (
	"bytes"
	"strings"
	"testing"
)

// Deleting remote data must stay opt-in: the flag exists, and it is off unless
// the user passes it.
func TestPushForceFlagDefaultsToOff(t *testing.T) {
	cmd := pushCmd()
	flag := cmd.Flags().Lookup("force")
	if flag == nil {
		t.Fatal("push has no --force flag")
	}
	if flag.DefValue != "false" {
		t.Errorf("--force default = %q, want false", flag.DefValue)
	}
	if !strings.Contains(cmd.Long, "--force") {
		t.Error("push help should explain what --force does")
	}
}

func TestPrintPendingDeletes(t *testing.T) {
	var buf bytes.Buffer
	printPendingDeletes(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("nothing to report should print nothing, got %q", buf.String())
	}

	printPendingDeletes(&buf, []string{"sessions/a.jsonl", "sessions/b.jsonl"})
	out := buf.String()
	for _, want := range []string{"sessions/a.jsonl", "sessions/b.jsonl", "push --force"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintPendingDeletesTruncatesLongLists(t *testing.T) {
	pending := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		pending = append(pending, "sessions/file.jsonl")
	}
	var buf bytes.Buffer
	printPendingDeletes(&buf, pending)
	if !strings.Contains(buf.String(), "and 15 more") {
		t.Errorf("expected a truncation note, got:\n%s", buf.String())
	}
}

func TestPrintUnverifiedDeletes(t *testing.T) {
	var buf bytes.Buffer
	printUnverifiedDeletes(&buf, nil)
	if buf.Len() != 0 {
		t.Fatalf("expected no output for a fully guarded push, got %q", buf.String())
	}

	printUnverifiedDeletes(&buf, []string{"sessions/a.jsonl"})
	out := buf.String()
	if !strings.Contains(out, "sessions/a.jsonl") {
		t.Errorf("output should name the file: %q", out)
	}
	if !strings.Contains(out, "no version") {
		t.Errorf("output should explain the weaker guarantee: %q", out)
	}
}

func TestPrintRemoteTrashBatch(t *testing.T) {
	var buf bytes.Buffer
	printRemoteTrashBatch(&buf, "", 0)
	if buf.Len() != 0 {
		t.Fatalf("expected no output when nothing was deleted, got %q", buf.String())
	}

	printRemoteTrashBatch(&buf, "20260920-141500Z", 3)
	out := buf.String()
	if !strings.Contains(out, "20260920-141500Z") {
		t.Errorf("output should name the batch so it can be found: %q", out)
	}
	if !strings.Contains(out, "_trash/") {
		t.Errorf("output should name the prefix the copies live under: %q", out)
	}
	if !strings.Contains(out, "3") {
		t.Errorf("output should say how many copies were kept: %q", out)
	}
}
