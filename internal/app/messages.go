package app

import (
	"github.com/Haslab-dev/RakitKode/internal/agent"
	"github.com/Haslab-dev/RakitKode/internal/message"
	"github.com/Haslab-dev/RakitKode/internal/permission"
	"github.com/Haslab-dev/RakitKode/internal/pubsub"
	"github.com/Haslab-dev/RakitKode/internal/session"
)

// SessionUpdateMsg wraps a session event for the TUI.
type SessionUpdateMsg pubsub.Event[session.Session]

// MessageUpdateMsg wraps a message event for the TUI.
type MessageUpdateMsg pubsub.Event[message.Message]

// PermissionRequestMsg surfaces a pending permission prompt.
type PermissionRequestMsg permission.PermissionRequest

// PermissionNotificationMsg reports a resolved permission.
type PermissionNotificationMsg permission.PermissionNotification

// RunCompleteMsg is the terminal event of an agent run.
type RunCompleteMsg agent.RunComplete

// SubmitMsg carries a prompt submission from the TUI.
type SubmitMsg struct {
	SessionID string
	Prompt    string
}
