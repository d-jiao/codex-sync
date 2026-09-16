#!/usr/bin/env python3
"""Make the Codex desktop app (ChatGPT.app) show threads that arrived via codex-sync.

The desktop app catalogs threads incrementally: after its one-time full build it only
looks at threads newer than the last `updated_at` it has seen, so a pulled thread —
whose `updated_at` is older — never appears (spec §13). Its engine also indexes rollout
files only when it discovers them itself. With the app QUIT, this script:

  1. backs up state_5.sqlite* and sqlite/codex-dev.db* to ~/.codex-sync/db-backup-<ts>/
  2. runs the app's own engine once (`codex app-server`, full `thread/list`) so it
     indexes every rollout on disk into state_5.sqlite
  3. copies thread names from session_index.jsonl into threads.name where empty
     (spec §8; skip with --no-names)
  4. clears the catalog's last_full_reconciled_at so the app redoes its full sweep on
     the next launch

Run it after any pull that brought new threads, then relaunch the app. Idempotent.
Never run it while the ChatGPT app or a codex process is using the same home.
"""
import argparse
import glob
import json
import os
import queue
import re
import shutil
import sqlite3
import subprocess
import sys
import threading
import time

KINDS = ["cli", "vscode", "exec", "appServer"]  # user-visible thread kinds
APP_ENGINE = "/Applications/ChatGPT.app/Contents/Resources/codex"


def codex_home():
    return os.path.expanduser(os.environ.get("CODEX_HOME") or "~/.codex")


def fail(msg):
    print("error:", msg, file=sys.stderr)
    sys.exit(1)


def running_codex_processes():
    out = subprocess.run(["pgrep", "-fl", r"ChatGPT\.app/Contents/MacOS/ChatGPT|Resources/codex |codex app-server"],
                         capture_output=True, text=True).stdout
    return [l for l in out.splitlines() if str(os.getpid()) not in l.split()[:1]]


def providers(home):
    found = {"openai"}
    try:
        with open(os.path.join(home, "config.toml")) as f:
            for line in f:
                m = re.match(r'\s*\[model_providers\.([^\]]+)\]', line)
                if m:
                    found.add(m.group(1).strip('"'))
    except FileNotFoundError:
        pass
    return sorted(found)


def backup(home):
    dest = os.path.expanduser(time.strftime("~/.codex-sync/db-backup-%Y%m%d-%H%M%S"))
    os.makedirs(os.path.join(dest, "sqlite"), exist_ok=True)
    files = glob.glob(os.path.join(home, "state_5.sqlite*")) + [os.path.join(home, "session_index.jsonl")]
    for p in files:
        if os.path.exists(p):
            shutil.copy2(p, dest)
    for p in glob.glob(os.path.join(home, "sqlite", "codex-dev.db*")):
        shutil.copy2(p, os.path.join(dest, "sqlite"))
    return dest


def index_rollouts(codex_bin, home):
    """Start the engine on `home` and page through thread/list so it indexes rollouts."""
    env = dict(os.environ, CODEX_HOME=home)
    try:
        p = subprocess.Popen([codex_bin, "app-server", "--stdio"], stdin=subprocess.PIPE,
                             stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, env=env, text=True)
    except FileNotFoundError:
        fail(f"codex engine binary not found: {codex_bin} (use --codex-bin or CODEX_BIN)")
    lines = queue.Queue()

    def pump():
        for line in p.stdout:
            lines.put(line)
        lines.put(None)

    threading.Thread(target=pump, daemon=True).start()

    def send(o):
        p.stdin.write(json.dumps(o) + "\n")
        p.stdin.flush()

    def wait(rid, timeout=300):
        end = time.time() + timeout
        while True:
            remaining = end - time.time()
            if remaining <= 0:
                raise RuntimeError(f"timeout waiting for request {rid}")
            try:
                line = lines.get(timeout=remaining)
            except queue.Empty:
                raise RuntimeError(f"timeout waiting for request {rid}")
            if line is None:
                raise RuntimeError("app-server closed its output")
            try:
                m = json.loads(line)
            except ValueError:
                continue
            if m.get("id") == rid:
                if "error" in m:
                    raise RuntimeError(m["error"])
                return m["result"]

    provs = providers(home)
    listed = {"live": 0, "archived": 0}
    try:
        send({"id": 1, "method": "initialize",
              "params": {"clientInfo": {"name": "codex-desktop-refresh", "version": "0"}}})
        wait(1)
        send({"method": "initialized", "params": {}})
        rid = 2
        for archived in (False, True):
            cursor = None
            while True:
                params = {"limit": 200, "sourceKinds": KINDS, "modelProviders": provs,
                          "archived": archived, "useStateDbOnly": False}
                if cursor:
                    params["cursor"] = cursor
                send({"id": rid, "method": "thread/list", "params": params})
                res = wait(rid)
                rid += 1
                listed["archived" if archived else "live"] += len(res["data"])
                cursor = res.get("nextCursor")
                if not cursor:
                    break
    finally:
        p.kill()
        p.wait()
    return listed


def reconcile_names(home):
    names = {}
    try:
        with open(os.path.join(home, "session_index.jsonl")) as f:
            for line in f:
                try:
                    e = json.loads(line)
                except ValueError:
                    continue
                if e.get("id") and e.get("thread_name"):
                    names[e["id"]] = e["thread_name"]
    except FileNotFoundError:
        return 0, 0, 0
    db = sqlite3.connect(os.path.join(home, "state_5.sqlite"))
    try:
        cols = {r[1] for r in db.execute("pragma table_info(threads)")}
        if "name" not in cols:
            fail("threads.name column missing — engine schema changed; not touching the DB")
        before = db.execute("select count(*) from threads where name is not null and name <> ''").fetchone()[0]
        with db:
            db.executemany("update threads set name = ? where id = ? and (name is null or name = '')",
                           [(n, i) for i, n in names.items()])
        after = db.execute("select count(*) from threads where name is not null and name <> ''").fetchone()[0]
    finally:
        db.close()
    return len(names), before, after


def reset_catalog(home):
    path = os.path.join(home, "sqlite", "codex-dev.db")
    if not os.path.exists(path):
        return "no desktop catalog found (sqlite/codex-dev.db) — nothing to reset"
    db = sqlite3.connect(path)
    try:
        cols = {r[1] for r in db.execute("pragma table_info(local_thread_catalog_sync_state)")}
        if not {"host_id", "last_full_reconciled_at"} <= cols:
            return "catalog schema not recognised — not touching it"
        with db:
            n = db.execute("update local_thread_catalog_sync_state set last_full_reconciled_at = null"
                           " where host_id = 'local'").rowcount
        return f"catalog full sweep scheduled for next launch ({n} host row updated)"
    finally:
        db.close()


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--codex-bin", default=os.environ.get("CODEX_BIN") or (APP_ENGINE if os.path.exists(APP_ENGINE) else "codex"),
                    help="engine binary; the ChatGPT app's bundled one is used when present")
    ap.add_argument("--no-names", action="store_true", help="skip copying names from session_index.jsonl")
    ap.add_argument("--no-backup", action="store_true", help="skip the database backup")
    a = ap.parse_args()

    home = codex_home()
    if not os.path.isdir(os.path.join(home, "sessions")):
        fail(f"{home} has no sessions/ directory")
    procs = running_codex_processes()
    if procs:
        fail("quit the ChatGPT app (and any codex app-server) first:\n  " + "\n  ".join(procs))

    print(f"home: {home}\nengine: {a.codex_bin}")
    if not a.no_backup:
        print("backup:", backup(home))
    listed = index_rollouts(a.codex_bin, home)
    db = sqlite3.connect(f"file:{os.path.join(home, 'state_5.sqlite')}?mode=ro", uri=True)
    rows = db.execute("select count(*) from threads").fetchone()[0]
    db.close()
    print(f"engine indexed rollouts: {listed['live']} live + {listed['archived']} archived user-visible threads listed; {rows} thread rows")
    if not a.no_names:
        total, before, after = reconcile_names(home)
        print(f"names: {total} in session_index.jsonl; named threads {before} -> {after}")
    print(reset_catalog(home))
    print("done — launch the ChatGPT app; its first scan rebuilds the sidebar from the engine")


if __name__ == "__main__":
    main()
