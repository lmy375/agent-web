package protocol

import (
	"encoding/json"
	"time"
)

// PermissionSuggestion is a rule the owner can accept along with an allow.
// Claude: a PermissionUpdate such as Bash(git *) with its destination.
// Codex: acceptForSession or an execpolicy amendment.
type PermissionSuggestion struct {
	Rule  string        `json:"rule"`
	Scope RememberScope `json:"scope"`
}

// InteractionPayload is what the harness is asking, discriminated by kind.
type InteractionPayload interface{ Kind() InteractionKind }

type PermissionPayload struct {
	PayloadKind InteractionKind        `json:"kind"` // always "permission"
	ToolUseID   string                 `json:"tool_use_id"`
	ToolName    string                 `json:"tool_name"`
	ToolKind    ToolKind               `json:"tool_kind"`
	ToolInput   json.RawMessage        `json:"tool_input"`
	Suggestions []PermissionSuggestion `json:"suggestions"`
}

func (p PermissionPayload) Kind() InteractionKind { return InteractionPermission }

type QuestionOption struct {
	Label       string  `json:"label"`
	Description *string `json:"description"`
}

// Question is one AskUserQuestion question (Claude, keyed by its text) or one
// requestUserInput question (Codex, keyed by id).
type Question struct {
	ID          string           `json:"id"`
	Prompt      string           `json:"prompt"`
	Options     []QuestionOption `json:"options"`
	MultiSelect bool             `json:"multi_select"`
}

type QuestionPayload struct {
	PayloadKind InteractionKind `json:"kind"` // always "question"
	Questions   []Question      `json:"questions"`
}

func (p QuestionPayload) Kind() InteractionKind { return InteractionQuestion }

// PlanPayload is Claude's ExitPlanMode input. Approving is an allow; changing
// mode after approval is a separate set_options.
type PlanPayload struct {
	PayloadKind  InteractionKind `json:"kind"` // always "plan"
	PlanMarkdown string          `json:"plan_markdown"`
}

func (p PlanPayload) Kind() InteractionKind { return InteractionPlan }

// InteractionRequest is one pending moment, as the client sees it.
type InteractionRequest struct {
	RequestID string             `json:"request_id"`
	CreatedAt time.Time          `json:"created_at"`
	Payload   InteractionPayload `json:"payload"`
}

// Accepts reports whether this decision type answers this request kind.
func (r InteractionRequest) Accepts(d InteractionDecision) bool {
	switch r.Payload.Kind() {
	case InteractionPermission, InteractionPlan:
		return d.Type == DecisionAllow || d.Type == DecisionDeny
	case InteractionQuestion:
		return d.Type == DecisionAnswer || d.Type == DecisionDeny
	}
	return false
}

// DecisionType is how the owner answered.
type DecisionType string

const (
	DecisionAllow  DecisionType = "allow"  // Claude: PermissionResultAllow | Codex: accept*
	DecisionDeny   DecisionType = "deny"   // Claude: PermissionResultDeny | Codex: decline / cancel
	DecisionAnswer DecisionType = "answer" // the chosen options of a question
)

type QuestionAnswer struct {
	QuestionID string   `json:"question_id"`
	Answers    []string `json:"answers"`
}

// InteractionDecision is a flat struct rather than three types: it crosses the
// wire inbound, where one shape decoded once beats a hand-written union, and
// Accepts already pins which fields matter for which request.
type InteractionDecision struct {
	Type DecisionType `json:"type"`
	// allow
	UpdatedInput json.RawMessage       `json:"updated_input,omitempty"`
	Remember     *PermissionSuggestion `json:"remember,omitempty"`
	// deny
	Message   string `json:"message,omitempty"`
	Interrupt bool   `json:"interrupt,omitempty"`
	// answer
	Answers []QuestionAnswer `json:"answers,omitempty"`
}

func (d InteractionDecision) Validate() error {
	switch d.Type {
	case DecisionAllow, DecisionDeny:
		return nil
	case DecisionAnswer:
		if len(d.Answers) == 0 {
			return Errorf(CodeBadRequest, "an answer decision needs at least one answer")
		}
		return nil
	}
	return Errorf(CodeBadRequest, "unknown decision type %q", d.Type)
}
