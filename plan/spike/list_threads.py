# Throwaway spike harness (2026-09-15): list Codex threads over the app-server protocol.
# Usage: CODEX_BIN=/Applications/ChatGPT.app/Contents/Resources/codex python3 list_threads.py <CODEX_HOME> [params-json]
import json, os, subprocess, sys, time, glob
home = sys.argv[1]; extra = json.loads(sys.argv[2]) if len(sys.argv) > 2 else {}
env = dict(os.environ, CODEX_HOME=home)
p = subprocess.Popen([os.environ.get("CODEX_BIN","codex"), "app-server", "--stdio"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, text=True)
def send(o): p.stdin.write(json.dumps(o) + "\n"); p.stdin.flush()
def wait(rid, timeout=120):
    end = time.time() + timeout
    while time.time() < end:
        line = p.stdout.readline()
        if not line: return {"error": "eof"}
        try: m = json.loads(line)
        except Exception: continue
        if m.get("id") == rid: return m
    return {"error": "timeout"}
send({"id": 1, "method": "initialize", "params": {"clientInfo": {"name": "codex-sync-spike", "version": "0"}}})
r = wait(1); 
if "error" in r: print("INIT ERROR", r); p.kill(); sys.exit(1)
send({"method": "initialized", "params": {}})
kinds = ["cli","vscode","exec","appServer","subAgent","subAgentReview","subAgentCompact","subAgentThreadSpawn","subAgentOther","unknown"]
threads, cursor, rid = [], None, 2
while True:
    params = {"limit": 200, "sourceKinds": kinds, "sortKey": "created_at", **extra}
    if cursor: params["cursor"] = cursor
    send({"id": rid, "method": "thread/list", "params": params}); r = wait(rid); rid += 1
    if "error" in r: print("LIST ERROR", json.dumps(r)[:400]); break
    threads += r["result"]["data"]; cursor = r["result"].get("nextCursor")
    if not cursor: break
p.kill()
listed = {t.get("path") for t in threads if t.get("path")}
on_disk = set(glob.glob(os.path.join(home, "sessions", "**", "*.jsonl"), recursive=True))
print(f"listed={len(threads)}  files_on_disk={len(on_disk)}  listed_paths_on_disk={len(listed & on_disk)}  missing_from_listing={len(on_disk - listed)}  listed_not_on_disk={len(listed - on_disk)}")
for m in sorted(on_disk - listed)[:8]: print("  MISSING:", m.replace(home + "/", ""))
for m in sorted(listed - on_disk)[:3]: print("  EXTRA:", m)
err = p.stderr.read()[-600:]
if err.strip(): print("stderr tail:", err.strip().replace("\n", " | ")[:600])
