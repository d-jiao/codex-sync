package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/d-jiao/codex-sync/internal/config"
	"github.com/d-jiao/codex-sync/internal/sync"
	"github.com/d-jiao/codex-sync/internal/util"
)

// trashCmd exposes the remote recycle bin. A forced push copies every object
// it removes into it, and these are the commands that make those copies
// findable and reversible.
func trashCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trash",
		Short: "Inspect and restore remote copies of deleted files",
		Long: `Work with the remote recycle bin.

'codex-sync push --force' copies every remote file it deletes to ` + sync.TrashPrefix + `<batch>/
before removing it, so a deletion made from stale state can be undone.`,
	}
	cmd.AddCommand(trashListCmd(), trashRestoreCmd())
	return cmd
}

func trashListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the batches of deleted files kept remotely",
		RunE: func(cmd *cobra.Command, args []string) error {
			syncer, err := newSyncerFromConfig()
			if err != nil {
				return err
			}
			batches, err := syncer.ListRemoteTrash(context.Background())
			if err != nil {
				return err
			}
			printTrashBatches(os.Stdout, batches)
			return nil
		},
	}
}

func trashRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <batch>",
		Short: "Copy a batch of deleted files back to the remote",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			syncer, err := newSyncerFromConfig()
			if err != nil {
				return err
			}
			restored, skipped, err := syncer.RestoreRemoteTrash(context.Background(), args[0])
			printTrashRestore(os.Stdout, restored, skipped)
			return err
		},
	}
}

// newSyncerFromConfig builds a syncer for the commands that only talk to the
// remote.
func newSyncerFromConfig() (*sync.Syncer, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return sync.NewSyncer(cfg, quiet)
}

// printTrashBatches lists the recycle bin newest first, with the batch name
// the restore command takes.
func printTrashBatches(w io.Writer, batches []sync.TrashBatchInfo) {
	if len(batches) == 0 {
		_, _ = fmt.Fprintf(w, "The remote recycle bin is empty.\n")
		return
	}

	_, _ = fmt.Fprintf(w, "%sDeleted files kept remotely:%s\n\n", colorBold, colorReset)
	for _, b := range batches {
		_, _ = fmt.Fprintf(w, "  %s%s%s  %d file(s), %s  %s%s%s\n",
			colorBold, b.Batch, colorReset,
			b.Files, util.FormatSize(b.Size),
			colorDim, b.Deleted.Local().Format("2006-01-02 15:04"), colorReset)
	}
	_, _ = fmt.Fprintf(w, "\nRestore one with %scodex-sync trash restore <batch>%s, then run %scodex-sync pull%s.\n",
		colorBold, colorReset, colorBold, colorReset)
}

// printTrashRestore reports what came back and what was left alone because a
// live object already holds the key.
func printTrashRestore(w io.Writer, restored, skipped []string) {
	if len(restored) > 0 {
		_, _ = fmt.Fprintf(w, "%s✓%s Restored %d file(s) to the remote:\n", colorGreen, colorReset, len(restored))
		for _, p := range restored {
			_, _ = fmt.Fprintf(w, "  %s•%s %s\n", colorDim, colorReset, util.TruncatePath(p, 60))
		}
		_, _ = fmt.Fprintf(w, "  Run %scodex-sync pull%s to bring them back down.\n", colorBold, colorReset)
	}
	if len(skipped) > 0 {
		_, _ = fmt.Fprintf(w, "\n%s%d file(s) were left alone because a live copy is in the way:%s\n",
			colorYellow, len(skipped), colorReset)
		for _, p := range skipped {
			_, _ = fmt.Fprintf(w, "  %s•%s %s\n", colorDim, colorReset, util.TruncatePath(p, 60))
		}
		_, _ = fmt.Fprintf(w, "  Another device re-created them, and that newer version wins.\n")
	}
}
