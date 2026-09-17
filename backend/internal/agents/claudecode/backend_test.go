package claudecode

import (
	"encoding/json"
	"testing"
)

// The models here are shaped like the initialize response: `default` and
// `opus[1m]` are two names for one model, and no value equals its own
// resolvedModel.
const modelList = `{"models":[
	{"value":"default","resolvedModel":"claude-opus-5[1m]","displayName":"Default (recommended)"},
	{"value":"opus[1m]","resolvedModel":"claude-opus-5[1m]","displayName":"Opus (1M context)"},
	{"value":"sonnet","resolvedModel":"claude-sonnet-5","displayName":"Sonnet"},
	{"value":"haiku","displayName":"Haiku"}
]}`

func TestSelectedModelNamesTheReportedModelByItsValue(t *testing.T) {
	var server serverInfo
	if err := json.Unmarshal([]byte(modelList), &server); err != nil {
		t.Fatal(err)
	}
	b := &Backend{resolved: server.resolvedModels()}
	if len(b.resolved) != 3 {
		t.Fatalf("a model with no resolvedModel should be left out: %v", b.resolved)
	}

	cases := []struct {
		name, reported, current, want string
	}{
		{"the selection stands", "claude-opus-5[1m]", "default", "default"},
		{"the other name for it stands too", "claude-opus-5[1m]", "opus[1m]", "opus[1m]"},
		{"a resolved id stored by an older build is named", "claude-opus-5[1m]", "claude-opus-5[1m]", "default"},
		{"the CLI came up with another model", "claude-sonnet-5", "default", "sonnet"},
		{"an unknown model leaves the selection alone", "claude-opus-4-1", "sonnet", "sonnet"},
		{"a thread with no selection yet", "claude-sonnet-5", "", "sonnet"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := b.selectedModel(c.reported, c.current); got != c.want {
				t.Fatalf("selectedModel(%q, %q) = %q, want %q", c.reported, c.current, got, c.want)
			}
		})
	}

	// Before the first probe there is nothing to translate with.
	if got := (&Backend{}).selectedModel("claude-opus-5[1m]", "default"); got != "default" {
		t.Fatalf("with no model list = %q, want %q", got, "default")
	}
}
