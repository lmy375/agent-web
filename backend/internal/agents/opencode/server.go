// Package opencode drives `opencode serve`: unlike the other two harnesses this
// one speaks HTTP, so the backend hosts a single headless server process and is
// an ordinary client of it, with one SSE subscription for every thread.
package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// listeningLine is how the server announces the port it picked when asked for
// port 0, which is how two agent-web instances avoid fighting over one.
var listeningLine = regexp.MustCompile(`listening on (http://[^\s]+)`)

type serverProcess struct {
	cmd     *exec.Cmd
	baseURL string
	client  *http.Client
	stopped chan struct{}
}

// spawn starts the headless server and waits for it to say where it is.
func spawn(ctx context.Context, bin, cwd string) (*serverProcess, error) {
	cmd := exec.Command(bin, "serve", "--hostname", "127.0.0.1", "--port", "0")
	cmd.Dir = cwd
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// The address is announced on stderr in some builds and stdout in others.
	cmd.Stderr = cmd.Stdout
	stderr, err := cmd.StderrPipe()
	if err == nil {
		go io.Copy(io.Discard, stderr)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot start %s serve: %w", bin, err)
	}

	found := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if match := listeningLine.FindStringSubmatch(scanner.Text()); match != nil {
				select {
				case found <- match[1]:
				default:
				}
			}
		}
	}()

	select {
	case base := <-found:
		return &serverProcess{
			cmd: cmd, baseURL: strings.TrimRight(base, "/"),
			// One client, so every request reuses the same keep-alive pool;
			// the SSE subscription needs no timeout of its own.
			client:  &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 8}},
			stopped: make(chan struct{}),
		}, nil
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("opencode serve did not report a listening address")
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return nil, ctx.Err()
	}
}

func (s *serverProcess) stop() {
	select {
	case <-s.stopped:
		return
	default:
		close(s.stopped)
	}
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	}
}

// request performs one JSON call. directory scopes the call to a thread's
// working directory, which is how one server serves threads in many projects.
func (s *serverProcess) request(ctx context.Context, method, path, directory string, body, out any) error {
	target := s.baseURL + path
	if directory != "" {
		target += "?directory=" + url.QueryEscape(directory)
	}
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, payload)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("%s %s: %s: %s", method, path, response.Status, strings.TrimSpace(string(detail)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// subscribe follows /event for the lifetime of the process, reconnecting until
// the server is stopped. Every thread is multiplexed onto this one stream.
func (s *serverProcess) subscribe(onEvent func(raw json.RawMessage)) {
	for {
		select {
		case <-s.stopped:
			return
		default:
		}
		s.readEvents(onEvent)
		select {
		case <-s.stopped:
			return
		case <-time.After(time.Second):
		}
	}
}

func (s *serverProcess) readEvents(onEvent func(raw json.RawMessage)) {
	request, err := http.NewRequest("GET", s.baseURL+"/event", nil)
	if err != nil {
		return
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := s.client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 32<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		raw := json.RawMessage(strings.TrimPrefix(line, "data: "))
		onEvent(raw)
		select {
		case <-s.stopped:
			return
		default:
		}
	}
}

// reachable reports whether the bin exists and a port could be bound at all,
// which is everything the probe can check without starting a model call.
func reachable(bin string) error {
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("the `opencode` CLI is not on PATH; install OpenCode or set AGENT_WEB_OPENCODE_PATH")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("cannot bind a local port for opencode: %w", err)
	}
	_ = listener.Close()
	return nil
}
