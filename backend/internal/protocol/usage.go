package protocol

// Usage is token accounting for one turn.
// Claude: ResultMessage.usage. Codex: thread/tokenUsage/updated. OpenCode: message tokens.
type Usage struct {
	InputTokens      int  `json:"input_tokens"`
	OutputTokens     int  `json:"output_tokens"`
	CacheReadTokens  int  `json:"cache_read_tokens"`  // Claude: cache_read_input_tokens | Codex: cachedInputTokens
	CacheWriteTokens int  `json:"cache_write_tokens"` // Claude: cache_creation_input_tokens
	ReasoningTokens  *int `json:"reasoning_tokens"`   // Codex: reasoningOutputTokens | Claude: none
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

// TurnSummary is how one turn ended and what it cost.
type TurnSummary struct {
	Status  TurnStatus `json:"status"`
	Usage   *Usage     `json:"usage"`
	CostUSD *float64   `json:"cost_usd"` // Claude only
}
