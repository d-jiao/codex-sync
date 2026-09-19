package desktop

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// sourceKinds are the user-visible thread kinds; sub-agent threads are children.
var sourceKinds = []string{"cli", "vscode", "exec", "appServer"}

// requestTimeout bounds each app-server request; the first thread/list after a
// large pull is where the engine indexes the new rollouts.
const requestTimeout = 5 * time.Minute

// Listed counts the user-visible threads the engine returned.
type Listed struct {
	Live     int
	Archived int
}

// IndexRollouts runs the engine once against baseDir and pages through
// thread/list with useStateDbOnly=false, which is what makes the engine scan
// sessions/ and archived_sessions/ and index rollouts it has not seen. The
// engine is killed afterwards; its state lives in state_5.sqlite.
func IndexRollouts(ctx context.Context, bin, baseDir string, providers []string) (Listed, error) {
	var listed Listed
	cmd := exec.CommandContext(ctx, bin, "app-server", "--stdio")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+baseDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return listed, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return listed, err
	}
	if err := cmd.Start(); err != nil {
		return listed, fmt.Errorf("start engine %s: %w", bin, err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	c := &rpcClient{in: stdin, lines: make(chan []byte, 64)}
	go c.pump(stdout)

	if _, err := c.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "codex-sync", "version": "0"},
	}); err != nil {
		return listed, err
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
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
			raw, err := c.call(ctx, "thread/list", params)
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

// rpcClient is a minimal JSON-RPC-over-stdio client for the app-server protocol.
type rpcClient struct {
	in    interface{ Write([]byte) (int, error) }
	lines chan []byte
	next  int
}

func (c *rpcClient) pump(r interface{ Read([]byte) (int, error) }) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := make([]byte, len(sc.Bytes()))
		copy(line, sc.Bytes())
		c.lines <- line
	}
	close(c.lines)
}

func (c *rpcClient) send(msg map[string]any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = c.in.Write(append(b, '\n'))
	return err
}

func (c *rpcClient) notify(method string, params any) error {
	return c.send(map[string]any{"method": method, "params": params})
}

// call sends a request and waits for the response carrying its id; other
// messages (notifications, unrelated responses) are skipped.
func (c *rpcClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.next++
	id := c.next
	if err := c.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	deadline := time.NewTimer(requestTimeout)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("%s: no response within %s", method, requestTimeout)
		case line, ok := <-c.lines:
			if !ok {
				return nil, errors.New(method + ": engine exited before answering")
			}
			var msg struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(line, &msg) != nil || msg.ID == nil || *msg.ID != id {
				continue
			}
			if msg.Error != nil {
				return nil, fmt.Errorf("%s: %s", method, msg.Error.Message)
			}
			return msg.Result, nil
		}
	}
}
