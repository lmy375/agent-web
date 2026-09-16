package protocol

import "encoding/json"

// ContentBlock is one piece of a user or assistant message. The unions in this
// package only ever travel server -> client, so marshalling the concrete type
// is enough; nothing needs a discriminated decoder.
type ContentBlock interface{ contentBlock() }

// UserBlock is what an owner prompt may contain: text and images.
type UserBlock interface {
	ContentBlock
	userBlock()
}

type TextBlock struct {
	Type string `json:"type"` // always "text"
	Text string `json:"text"`
}

func Text(text string) TextBlock { return TextBlock{Type: "text", Text: text} }

func (TextBlock) contentBlock() {}
func (TextBlock) userBlock()    {}

// ThinkingBlock is Claude's ThinkingBlock.thinking, Codex's reasoning item.
type ThinkingBlock struct {
	Type string `json:"type"` // always "thinking"
	Text string `json:"text"`
}

func Thinking(text string) ThinkingBlock { return ThinkingBlock{Type: "thinking", Text: text} }

func (ThinkingBlock) contentBlock() {}

// ImageBlock carries base64 image bytes inline. Claude: a base64 image block.
// Codex: a turn/start input image. OpenCode: a file part with a data URL.
type ImageBlock struct {
	Type       string `json:"type"` // always "image"
	MediaType  string `json:"media_type"`
	DataBase64 string `json:"data_base64"`
}

func (ImageBlock) contentBlock() {}
func (ImageBlock) userBlock()    {}

// ByteSize is the decoded size: base64 spends four characters per three bytes,
// and its padding stands for no byte.
func (b ImageBlock) ByteSize() int {
	n := len(b.DataBase64)
	for n > 0 && b.DataBase64[n-1] == '=' {
		n--
	}
	return n * 3 / 4
}

var imageMediaTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
}

func (b ImageBlock) Valid() bool { return imageMediaTypes[b.MediaType] && b.DataBase64 != "" }

type ToolUseBlock struct {
	Type     string          `json:"type"` // always "tool_use"
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	ToolKind ToolKind        `json:"tool_kind"`
	Input    json.RawMessage `json:"input"`
}

func (ToolUseBlock) contentBlock() {}

type ToolResultBlock struct {
	Type      string         `json:"type"` // always "tool_result"
	ToolUseID string         `json:"tool_use_id"`
	Content   []ContentBlock `json:"content"` // text and image blocks only
	IsError   bool           `json:"is_error"`
}

func (ToolResultBlock) contentBlock() {}
