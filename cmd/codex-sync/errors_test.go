package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestReportSyncErrorsNothingToReport(t *testing.T) {
	var out bytes.Buffer
	cmd := &cobra.Command{}
	if err := reportSyncErrors(cmd, &out, nil); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q, want nothing", out.String())
	}
	if cmd.SilenceUsage {
		t.Error("usage must stay enabled when nothing failed")
	}
}

func TestReportSyncErrorsQuietStillPrintsAndFails(t *testing.T) {
	defer func(q bool) { quiet = q }(quiet)
	quiet = true
	var out bytes.Buffer
	cmd := &cobra.Command{}
	errs := []error{
		errors.New("sessions/a.jsonl: failed to upload: boom"),
		errors.New("unresolved conflict for sessions/b.jsonl; run 'codex-sync conflicts'"),
	}
	err := reportSyncErrors(cmd, &out, errs)
	if err == nil || err.Error() != "2 file(s) failed; see above" {
		t.Errorf("err = %v, want \"2 file(s) failed; see above\"", err)
	}
	for _, e := range errs {
		if !strings.Contains(out.String(), e.Error()) {
			t.Errorf("output %q lacks %q", out.String(), e.Error())
		}
	}
	if strings.Count(out.String(), "sessions/a.jsonl") != 1 {
		t.Errorf("each error must be printed exactly once:\n%s", out.String())
	}
	if !cmd.SilenceUsage {
		t.Error("a failed sync must not print the usage text")
	}
}

func TestReportSyncErrorsFailsTheCommandWithoutUsage(t *testing.T) {
	defer func(q bool) { quiet = q }(quiet)
	quiet = true
	var errOut, list bytes.Buffer
	sub := &cobra.Command{
		Use: "push",
		RunE: func(cmd *cobra.Command, args []string) error {
			return reportSyncErrors(cmd, &list, []error{errors.New("sessions/a.jsonl: boom")})
		},
	}
	root := &cobra.Command{Use: "codex-sync"}
	root.AddCommand(sub)
	root.SetErr(&errOut)
	root.SetOut(&errOut)
	root.SetArgs([]string{"push"})
	if err := root.Execute(); err == nil {
		t.Fatal("Execute must fail so `pull -q && push -q` stops")
	}
	if !strings.Contains(errOut.String(), "1 file(s) failed; see above") {
		t.Errorf("cobra output %q lacks the failure message", errOut.String())
	}
	if strings.Contains(errOut.String(), "Usage:") {
		t.Errorf("a failed sync must not dump usage:\n%s", errOut.String())
	}
	if !strings.Contains(list.String(), "sessions/a.jsonl: boom") {
		t.Errorf("error list %q lacks the file error", list.String())
	}
}

func TestReportSyncErrorsVerboseListsErrors(t *testing.T) {
	defer func(q bool) { quiet = q }(quiet)
	quiet = false
	var out bytes.Buffer
	err := reportSyncErrors(&cobra.Command{}, &out, []error{errors.New("x: nope")})
	if err == nil || err.Error() != "1 file(s) failed; see above" {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(out.String(), "Errors:") || !strings.Contains(out.String(), "x: nope") {
		t.Errorf("output = %q", out.String())
	}
}
