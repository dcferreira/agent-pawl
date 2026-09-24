// Package hook decides what pawl's Claude Code hooks (PreToolUse, Stop)
// allow. Its decision functions are pure: the cli layer loads live runs off
// disk and maps a Decision to the hook exit-code protocol.
package hook

import (
	"encoding/json"
	"fmt"
)

// Payload is the subset of a Claude Code hook's stdin JSON pawl reads.
// AgentID is present only when the hook fires inside a subagent.
// StopHookActive, BackgroundTasks and SessionCrons are only meaningful on
// Stop payloads. BackgroundTasks/SessionCrons are counts of the payload's
// `background_tasks`/`session_crons` arrays (in-flight tasks and scheduled
// wakeups respectively) — both are 0 when the arrays are absent or empty.
type Payload struct {
	SessionID       string
	Cwd             string
	HookEventName   string
	ToolName        string
	Command         string // tool_input.command; "" for non-Bash or Stop
	AgentID         string // "" for the main session
	StopHookActive  bool
	BackgroundTasks int
	SessionCrons    int
}

// rawPayload mirrors the JSON shape Claude Code sends on a hook's stdin.
// BackgroundTasks/SessionCrons are decoded as raw messages, not a typed
// struct, so an entry shape pawl doesn't model never fails parsing — pawl
// only needs their counts.
type rawPayload struct {
	SessionID     string `json:"session_id"`
	Cwd           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	AgentID         string            `json:"agent_id"`
	StopHookActive  bool              `json:"stop_hook_active"`
	BackgroundTasks []json.RawMessage `json:"background_tasks"`
	SessionCrons    []json.RawMessage `json:"session_crons"`
}

// ParsePayload decodes a hook's stdin JSON into a Payload.
func ParsePayload(data []byte) (Payload, error) {
	var r rawPayload
	if err := json.Unmarshal(data, &r); err != nil {
		return Payload{}, fmt.Errorf("hook: parsing payload: %w", err)
	}
	return Payload{
		SessionID:       r.SessionID,
		Cwd:             r.Cwd,
		HookEventName:   r.HookEventName,
		ToolName:        r.ToolName,
		Command:         r.ToolInput.Command,
		AgentID:         r.AgentID,
		StopHookActive:  r.StopHookActive,
		BackgroundTasks: len(r.BackgroundTasks),
		SessionCrons:    len(r.SessionCrons),
	}, nil
}
