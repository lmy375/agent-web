// Package pi drives the `pi` coding agent through its RPC mode: one subprocess
// per live thread, one JSON object per line on stdin and on stdout, and every
// command answered by a response line carrying the id the command was sent
// with. pi keeps stdout to JSON alone -- its own console output is redirected
// to stderr -- and exits cleanly when stdin closes.
package pi

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
	requestTimeout = 60 * time.Second
	// startupTimeout covers the first request after a launch: pi loads its
	// model catalog, extensions and the session file before it answers.
	startupTimeout = 120 * time.Second
	// maxLineBytes bounds one stdout line. pi echoes an owner prompt back as
	// one line, base64 images included, so this has to hold what the image
	// limits let through.
	maxLineBytes = 128 << 20
)

// response is the line pi writes for every command it was sent.
type response struct {
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// handlers are what the owning session plugs into a process. All of them run
// on the reader goroutine, one line at a time, so none of them may wait for a
// response of its own.
type handlers struct {
	// onEvent receives every line that is neither a response nor an
	// extension dialog, still as raw JSON.
	onEvent func(kind string, raw json.RawMessage)
	// onUIRequest receives an extension_ui_request; answering, when the
	// method needs an answer, is the handler's job via process.send.
	onUIRequest func(raw json.RawMessage)
	// onExit fires once, after the process is gone for any reason.
	onExit func(err error)
}

// process is one `pi --mode rpc` subprocess.
type process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *ringBuffer

	writeMu sync.Mutex
	counter atomic.Uint64

	pendingMu sync.Mutex
	pending   map[string]chan response

	closed atomic.Bool
	done   chan struct{}
}

type launch struct {
	bin  string
	args []string
	cwd  string
}

func start(spec launch, h handlers) (*process, error) {
	cmd := exec.Command(spec.bin, spec.args...)
	cmd.Dir = spec.cwd
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
		return nil, fmt.Errorf("cannot start %s: %w", spec.bin, err)
	}

	p := &process{
		cmd:     cmd,
		stdin:   stdin,
		stderr:  newRingBuffer(8 << 10),
		pending: map[string]chan response{},
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
// reaps the child and unblocks everyone waiting on a response.
func (p *process) readLoop(stdout io.Reader, h handlers) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxLineBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var head struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &head) != nil {
			continue // A line we cannot even classify is not worth a stream error.
		}
		if head.Type == "response" {
			p.resolve(line)
			continue
		}
		// Copy: the scanner reuses its buffer under the handler's feet.
		raw := make(json.RawMessage, len(line))
		copy(raw, line)
		if head.Type == "extension_ui_request" {
			h.onUIRequest(raw)
			continue
		}
		h.onEvent(head.Type, raw)
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

func (p *process) resolve(line []byte) {
	var r response
	if json.Unmarshal(line, &r) != nil {
		return
	}
	p.pendingMu.Lock()
	waiter, ok := p.pending[r.ID]
	delete(p.pending, r.ID)
	p.pendingMu.Unlock()
	if ok {
		waiter <- r
	}
}

// failPending unblocks everyone waiting on a response the dead process will
// never send, and hands each of them the reason it died.
func (p *process) failPending(cause error) {
	reason := "the pi process exited"
	if cause != nil {
		reason = "the pi process exited: " + cause.Error()
	}
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	for id, waiter := range p.pending {
		waiter <- response{ID: id, Error: reason}
		delete(p.pending, id)
	}
}

var errProcessGone = errors.New("the pi process is gone")

// send writes one line pi will not answer, such as an extension_ui_response.
func (p *process) send(v any) error {
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

// request sends one command and waits for its response. cmd must marshal to
// an object carrying at least {"type": ...}; the id is added here. A response
// with success:false is returned as an error carrying pi's message.
func (p *process) request(ctx context.Context, cmd map[string]any, timeout time.Duration) (json.RawMessage, error) {
	id := fmt.Sprintf("req_%d", p.counter.Add(1))
	line := make(map[string]any, len(cmd)+1)
	for k, v := range cmd {
		line[k] = v
	}
	line["id"] = id

	waiter := make(chan response, 1)
	p.pendingMu.Lock()
	p.pending[id] = waiter
	p.pendingMu.Unlock()

	if err := p.send(line); err != nil {
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-waiter:
		if !r.Success {
			return nil, errors.New(r.Error)
		}
		return r.Data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
		return nil, fmt.Errorf("%s did not answer within %s", cmd["type"], timeout)
	}
}

// stop closes stdin, which is how pi is asked to exit and the only way that
// lets it flush its last stdout lines; it is killed if it has not gone after
// the grace period.
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
