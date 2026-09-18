package protocol

// ModelOption is one entry of a kind's model list.
// Claude: ServerInfo.models. Codex: model/list. OpenCode: GET /config/providers.
type ModelOption struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	Description *string `json:"description"`
}

// OptionChoice is one value a group takes, spelled exactly as the harness
// spells it. Label repeats the value unless the harness has a name of its own.
type OptionChoice struct {
	Value       string  `json:"value"`
	Label       string  `json:"label"`
	Description *string `json:"description"`
}

// Choices builds a group's options from values a harness offers under no name
// but the value itself.
func Choices(values ...string) []OptionChoice {
	out := make([]OptionChoice, len(values))
	for i, value := range values {
		out[i] = OptionChoice{Value: value, Label: value}
	}
	return out
}

// OptionGroup is one knob a kind offers, in that kind's own vocabulary: the id
// is the harness's own parameter name and every value reaches it untranslated.
// Nothing here is interpreted. A harness that splits a decision across two
// parameters therefore declares two groups rather than folding them into one:
// folding is what once made a single control mean opposite things on two
// harnesses.
type OptionGroup struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Options []OptionChoice `json:"options"`
}

// SlashCommandInfo feeds the composer's autocomplete.
// Claude: ServerInfo.commands. Codex: none. OpenCode: GET /command.
type SlashCommandInfo struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argument_hint"`
}

// AgentCapabilities is fixed for the process lifetime and declared on the
// backend. The service enforces these before a command reaches the backend, so
// no backend re-implements a limit.
type AgentCapabilities struct {
	MaxImagesPerPrompt int  `json:"max_images_per_prompt"`
	MaxImageBytes      int  `json:"max_image_bytes"`
	SupportsSteer      bool `json:"supports_steer"` // Codex: turn/steer
	ReportsCost        bool `json:"reports_cost"`   // Claude: total_cost_usd
	SupportsInterrupt  bool `json:"supports_interrupt"`
	// SystemPromptSupport is the most this harness does with a prompt of the
	// owner's: Replace when it can be given one that stands in for its own
	// instructions, Append when it can only add to them, and "" when it takes
	// none at all. The client reads this to say per agent what the one
	// workspace prompt will do there.
	SystemPromptSupport SystemPromptMode `json:"system_prompt_support"`
}

// EffectiveSystemPrompt is what this harness actually does with the workspace
// prompt. A replace asked of a harness that can only append is appended, which
// is the whole of "replace wherever the harness allows it"; a harness that
// takes no prompt at all gets none.
func (c AgentCapabilities) EffectiveSystemPrompt(p SystemPrompt) SystemPrompt {
	if p.Text == "" || c.SystemPromptSupport == "" {
		return SystemPrompt{}
	}
	if p.Mode == SystemPromptReplace && c.SystemPromptSupport != SystemPromptReplace {
		p.Mode = SystemPromptAppend
	}
	return p
}

func (c AgentCapabilities) CheckImages(images []ImageBlock) error {
	if len(images) > c.MaxImagesPerPrompt {
		return Errorf(CodeTooManyImages, "at most %d images per prompt", c.MaxImagesPerPrompt)
	}
	for _, img := range images {
		if !img.Valid() {
			return Errorf(CodeBadRequest, "unsupported image media type %q", img.MediaType)
		}
		if img.ByteSize() > c.MaxImageBytes {
			return Errorf(CodeImageTooLarge, "an image exceeds %d bytes", c.MaxImageBytes)
		}
	}
	return nil
}

// AgentRuntimeInfo is what only the harness can answer, from a probe that must
// not fail the request: a failed probe is an UnavailableReason, so one kind
// being down never hides the others from GET /agents.
type AgentRuntimeInfo struct {
	UnavailableReason *string            `json:"unavailable_reason"` // nil means the kind can start a thread now
	Models            []ModelOption      `json:"models"`
	Groups            []OptionGroup      `json:"groups"` // the kind's own knobs, in the order it wants them shown
	DefaultCwd        string             `json:"default_cwd"`
	Defaults          ThreadOptions      `json:"defaults"` // every knob the kind has is filled
	Commands          []SlashCommandInfo `json:"commands"`
}

// Group is the declared knob with this id, or nil when the kind has none.
func (r AgentRuntimeInfo) Group(id string) *OptionGroup {
	for i := range r.Groups {
		if r.Groups[i].ID == id {
			return &r.Groups[i]
		}
	}
	return nil
}

// CheckOptions rejects a setting this kind does not offer, or a value the knob
// does not take. The model list is probed the same way, but a model stays the
// backend's to reject: it can go stale between the probe and the prompt.
func (r AgentRuntimeInfo) CheckOptions(o ThreadOptions) error {
	for id, value := range o.Settings {
		group := r.Group(id)
		if group == nil {
			return Errorf(CodeOptionInvalid, "%s is not a setting this agent has", id)
		}
		found := false
		for _, choice := range group.Options {
			found = found || choice.Value == value
		}
		if !found {
			return Errorf(CodeOptionInvalid, "%s does not take the value %s", id, value)
		}
	}
	return nil
}

// AgentDescriptor is what the client renders controls from. A hard-coded kind
// check in the client is a defect.
type AgentDescriptor struct {
	Kind         AgentKind         `json:"kind"`
	Label        string            `json:"label"`
	Capabilities AgentCapabilities `json:"capabilities"`
	Runtime      AgentRuntimeInfo  `json:"runtime"`
}
