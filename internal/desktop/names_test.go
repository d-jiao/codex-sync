package desktop

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// newStateDB creates a state_5.sqlite with the columns ReconcileNames touches.
func newStateDB(t *testing.T, baseDir string, rows [][2]string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(baseDir, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL, name TEXT, title TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		var name any
		if r[1] != "" {
			name = r[1]
		}
		if _, err := db.Exec(`INSERT INTO threads (id, rollout_path, name) VALUES (?, ?, ?)`, r[0], "sessions/"+r[0]+".jsonl", name); err != nil {
			t.Fatal(err)
		}
	}
}

func threadName(t *testing.T, baseDir, id string) string {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(baseDir, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name sql.NullString
	if err := db.QueryRow(`SELECT name FROM threads WHERE id = ?`, id).Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name.String
}

func writeIndex(t *testing.T, baseDir string, lines ...string) {
	t.Helper()
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(baseDir, "session_index.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileNamesFillsOnlyEmptyNames(t *testing.T) {
	base := t.TempDir()
	newStateDB(t, base, [][2]string{{"a", ""}, {"b", "Kept locally"}, {"c", ""}})
	writeIndex(t, base,
		`{"id":"a","thread_name":"From the other Mac","updated_at":"2026-09-13T04:26:26Z"}`,
		`{"id":"b","thread_name":"Renamed remotely","updated_at":"2026-09-13T04:26:26Z"}`,
		`{"id":"c","thread_name":"","updated_at":"2026-09-13T04:26:26Z"}`,
		`{"id":"zzz","thread_name":"Not indexed yet","updated_at":"2026-09-13T04:26:26Z"}`,
		`not json at all`,
	)

	rep, err := ReconcileNames(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := threadName(t, base, "a"); got != "From the other Mac" {
		t.Errorf("a: name = %q, want the index name", got)
	}
	if got := threadName(t, base, "b"); got != "Kept locally" {
		t.Errorf("b: existing name overwritten: %q", got)
	}
	if got := threadName(t, base, "c"); got != "" {
		t.Errorf("c: empty index name applied: %q", got)
	}
	if rep.InIndex != 3 || rep.NamedBefore != 1 || rep.NamedAfter != 2 {
		t.Errorf("report = %+v, want InIndex 3, NamedBefore 1, NamedAfter 2", rep)
	}
}

func TestReconcileNamesIsIdempotent(t *testing.T) {
	base := t.TempDir()
	newStateDB(t, base, [][2]string{{"a", ""}})
	writeIndex(t, base, `{"id":"a","thread_name":"Once","updated_at":"2026-09-13T04:26:26Z"}`)
	if _, err := ReconcileNames(base); err != nil {
		t.Fatal(err)
	}
	rep, err := ReconcileNames(base)
	if err != nil {
		t.Fatal(err)
	}
	if rep.NamedBefore != 1 || rep.NamedAfter != 1 {
		t.Errorf("second run changed names: %+v", rep)
	}
}

func TestReconcileNamesRefusesUnknownSchema(t *testing.T) {
	base := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(base, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	writeIndex(t, base, `{"id":"a","thread_name":"x","updated_at":"2026-09-13T04:26:26Z"}`)

	if _, err := ReconcileNames(base); err == nil {
		t.Fatal("expected an error when threads.name is missing")
	}
}

func TestReconcileNamesWithoutIndexIsANoOp(t *testing.T) {
	base := t.TempDir()
	newStateDB(t, base, [][2]string{{"a", ""}})

	rep, err := ReconcileNames(base)
	if err != nil {
		t.Fatal(err)
	}
	if rep.InIndex != 0 || rep.NamedAfter != 0 {
		t.Errorf("report = %+v, want nothing applied", rep)
	}
}
