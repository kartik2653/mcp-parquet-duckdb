package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIError is a failure from the language-model HTTP API.
// Error text never includes the API key or response body.
type APIError struct {
	Status int
	detail string
}

func (e *APIError) Error() string {
	if e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden {
		return "language model rejected the API key"
	}
	if e.Status == http.StatusTooManyRequests {
		return "language model is rate limiting requests"
	}
	if e.Status == 0 {
		return "language model request failed"
	}
	return fmt.Sprintf("language model request failed (HTTP %d)", e.Status)
}

// Detail is a redacted response snippet for logs and tests.
func (e *APIError) Detail() string { return e.detail }

// XAI calls the xAI chat completions API.
type XAI struct {
	APIKey     string
	Model      string
	BaseURL    string
	HTTPClient *http.Client
}

// Complete sends one chat completion. The model name is sent exactly as configured.
func (x XAI) Complete(ctx context.Context, req Request) (Response, error) {
	if strings.TrimSpace(x.APIKey) == "" {
		return Response{}, errors.New("XAI_API_KEY is required")
	}
	if strings.TrimSpace(x.Model) == "" {
		return Response{}, errors.New("model is required")
	}
	client := x.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	body, err := json.Marshal(chatRequest{
		Model:      x.Model,
		Messages:   encodeMessages(req.Messages),
		Tools:      encodeTools(req.Tools),
		ToolChoice: toolChoice(req.Tools),
	})
	if err != nil {
		return Response{}, err
	}
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, time.Duration(attempt)*200*time.Millisecond); err != nil {
				return Response{}, err
			}
		}
		resp, err := x.do(ctx, client, body)
		if err == nil {
			return resp, nil
		}
		last = err
		var api *APIError
		if !errors.As(err, &api) || (api.Status != http.StatusTooManyRequests && api.Status < 500) {
			return Response{}, err
		}
	}
	return Response{}, last
}

func (x XAI) do(ctx context.Context, client *http.Client, body []byte) (Response, error) {
	endpoint := strings.TrimRight(x.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+x.APIKey)
	httpResp, err := client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, fmt.Errorf("language model request failed: %w", err)
	}
	defer httpResp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return Response{}, fmt.Errorf("language model request failed: %w", err)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return Response{}, &APIError{Status: httpResp.StatusCode, detail: redact(string(payload), x.APIKey)}
	}
	var decoded chatResponse
	if err := json.Unmarshal(payload, &decoded); err != nil || len(decoded.Choices) == 0 {
		return Response{}, errors.New("language model returned an unexpected response")
	}
	msg := decoded.Choices[0].Message
	out := Response{Content: msg.Content}
	for _, call := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	return out, nil
}

func toolChoice(tools []Tool) any {
	if len(tools) == 0 {
		return nil
	}
	return "auto"
}

func encodeTools(tools []Tool) []chatTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]chatTool, 0, len(tools))
	for _, tool := range tools {
		params := tool.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  params,
			},
		})
	}
	return out
}

func encodeMessages(messages []Message) []chatMessage {
	out := make([]chatMessage, 0, len(messages))
	for _, msg := range messages {
		item := chatMessage{Role: msg.Role, Content: msg.Content, ToolCallID: msg.ToolCallID}
		for _, call := range msg.ToolCalls {
			args := call.Arguments
			if args == "" {
				args = "{}"
			}
			item.ToolCalls = append(item.ToolCalls, chatToolCall{
				ID:   call.ID,
				Type: "function",
				Function: chatFunctionCall{
					Name:      call.Name,
					Arguments: args,
				},
			})
		}
		out = append(out, item)
	}
	return out
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func redact(text, secret string) string {
	if secret != "" {
		text = strings.ReplaceAll(text, secret, "[redacted]")
	}
	if len(text) > 180 {
		text = text[:180]
	}
	return text
}

type chatRequest struct {
	Model      string        `json:"model"`
	Messages   []chatMessage `json:"messages"`
	Tools      []chatTool    `json:"tools,omitempty"`
	ToolChoice any           `json:"tool_choice,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}
