// Package tools implements the built-in agent tools.
package tools

import (
	"context"
	"fmt"

	"charm.land/fantasy"
)

type sessionIDContextKey string
type messageIDContextKey string
type supportsImagesKey string
type modelNameKey string

const (
	// SessionIDContextKey is the context key carrying the session ID.
	SessionIDContextKey = sessionIDContextKey("sessionID")
	// MessageIDContextKey is the context key carrying the message ID.
	MessageIDContextKey = messageIDContextKey("messageID")
	// SupportsImagesContextKey reports whether the model takes images.
	SupportsImagesContextKey = supportsImagesKey("supportsImages")
	// ModelNameContextKey carries the model display name.
	ModelNameContextKey = modelNameKey("modelName")
)

// GetSessionFromContext retrieves the session ID from the context.
func GetSessionFromContext(ctx context.Context) string {
	v, _ := ctx.Value(SessionIDContextKey).(string)
	return v
}

// GetMessageFromContext retrieves the message ID from the context.
func GetMessageFromContext(ctx context.Context) string {
	v, _ := ctx.Value(MessageIDContextKey).(string)
	return v
}

// GetSupportsImagesFromContext retrieves whether the model supports images.
func GetSupportsImagesFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(SupportsImagesContextKey).(bool)
	return v
}

// GetModelNameFromContext retrieves the model name from the context.
func GetModelNameFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ModelNameContextKey).(string)
	return v
}

// NewPermissionDeniedResponse returns a tool response indicating the
// user denied permission, with StopTurn set so the agent loop does not
// retry.
func NewPermissionDeniedResponse() fantasy.ToolResponse {
	return fantasy.ToolResponse{
		Type:     "text",
		Content:  "The user denied permission for this tool call.",
		IsError:  true,
		StopTurn: true,
	}
}

// GetSeekPosition clamps a requested offset to the file line count.
func GetSeekPosition(offset, limit int) int {
	if offset < 0 {
		return 0
	}
	if limit == 0 && offset == 0 {
		return 0
	}
	return offset
}

// TruncateLines cuts a string at max lines, appending a notice.
func TruncateLines(s string, max int) string {
	if max <= 0 {
		return s
	}
	count := 0
	for i := range len(s) {
		if s[i] == '\n' {
			count++
			if count > max {
				return s[:i] + fmt.Sprintf("\n\n(Output truncated at %d lines)", max)
			}
		}
	}
	return s
}
