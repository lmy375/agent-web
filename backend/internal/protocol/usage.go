package protocol

import "time"

// Usage is token accounting for one turn.
// Claude: ResultMessage.usage. Codex: thread/tokenUsage/updated. OpenCode: message tokens.
type Usage struct {
	InputTokens      int  `json:"input_tokens"`
	OutputTokens     int  `json:"output_tokens"`
	CacheReadTokens  int  `json:"cache_read_tokens"`  // Claude: cache_read_input_tokens | Codex: cachedInputTokens
	CacheWriteTokens int  `json:"cache_write_tokens"` // Claude: cache_creation_input_tokens
	ReasoningTokens  *int `json:"reasoning_tokens"`   // Codex: reasoningOutputTokens | Claude: none
}

// Add folds one message's accounting into a turn's running total.
func (u *Usage) Add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.CacheReadTokens += other.CacheReadTokens
	u.CacheWriteTokens += other.CacheWriteTokens
	if other.ReasoningTokens != nil {
		total := *other.ReasoningTokens
		if u.ReasoningTokens != nil {
			total += *u.ReasoningTokens
		}
		u.ReasoningTokens = &total
	}
}

// ContextCategory is one slice of what fills the context window.
// Claude: get_context_usage() categories. Codex / OpenCode: none.
type ContextCategory struct {
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
}

// ContextUsage is how full the context window is right now.
type ContextUsage struct {
	TotalTokens               int               `json:"total_tokens"`
	MaxTokens                 int               `json:"max_tokens"`
	Categories                []ContextCategory `json:"categories"`
	AutoCompactThresholdToken *int              `json:"auto_compact_threshold_tokens"` // Claude only
}

// TurnSummary is how one turn ended, what it cost and how long it took. The
// timings are the session's own clock, taken when it claimed the turn and when
// the harness closed it, so a client that reloads afterwards reads the same
// elapsed time it watched tick.
type TurnSummary struct {
	Status     TurnStatus `json:"status"`
	Usage      *Usage     `json:"usage"`
	CostUSD    *float64   `json:"cost_usd"` // Claude only
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt time.Time  `json:"finished_at"`
}

// RunningTurn is the turn under way right now. The stream replays nothing, so
// a client that connects mid-turn learns when it began, and what it has spent
// so far, only from the thread detail.
type RunningTurn struct {
	// Empty for a turn the harness opened by itself, as in TurnStartedEvent.
	ClientMessageID string    `json:"client_message_id"`
	StartedAt       time.Time `json:"started_at"`
	// Nil until the harness has reported any accounting for this turn.
	Usage *Usage `json:"usage"`
}
