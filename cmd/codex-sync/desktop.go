package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/d-jiao/codex-sync/internal/config"
	"github.com/d-jiao/codex-sync/internal/desktop"
)

// refreshDesktop is desktop.Refresh, replaceable in tests.
var refreshDesktop = desktop.Refresh

func desktopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "desktop",
		Short: "Make synced threads visible in the Codex desktop app",
		Long: `Make threads that arrived via pull visible in the ChatGPT desktop app.

The app builds its thread catalog once and then scans incrementally past the
newest thread it has seen, so pulled threads — always older than that — never
appear in its sidebar on their own, and its engine indexes only rollouts it
discovers itself. 'desktop refresh' fixes both; 'pull --desktop' runs it after
a pull.`,
	}
	cmd.AddCommand(desktopRefreshCmd())
	return cmd
}

func desktopRefreshCmd() *cobra.Command {
	var noNames, noBackup bool
	var codexBin string

	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Index pulled threads for the desktop app (quit the app first)",
		Long: `Index pulled threads for the ChatGPT desktop app. With the app quit:

  1. back up state_5.sqlite, sqlite/codex-dev.db and session_index.jsonl to
     ~/.codex-sync/db-backup-<timestamp>/
  2. run the app's own engine once so it indexes every rollout on disk
  3. copy thread names from session_index.jsonl into the engine database
     for threads that have none
  4. schedule the app's full catalog sweep for its next launch

Then launch the app. Safe to run repeatedly. Refuses to run while the app or
a codex engine is open, since they hold the databases.

Examples:
  codex-sync desktop refresh              # after a pull that brought new threads
  codex-sync desktop refresh --no-names   # leave thread names alone
  CODEX_BIN=/path/to/codex codex-sync desktop refresh`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := desktopOptions(codexBin, noNames, noBackup)
			if err != nil {
				return err
			}
			return runDesktopRefresh(cmd, opts)
		},
	}

	cmd.Flags().BoolVar(&noNames, "no-names", false, "Do not copy thread names from session_index.jsonl")
	cmd.Flags().BoolVar(&noBackup, "no-backup", false, "Skip the database backup")
	cmd.Flags().StringVar(&codexBin, "codex-bin", "", "Engine binary (default: $CODEX_BIN, the ChatGPT app's bundled engine, or codex on PATH; a different engine version may migrate every Codex database)")

	return cmd
}

// desktopOptions resolves the Codex home, backup root and engine for a refresh.
func desktopOptions(codexBin string, noNames, noBackup bool) (desktop.Options, error) {
	baseDir, err := config.BaseDirE()
	if err != nil {
		return desktop.Options{}, err
	}
	root, err := config.ConfigDirPathE()
	if err != nil {
		return desktop.Options{}, err
	}
	return desktop.Options{
		BaseDir:    baseDir,
		BackupRoot: root,
		EngineBin:  desktop.ResolveEngine(codexBin),
		NoNames:    noNames,
		NoBackup:   noBackup,
	}, nil
}

// runDesktopRefresh runs the refresh and prints its report to the command's
// output. A running app is reported as an error that names the next step, so
// the command exits non-zero without having touched anything. Neither failure
// is a usage mistake, so the usage text is silenced.
func runDesktopRefresh(cmd *cobra.Command, opts desktop.Options) error {
	rep, err := refreshDesktop(context.Background(), opts)
	if err != nil {
		cmd.SilenceUsage = true
		var running *desktop.AppRunningError
		if errors.As(err, &running) {
			return fmt.Errorf("desktop refresh skipped — %w\n  then run '%scodex-sync desktop refresh%s'", err, colorCyan, colorReset)
		}
		return err
	}
	if !quiet {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s✓%s Desktop refresh complete\n%s\n", colorGreen, colorReset, rep)
	}
	return nil
}

// refreshAfterPull decides whether pull --desktop runs the refresh: only when it
// was requested, the pull was real (not --dry-run), something was actually
// pulled (a first pull can be aborted at the prompt), and no file failed.
func refreshAfterPull(pullErr error, requested, dryRun, pulled bool) bool {
	return pullErr == nil && requested && !dryRun && pulled
}
