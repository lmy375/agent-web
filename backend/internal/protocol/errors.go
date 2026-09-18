package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ErrorCode is the machine-readable half of every non-2xx body under /api.
type ErrorCode string

const (
	CodeThreadNotFound        ErrorCode = "thread_not_found"
	CodeThreadBusy            ErrorCode = "thread_busy"
	CodeAgentUnavailable      ErrorCode = "agent_unavailable"
	CodeCapabilityUnsupported ErrorCode = "capability_unsupported"
	CodeNoRunningTurn         ErrorCode = "no_running_turn"
	CodeClientMessageConflict ErrorCode = "client_message_conflict"
	CodeInteractionNotPending ErrorCode = "interaction_not_pending"
	CodeTaskNotFound          ErrorCode = "task_not_found"
	CodeDecisionMismatch      ErrorCode = "interaction_decision_mismatch"
	CodeTooManyImages         ErrorCode = "too_many_images"
	CodeImageTooLarge         ErrorCode = "image_too_large"
	CodeCwdInvalid            ErrorCode = "cwd_invalid"
	CodeOptionInvalid         ErrorCode = "option_invalid"
	CodeCursorInvalid         ErrorCode = "cursor_invalid"
	CodeSettingsInvalid       ErrorCode = "settings_invalid"
	CodeBadRequest            ErrorCode = "bad_request"
	CodeUnauthorized          ErrorCode = "unauthorized"
	CodeInternal              ErrorCode = "internal"
)

var httpStatus = map[ErrorCode]int{
	CodeThreadNotFound:        http.StatusNotFound,
	CodeThreadBusy:            http.StatusConflict,
	CodeAgentUnavailable:      http.StatusServiceUnavailable,
	CodeCapabilityUnsupported: http.StatusUnprocessableEntity,
	CodeNoRunningTurn:         http.StatusConflict,
	CodeClientMessageConflict: http.StatusConflict,
	CodeInteractionNotPending: http.StatusConflict,
	CodeTaskNotFound:          http.StatusNotFound,
	CodeDecisionMismatch:      http.StatusUnprocessableEntity,
	CodeTooManyImages:         http.StatusUnprocessableEntity,
	CodeImageTooLarge:         http.StatusUnprocessableEntity,
	CodeCwdInvalid:            http.StatusUnprocessableEntity,
	CodeOptionInvalid:         http.StatusUnprocessableEntity,
	CodeCursorInvalid:         http.StatusUnprocessableEntity,
	CodeSettingsInvalid:       http.StatusUnprocessableEntity,
	CodeBadRequest:            http.StatusBadRequest,
	CodeUnauthorized:          http.StatusUnauthorized,
	CodeInternal:              http.StatusInternalServerError,
}

// Error is the one error type routes translate into a response. Anything else
// reaching the handler is a bug and becomes a 500.
type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func (e *Error) Status() int {
	if status, ok := httpStatus[e.Code]; ok {
		return status
	}
	return http.StatusInternalServerError
}

func Errorf(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WriteError renders err as {"detail": {...}}; a non-protocol error is a 500
// whose message the client is not shown.
func WriteError(w http.ResponseWriter, err error) {
	detail, ok := err.(*Error)
	if !ok {
		detail = &Error{Code: CodeInternal, Message: "internal error"}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(detail.Status())
	_ = json.NewEncoder(w).Encode(map[string]*Error{"detail": detail})
}
