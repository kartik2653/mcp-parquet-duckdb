package llm

import (
	"context"
	"encoding/json"
)

// Tool describes one function the model may call.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall is one function call requested by the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Message is one chat message. Tool results use Role "tool".
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

// Request is one model completion.
type Request struct {
	Messages []Message
	Tools    []Tool
}

// Response is the model's next message.
type Response struct {
	Content   string
	ToolCalls []ToolCall
}

// Provider completes a chat request.
type Provider interface {
	Complete(ctx context.Context, req Request) (Response, error)
}
