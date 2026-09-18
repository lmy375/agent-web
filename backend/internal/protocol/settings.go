package protocol

// Locale is the language the browser UI speaks. It is stored on the server so
// the choice follows the owner to every browser they open the app in. Empty is
// an install nobody has chosen a language for yet, which is what leaves the
// first visit following the browser's own language preferences.
type Locale string

const (
	LocaleEN Locale = "en"
	LocaleZH Locale = "zh"
)

// SystemPromptMode is what the owner asked their prompt to do to the harness's
// own instructions. Each member names the native parameter it becomes.
type SystemPromptMode string

const (
	// Append adds the text to the instructions the harness already has.
	// Claude: --append-system-prompt | Codex: developerInstructions |
	// OpenCode: the message's `system` | Pi: --append-system-prompt
	SystemPromptAppend SystemPromptMode = "append"
	// Replace stands in for them.
	// Claude: --system-prompt | Codex: baseInstructions | Pi: --system-prompt
	SystemPromptReplace SystemPromptMode = "replace"
)

// maxSystemPromptBytes is what one prompt may be. Two of the four harnesses
// take it as a command-line argument, so it has to stay well inside any
// platform's argument limit.
const maxSystemPromptBytes = 16 << 10

// SystemPrompt is the owner's own instructions and what to do with them. An
// empty Text is no prompt at all, whatever the mode says.
type SystemPrompt struct {
	Text string           `json:"text"`
	Mode SystemPromptMode `json:"mode"`
}

// WorkspaceSettings is what is true of the whole workspace rather than of one
// thread: the language the UI speaks, and the system prompt every thread
// created from now on is given.
type WorkspaceSettings struct {
	Locale       Locale       `json:"locale"`
	SystemPrompt SystemPrompt `json:"system_prompt"`
}

// DefaultSettings is what an install that has never been configured reads as:
// no prompt, and no language chosen.
func DefaultSettings() WorkspaceSettings {
	return WorkspaceSettings{SystemPrompt: SystemPrompt{Mode: SystemPromptAppend}}
}

func (s WorkspaceSettings) Validate() error {
	switch s.Locale {
	case "", LocaleEN, LocaleZH:
	default:
		return Errorf(CodeSettingsInvalid, "%s is not a language this build speaks", s.Locale)
	}
	switch s.SystemPrompt.Mode {
	case SystemPromptAppend, SystemPromptReplace:
	default:
		return Errorf(CodeSettingsInvalid, "%s is not a system prompt mode", s.SystemPrompt.Mode)
	}
	if len(s.SystemPrompt.Text) > maxSystemPromptBytes {
		return Errorf(CodeSettingsInvalid, "a system prompt is at most %d bytes", maxSystemPromptBytes)
	}
	return nil
}
