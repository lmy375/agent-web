package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmy375/agent-web/backend/internal/agents/claudecode"
	"github.com/lmy375/agent-web/backend/internal/agents/pi"
	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/config"
	"github.com/lmy375/agent-web/backend/internal/protocol"
	"github.com/lmy375/agent-web/backend/internal/server"
)

// TestClaudeCodeTurn drives a real `claude` subprocess through the whole stack:
// create a thread, open the event stream, prompt, and read the turn back. It
// costs a model call, so it only runs when asked for.
func TestClaudeCodeTurn(t *testing.T) {
	if os.Getenv("AGENT_WEB_E2E") == "" {
		t.Skip("set AGENT_WEB_E2E=1 to run the live harness test")
	}
	work := t.TempDir()
	roundTrip(t, newHarness(t, work, claudeCode), protocol.KindClaudeCode, work)
}

// TestPiTurn is the same round trip through a real `pi --mode rpc` subprocess.
func TestPiTurn(t *testing.T) {
	if os.Getenv("AGENT_WEB_E2E") == "" {
		t.Skip("set AGENT_WEB_E2E=1 to run the live harness test")
	}
	work := t.TempDir()
	roundTrip(t, newHarness(t, work, func(work string, deps chat.Deps) chat.Backend {
		return pi.New(pi.Options{DefaultCwd: work, IdleTimeout: 15 * time.Minute}, deps)
	}), protocol.KindPi, work)
}

// roundTrip is one turn against whichever harness the server was built with:
// the descriptor is usable, a thread is created, the stream is opened before
// the prompt, and the settled transcript agrees with what was streamed.
func roundTrip(t *testing.T, http *harness, kind protocol.AgentKind, work string) {
	t.Helper()

	// A descriptor has to arrive before anything else can be rendered.
	var agents []protocol.AgentDescriptor
	http.get(t, "/api/agents", &agents)
	if len(agents) != 1 || agents[0].Kind != kind {
		t.Fatalf("expected one %s descriptor, got %+v", kind, agents)
	}
	if reason := agents[0].Runtime.UnavailableReason; reason != nil {
		t.Fatalf("%s is unavailable: %s", kind, *reason)
	}
	if len(agents[0].Runtime.Models) == 0 {
		t.Fatal("the probe returned no models")
	}

	var thread protocol.ThreadSummary
	http.post(t, "/api/threads", map[string]any{"agent_kind": kind, "cwd": work}, &thread)
	if thread.Cwd == "" || thread.RunState != protocol.StateIdle {
		t.Fatalf("unexpected new thread: %+v", thread)
	}

	// Open the stream before prompting: the other order drops whatever lands
	// in the gap, which is exactly where a permission prompt would be.
	events := http.stream(t, "/api/threads/"+thread.ThreadID+"/events")
	defer events.close()

	http.post(t, "/api/threads/"+thread.ThreadID+"/input", map[string]any{
		"type":              "prompt",
		"client_message_id": "01JBXQ8G7M4K2P9R3T5V7W9Y1Z",
		"text":              "Reply with exactly the word PONG and nothing else.",
	}, nil)

	seen := map[string]bool{}
	var text strings.Builder
	deadline := time.After(3 * time.Minute)
	for !seen["turn_finished"] {
		select {
		case <-deadline:
			t.Fatalf("timed out; saw %v", keys(seen))
		case frame := <-events.frames:
			seen[frame.kind] = true
			switch frame.kind {
			case "text_delta":
				var delta protocol.TextDeltaEvent
				decode(t, frame.data, &delta)
				text.WriteString(delta.Text)
			case "error":
				var failure protocol.ErrorEvent
				decode(t, frame.data, &failure)
				t.Fatalf("stream error %s: %s", failure.Code, failure.Message)
			}
		}
	}

	for _, want := range []string{"turn_started", "user_message", "assistant_message", "turn_finished"} {
		if !seen[want] {
			t.Errorf("no %s event; saw %v", want, keys(seen))
		}
	}
	if !strings.Contains(strings.ToUpper(text.String()), "PONG") {
		t.Errorf("streamed text was %q", text.String())
	}

	// The settled transcript has to say the same thing as the live stream.
	// Entries are decoded as raw JSON here for the same reason a browser does:
	// the union is discriminated by "type" and only ever travels outbound.
	var page struct {
		Entries []struct {
			Type   string          `json:"type"`
			Blocks json.RawMessage `json:"blocks"`
		} `json:"entries"`
	}
	http.get(t, "/api/threads/"+thread.ThreadID+"/messages", &page)
	kinds := map[string]bool{}
	for _, entry := range page.Entries {
		kinds[entry.Type] = true
	}
	if !kinds["user_message"] || !kinds["assistant_message"] {
		t.Errorf("transcript is missing the prompt or the reply: %+v", page.Entries)
	}
}

// Local commands do not need a model call, but still need the installed CLI.
// Reopening a thread must preserve the reply and hide the CLI's wrappers.
func TestClaudeCodeLocalCommandHistory(t *testing.T) {
	if os.Getenv("AGENT_WEB_E2E") == "" {
		t.Skip("set AGENT_WEB_E2E=1 to run the live harness test")
	}
	work := t.TempDir()
	http := newHarness(t, work, claudeCode)
	var thread protocol.ThreadSummary
	http.post(t, "/api/threads", map[string]any{"agent_kind": "claude_code", "cwd": work}, &thread)
	path := "/api/threads/" + thread.ThreadID
	events := http.stream(t, path+"/events")
	defer events.close()
	http.post(t, path+"/input", map[string]any{
		"type": "prompt", "client_message_id": "01JBXQ8G7M4K2P9R3T5V7W9Y1Z", "text": "/context",
	}, nil)

	var liveReply strings.Builder
	deadline := time.After(2 * time.Minute)
stream:
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for /context")
		case frame, ok := <-events.frames:
			if !ok {
				t.Fatal("stream closed before /context finished")
			}
			switch frame.kind {
			case "assistant_message":
				var message struct {
					Blocks []protocol.TextBlock `json:"blocks"`
				}
				decode(t, frame.data, &message)
				for _, block := range message.Blocks {
					liveReply.WriteString(block.Text)
				}
			case "error":
				t.Fatalf("stream error: %s", frame.data)
			case "turn_finished":
				break stream
			}
		}
	}
	if !strings.Contains(liveReply.String(), "Context Usage") {
		t.Fatalf("missing live /context output: %q", liveReply.String())
	}

	var page struct {
		Entries []struct {
			Type      string               `json:"type"`
			MessageID string               `json:"message_id"`
			Blocks    []protocol.TextBlock `json:"blocks"`
		} `json:"entries"`
	}
	// The CLI can flush its JSONL shortly after announcing turn_finished.
	for deadline := time.Now().Add(5 * time.Second); ; {
		http.get(t, path+"/messages", &page)
		if len(page.Entries) >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("expected just the command and its reply, got %+v", page.Entries)
	}
	command, reply := page.Entries[0], page.Entries[1]
	if command.Type != "user_message" || len(command.Blocks) != 1 || command.Blocks[0].Text != "/context" {
		t.Fatalf("unexpected command after reopening: %+v", command)
	}
	if reply.Type != "assistant_message" || len(reply.Blocks) != 1 || strings.TrimSpace(reply.Blocks[0].Text) != strings.TrimSpace(liveReply.String()) {
		t.Fatalf("history differs from live reply: %+v", reply)
	}
	if reply.MessageID == "" || reply.MessageID == command.MessageID {
		t.Fatalf("command and reply need distinct stable IDs: %+v", page.Entries)
	}
}

// --- harness ---

type harness struct{ server *httptest.Server }

func claudeCode(work string, deps chat.Deps) chat.Backend {
	return claudecode.New(claudecode.Options{DefaultCwd: work, IdleTimeout: 15 * time.Minute}, deps)
}

// newHarness serves the whole stack over one backend, built by the caller so
// each live test names the harness it drives.
func newHarness(t *testing.T, work string, backend func(work string, deps chat.Deps) chat.Backend) *harness {
	t.Helper()
	cfg := config.Config{RootDir: work, DataDir: filepath.Join(work, "state"), IdleTimeoutS: 900}
	registry, err := chat.NewRegistry(filepath.Join(cfg.DataDir, "threads.db"))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	hub := chat.NewHub()
	deps := chat.Deps{Publish: hub.Publish, Registry: registry}
	svc := chat.NewService(registry, hub, backend(work, deps))
	t.Cleanup(svc.Close)
	ts := httptest.NewServer(server.New(cfg, svc))
	t.Cleanup(ts.Close)
	return &harness{server: ts}
}

func (h *harness) get(t *testing.T, path string, into any) {
	t.Helper()
	response, err := http.Get(h.server.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("GET %s: %d %s", path, response.StatusCode, body)
	}
	if into != nil {
		if err := json.NewDecoder(response.Body).Decode(into); err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
	}
}

func (h *harness) post(t *testing.T, path string, body any, into any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	response, err := http.Post(h.server.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		detail, _ := io.ReadAll(response.Body)
		t.Fatalf("POST %s: %d %s", path, response.StatusCode, detail)
	}
	if into != nil {
		if err := json.NewDecoder(response.Body).Decode(into); err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
	}
}

type frame struct {
	kind string
	data []byte
}

type streamReader struct {
	frames chan frame
	close  func()
}

// stream reads an SSE body into a channel of frames.
func (h *harness) stream(t *testing.T, path string) *streamReader {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, "GET", h.server.URL+path, nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		cancel()
		t.Fatalf("GET %s: %v", path, err)
	}
	if response.StatusCode != 200 {
		cancel()
		t.Fatalf("GET %s: %d", path, response.StatusCode)
	}
	frames := make(chan frame, 512)
	go func() {
		defer response.Body.Close()
		defer close(frames)
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64<<10), 8<<20)
		kind := ""
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				kind = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				frames <- frame{kind, []byte(strings.TrimPrefix(line, "data: "))}
			}
		}
	}()
	return &streamReader{frames: frames, close: cancel}
}

func decode(t *testing.T, raw []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("cannot decode %s: %v", raw, err)
	}
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
