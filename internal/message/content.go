// Package message defines the message model and its content parts.
package message

import (
	"errors"
	"strings"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
)

// MessageRole is who authored a message.
type MessageRole string

const (
	User      MessageRole = "user"
	Assistant MessageRole = "assistant"
	Tool      MessageRole = "tool"
	System    MessageRole = "system"
)

// FinishReason classifies how an assistant turn ended.
type FinishReason string

const (
	FinishReasonUnknown       FinishReason = "unknown"
	FinishReasonEndTurn       FinishReason = "stop"
	FinishReasonMaxTokens     FinishReason = "max_tokens"
	FinishReasonToolUse       FinishReason = "tool_use"
	FinishReasonError         FinishReason = "error"
	FinishReasonCanceled      FinishReason = "canceled"
	FinishReasonContentFilter FinishReason = "content_filter"
)

// ContentPart is any part of a message.
type ContentPart interface{ part() }

// TextContent is plain text.
type TextContent struct {
	Text string `json:"text"`
}

func (TextContent) part() {}

// ReasoningContent is chain-of-thought emitted by reasoning models.
type ReasoningContent struct {
	Thinking  string `json:"thinking"`
	Signature string `json:"signature,omitempty"`
}

func (ReasoningContent) part() {}

// ToolCall is the model invoking a tool.
type ToolCall struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Input            string `json:"input,omitempty"`
	ProviderExecuted bool   `json:"provider_executed"`
	Finished         bool   `json:"finished"`
}

func (ToolCall) part() {}

// ToolResult is the outcome of a tool execution.
type ToolResult struct {
	ToolCallID  string `json:"tool_call_id"`
	Name        string `json:"name"`
	Content     string `json:"content"`
	MIMEType    string `json:"mime_type,omitempty"`
	Data        string `json:"data,omitempty"`
	IsError     bool   `json:"is_error"`
	StopTurn    bool   `json:"stop_turn,omitempty"`
	IsConfirmed bool   `json:"is_confirmed,omitempty"`
}

func (ToolResult) part() {}

// Finish marks the terminal state of an assistant message.
type Finish struct {
	Reason  FinishReason `json:"reason"`
	Message string       `json:"message,omitempty"`
	Details string       `json:"details,omitempty"`
}

func (Finish) part() {}

// Message is one persisted conversation entry.
type Message struct {
	ID               string
	Role             MessageRole
	SessionID        string
	Parts            []ContentPart
	Model            string
	Provider         string
	CreatedAt        int64
	UpdatedAt        int64
	IsSummaryMessage bool
	FinishedAt       *int64
}

// Content returns the first text part.
func (m *Message) Content() TextContent {
	for _, part := range m.Parts {
		if c, ok := part.(TextContent); ok {
			return c
		}
	}
	return TextContent{}
}

// ReasoningContent returns the first reasoning part.
func (m *Message) ReasoningContent() ReasoningContent {
	for _, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			return c
		}
	}
	return ReasoningContent{}
}

// ToolCalls returns all tool call parts.
func (m *Message) ToolCalls() []ToolCall {
	out := make([]ToolCall, 0)
	for _, part := range m.Parts {
		if c, ok := part.(ToolCall); ok {
			out = append(out, c)
		}
	}
	return out
}

// ToolResults returns all tool result parts.
func (m *Message) ToolResults() []ToolResult {
	out := make([]ToolResult, 0)
	for _, part := range m.Parts {
		if c, ok := part.(ToolResult); ok {
			out = append(out, c)
		}
	}
	return out
}

// IsFinished reports whether a Finish part is present.
func (m *Message) IsFinished() bool {
	for _, part := range m.Parts {
		if _, ok := part.(Finish); ok {
			return true
		}
	}
	return false
}

// SimpleTextValue returns the text content, empty when absent.
func (m *Message) SimpleTextValue() string { return m.Content().Text }

// AppendContent appends streamed text to the text part.
func (m *Message) AppendContent(text string) {
	for i, part := range m.Parts {
		if c, ok := part.(TextContent); ok {
			m.Parts[i] = TextContent{Text: c.Text + text}
			return
		}
	}
	m.Parts = append(m.Parts, TextContent{Text: text})
}

// ResetStreamedContent drops non-tool parts so a retried response does
// not concatenate with partial content from the failed attempt.
func (m *Message) ResetStreamedContent() {
	var parts []ContentPart
	for _, part := range m.Parts {
		switch part.(type) {
		case ToolCall, Finish:
			parts = append(parts, part)
		}
	}
	m.Parts = parts
}

// AppendReasoningContent appends streamed reasoning text.
func (m *Message) AppendReasoningContent(text string) {
	for i, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			c.Thinking += text
			m.Parts[i] = c
			return
		}
	}
	m.Parts = append(m.Parts, ReasoningContent{Thinking: text})
}

// AppendReasoningSignature stores the provider thinking signature.
func (m *Message) AppendReasoningSignature(sig string) {
	for i, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			c.Signature = sig
			m.Parts[i] = c
			return
		}
	}
	m.Parts = append(m.Parts, ReasoningContent{Signature: sig})
}

// FinishThinking marks the reasoning block as closed (structural flush).
func (m *Message) FinishThinking() {}

// AddToolCall upserts a tool call part by ID.
func (m *Message) AddToolCall(tc ToolCall) {
	for i, part := range m.Parts {
		if existing, ok := part.(ToolCall); ok && existing.ID == tc.ID {
			m.Parts[i] = tc
			return
		}
	}
	m.Parts = append(m.Parts, tc)
}

// AddFinish appends the terminal Finish part.
func (m *Message) AddFinish(reason FinishReason, message, details string) {
	for _, part := range m.Parts {
		if _, ok := part.(Finish); ok {
			return
		}
	}
	m.Parts = append(m.Parts, Finish{Reason: reason, Message: message, Details: details})
}

// ToAIMessage converts the message into fantasy protocol messages.
func (m *Message) ToAIMessage() []fantasy.Message {
	var messages []fantasy.Message
	switch m.Role {
	case User:
		var parts []fantasy.MessagePart
		text := strings.TrimSpace(m.Content().Text)
		if text != "" {
			parts = append(parts, fantasy.TextPart{Text: text})
		}
		messages = append(messages, fantasy.Message{
			Role:    fantasy.MessageRoleUser,
			Content: parts,
		})
	case Assistant:
		var parts []fantasy.MessagePart
		text := strings.TrimSpace(m.Content().Text)
		if text != "" {
			parts = append(parts, fantasy.TextPart{Text: text})
		}
		reasoning := m.ReasoningContent()
		if reasoning.Thinking != "" {
			reasoningPart := fantasy.ReasoningPart{Text: reasoning.Thinking, ProviderOptions: fantasy.ProviderOptions{}}
			if reasoning.Signature != "" {
				reasoningPart.ProviderOptions[anthropic.Name] = &anthropic.ReasoningOptionMetadata{
					Signature: reasoning.Signature,
				}
			}
			parts = append(parts, reasoningPart)
		}
		for _, call := range m.ToolCalls() {
			parts = append(parts, fantasy.ToolCallPart{
				ToolCallID:       call.ID,
				ToolName:         call.Name,
				Input:            call.Input,
				ProviderExecuted: call.ProviderExecuted,
			})
		}
		messages = append(messages, fantasy.Message{
			Role:    fantasy.MessageRoleAssistant,
			Content: parts,
		})
	case Tool:
		var parts []fantasy.MessagePart
		for _, result := range m.ToolResults() {
			var content fantasy.ToolResultOutputContent
			if result.IsError {
				content = fantasy.ToolResultOutputContentError{
					Error: errors.New(result.Content),
				}
			} else if result.Data != "" {
				content = fantasy.ToolResultOutputContentMedia{
					Data:      result.Data,
					MediaType: result.MIMEType,
				}
			} else {
				content = fantasy.ToolResultOutputContentText{
					Text: result.Content,
				}
			}
			parts = append(parts, fantasy.ToolResultPart{
				ToolCallID: result.ToolCallID,
				Output:     content,
			})
		}
		messages = append(messages, fantasy.Message{
			Role:    fantasy.MessageRoleTool,
			Content: parts,
		})
	}
	return messages
}
