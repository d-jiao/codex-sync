package desktop

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProvidersReadsConfigTomlAndAlwaysIncludesOpenAI(t *testing.T) {
	base := t.TempDir()
	cfg := "[model_providers.cpa]\nname = \"x\"\n[model_providers.cpa.http_headers]\nx = \"y\"\n\n  [model_providers.\"my-proxy\"]\nbase_url = \"http://localhost\"\n[other]\nfoo = 1\n"
	if err := os.WriteFile(filepath.Join(base, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	got := Providers(base)
	if want := []string{"cpa", "my-proxy", "openai"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Providers = %v, want %v", got, want)
	}
}

func TestProvidersWithoutConfigIsJustOpenAI(t *testing.T) {
	if got := Providers(t.TempDir()); !reflect.DeepEqual(got, []string{"openai"}) {
		t.Errorf("Providers = %v, want [openai]", got)
	}
}

func TestResolveEnginePrecedence(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "flag-codex")
	env := filepath.Join(dir, "env-codex")
	app := filepath.Join(dir, "app-codex")
	for _, p := range []string{flag, env, app} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if got := resolveEngine(flag, env, app); got != flag {
		t.Errorf("flag should win, got %q", got)
	}
	if got := resolveEngine("", env, app); got != env {
		t.Errorf("env should beat the app engine, got %q", got)
	}
	if got := resolveEngine("", "", app); got != app {
		t.Errorf("app engine should beat PATH lookup, got %q", got)
	}
	if got := resolveEngine("", "", filepath.Join(dir, "missing")); got != "codex" {
		t.Errorf("fallback should be codex on PATH, got %q", got)
	}
}

func TestBackupCopiesDatabasesAndIndex(t *testing.T) {
	base := t.TempDir()
	for _, rel := range []string{"state_5.sqlite", "state_5.sqlite-wal", "memories_1.sqlite", "goals_1.sqlite-shm", "session_index.jsonl", "sqlite/codex-dev.db", "sqlite/codex-dev.db-shm", "sqlite/other.db", "config.toml"} {
		p := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(rel), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()

	dest, err := Backup(base, root)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dest) != root {
		t.Errorf("backup dir %q not under %q", dest, root)
	}
	for _, rel := range []string{"state_5.sqlite", "state_5.sqlite-wal", "memories_1.sqlite", "goals_1.sqlite-shm", "session_index.jsonl", "sqlite/codex-dev.db", "sqlite/codex-dev.db-shm"} {
		b, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil || string(b) != rel {
			t.Errorf("%s not backed up (%v)", rel, err)
		}
	}
	for _, rel := range []string{"sqlite/other.db", "config.toml"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s was copied but is not a database the refresh touches", rel)
		}
	}
}

func TestBackupNeverReusesADirectory(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "state_5.sqlite"), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	first, err := Backup(base, root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Backup(base, root)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Errorf("two backups in the same second share %q", first)
	}
}

func TestBackupToleratesMissingFiles(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "state_5.sqlite"), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest, err := Backup(base, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "state_5.sqlite")); err != nil {
		t.Error("state_5.sqlite missing from backup")
	}
}
