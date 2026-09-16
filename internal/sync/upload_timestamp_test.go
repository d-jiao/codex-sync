package sync

import (
	"context"
	"testing"
	"time"
)

// A storage service can stamp an object a few milliseconds after the upload
// call returns (R2 does). If state records the local clock at that moment, the
// remote looks newer than "uploaded" and the next pull re-downloads identical
// files. Push must record the remote's own timestamp instead.
func TestPushRecordsRemoteTimestampSoPullDoesNotRedownload(t *testing.T) {
	env := setupTestEnv(t)
	env.store.clockSkew = 50 * time.Millisecond
	writeFile(t, env.claudeDir, "sessions/2026/01/01/rollout-a.jsonl", rollout)

	if _, err := env.syncer.Push(context.Background()); err != nil {
		t.Fatalf("push: %v", err)
	}
	preview, err := env.syncer.PreviewPull(context.Background())
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.WouldOverwrite) != 0 || len(preview.WouldDownload) != 0 {
		t.Fatalf("own upload must not be re-downloaded: overwrite=%v download=%v", preview.WouldOverwrite, preview.WouldDownload)
	}
	res, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(res.Downloaded) != 0 {
		t.Fatalf("pull re-downloaded own upload: %v", res.Downloaded)
	}
	f := env.syncer.state.GetFile("sessions/2026/01/01/rollout-a.jsonl")
	if f == nil || f.Uploaded.IsZero() {
		t.Fatal("state must record an uploaded timestamp")
	}
}
