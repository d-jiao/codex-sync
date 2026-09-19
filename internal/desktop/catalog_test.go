package desktop

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// newCatalogDB creates sqlite/codex-dev.db with the sync-state table the app keeps.
func newCatalogDB(t *testing.T, baseDir string, hosts map[string]int64) string {
	t.Helper()
	dir := filepath.Join(baseDir, "sqlite")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "codex-dev.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE local_thread_catalog_sync_state (
		host_id TEXT PRIMARY KEY, watermark_updated_at REAL,
		initial_build_complete INTEGER NOT NULL DEFAULT 0,
		observation_sequence INTEGER NOT NULL DEFAULT 0, last_full_reconciled_at INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for host, at := range hosts {
		if _, err := db.Exec(`INSERT INTO local_thread_catalog_sync_state VALUES (?, 1789508916.0, 1, 585, ?)`, host, at); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func lastFullReconciledAt(t *testing.T, path, host string) sql.NullInt64 {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var v sql.NullInt64
	if err := db.QueryRow(`SELECT last_full_reconciled_at FROM local_thread_catalog_sync_state WHERE host_id = ?`, host).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestResetCatalogSchedulesFullSweepForLocalHostOnly(t *testing.T) {
	base := t.TempDir()
	path := newCatalogDB(t, base, map[string]int64{"local": 1788448848586, "chatgpt:ws:user": 1788449125439})

	rep, err := ResetCatalog(base)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Reset {
		t.Errorf("report = %+v, want Reset", rep)
	}
	if v := lastFullReconciledAt(t, path, "local"); v.Valid {
		t.Errorf("local last_full_reconciled_at = %v, want NULL", v.Int64)
	}
	if v := lastFullReconciledAt(t, path, "chatgpt:ws:user"); !v.Valid {
		t.Error("remote host row was touched")
	}
}

func TestResetCatalogWithoutDesktopCatalogIsSkipped(t *testing.T) {
	base := t.TempDir()

	rep, err := ResetCatalog(base)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Reset || rep.Note == "" {
		t.Errorf("report = %+v, want a skip note and Reset=false", rep)
	}
}

func TestResetCatalogLeavesUnknownSchemaAlone(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "sqlite")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "codex-dev.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE something_else (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	rep, err := ResetCatalog(base)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Reset || rep.Note == "" {
		t.Errorf("report = %+v, want a skip note and Reset=false", rep)
	}
}
