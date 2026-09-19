// Package desktop makes threads that arrived via sync visible to the Codex desktop
// app (ChatGPT.app). The app builds its thread catalog once and then scans
// incrementally past an updated_at watermark, so pulled threads — always older
// than the watermark — never appear on their own, and its engine only indexes
// rollouts it discovers itself. Refresh re-indexes through the app's engine,
// copies thread names from session_index.jsonl into the engine database, and
// schedules the app's full catalog sweep (design spec §8, §9, §13).
package desktop
