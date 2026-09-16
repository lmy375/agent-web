// Package claudecode drives the `claude` CLI over the same stream-json stdio
// protocol the official SDKs speak: newline-delimited JSON out of the process
// for transcript messages, and a request/response control channel multiplexed
// onto the same two pipes for everything interactive (initialize, interrupt,
// set_model, get_context_usage, and the CLI's own can_use_tool prompts).
package claudecode

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

// controlTimeout bounds a control round-trip. initialize is slower than the
// rest because the CLI starts MCP servers before answering it.
const (
	controlTimeout    = 60 * time.Second
	initializeTimeout = 120 * time.Second
	maxLineBytes      = 32 << 20
)

// envelope is the shape shared by everything on the CLI's stdout.
type envelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response"`
}

type controlResponse struct {
	Subtype   string          `json:"subtype"`
	RequestID string          `json:"request_id"`
	Response  json.RawMessage `json:"response"`
	Error     string          `json:"error"`
}

// handlers are what the owning session plugs into a process.
type handlers struct {
	// onMessage receives every non-control line, still as raw JSON.
	onMessage func(kind string, raw json.RawMessage)
	// onControlRequest receives a request the CLI made of us -- in practice
	// can_use_tool. Replying is the handler's job, via process.reply.
	onControlRequest func(requestID, subtype string, payload json.RawMessage)
	// onExit fires once, after the process is gone for any reason.
	onExit func(err error)
}

// process is one `claude` subprocess.
type process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *ringBuffer

	writeMu sync.Mutex
	counter atomic.Uint64

	pendingMu sync.Mutex
	pending   map[string]chan controlResponse

	closed atomic.Bool
	done   chan struct{}
}

type launch struct {
	bin  string
	args []string
	cwd  string
	env  []string
}

func start(spec launch, h handlers) (*process, error) {
	cmd := exec.Command(spec.bin, spec.args...)
	cmd.Dir = spec.cwd
	cmd.Env = append(os.Environ(), spec.env...)

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
		return nil, fmt.Errorf("cannot start %s: %w", spec.bin, err)
	}

	p := &process{
		cmd:     cmd,
		stdin:   stdin,
		stderr:  newRingBuffer(8 << 10),
		pending: map[string]chan controlResponse{},
		done:    make(chan struct{}),
	}
	go p.drainStderr(stderrPipe)
	go p.readLoop(stdout, h)
	return p, nil
}

func (p *process) drainStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		p.stderr.write(scanner.Text())
	}
}

// readLoop owns the process lifetime: it ends when stdout closes, and then
// reaps the child and unblocks everyone waiting on a control response.
func (p *process) readLoop(stdout io.Reader, h handlers) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxLineBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env envelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue // A line we cannot even classify is not worth a stream error.
		}
		switch env.Type {
		case "control_response":
			p.resolve(env.Response)
		case "control_request":
			var req struct {
				Subtype string `json:"subtype"`
			}
			_ = json.Unmarshal(env.Request, &req)
			h.onControlRequest(env.RequestID, req.Subtype, env.Request)
		case "control_cancel_request":
			// The CLI abandoned a request it made of us; the pending decision
			// simply stops mattering and its answer is dropped on write.
		default:
			// Copy: the scanner reuses its buffer under the handler's feet.
			raw := make(json.RawMessage, len(line))
			copy(raw, line)
			h.onMessage(env.Type, raw)
		}
	}

	err := p.cmd.Wait()
	if scanErr := scanner.Err(); scanErr != nil && err == nil {
		err = scanErr
	}
	if err != nil && p.stderr.text() != "" {
		err = fmt.Errorf("%w: %s", err, p.stderr.text())
	}
	p.closed.Store(true)
	close(p.done)
	p.failPending(err)
	h.onExit(err)
}

func (p *process) resolve(raw json.RawMessage) {
	var response controlResponse
	if json.Unmarshal(raw, &response) != nil {
		return
	}
	p.pendingMu.Lock()
	waiter, ok := p.pending[response.RequestID]
	delete(p.pending, response.RequestID)
	p.pendingMu.Unlock()
	if ok {
		waiter <- response
	}
}

// failPending unblocks everyone waiting on a response the dead process will
// never send, and hands each of them the reason it died.
func (p *process) failPending(cause error) {
	reason := "the claude process exited"
	if cause != nil {
		reason = "the claude process exited: " + cause.Error()
	}
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	for id, waiter := range p.pending {
		waiter <- controlResponse{Subtype: "error", RequestID: id, Error: reason}
		delete(p.pending, id)
	}
}

var errProcessGone = errors.New("the claude process is gone")

func (p *process) writeJSON(v any) error {
	if p.closed.Load() {
		return errProcessGone
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.stdin.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("%w: %v", errProcessGone, err)
	}
	return nil
}

// control sends one control request and waits for its response. request must
// marshal to an object carrying at least {"subtype": ...}.
func (p *process) control(ctx context.Context, request any, timeout time.Duration) (json.RawMessage, error) {
	id := fmt.Sprintf("req_%d_%d", p.counter.Add(1), time.Now().UnixNano())
	waiter := make(chan controlResponse, 1)
	p.pendingMu.Lock()
	p.pending[id] = waiter
	p.pendingMu.Unlock()

	if err := p.writeJSON(map[string]any{"type": "control_request", "request_id": id, "request": request}); err != nil {
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case response := <-waiter:
		if response.Subtype == "error" {
			return nil, errors.New(response.Error)
		}
		return response.Response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
		return nil, fmt.Errorf("control request timed out")
	}
}

// reply answers a control request the CLI made of us.
func (p *process) reply(requestID string, payload any) error {
	return p.writeJSON(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype": "success", "request_id": requestID, "response": payload,
		},
	})
}

func (p *process) replyError(requestID string, message string) error {
	return p.writeJSON(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype": "error", "request_id": requestID, "error": message,
		},
	})
}

// stop closes stdin, which is how the CLI is asked to exit, and kills it if it
// has not gone after the grace period.
func (p *process) stop() {
	_ = p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

// ringBuffer keeps the tail of stderr so a crash can say why.
type ringBuffer struct {
	mu    sync.Mutex
	limit int
	text_ string
}

func newRingBuffer(limit int) *ringBuffer { return &ringBuffer{limit: limit} }

func (b *ringBuffer) write(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.text_ += line + "\n"
	if len(b.text_) > b.limit {
		b.text_ = b.text_[len(b.text_)-b.limit:]
	}
}

func (b *ringBuffer) text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text_
}
