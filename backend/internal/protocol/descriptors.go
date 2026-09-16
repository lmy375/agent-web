package protocol

// ModelOption is one entry of a kind's model list.
// Claude: ServerInfo.models. Codex: model/list. OpenCode: GET /api/model.
type ModelOption struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	Description *string `json:"description"`
}

// EffortOption is one reasoning-effort level. The client renders the list and
// sends id back; it attaches no meaning to the value.
// Claude: --effort low|medium|high|xhigh|max. Codex: model_reasoning_effort.
type EffortOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
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
	Modes              []PermissionMode `json:"modes"`
	Efforts            []EffortOption   `json:"efforts"` // empty when the kind has no effort knob
	MaxImagesPerPrompt int              `json:"max_images_per_prompt"`
	MaxImageBytes      int              `json:"max_image_bytes"`
	SupportsSteer      bool             `json:"supports_steer"` // Codex: turn/steer
	ReportsCost        bool             `json:"reports_cost"`   // Claude: total_cost_usd
	SupportsInterrupt  bool             `json:"supports_interrupt"`
}

// CheckOptions rejects a mode or effort this kind does not offer. The model
// list is probed, so a model stays the backend's to reject.
func (c AgentCapabilities) CheckOptions(o ThreadOptions) error {
	if o.Mode != nil {
		found := false
		for _, m := range c.Modes {
			found = found || m == *o.Mode
		}
		if !found {
			return Errorf(CodeOptionInvalid, "mode %s is not offered", *o.Mode)
		}
	}
	if o.Effort != nil {
		found := false
		for _, e := range c.Efforts {
			found = found || e.ID == *o.Effort
		}
		if !found {
			return Errorf(CodeOptionInvalid, "effort %s is not offered", *o.Effort)
		}
	}
	return nil
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
	DefaultCwd        string             `json:"default_cwd"`
	Defaults          ThreadOptions      `json:"defaults"` // every knob the kind has is filled
	Commands          []SlashCommandInfo `json:"commands"`
}

// AgentDescriptor is what the client renders controls from. A hard-coded kind
// check in the client is a defect.
type AgentDescriptor struct {
	Kind         AgentKind         `json:"kind"`
	Label        string            `json:"label"`
	Capabilities AgentCapabilities `json:"capabilities"`
	Runtime      AgentRuntimeInfo  `json:"runtime"`
}
