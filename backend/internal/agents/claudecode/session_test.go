package claudecode

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/protocol"
)

func testSession(t *testing.T) (*session, *[]protocol.ServerEvent) {
	t.Helper()
	registry, err := chat.NewRegistry(filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close() })

	published := []protocol.ServerEvent{}
	deps := chat.Deps{
		Publish:  func(e protocol.ServerEvent) { published = append(published, e) },
		Registry: registry,
	}
	s := newSession("claude_code:thread", deps, nil, func(_, current string) string { return current })
	return s, &published
}

func eventTypes(events []protocol.ServerEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = protocol.EventType(e)
	}
	return out
}

// A thread whose turn is over but whose harness is still carrying a background
// task has not gone idle: the CLI opens a turn of its own when the task reports
// back, and the reaper must leave the process alone until it does.
func TestBackgroundTasksKeepTheThreadWorking(t *testing.T) {
	s, published := testSession(t)

	s.onSystem(json.RawMessage(`{"subtype":"background_tasks_changed","tasks":[
		{"task_id":"b1","task_type":"local_bash","description":"sleep 45"},
		{"task_id":"w1","task_type":"monitor_ws","description":"watching","ambient":true}
	]}`))

	if got := s.runState(); got != protocol.StateBackground {
		t.Fatalf("run state is %q, want %q", got, protocol.StateBackground)
	}
	tasks := s.liveState().BackgroundTasks
	if len(tasks) != 2 || tasks[0].TaskID != "b1" || !tasks[1].Ambient {
		t.Fatalf("the live set does not carry both tasks: %+v", tasks)
	}
	if types := eventTypes(*published); len(types) != 1 || types[0] != "background_tasks" {
		t.Fatalf("published %v, want one background_tasks", types)
	}

	// Ambient work is housekeeping and says nothing about the conversation.
	s.onSystem(json.RawMessage(`{"subtype":"background_tasks_changed","tasks":[
		{"task_id":"w1","task_type":"monitor_ws","description":"watching","ambient":true}
	]}`))
	if got := s.runState(); got != protocol.StateIdle {
		t.Fatalf("with only ambient work the run state is %q, want %q", got, protocol.StateIdle)
	}

	s.onSystem(json.RawMessage(`{"subtype":"background_tasks_changed","tasks":[]}`))
	if tasks := s.liveState().BackgroundTasks; len(tasks) != 0 {
		t.Fatalf("the set should be empty, got %+v", tasks)
	}
}

// The turn nobody prompted: a finished background task makes the CLI answer
// itself, and content arriving with no prompt behind it still has to bracket a
// turn or the thread reads as idle while the model works.
func TestUnpromptedTurnIsBracketed(t *testing.T) {
	s, published := testSession(t)

	s.onMessage("assistant", json.RawMessage(`{"message":{"id":"msg_1","content":[{"type":"text","text":"the command finished"}]}}`))

	if got := s.runState(); got != protocol.StateRunning {
		t.Fatalf("run state is %q, want %q", got, protocol.StateRunning)
	}
	types := eventTypes(*published)
	if len(types) != 2 || types[0] != "turn_started" || types[1] != "assistant_message" {
		t.Fatalf("published %v, want a turn_started before the message", types)
	}
	started, ok := (*published)[0].(protocol.TurnStartedEvent)
	if !ok || started.ClientMessageID != "" {
		t.Fatalf("a turn the owner did not start carries no client_message_id: %+v", (*published)[0])
	}

	s.onMessage("result", json.RawMessage(`{"subtype":"success","duration_ms":10}`))
	if got := s.runState(); got != protocol.StateIdle {
		t.Fatalf("after the result the run state is %q, want %q", got, protocol.StateIdle)
	}
}

// A task's notification reaches the stream as a system message and the file as
// the prompt the CLI fed itself; both have to render as the same entry.
func TestTaskNotificationRendersTheSameLiveAndReplayed(t *testing.T) {
	s, published := testSession(t)
	s.onSystem(json.RawMessage(`{"subtype":"task_notification","task_id":"b1","status":"stopped",
		"output_file":"/tmp/b1.output","summary":"Background command \"sleep 45\" was stopped"}`))
	if len(*published) != 1 {
		t.Fatalf("published %v, want one event", eventTypes(*published))
	}
	live, ok := (*published)[0].(protocol.BackgroundTaskFinishedEvent)
	if !ok {
		t.Fatalf("published %T, want a background_task_finished", (*published)[0])
	}

	line := transcriptLine{Type: "user"}
	line.Message.Content, _ = json.Marshal("<task-notification>\n" +
		"<task-id>b1</task-id>\n" +
		"<tool-use-id>toolu_1</tool-use-id>\n" +
		"<output-file>/tmp/b1.output</output-file>\n" +
		"<status>stopped</status>\n" +
		"<summary>Background command \"sleep 45\" was stopped</summary>\n" +
		"</task-notification>")
	entries := transcriptEntries("claude_code:thread", line)
	if len(entries) != 1 {
		t.Fatalf("expected one entry from the file, got %+v", entries)
	}
	replayed, ok := entries[0].(protocol.BackgroundTaskFinishedEvent)
	if !ok {
		t.Fatalf("the file produced a %T", entries[0])
	}
	if replayed.TaskID != live.TaskID || replayed.Status != live.Status || replayed.Summary != live.Summary {
		t.Fatalf("replayed %+v does not match the live %+v", replayed, live)
	}
	if live.Status != protocol.TaskStopped {
		t.Fatalf("status is %q, want %q", live.Status, protocol.TaskStopped)
	}
}
