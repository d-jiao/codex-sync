// Package desktop makes threads that arrived via sync visible to the Codex desktop
// app (ChatGPT.app). The app builds its thread catalog once and then scans
// incrementally past an updated_at watermark, so pulled threads — always older
// than the watermark — never appear on their own, and its engine only indexes
// rollouts it discovers itself. Refresh re-indexes through the app's engine,
// copies thread names from session_index.jsonl into the engine database, and
// schedules the app's full catalog sweep (design spec §8, §9, §13).
package desktop

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // database/sql driver
)

// StateDB is the engine database relative to the Codex home.
const StateDB = "state_5.sqlite"

// NamesReport describes what ReconcileNames found and changed.
type NamesReport struct {
	InIndex     int // entries in session_index.jsonl that carry a name
	NamedBefore int // threads with a name before the run
	NamedAfter  int // threads with a name after the run
}

// ReconcileNames copies thread names from session_index.jsonl into threads.name
// for threads that have none. Names set locally are never overwritten, so the
// operation is idempotent. It refuses to touch a database whose threads table has
// no name column.
func ReconcileNames(baseDir string) (NamesReport, error) {
	var rep NamesReport
	names, err := indexNames(filepath.Join(baseDir, "session_index.jsonl"))
	if err != nil {
		return rep, err
	}
	rep.InIndex = len(names)
	if len(names) == 0 {
		return rep, nil
	}

	db, err := openDB(filepath.Join(baseDir, StateDB))
	if err != nil {
		return rep, err
	}
	defer db.Close()

	cols, err := tableColumns(db, "threads")
	if err != nil {
		return rep, err
	}
	if !cols["name"] {
		return rep, errors.New("threads.name column missing in " + StateDB + " — engine schema changed; not touching it")
	}

	const named = `SELECT count(*) FROM threads WHERE name IS NOT NULL AND name <> ''`
	if err := db.QueryRow(named).Scan(&rep.NamedBefore); err != nil {
		return rep, err
	}
	tx, err := db.Begin()
	if err != nil {
		return rep, err
	}
	stmt, err := tx.Prepare(`UPDATE threads SET name = ? WHERE id = ? AND (name IS NULL OR name = '')`)
	if err != nil {
		_ = tx.Rollback()
		return rep, err
	}
	for id, name := range names {
		if _, err := stmt.Exec(name, id); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return rep, fmt.Errorf("update name for %s: %w", id, err)
		}
	}
	_ = stmt.Close()
	if err := tx.Commit(); err != nil {
		return rep, err
	}
	if err := db.QueryRow(named).Scan(&rep.NamedAfter); err != nil {
		return rep, err
	}
	return rep, nil
}

// indexNames reads {id, thread_name} pairs from a session index, skipping
// unparsable lines and entries without a name. A missing file yields no names.
func indexNames(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	names := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var e struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.ID == "" || e.Name == "" {
			continue
		}
		names[e.ID] = e.Name
	}
	return names, sc.Err()
}

// openDB opens an existing SQLite file; it never creates one.
func openDB(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// tableColumns returns the column names of a table; an unknown table yields an
// empty set.
func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}
