package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// Roles used in chat messages.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is one chat message.
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall // set on assistant messages that request tools
	ToolCallID string     // set on tool messages, matching ToolCall.ID
	// Images supplies untrusted image input on user messages, only for this turn.
	Images []Image
}

// Image is a bounded in-memory PNG or JPEG sent to a vision-capable model.
type Image struct {
	MediaType string
	Data      []byte
}

// MarshalJSON sends null content for tool-calling assistant messages, which is
// what the API expects when the model produced no text.
func (m Message) MarshalJSON() ([]byte, error) {
	type wire struct {
		Role       string     `json:"role"`
		Content    any        `json:"content"`
		ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
	}
	w := wire{Role: m.Role, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID}
	if m.Content != "" || len(m.ToolCalls) == 0 {
		w.Content = m.Content
	}
	if len(m.Images) > 0 {
		if m.Role != RoleUser {
			return nil, errors.New("image input requires a user message")
		}
		parts := []map[string]any{{"type": "text", "text": m.Content}}
		for _, im := range m.Images {
			if (im.MediaType != "image/png" && im.MediaType != "image/jpeg") || len(im.Data) == 0 || len(im.Data) > 10<<20 {
				return nil, errors.New("invalid or oversized image input")
			}
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + im.MediaType + ";base64," + base64.StdEncoding.EncodeToString(im.Data), "detail": "auto"}})
		}
		w.Content = parts
	}
	return json.Marshal(w)
}

// UnmarshalJSON accepts null content.
func (m *Message) UnmarshalJSON(b []byte) error {
	var w struct {
		Role       string     `json:"role"`
		Content    *string    `json:"content"`
		ToolCalls  []ToolCall `json:"tool_calls"`
		ToolCallID string     `json:"tool_call_id"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	m.Role, m.ToolCalls, m.ToolCallID = w.Role, w.ToolCalls, w.ToolCallID
	m.Content = ""
	if w.Content != nil {
		m.Content = *w.Content
	}
	return nil
}

// ToolCall is a tool invocation requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall names a tool and carries its arguments as a JSON string, which
// the caller must parse and validate.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool describes a function the model may call.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema object
}

// MarshalJSON renders the OpenAI-compatible tool envelope.
func (t Tool) MarshalJSON() ([]byte, error) {
	params := t.Parameters
	if len(params) == 0 {
		params = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return json.Marshal(map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"parameters":  params,
		},
	})
}

// Usage is token and cost accounting for one call.
type Usage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Cost             float64 `json:"cost"`
}

// ChatRequest is one call to the general model.
type ChatRequest struct {
	Messages []Message
	Tools    []Tool
	// ToolChoice is "auto" (default), "none", or "required". Empty means auto.
	ToolChoice string
	// ParallelToolCalls controls parallel tool calls. Nil leaves the API default.
	ParallelToolCalls *bool
	Temperature       *float64
	MaxTokens         int
	// Proactive marks calls Kest starts on its own; they are refused when the
	// daily budget is exhausted.
	Proactive bool
}

// ChatResponse is the model's answer.
type ChatResponse struct {
	ID           string
	Model        string // the model that actually answered
	Message      Message
	FinishReason string
	Usage        Usage
}

// ErrNoChoices is returned when the API replies without any completion.
var ErrNoChoices = errors.New("openrouter: response contained no choices")

// Chat sends a chat completion request to the general model slot, using the
// configured fallbacks if the primary model fails.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if len(req.Messages) == 0 {
		return nil, errors.New("llm: chat request has no messages")
	}

	if req.Proactive && c.budget != nil {
		if err := c.budget.Check(ctx); err != nil {
			return nil, err
		}
	}

	payload := map[string]any{"messages": req.Messages}
	if len(c.o.Fallbacks) > 0 {
		payload["models"] = append([]string{c.o.General}, c.o.Fallbacks...)
		payload["route"] = "fallback"
	} else {
		payload["model"] = c.o.General
	}
	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
		if req.ToolChoice != "" {
			payload["tool_choice"] = req.ToolChoice
		}
		if req.ParallelToolCalls != nil {
			payload["parallel_tool_calls"] = *req.ParallelToolCalls
		}
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	if c.o.ZDROnly {
		payload["provider"] = map[string]any{"zdr": true}
	}

	body, _, err := c.do(ctx, "/chat/completions", payload)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
		Error *struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}
	// OpenRouter can report upstream failures inside a 200 response.
	if resp.Error != nil && resp.Error.Message != "" {
		return nil, &APIError{Status: 200, Message: resp.Error.Message}
	}
	if len(resp.Choices) == 0 {
		return nil, ErrNoChoices
	}

	c.record(ctx, "general", resp.Model, resp.Usage, resp.ID)

	ch := resp.Choices[0]
	return &ChatResponse{
		ID:           resp.ID,
		Model:        resp.Model,
		Message:      ch.Message,
		FinishReason: ch.FinishReason,
		Usage:        resp.Usage,
	}, nil
}
