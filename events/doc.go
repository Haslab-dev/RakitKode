// Package events defines the engine's event bus and the wire contract for
// agent events (CONTRACT.md, Tier 1): event names and payload keys are part
// of the public interface. Hosts (desktop apps, servers, TUIs) subscribe to
// these to render agent progress.
//
// Contract rules:
//
//   - Event name strings never change; new events are additions.
//   - Payload keys are additive-only: existing keys keep their name, type,
//     and meaning. Consumers must tolerate unknown keys.
//   - Every agent event payload includes "session_id" (string) identifying
//     the session the event belongs to.
//
// Agent event payloads:
//
//	agent:turn_start     (no additional keys)
//	agent:turn_end       error string ("" = success)
//	agent:message_start  message_id, role
//	agent:message_delta  kind ("text" | "thinking"), delta, turn
//	agent:message_end    message_id, role
//	agent:thinking_delta delta, turn
//	agent:tool_start     tool_call_id, name, args (map), turn
//	agent:tool_delta     index, name, args (string fragments), turn
//	agent:tool_end       tool_call_id, name, args, result, is_error, turn
//	agent:ask            questions ([]tools.AskQuestion)
//	agent:task_update    id, task_id, command, cwd, status, started_at,
//	                     output_tail, output_file (+ exit_code, finished_at
//	                     when finished)
//	agent:schedule_update — schedule snapshot / change record
//	agent:updated        (session-snapshot change signal; no payload contract)
//	agent:usage          prompt_tokens, completion_tokens, cached_tokens,
//	                     cache_hit_rate, context_tokens, turn
//	agent:tool_progress  tool_call_id, name, elapsed_ms, turn
//	agent:permission_request  tool_call_id, name, args, reason, turn
//	agent:subagent_update    subagent_id, phase, summary, usage
//	agent:compaction     reason, before_tokens, after_tokens
//	agent:turn_resumed   (no additional keys)
//	agent:plan_update    mode, plan_path, approved
//
// Non-agent events (workspace:*, file:*, terminal:*, git:*, ssh:*, plugin:*)
// are bus-internal to hosts that also use those features and follow the same
// additive-only rule.
package events
