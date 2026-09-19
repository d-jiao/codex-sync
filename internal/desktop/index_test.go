package desktop

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeEngine writes a wrapper script that re-runs this test binary as a fake
// `codex app-server`; every request it receives is appended to logPath.
func fakeEngine(t *testing.T, logPath, mode string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "codex")
	body := fmt.Sprintf("#!/bin/sh\nGO_WANT_HELPER_PROCESS=1 FAKE_CODEX_LOG=%q FAKE_CODEX_MODE=%q exec %q -test.run=TestHelperProcess -- \"$@\"\n", logPath, mode, exe)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// TestHelperProcess is the fake app-server; it is only active when spawned by fakeEngine.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args[len(os.Args)-2:]
	if strings.Join(args, " ") != "app-server --stdio" {
		fmt.Fprintf(os.Stderr, "unexpected args %v\n", os.Args)
		os.Exit(2)
	}
	if os.Getenv("FAKE_CODEX_MODE") == "die" {
		os.Exit(1)
	}
	logf, _ := os.OpenFile(os.Getenv("FAKE_CODEX_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	defer logf.Close()
	fmt.Fprintf(logf, "{\"env\":{\"CODEX_HOME\":%q}}\n", os.Getenv("CODEX_HOME"))

	in := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	for in.Scan() {
		fmt.Fprintln(logf, in.Text())
		var req struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(in.Bytes(), &req) != nil || req.ID == nil {
			continue // notification or noise
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"userAgent": "fake"}
		case "thread/list":
			var p struct {
				Cursor   string `json:"cursor"`
				Archived bool   `json:"archived"`
			}
			_ = json.Unmarshal(req.Params, &p)
			switch {
			case p.Archived:
				result = map[string]any{"data": []map[string]any{{"id": "arch-1"}}, "nextCursor": nil}
			case p.Cursor == "":
				result = map[string]any{"data": []map[string]any{{"id": "live-1"}, {"id": "live-2"}}, "nextCursor": "page-2"}
			default:
				result = map[string]any{"data": []map[string]any{{"id": "live-3"}}, "nextCursor": nil}
			}
		default:
			fmt.Fprintf(out, `{"id":%d,"error":{"code":-32601,"message":"unknown method %s"}}`+"\n", *req.ID, req.Method)
			out.Flush()
			continue
		}
		b, _ := json.Marshal(map[string]any{"id": *req.ID, "result": result})
		out.Write(b)
		out.WriteString("\n")
		out.Flush()
	}
	os.Exit(0)
}

func TestIndexRolloutsPagesThroughLiveAndArchivedThreads(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "requests.log")
	base := t.TempDir()
	bin := fakeEngine(t, logPath, "ok")

	listed, err := IndexRollouts(context.Background(), bin, base, []string{"cpa", "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if listed.Live != 3 || listed.Archived != 1 {
		t.Errorf("listed = %+v, want 3 live, 1 archived", listed)
	}

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if !strings.Contains(lines[0], fmt.Sprintf("%q", base)) {
		t.Errorf("engine was not started with CODEX_HOME=%s: %s", base, lines[0])
	}
	var methods []string
	for _, l := range lines[1:] {
		var m struct {
			Method string `json:"method"`
			Params struct {
				UseStateDbOnly *bool    `json:"useStateDbOnly"`
				SourceKinds    []string `json:"sourceKinds"`
				ModelProviders []string `json:"modelProviders"`
				Limit          int      `json:"limit"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad request line %q: %v", l, err)
		}
		methods = append(methods, m.Method)
		if m.Method == "thread/list" {
			if m.Params.UseStateDbOnly == nil || *m.Params.UseStateDbOnly {
				t.Errorf("thread/list must pass useStateDbOnly=false so the engine scans rollouts: %s", l)
			}
			if len(m.Params.SourceKinds) == 0 || len(m.Params.ModelProviders) != 2 || m.Params.Limit == 0 {
				t.Errorf("thread/list params incomplete: %s", l)
			}
		}
	}
	if want := "initialize initialized thread/list thread/list thread/list"; strings.Join(methods, " ") != want {
		t.Errorf("request sequence = %q, want %q", strings.Join(methods, " "), want)
	}
}

func TestIndexRolloutsReportsAnEngineThatExits(t *testing.T) {
	bin := fakeEngine(t, filepath.Join(t.TempDir(), "requests.log"), "die")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := IndexRollouts(ctx, bin, t.TempDir(), []string{"openai"})
	if err == nil {
		t.Fatal("expected an error when the engine exits before answering")
	}
}

func TestIndexRolloutsReportsAMissingBinary(t *testing.T) {
	_, err := IndexRollouts(context.Background(), filepath.Join(t.TempDir(), "nope"), t.TempDir(), []string{"openai"})
	if err == nil {
		t.Fatal("expected an error for a missing engine binary")
	}
}
