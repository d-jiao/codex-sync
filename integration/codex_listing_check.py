#!/usr/bin/env python3
"""Compare the user-visible Codex threads of two homes (spec §12, step 3).

Lists threads through the app-server protocol of a Codex engine binary,
using every model provider defined in the source home's config.toml so the
comparison is not narrowed to the current provider. Nothing in either home is
modified beyond what the engine itself writes on startup.

Usage:
  codex_listing_check.py --source ~/.codex --synced /path/to/other/home \
      [--codex-bin /Applications/ChatGPT.app/Contents/Resources/codex]
"""
import argparse, json, os, re, subprocess, sys, time

KINDS = ["cli", "vscode", "exec", "appServer"]  # user-visible kinds; sub-agent threads are children


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


def list_threads(codex_bin, home, provs):
    env = dict(os.environ, CODEX_HOME=home)
    p = subprocess.Popen([codex_bin, "app-server", "--stdio"], stdin=subprocess.PIPE,
                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, text=True)

    def send(o):
        p.stdin.write(json.dumps(o) + "\n"); p.stdin.flush()

    def wait(rid, timeout=120):
        end = time.time() + timeout
        while time.time() < end:
            line = p.stdout.readline()
            if not line:
                raise RuntimeError("app-server closed its output")
            try:
                m = json.loads(line)
            except ValueError:
                continue
            if m.get("id") == rid:
                if "error" in m:
                    raise RuntimeError(m["error"])
                return m["result"]
        raise RuntimeError("timeout waiting for id %s" % rid)

    try:
        send({"id": 1, "method": "initialize", "params": {"clientInfo": {"name": "codex-sync-check", "version": "0"}}})
        wait(1)
        send({"method": "initialized", "params": {}})
        threads, cursor, rid = {}, None, 2
        for archived in (False, True):
            cursor = None
            while True:
                params = {"limit": 200, "sourceKinds": KINDS, "modelProviders": provs, "archived": archived}
                if cursor:
                    params["cursor"] = cursor
                send({"id": rid, "method": "thread/list", "params": params})
                res = wait(rid); rid += 1
                for t in res["data"]:
                    threads[t["id"]] = {"name": t.get("name"), "archived": archived}
                cursor = res.get("nextCursor")
                if not cursor:
                    break
        return threads
    finally:
        p.kill()


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--source", required=True, help="Codex home the data was pushed from")
    ap.add_argument("--synced", required=True, help="Codex home that pulled it")
    ap.add_argument("--codex-bin", default=os.environ.get("CODEX_BIN", "codex"), help="engine binary (default: codex on PATH or $CODEX_BIN)")
    a = ap.parse_args()

    provs = providers(a.source)
    src = list_threads(a.codex_bin, os.path.expanduser(a.source), provs)
    dst = list_threads(a.codex_bin, os.path.expanduser(a.synced), provs)
    missing = sorted(set(src) - set(dst))
    extra = sorted(set(dst) - set(src))
    unnamed = sorted(i for i in src if src[i]["name"] and i in dst and not dst[i]["name"])

    print(f"source threads: {len(src)}   synced threads: {len(dst)}   providers: {provs}")
    for i in missing[:20]:
        print("  MISSING in synced:", i)
    for i in extra[:20]:
        print("  EXTRA in synced:  ", i)
    if unnamed:
        print(f"  {len(unnamed)} threads are named in source but unnamed in synced (known v1 limitation, spec §8)")
    ok = not missing and not extra
    print("OK: listings match" if ok else "MISMATCH")
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
