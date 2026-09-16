// Package codex drives `codex app-server`: one process for every thread, with
// JSON-RPC 2.0 over stdio. Unlike the Claude CLI, the server is multi-threaded,
// so notifications carry a threadId and the backend fans them out itself.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const (
	requestTimeout = 120 * time.Second
	maxLineBytes   = 32 << 20
)

// message is any JSON-RPC frame. A request has an id and a method, a
// notification a method alone, and a response an id alone.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e rpcError) Error() string { return fmt.Sprintf("codex error %d: %s", e.Code, e.Message) }

// rpcHandlers are what the backend plugs into the client.
type rpcHandlers struct {
	// onNotification receives a server-initiated message with no reply.
	onNotification func(method string, params json.RawMessage)
	// onRequest receives a server-initiated request the backend must answer
	// via client.respond, most importantly the approval prompts.
	onRequest func(id json.RawMessage, method string, params json.RawMessage)
	onExit    func(err error)
}

// client is one `codex app-server` process.
type client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *tail

	writeMu sync.Mutex
	counter atomic.Int64

	pendingMu sync.Mutex
	pending   map[string]chan message

	closed atomic.Bool
	done   chan struct{}
}

func dial(bin, cwd string, h rpcHandlers) (*client, error) {
	cmd := exec.Command(bin, "app-server")
	cmd.Dir = cwd
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot start %s app-server: %w", bin, err)
	}

	c := &client{
		cmd: cmd, stdin: stdin, stderr: newTail(8 << 10),
		pending: map[string]chan message{}, done: make(chan struct{}),
	}
	go c.drainStderr(stderrPipe)
	go c.readLoop(stdout, h)
	return c, nil
}

func (c *client) drainStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		c.stderr.write(scanner.Text())
	}
}

func (c *client) readLoop(stdout io.Reader, h rpcHandlers) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxLineBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg message
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		switch {
		case msg.Method != "" && len(msg.ID) > 0:
			id := append(json.RawMessage(nil), msg.ID...)
			params := append(json.RawMessage(nil), msg.Params...)
			h.onRequest(id, msg.Method, params)
		case msg.Method != "":
			params := append(json.RawMessage(nil), msg.Params...)
			h.onNotification(msg.Method, params)
		case len(msg.ID) > 0:
			c.resolve(msg)
		}
	}

	err := c.cmd.Wait()
	if scanErr := scanner.Err(); scanErr != nil && err == nil {
		err = scanErr
	}
	if err != nil && c.stderr.text() != "" {
		err = fmt.Errorf("%w: %s", err, c.stderr.text())
	}
	c.closed.Store(true)
	close(c.done)
	c.failPending(err)
	h.onExit(err)
}

func (c *client) resolve(msg message) {
	c.pendingMu.Lock()
	waiter, ok := c.pending[string(msg.ID)]
	delete(c.pending, string(msg.ID))
	c.pendingMu.Unlock()
	if ok {
		waiter <- msg
	}
}

func (c *client) failPending(cause error) {
	reason := "the codex app-server exited"
	if cause != nil {
		reason += ": " + cause.Error()
	}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, waiter := range c.pending {
		waiter <- message{Error: &rpcError{Code: -32000, Message: reason}}
		delete(c.pending, id)
	}
}

var errServerGone = errors.New("the codex app-server is gone")

func (c *client) write(v any) error {
	if c.closed.Load() {
		return errServerGone
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("%w: %v", errServerGone, err)
	}
	return nil
}

func (c *client) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// call sends a request and decodes its result into out, which may be nil when
// the caller only needs to know the request succeeded.
func (c *client) call(ctx context.Context, method string, params any, out any) error {
	id := c.counter.Add(1)
	key := fmt.Sprintf("%d", id)
	waiter := make(chan message, 1)
	c.pendingMu.Lock()
	c.pending[key] = waiter
	c.pendingMu.Unlock()

	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
		return err
	}

	timer := time.NewTimer(requestTimeout)
	defer timer.Stop()
	select {
	case response := <-waiter:
		if response.Error != nil {
			return *response.Error
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(response.Result, out)
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
		return fmt.Errorf("codex %s timed out", method)
	}
}

// respond answers a request the server made of us.
func (c *client) respond(id json.RawMessage, result any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *client) stop() {
	_ = c.stdin.Close()
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.done
	}
}

// tail keeps the last bytes of stderr so a crash can say why.
type tail struct {
	mu    sync.Mutex
	limit int
	text_ string
}

func newTail(limit int) *tail { return &tail{limit: limit} }

func (t *tail) write(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.text_ += line + "\n"
	if len(t.text_) > t.limit {
		t.text_ = t.text_[len(t.text_)-t.limit:]
	}
}

func (t *tail) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.text_
}
