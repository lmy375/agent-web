package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
)

// CommandType discriminates what the client posted to /threads/{id}/input.
// Acceptance is the 204; anything the command produces arrives on the stream.
type CommandType string

const (
	CmdPrompt              CommandType = "prompt"               // Claude: query() | Codex: turn/start | OpenCode: POST /session/{id}/prompt
	CmdInterrupt           CommandType = "interrupt"            // Claude: interrupt() | Codex: turn/interrupt | OpenCode: POST /session/{id}/abort
	CmdSteer               CommandType = "steer"                // Codex: turn/steer
	CmdSetOptions          CommandType = "set_options"          // Claude: set_model / set_permission_mode | Codex: config/value/write
	CmdInteractionResponse CommandType = "interaction_response" // resolves one pending request
)

// ulidPattern is Crockford base32; a prompt's client_message_id is the
// idempotency key of owner-originated input.
var ulidPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// ClientCommand is decoded once from the request body. The union is flat
// because it only ever travels inbound and Validate pins which fields each
// type reads; a hand-written discriminated decoder would buy nothing.
type ClientCommand struct {
	Type CommandType `json:"type"`

	// prompt
	ClientMessageID string       `json:"client_message_id,omitempty"`
	Text            string       `json:"text,omitempty"`
	Images          []ImageBlock `json:"images,omitempty"`

	// set_options
	Options ThreadOptions `json:"options,omitempty"`

	// interaction_response
	RequestID string              `json:"request_id,omitempty"`
	Decision  InteractionDecision `json:"decision,omitempty"`
}

func (c ClientCommand) Validate() error {
	switch c.Type {
	case CmdPrompt:
		if !ulidPattern.MatchString(c.ClientMessageID) {
			return Errorf(CodeBadRequest, "client_message_id must be a ULID")
		}
		if c.Text == "" && len(c.Images) == 0 {
			return Errorf(CodeBadRequest, "a prompt needs text or images")
		}
	case CmdSteer:
		if c.Text == "" {
			return Errorf(CodeBadRequest, "a steer needs text")
		}
	case CmdInterrupt, CmdSetOptions:
	case CmdInteractionResponse:
		if c.RequestID == "" {
			return Errorf(CodeBadRequest, "an interaction response needs a request_id")
		}
		return c.Decision.Validate()
	default:
		return Errorf(CodeBadRequest, "unknown command type %q", c.Type)
	}
	return nil
}

// PayloadDigest is what a replay must match to count as the same prompt.
func (c ClientCommand) PayloadDigest() string {
	raw, _ := json.Marshal(struct {
		Text   string       `json:"text"`
		Images []ImageBlock `json:"images"`
	}{c.Text, c.Images})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
