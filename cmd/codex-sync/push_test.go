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
