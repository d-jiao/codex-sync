package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/d-jiao/codex-sync/internal/desktop"
)

// outCmd returns a command whose output goes to the returned buffer.
func outCmd() (*cobra.Command, *bytes.Buffer) {
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	return cmd, &out
}

func TestDesktopCmdExposesRefresh(t *testing.T) {
	cmd := desktopCmd()
	if cmd.Use != "desktop" {
		t.Errorf("Use = %q, want desktop", cmd.Use)
	}
	var refresh bool
	for _, sub := range cmd.Commands() {
		if sub.Use == "refresh" {
			refresh = true
			for _, flag := range []string{"no-names", "no-backup", "codex-bin"} {
				if sub.Flags().Lookup(flag) == nil {
					t.Errorf("refresh lacks --%s", flag)
				}
			}
			if sub.Short == "" || sub.Long == "" {
				t.Error("refresh is undocumented")
			}
		}
	}
	if !refresh {
		t.Fatal("desktop has no refresh subcommand")
	}
}

func TestPullCmdHasDesktopFlag(t *testing.T) {
	flag := pullCmd().Flags().Lookup("desktop")
	if flag == nil {
		t.Fatal("pull lacks --desktop")
	}
	if flag.Usage == "" {
		t.Error("--desktop is undocumented")
	}
}

func TestRunDesktopRefreshPrintsTheReport(t *testing.T) {
	orig := refreshDesktop
	defer func() { refreshDesktop = orig }()
	var got desktop.Options
	refreshDesktop = func(ctx context.Context, opts desktop.Options) (*desktop.Report, error) {
		got = opts
		return &desktop.Report{BackupDir: "/tmp/b", Listed: desktop.Listed{Live: 3, Archived: 1}, ThreadRows: 42,
			Names: &desktop.NamesReport{InIndex: 5, NamedBefore: 1, NamedAfter: 4}, Catalog: desktop.CatalogReport{Reset: true}}, nil
	}

	cmd, out := outCmd()
	err := runDesktopRefresh(cmd, desktop.Options{BaseDir: "/x", EngineBin: "/y"})
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseDir != "/x" || got.EngineBin != "/y" {
		t.Errorf("options not passed through: %+v", got)
	}
	for _, want := range []string{"/tmp/b", "3 live", "42 thread rows", "1 -> 4", "full sweep", "Launch the ChatGPT app"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestRunDesktopRefreshExplainsARunningApp(t *testing.T) {
	orig := refreshDesktop
	defer func() { refreshDesktop = orig }()
	refreshDesktop = func(ctx context.Context, opts desktop.Options) (*desktop.Report, error) {
		return nil, &desktop.AppRunningError{Processes: []string{"85620 /Applications/ChatGPT.app/Contents/Resources/codex app-server"}}
	}

	cmd, _ := outCmd()
	err := runDesktopRefresh(cmd, desktop.Options{})
	if err == nil {
		t.Fatal("expected an error so the command exits non-zero")
	}
	if !cmd.SilenceUsage {
		t.Error("a running app is not a usage mistake; usage must be silenced")
	}
	var running *desktop.AppRunningError
	if !errors.As(err, &running) {
		t.Errorf("error should wrap AppRunningError: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"skipped", "85620", "codex-sync desktop refresh"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q: %s", want, msg)
		}
	}
}

func TestRunDesktopRefreshPassesOtherErrorsThrough(t *testing.T) {
	orig := refreshDesktop
	defer func() { refreshDesktop = orig }()
	refreshDesktop = func(ctx context.Context, opts desktop.Options) (*desktop.Report, error) {
		return nil, errors.New("engine: boom")
	}

	cmd, _ := outCmd()
	if err := runDesktopRefresh(cmd, desktop.Options{}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the engine error", err)
	}
	if !cmd.SilenceUsage {
		t.Error("an engine failure is not a usage mistake; usage must be silenced")
	}
}

func TestRefreshAfterPullGate(t *testing.T) {
	cases := []struct {
		name                      string
		err                       error
		requested, dryRun, pulled bool
		want                      bool
	}{
		{"requested after a real pull", nil, true, false, true, true},
		{"not requested", nil, false, false, true, false},
		{"dry run only previews", nil, true, true, true, false},
		{"first pull aborted: nothing pulled", nil, true, false, false, false},
		{"pull failed", errors.New("3 file(s) failed"), true, false, true, false},
	}
	for _, c := range cases {
		if got := refreshAfterPull(c.err, c.requested, c.dryRun, c.pulled); got != c.want {
			t.Errorf("%s: refreshAfterPull = %v, want %v", c.name, got, c.want)
		}
	}
}
