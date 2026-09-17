package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

// Changing one knob must leave the others alone: the client sends only what
// the owner touched, and the row holds the rest.
func TestMergeKeepsUntouchedSettings(t *testing.T) {
	stored := protocol.ThreadOptions{
		Model:    ptr("opus"),
		Settings: map[string]string{"approvalPolicy": "on-request", "sandbox": "workspace-write"},
	}
	merged := stored.Merge(protocol.ThreadOptions{Settings: map[string]string{"sandbox": "read-only"}})

	if merged.Setting("approvalPolicy") != "on-request" {
		t.Errorf("a knob nobody touched changed: %q", merged.Setting("approvalPolicy"))
	}
	if merged.Setting("sandbox") != "read-only" {
		t.Errorf("the changed knob did not take: %q", merged.Setting("sandbox"))
	}
	if stored.Setting("sandbox") != "workspace-write" {
		t.Error("Merge wrote through to the receiver's map")
	}
	if merged.Model == nil || *merged.Model != "opus" {
		t.Error("a settings-only change cleared the model")
	}
}

// Rows written before settings existed keep their effort, whose values were
// always the harness's own. Their mode was ours alone and is gone.
func TestLegacyOptionsKeepEffort(t *testing.T) {
	var options protocol.ThreadOptions
	if err := json.Unmarshal([]byte(`{"model":"o3","mode":"auto_edit","effort":"xhigh"}`), &options); err != nil {
		t.Fatalf("legacy row did not decode: %v", err)
	}
	if options.Setting("effort") != "xhigh" {
		t.Errorf("effort did not survive: %q", options.Setting("effort"))
	}
	if len(options.Settings) != 1 {
		t.Errorf("the legacy mode came across as a setting: %v", options.Settings)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	options := protocol.ThreadOptions{Settings: map[string]string{"effort": "high"}}
	raw, err := json.Marshal(options)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back protocol.ThreadOptions
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Setting("effort") != "high" {
		t.Errorf("round trip lost the setting: %s", raw)
	}
}

func ptr(s string) *string { return &s }
