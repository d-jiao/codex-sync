package desktop

import (
	"errors"
	"os"
	"path/filepath"
)

// CatalogDB is the desktop app's thread catalog relative to the Codex home.
const CatalogDB = "sqlite/codex-dev.db"

// CatalogReport describes what ResetCatalog did.
type CatalogReport struct {
	Reset bool   // the local host's full sweep was scheduled
	Note  string // why nothing was changed, when Reset is false
}

// ResetCatalog clears last_full_reconciled_at for the desktop app's local host so
// that its next launch performs a full catalog sweep instead of an incremental
// scan (the app's isFullReconciliationDue is true only while that column is NULL).
// A missing catalog — a machine without the desktop app — or an unrecognised
// schema is reported, not treated as an error.
func ResetCatalog(baseDir string) (CatalogReport, error) {
	path := filepath.Join(baseDir, filepath.FromSlash(CatalogDB))
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CatalogReport{Note: "no desktop app catalog (" + CatalogDB + ") — nothing to schedule"}, nil
		}
		return CatalogReport{}, err
	}
	db, err := openDB(path)
	if err != nil {
		return CatalogReport{}, err
	}
	defer func() { _ = db.Close() }()

	cols, err := tableColumns(db, "local_thread_catalog_sync_state")
	if err != nil {
		return CatalogReport{}, err
	}
	if !cols["host_id"] || !cols["last_full_reconciled_at"] {
		return CatalogReport{Note: "desktop catalog schema not recognised — left untouched"}, nil
	}
	res, err := db.Exec(`UPDATE local_thread_catalog_sync_state SET last_full_reconciled_at = NULL WHERE host_id = 'local'`)
	if err != nil {
		return CatalogReport{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return CatalogReport{}, err
	}
	if n == 0 {
		return CatalogReport{Note: "desktop catalog has no local host row yet — it will build itself on first launch"}, nil
	}
	return CatalogReport{Reset: true}, nil
}
