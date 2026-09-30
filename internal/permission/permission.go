// Package permission provides tool permission checking and prompts.
package permission

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/Haslab-dev/RakitKode/internal/pubsub"
)

// CreatePermissionRequest is what a tool asks for.
type CreatePermissionRequest struct {
	SessionID   string `json:"session_id"`
	ToolCallID  string `json:"tool_call_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Params      any    `json:"params"`
	Path        string `json:"path"`
}

// PermissionNotification tells subscribers a request was resolved (or surfaced).
type PermissionNotification struct {
	ToolCallID string `json:"tool_call_id"`
	Granted    bool   `json:"granted"`
	Denied     bool   `json:"denied"`
}

// PermissionRequest is the published prompt for the UI.
type PermissionRequest struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	ToolCallID  string `json:"tool_call_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Params      any    `json:"params"`
	Path        string `json:"path"`
}

// Service gates tool execution behind user approval.
type Service interface {
	pubsub.Subscriber[PermissionRequest]
	// GrantPersistent grants and remembers the grant for the session.
	GrantPersistent(permission PermissionRequest) bool
	// Grant resolves a pending request as allowed.
	Grant(permission PermissionRequest) bool
	// Deny resolves a pending request as denied.
	Deny(permission PermissionRequest) bool
	Request(ctx context.Context, opts CreatePermissionRequest) (bool, error)
	AutoApproveSession(sessionID string)
	SetSkipRequests(skip bool)
	SkipRequests() bool
	SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[PermissionNotification]
}

// PermissionKey identifies a remembered (session, tool, action, path) grant.
type PermissionKey struct {
	SessionID string
	ToolName  string
	Action    string
	Path      string
}

type permissionService struct {
	*pubsub.Broker[PermissionRequest]

	notificationBroker    *pubsub.Broker[PermissionNotification]
	workingDir            string
	sessionPermissions    sync.Map
	pendingRequests       sync.Map
	autoApproveSessions   sync.Map
	skip                  atomic.Bool
	allowedTools          []string
}

// NewPermissionService builds the permission service.
func NewPermissionService(workingDir string, skip bool, allowedTools []string) Service {
	svc := &permissionService{
		Broker:             pubsub.NewBroker[PermissionRequest](),
		notificationBroker: pubsub.NewBroker[PermissionNotification](),
		workingDir:         workingDir,
		allowedTools:       allowedTools,
	}
	svc.skip.Store(skip)
	return svc
}

// resolve atomically removes a pending request and forwards the outcome.
// Returns false when the request was already resolved or is unknown.
func (s *permissionService) resolve(permission PermissionRequest, granted, denied bool, onResolve func()) bool {
	v, ok := s.pendingRequests.LoadAndDelete(permission.ID)
	if !ok {
		return false
	}
	respCh, ok := v.(chan bool)
	if !ok {
		return false
	}
	if onResolve != nil {
		onResolve()
	}
	s.notificationBroker.Publish(pubsub.CreatedEvent, PermissionNotification{
		ToolCallID: permission.ToolCallID,
		Granted:    granted,
		Denied:     denied,
	})
	respCh <- granted
	return true
}

// GrantPersistent grants a permission and remembers it for the session.
func (s *permissionService) GrantPersistent(permission PermissionRequest) bool {
	return s.resolve(permission, true, false, func() {
		s.sessionPermissions.Store(PermissionKey{
			SessionID: permission.SessionID,
			ToolName:  permission.ToolName,
			Action:    permission.Action,
			Path:      permission.Path,
		}, true)
	})
}

// Grant resolves a pending request as allowed without remembering.
func (s *permissionService) Grant(permission PermissionRequest) bool {
	return s.resolve(permission, true, false, nil)
}

// Deny resolves a pending request as denied.
func (s *permissionService) Deny(permission PermissionRequest) bool {
	return s.resolve(permission, false, true, nil)
}

// Request blocks until the user grants or denies, or ctx is cancelled.
func (s *permissionService) Request(ctx context.Context, opts CreatePermissionRequest) (bool, error) {
	if s.skip.Load() {
		return true, nil
	}

	// Allowlist check: "tool" or "tool:action".
	commandKey := opts.ToolName + ":" + opts.Action
	if slices.Contains(s.allowedTools, commandKey) || slices.Contains(s.allowedTools, opts.ToolName) {
		return true, nil
	}

	s.notificationBroker.Publish(pubsub.CreatedEvent, PermissionNotification{
		ToolCallID: opts.ToolCallID,
	})

	if _, autoApprove := s.autoApproveSessions.Load(opts.SessionID); autoApprove {
		s.notificationBroker.Publish(pubsub.CreatedEvent, PermissionNotification{
			ToolCallID: opts.ToolCallID,
			Granted:    true,
		})
		return true, nil
	}

	dir := opts.Path
	if fileInfo, err := os.Stat(opts.Path); err == nil && !fileInfo.IsDir() {
		dir = filepath.Dir(opts.Path)
	}
	if dir == "." || dir == "" {
		dir = s.workingDir
	}

	permission := PermissionRequest{
		ID:          uuid.New().String(),
		Path:        dir,
		SessionID:   opts.SessionID,
		ToolCallID:  opts.ToolCallID,
		ToolName:    opts.ToolName,
		Description: opts.Description,
		Action:      opts.Action,
		Params:      opts.Params,
	}

	if _, ok := s.sessionPermissions.Load(PermissionKey{
		SessionID: permission.SessionID,
		ToolName:  permission.ToolName,
		Action:    permission.Action,
		Path:      permission.Path,
	}); ok {
		s.notificationBroker.Publish(pubsub.CreatedEvent, PermissionNotification{
			ToolCallID: opts.ToolCallID,
			Granted:    true,
		})
		return true, nil
	}

	respCh := make(chan bool, 1)
	s.pendingRequests.Store(permission.ID, respCh)
	defer s.pendingRequests.Delete(permission.ID)

	s.Publish(pubsub.CreatedEvent, permission)

	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case granted := <-respCh:
		return granted, nil
	}
}

// AutoApproveSession marks a session to auto-grant every request.
func (s *permissionService) AutoApproveSession(sessionID string) {
	s.autoApproveSessions.Store(sessionID, true)
}

// SubscribeNotifications returns the notification channel.
func (s *permissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[PermissionNotification] {
	return s.notificationBroker.Subscribe(ctx)
}

// SetSkipRequests toggles global skip (yolo mode).
func (s *permissionService) SetSkipRequests(skip bool) {
	s.skip.Store(skip)
}

// SkipRequests reports whether permission prompting is disabled.
func (s *permissionService) SkipRequests() bool {
	return s.skip.Load()
}
