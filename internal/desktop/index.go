package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// sourceKinds are the user-visible thread kinds; sub-agent threads are children.
var sourceKinds = []string{"cli", "vscode", "exec", "appServer"}

const (
	// requestTimeout bounds each app-server request; the first thread/list after
	// a large pull is where the engine indexes the new rollouts.
	requestTimeout = 5 * time.Minute
	// shutdownGrace is how long the engine gets to exit on its own after stdin
	// closes, so it can finish deferred writes before it is killed.
	shutdownGrace = 5 * time.Second
	// stderrLimit caps how much engine stderr is kept for error messages.
	stderrLimit = 8 * 1024
)

// Listed counts the user-visible threads the engine returned.
type Listed struct {
	Live     int
	Archived int
}

// IndexRollouts runs the engine once against baseDir and pages through
// thread/list with useStateDbOnly=false, which is what makes the engine scan
// sessions/ and archived_sessions/ and index rollouts it has not seen. The
// engine's state lives in state_5.sqlite; the process is shut down afterwards.
func IndexRollouts(ctx context.Context, bin, baseDir string, providers []string) (Listed, error) {
	var listed Listed
	eng, err := startEngine(ctx, bin, baseDir)
	if err != nil {
		return listed, err
	}
	defer eng.shutdown()

	if _, err := eng.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "codex-sync", "version": "0"},
	}); err != nil {
		return listed, err
	}
	if err := eng.notify("initialized", map[string]any{}); err != nil {
		return listed, err
	}
	for _, archived := range []bool{false, true} {
		cursor := ""
		for {
			params := map[string]any{
				"limit":          200,
				"sourceKinds":    sourceKinds,
				"modelProviders": providers,
				"archived":       archived,
				"useStateDbOnly": false,
			}
			if cursor != "" {
				params["cursor"] = cursor
			}
			raw, err := eng.call(ctx, "thread/list", params)
			if err != nil {
				return listed, err
			}
			var page struct {
				Data       []json.RawMessage `json:"data"`
				NextCursor *string           `json:"nextCursor"`
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				return listed, fmt.Errorf("thread/list: %w", err)
			}
			if archived {
				listed.Archived += len(page.Data)
			} else {
				listed.Live += len(page.Data)
			}
			if page.NextCursor == nil || *page.NextCursor == "" {
				break
			}
			cursor = *page.NextCursor
		}
	}
	return listed, nil
}

// engine is a running `codex app-server --stdio` spoken to over JSON-RPC.
type engine struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stderr  *boundedBuffer
	lines   chan []byte
	pumpErr error // set before lines is closed
	exited  chan struct{}
	waitErr error // valid once exited is closed
	next    int
}

func startEngine(ctx context.Context, bin, baseDir string) (*engine, error) {
	cmd := exec.CommandContext(ctx, bin, "app-server", "--stdio")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+baseDir)
	// Stderr is a pipe, and Wait would otherwise block until every process that
	// inherited it — an MCP server the engine spawned, say — has exited.
	cmd.WaitDelay = shutdownGrace
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	e := &engine{cmd: cmd, stdin: stdin, stderr: &boundedBuffer{limit: stderrLimit},
		lines: make(chan []byte, 64), exited: make(chan struct{})}
	cmd.Stderr = e.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start engine %s: %w", bin, err)
	}
	go e.pump(stdout)
	go func() {
		e.waitErr = cmd.Wait()
		close(e.exited)
	}()
	return e, nil
}

// pump delivers stdout lines; on EOF or a scan error it records the error and
// closes the channel.
func (e *engine) pump(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := make([]byte, len(sc.Bytes()))
		copy(line, sc.Bytes())
		e.lines <- line
	}
	e.pumpErr = sc.Err()
	close(e.lines)
}

// shutdown closes stdin so the engine can exit cleanly, then kills it if it
// does not within shutdownGrace.
func (e *engine) shutdown() {
	_ = e.stdin.Close()
	select {
	case <-e.exited:
	case <-time.After(shutdownGrace):
		_ = e.cmd.Process.Kill()
		<-e.exited
	}
}

// exitReason describes why the engine stopped answering, with its exit status
// and the tail of its stderr when available.
func (e *engine) exitReason() string {
	select {
	case <-e.exited:
	case <-time.After(2 * time.Second):
		return "engine stopped answering"
	}
	msg := "engine exited"
	if e.waitErr != nil {
		msg += " (" + e.waitErr.Error() + ")"
	}
	if s := strings.TrimSpace(e.stderr.String()); s != "" {
		msg += ": " + s
	}
	if e.pumpErr != nil {
		msg += "; reading its output failed: " + e.pumpErr.Error()
	}
	return msg
}

func (e *engine) send(msg map[string]any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = e.stdin.Write(append(b, '\n'))
	return err
}

func (e *engine) notify(method string, params any) error {
	return e.send(map[string]any{"method": method, "params": params})
}

// call sends a request and waits for the response carrying its id. Anything
// else — notifications, server-initiated requests (which carry a method, even
// when their id collides with ours), non-JSON lines — is skipped.
func (e *engine) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	e.next++
	id := e.next
	if err := e.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		// A broken pipe means the engine already died; say why rather than EPIPE.
		return nil, fmt.Errorf("%s: %s", method, e.exitReason())
	}
	deadline := time.NewTimer(requestTimeout)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("%s: no response within %s", method, requestTimeout)
		case line, ok := <-e.lines:
			if !ok {
				return nil, fmt.Errorf("%s: %s", method, e.exitReason())
			}
			var msg struct {
				ID     *int            `json:"id"`
				Method string          `json:"method"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(line, &msg) != nil || msg.ID == nil || *msg.ID != id || msg.Method != "" {
				continue
			}
			if msg.Error != nil {
				return nil, fmt.Errorf("%s: %s", method, msg.Error.Message)
			}
			return msg.Result, nil
		}
	}
}

// boundedBuffer keeps the last `limit` bytes written to it.
type boundedBuffer struct {
	limit int
	buf   bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.buf.Write(p)
	if extra := b.buf.Len() - b.limit; extra > 0 {
		b.buf.Next(extra)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string { return b.buf.String() }
