package claudecode

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/lmy375/agent-web/backend/internal/protocol"
)

const (
	questionTool = "AskUserQuestion"
	planTool     = "ExitPlanMode"
)

// canUseToolRequest is what the CLI asks us when a tool needs a decision.
type canUseToolRequest struct {
	ToolName              string            `json:"tool_name"`
	Input                 json.RawMessage   `json:"input"`
	ToolUseID             string            `json:"tool_use_id"`
	PermissionSuggestions []json.RawMessage `json:"permission_suggestions"`
}

// permissionUpdate is the CLI's own suggestion object. We keep each one verbatim
// so an allow that remembers it can hand the very same object back.
type permissionUpdate struct {
	Type        string `json:"type"`
	Destination string `json:"destination"`
	Behavior    string `json:"behavior"`
	Rules       []struct {
		ToolName    string `json:"toolName"`
		RuleContent string `json:"ruleContent"`
	} `json:"rules"`
	Mode        string   `json:"mode"`
	Directories []string `json:"directories"`
}

// pending is one unanswered can_use_tool, with everything the answer needs.
type pending struct {
	request protocol.InteractionRequest
	// toolInput is the original input, which an AskUserQuestion answer is
	// folded back into and an allow echoes when the client changed nothing.
	toolInput json.RawMessage
	// suggestions maps a rendered rule back to the CLI object it came from.
	suggestions map[string]json.RawMessage
	answered    chan protocol.InteractionDecision
}

// describeSuggestions renders each CLI suggestion as a rule string the client
// can show, and remembers the original object behind it.
func describeSuggestions(raw []json.RawMessage) ([]protocol.PermissionSuggestion, map[string]json.RawMessage) {
	out := []protocol.PermissionSuggestion{}
	byRule := map[string]json.RawMessage{}
	for _, item := range raw {
		var update permissionUpdate
		if json.Unmarshal(item, &update) != nil {
			continue
		}
		// Only a rule addition is something the owner can meaningfully accept
		// alongside one tool call; mode and directory changes are settings.
		if update.Type != "addRules" || len(update.Rules) == 0 {
			continue
		}
		scope := protocol.ScopeAlways
		if update.Destination == "session" {
			scope = protocol.ScopeSession
		}
		names := make([]string, 0, len(update.Rules))
		for _, rule := range update.Rules {
			if rule.RuleContent == "" {
				names = append(names, rule.ToolName)
				continue
			}
			names = append(names, rule.ToolName+"("+rule.RuleContent+")")
		}
		rule := strings.Join(names, ", ")
		if _, seen := byRule[rule]; seen {
			continue
		}
		byRule[rule] = item
		out = append(out, protocol.PermissionSuggestion{Rule: rule, Scope: scope})
	}
	return out, byRule
}

// rawQuestions is the AskUserQuestion tool input we have to understand, so the
// client gets a kind-neutral question list instead of the tool's own schema.
type rawQuestions struct {
	Questions []struct {
		Question    string `json:"question"`
		Header      string `json:"header"`
		MultiSelect bool   `json:"multiSelect"`
		Options     []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

// newPending turns one can_use_tool into the protocol's interaction request.
// AskUserQuestion and ExitPlanMode are tools in the CLI but their own kinds
// here, because a client renders them as a question and a plan, not as a
// permission prompt over a tool with a strange name.
func newPending(requestID string, req canUseToolRequest) *pending {
	suggestions, byRule := describeSuggestions(req.PermissionSuggestions)
	p := &pending{
		toolInput:   req.Input,
		suggestions: byRule,
		answered:    make(chan protocol.InteractionDecision, 1),
	}
	p.request = protocol.InteractionRequest{
		RequestID: requestID,
		CreatedAt: time.Now().UTC(),
		Payload:   payloadFor(req, suggestions),
	}
	return p
}

func payloadFor(req canUseToolRequest, suggestions []protocol.PermissionSuggestion) protocol.InteractionPayload {
	switch req.ToolName {
	case questionTool:
		var parsed rawQuestions
		_ = json.Unmarshal(req.Input, &parsed)
		questions := make([]protocol.Question, 0, len(parsed.Questions))
		for _, q := range parsed.Questions {
			options := make([]protocol.QuestionOption, 0, len(q.Options))
			for _, o := range q.Options {
				option := protocol.QuestionOption{Label: o.Label}
				if o.Description != "" {
					description := o.Description
					option.Description = &description
				}
				options = append(options, option)
			}
			// The CLI keys its answers by the question text, so that is the id.
			questions = append(questions, protocol.Question{
				ID: q.Question, Prompt: q.Question, Options: options, MultiSelect: q.MultiSelect,
			})
		}
		if len(questions) > 0 {
			return protocol.QuestionPayload{PayloadKind: protocol.InteractionQuestion, Questions: questions}
		}
	case planTool:
		var parsed struct {
			Plan string `json:"plan"`
		}
		_ = json.Unmarshal(req.Input, &parsed)
		return protocol.PlanPayload{PayloadKind: protocol.InteractionPlan, PlanMarkdown: parsed.Plan}
	}
	return protocol.PermissionPayload{
		PayloadKind: protocol.InteractionPermission,
		ToolUseID:   req.ToolUseID,
		ToolName:    req.ToolName,
		ToolKind:    toolKind(req.ToolName),
		ToolInput:   req.Input,
		Suggestions: suggestions,
	}
}

// controlReply turns a decision into the can_use_tool response body.
func (p *pending) controlReply(decision protocol.InteractionDecision) map[string]any {
	switch decision.Type {
	case protocol.DecisionAllow:
		input := decision.UpdatedInput
		if len(input) == 0 {
			input = p.toolInput
		}
		reply := map[string]any{"behavior": "allow", "updatedInput": json.RawMessage(input)}
		if decision.Remember != nil {
			if original, ok := p.suggestions[decision.Remember.Rule]; ok {
				reply["updatedPermissions"] = []json.RawMessage{original}
			}
		}
		return reply

	case protocol.DecisionAnswer:
		// AskUserQuestion is answered by allowing the tool with its answers
		// folded into the input, keyed by the question text.
		return map[string]any{"behavior": "allow", "updatedInput": p.withAnswers(decision.Answers)}

	default:
		message := decision.Message
		if message == "" {
			message = "The user declined."
		}
		reply := map[string]any{"behavior": "deny", "message": message}
		if decision.Interrupt {
			reply["interrupt"] = true
		}
		return reply
	}
}

func (p *pending) withAnswers(answers []protocol.QuestionAnswer) json.RawMessage {
	input := map[string]json.RawMessage{}
	_ = json.Unmarshal(p.toolInput, &input)
	byQuestion := map[string]string{}
	for _, a := range answers {
		byQuestion[a.QuestionID] = strings.Join(a.Answers, ", ")
	}
	encoded, err := json.Marshal(byQuestion)
	if err != nil {
		return p.toolInput
	}
	input["answers"] = encoded
	merged, err := json.Marshal(input)
	if err != nil {
		return p.toolInput
	}
	return merged
}
