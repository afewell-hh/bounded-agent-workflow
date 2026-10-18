package claude

// Event actions from the frozen 2.1.285 classification evidence.
const (
	ActionCore        = "core"
	ActionInformation = "conditional_information"
	ActionStop        = "stop"
)

// StopUnknown is the fixed stop code for any (type, subtype) pair absent
// from the classification table.
const StopUnknown = "stream_unestablished"

// Classification is one row of the frozen table. An empty Subtype with
// HasSubtype false is the JSON null subtype (the member is absent).
type Classification struct {
	Type, Subtype string
	HasSubtype    bool
	Action        string
	StopReason    string
}

const (
	stopNotEnabled = "native_event_not_enabled_in_worker_profile"
	stopBoundary   = "native_profile_boundary_event"
	stopModel      = "native_model_or_refusal_stop"
	stopCompletion = "native_completion_failed"
)

func row(typ, sub, action, stop string) Classification {
	return Classification{Type: typ, Subtype: sub, HasSubtype: sub != "", Action: action, StopReason: stop}
}

// Classifications is the frozen claude-stream-classifications table for
// version 2.1.285, in fixture order. It is checked against testdata.
var Classifications = []Classification{
	row("active_goal", "", ActionStop, stopNotEnabled),
	row("api_metrics", "", ActionStop, stopNotEnabled),
	row("apply_flag_settings", "", ActionStop, stopNotEnabled),
	row("assistant", "", ActionCore, ""),
	row("attachment", "", ActionStop, stopNotEnabled),
	row("auth_status", "", ActionStop, "native_auth_transition"),
	row("autocompact_state", "", ActionStop, stopNotEnabled),
	row("bash_command", "", ActionStop, stopNotEnabled),
	row("command_lifecycle", "", ActionStop, stopNotEnabled),
	row("compact_progress", "", ActionStop, stopNotEnabled),
	row("conversation_reset", "", ActionStop, stopNotEnabled),
	row("hint_clears", "", ActionStop, stopNotEnabled),
	row("local", "", ActionStop, stopNotEnabled),
	row("open_message_selector", "", ActionStop, stopNotEnabled),
	row("os_notification", "", ActionStop, stopNotEnabled),
	row("prompt_suggestion", "", ActionStop, stopNotEnabled),
	row("rate_limit_event", "", ActionInformation, ""),
	row("result", "", ActionCore, ""),
	row("result", "success", ActionCore, ""),
	row("set_expanded_view", "", ActionStop, stopNotEnabled),
	row("set_in_progress_tool_use_ids", "", ActionStop, stopNotEnabled),
	row("stream_event", "", ActionStop, stopNotEnabled),
	row("stream_mode", "", ActionStop, stopNotEnabled),
	row("system", "agents_killed", ActionStop, stopNotEnabled),
	row("system", "api_error", ActionStop, stopNotEnabled),
	row("system", "api_retry", ActionStop, "native_api_retry"),
	row("system", "away_summary", ActionStop, stopNotEnabled),
	row("system", "background_tasks_changed", ActionStop, stopBoundary),
	row("system", "cloud_session_delta", ActionStop, stopNotEnabled),
	row("system", "code_change_published", ActionStop, stopNotEnabled),
	row("system", "commands_changed", ActionStop, stopNotEnabled),
	row("system", "compact_boundary", ActionInformation, ""),
	row("system", "control_request_progress", ActionStop, stopNotEnabled),
	row("system", "dev_intent", ActionStop, stopNotEnabled),
	row("system", "elicitation_complete", ActionStop, stopBoundary),
	row("system", "feedback_draft_queued", ActionStop, stopNotEnabled),
	row("system", "file_snapshot", ActionStop, stopNotEnabled),
	row("system", "files_persisted", ActionStop, stopNotEnabled),
	row("system", "hook_progress", ActionStop, stopBoundary),
	row("system", "hook_response", ActionStop, stopBoundary),
	row("system", "hook_started", ActionStop, stopBoundary),
	row("system", "informational", ActionStop, "native_information_requires_classification"),
	row("system", "init", ActionCore, ""),
	row("system", "local_command_output", ActionStop, stopNotEnabled),
	row("system", "memory_recall", ActionStop, stopBoundary),
	row("system", "memory_saved", ActionStop, stopBoundary),
	row("system", "mirror_error", ActionStop, stopNotEnabled),
	row("system", "model_consent_fallback", ActionStop, stopModel),
	row("system", "model_fallback", ActionStop, stopModel),
	row("system", "model_refusal_fallback", ActionStop, stopModel),
	row("system", "model_refusal_no_fallback", ActionStop, stopModel),
	row("system", "notification", ActionStop, stopNotEnabled),
	row("system", "peer_message_hold", ActionStop, stopBoundary),
	row("system", "per_turn_effort_changed", ActionInformation, ""),
	row("system", "permission_denied", ActionInformation, ""),
	row("system", "permission_retry", ActionStop, stopNotEnabled),
	row("system", "plugin_install", ActionStop, stopBoundary),
	row("system", "post_turn_summary", ActionStop, stopNotEnabled),
	row("system", "scheduled_task_fire", ActionStop, stopBoundary),
	row("system", "session_metadata", ActionStop, stopNotEnabled),
	row("system", "session_state_changed", ActionStop, stopNotEnabled),
	row("system", "session_title_changed", ActionInformation, ""),
	row("system", "status", ActionInformation, ""),
	row("system", "stop_hook_summary", ActionStop, stopBoundary),
	row("system", "task_notification", ActionStop, stopBoundary),
	row("system", "task_progress", ActionStop, stopBoundary),
	row("system", "task_started", ActionStop, stopBoundary),
	row("system", "task_summary", ActionStop, stopBoundary),
	row("system", "task_updated", ActionStop, stopBoundary),
	row("system", "thinking", ActionStop, stopNotEnabled),
	row("system", "thinking_tokens", ActionInformation, ""),
	row("system", "turn_duration", ActionStop, stopNotEnabled),
	row("system", "turn_handoff_available", ActionStop, stopNotEnabled),
	row("system", "turn_preempted", ActionStop, stopNotEnabled),
	row("system", "ui_focus", ActionStop, stopBoundary),
	row("system", "ui_invalidate", ActionStop, stopBoundary),
	row("system", "ui_log", ActionStop, stopBoundary),
	row("system", "ui_panes", ActionStop, stopBoundary),
	row("system", "ui_scroll", ActionStop, stopBoundary),
	row("system", "ui_status", ActionStop, stopBoundary),
	row("system", "ui_toast", ActionStop, stopBoundary),
	row("system", "vcs_state_changed", ActionStop, stopNotEnabled),
	row("system", "worker_shutting_down", ActionStop, stopNotEnabled),
	row("tombstone", "", ActionStop, stopNotEnabled),
	row("tool_progress", "", ActionStop, stopNotEnabled),
	row("tool_use_summary", "", ActionStop, stopNotEnabled),
	row("transcript_mirror", "", ActionStop, stopNotEnabled),
	row("user", "", ActionCore, ""),
	row("result", "error_during_execution", ActionStop, stopCompletion),
	row("result", "error_max_turns", ActionStop, stopCompletion),
	row("result", "error_max_budget_usd", ActionStop, stopCompletion),
	row("result", "error_max_structured_output_retries", ActionStop, stopCompletion),
}

// classify looks up the exact (type, subtype) row; an absent subtype only
// matches a null-subtype row. ok is false for an unknown pair.
func classify(typ, sub string, hasSub bool) (Classification, bool) {
	for _, c := range Classifications {
		if c.Type == typ && c.HasSubtype == hasSub && c.Subtype == sub {
			return c, true
		}
	}
	return Classification{}, false
}

// label is the fixed information-count label of a classified row.
func (c Classification) label() string {
	if c.HasSubtype {
		return c.Type + "/" + c.Subtype
	}
	return c.Type
}
