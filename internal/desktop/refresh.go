package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Options configures Refresh.
type Options struct {
	BaseDir    string // the Codex home
	BackupRoot string // directory that receives db-backup-<ts>/ (normally ~/.codex-sync)
	EngineBin  string // engine binary; see ResolveEngine
	NoBackup   bool
	NoNames    bool
	// Processes lists Codex/ChatGPT processes that would hold the databases.
	// nil uses pgrep; tests inject their own.
	Processes func() ([]string, error)
}

// Report is what Refresh did, for the CLI to print.
type Report struct {
	BackupDir  string
	Listed     Listed
	ThreadRows int
	Names      *NamesReport // nil when the step was skipped
	Catalog    CatalogReport
}

// String renders the report as the CLI prints it.
func (r *Report) String() string {
	var b strings.Builder
	if r.BackupDir != "" {
		fmt.Fprintf(&b, "backup: %s\n", r.BackupDir)
	}
	fmt.Fprintf(&b, "engine indexed rollouts: %d live + %d archived threads listed, %d thread rows\n",
		r.Listed.Live, r.Listed.Archived, r.ThreadRows)
	if r.Names != nil {
		fmt.Fprintf(&b, "names: %d in session_index.jsonl; named threads %d -> %d\n",
			r.Names.InIndex, r.Names.NamedBefore, r.Names.NamedAfter)
	}
	if r.Catalog.Reset {
		b.WriteString("desktop catalog: full sweep scheduled for the next launch\n")
	} else {
		fmt.Fprintf(&b, "desktop catalog: %s\n", r.Catalog.Note)
	}
	b.WriteString("Launch the ChatGPT app; its first scan rebuilds the sidebar.")
	return b.String()
}

// AppRunningError reports that the desktop app or an engine holds the databases.
type AppRunningError struct {
	Processes []string
}

func (e *AppRunningError) Error() string {
	return "the ChatGPT app or a codex engine is running; quit it first:\n  " + strings.Join(e.Processes, "\n  ")
}

// Refresh performs the four steps described in the package comment. It refuses
// to run while the desktop app or an engine is open, before touching anything.
func Refresh(ctx context.Context, opts Options) (*Report, error) {
	if _, err := os.Stat(filepath.Join(opts.BaseDir, "sessions")); err != nil {
		return nil, fmt.Errorf("%s has no sessions/ directory — is this the Codex home?", opts.BaseDir)
	}
	lister := opts.Processes
	if lister == nil {
		lister = runningCodexProcesses
	}
	procs, err := lister()
	if err != nil {
		return nil, err
	}
	if len(procs) > 0 {
		return nil, &AppRunningError{Processes: procs}
	}

	rep := &Report{}
	if !opts.NoBackup {
		dir, err := Backup(opts.BaseDir, opts.BackupRoot)
		if err != nil {
			return nil, fmt.Errorf("backup: %w", err)
		}
		rep.BackupDir = dir
	}
	rep.Listed, err = IndexRollouts(ctx, opts.EngineBin, opts.BaseDir, Providers(opts.BaseDir))
	if err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}
	rep.ThreadRows, err = threadRows(opts.BaseDir)
	if err != nil {
		return nil, err
	}
	if !opts.NoNames {
		n, err := ReconcileNames(opts.BaseDir)
		if err != nil {
			return nil, fmt.Errorf("names: %w", err)
		}
		rep.Names = &n
	}
	rep.Catalog, err = ResetCatalog(opts.BaseDir)
	if err != nil {
		return nil, fmt.Errorf("desktop catalog: %w", err)
	}
	return rep, nil
}

func threadRows(baseDir string) (int, error) {
	db, err := openDB(filepath.Join(baseDir, StateDB))
	if err != nil {
		return 0, fmt.Errorf("engine left no %s: %w", StateDB, err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM threads`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// processPattern matches the processes that hold the databases open: the
// desktop app and any codex engine or CLI, matched on argv[0] so that a shell
// merely mentioning the engine path does not count. Paths containing spaces are
// not matched; the app engine and PATH installs have none.
const processPattern = `^([^ ]*/)?(codex|ChatGPT)( |$)`

// runningCodexProcesses lists matching processes via pgrep. -a includes our own
// ancestors, which pgrep skips by default — without it a Codex session running
// codex-sync would hide the very engine that holds the databases. (On procps
// -a merely means "list the full command line", which -fl already does.) No
// match is not an error.
func runningCodexProcesses() ([]string, error) {
	out, err := exec.Command("pgrep", "-afl", processPattern).Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil, nil // no match
	}
	if err != nil {
		return nil, fmt.Errorf("pgrep: %w", err)
	}
	return parseProcessLines(string(out), os.Getpid()), nil
}

// parseProcessLines keeps the non-empty "PID command" lines that are not our
// own process.
func parseProcessLines(out string, self int) []string {
	var procs []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if pid, _, _ := strings.Cut(l, " "); pid == strconv.Itoa(self) {
			continue
		}
		procs = append(procs, l)
	}
	return procs
}
