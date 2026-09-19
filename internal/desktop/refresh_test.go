package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func refreshFixture(t *testing.T) (base string, catalog string) {
	t.Helper()
	base = t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	newStateDB(t, base, [][2]string{{"a", ""}, {"b", "Mine"}})
	writeIndex(t, base, `{"id":"a","thread_name":"Theirs","updated_at":"2026-09-13T04:26:26Z"}`)
	catalog = newCatalogDB(t, base, map[string]int64{"local": 1788448848586})
	return base, catalog
}

func noProcesses() ([]string, error) { return nil, nil }

func TestRefreshRunsEverythingAndReports(t *testing.T) {
	base, catalog := refreshFixture(t)
	root := t.TempDir()
	bin := fakeEngine(t, filepath.Join(t.TempDir(), "requests.log"), "ok")

	rep, err := Refresh(context.Background(), Options{BaseDir: base, BackupRoot: root, EngineBin: bin, Processes: noProcesses})
	if err != nil {
		t.Fatal(err)
	}
	if rep.BackupDir == "" || filepath.Dir(rep.BackupDir) != root {
		t.Errorf("backup dir = %q, want one under %q", rep.BackupDir, root)
	}
	if rep.Listed.Live != 3 || rep.Listed.Archived != 1 || rep.ThreadRows != 2 {
		t.Errorf("engine numbers = %+v rows=%d", rep.Listed, rep.ThreadRows)
	}
	if rep.Names == nil || rep.Names.NamedBefore != 1 || rep.Names.NamedAfter != 2 {
		t.Errorf("names = %+v, want 1 -> 2", rep.Names)
	}
	if !rep.Catalog.Reset {
		t.Errorf("catalog = %+v, want Reset", rep.Catalog)
	}
	if v := lastFullReconciledAt(t, catalog, "local"); v.Valid {
		t.Error("catalog row was not reset")
	}
	if got := threadName(t, base, "a"); got != "Theirs" {
		t.Errorf("name not applied: %q", got)
	}
	s := rep.String()
	for _, want := range []string{"backup:", "3 live", "1 archived", "1 -> 2", "full sweep", "launch"} {
		if !strings.Contains(strings.ToLower(s), strings.ToLower(want)) {
			t.Errorf("report text lacks %q:\n%s", want, s)
		}
	}
}

func TestRefreshRefusesWhileTheAppRunsAndTouchesNothing(t *testing.T) {
	base, catalog := refreshFixture(t)
	root := t.TempDir()
	running := func() ([]string, error) {
		return []string{"85620 /Applications/ChatGPT.app/Contents/Resources/codex app-server"}, nil
	}

	_, err := Refresh(context.Background(), Options{BaseDir: base, BackupRoot: root, EngineBin: "/nonexistent", Processes: running})
	var appErr *AppRunningError
	if !errors.As(err, &appErr) {
		t.Fatalf("err = %v, want *AppRunningError", err)
	}
	if !strings.Contains(err.Error(), "85620") {
		t.Errorf("error should list the process: %v", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Error("a backup was written despite refusing")
	}
	if v := lastFullReconciledAt(t, catalog, "local"); !v.Valid {
		t.Error("catalog was modified despite refusing")
	}
	if got := threadName(t, base, "a"); got != "" {
		t.Error("names were modified despite refusing")
	}
}

func TestRefreshHonoursNoNamesAndNoBackup(t *testing.T) {
	base, _ := refreshFixture(t)
	root := t.TempDir()
	bin := fakeEngine(t, filepath.Join(t.TempDir(), "requests.log"), "ok")

	rep, err := Refresh(context.Background(), Options{BaseDir: base, BackupRoot: root, EngineBin: bin, Processes: noProcesses, NoNames: true, NoBackup: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.BackupDir != "" || rep.Names != nil {
		t.Errorf("report = %+v, want no backup and no names step", rep)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Error("a backup was written with NoBackup")
	}
	if got := threadName(t, base, "a"); got != "" {
		t.Error("names were applied with NoNames")
	}
}

func TestRefreshRejectsAHomeWithoutSessions(t *testing.T) {
	_, err := Refresh(context.Background(), Options{BaseDir: t.TempDir(), BackupRoot: t.TempDir(), EngineBin: "/nonexistent", Processes: noProcesses})
	if err == nil || !strings.Contains(err.Error(), "sessions") {
		t.Fatalf("err = %v, want a complaint about sessions/", err)
	}
}

func TestRefreshSurfacesEngineFailure(t *testing.T) {
	base, _ := refreshFixture(t)
	bin := fakeEngine(t, filepath.Join(t.TempDir(), "requests.log"), "die")

	_, err := Refresh(context.Background(), Options{BaseDir: base, BackupRoot: t.TempDir(), EngineBin: bin, Processes: noProcesses})
	if err == nil {
		t.Fatal("expected the engine failure to be reported")
	}
}

func TestParseProcessLinesDropsSelfAndBlanks(t *testing.T) {
	out := "7420 /Applications/ChatGPT.app/Contents/MacOS/ChatGPT\n\n9999 codex-sync desktop refresh\n85620 /Applications/ChatGPT.app/Contents/Resources/codex app-server --stdio\n"
	got := parseProcessLines(out, 9999)
	want := []string{
		"7420 /Applications/ChatGPT.app/Contents/MacOS/ChatGPT",
		"85620 /Applications/ChatGPT.app/Contents/Resources/codex app-server --stdio",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("parseProcessLines = %q, want %q", got, want)
	}
}

func TestProcessPatternMatchesHoldersOfTheDatabases(t *testing.T) {
	re := regexp.MustCompile(processPattern)
	for _, line := range []string{
		"/Applications/ChatGPT.app/Contents/MacOS/ChatGPT",
		"/Applications/ChatGPT.app/Contents/Resources/codex -c features.code_mode_host=true app-server --analytics-default-enabled",
		"/Users/me/.local/bin/codex app-server --stdio",
		"codex",
		"codex resume --last",
		"/opt/homebrew/bin/codex exec 'hi'",
	} {
		if !re.MatchString(line) {
			t.Errorf("pattern should match %q", line)
		}
	}
	for _, line := range []string{
		"codex-sync desktop refresh",
		"/Users/me/.local/bin/codex-sync pull --desktop",
		"sh -c 'echo /Applications/ChatGPT.app/Contents/Resources/codex app-server'",
		"/Applications/ChatGPT.app/Contents/Frameworks/Codex Framework.framework/Helpers/Codex (Renderer)",
		"python3 codex_listing_check.py",
	} {
		if re.MatchString(line) {
			t.Errorf("pattern must not match %q", line)
		}
	}
}
