package hook

import (
	"encoding/json"
	"sort"
)

// Caps on what Summarize keeps, so one odd payload cannot make a log line
// large: the counts stay exact, only the listed entries and key names stop.
const (
	maxSummaryTasks = 20
	maxSummaryKeys  = 50
)

// TaskSummary is the part of one `background_tasks` entry a capture keeps:
// the three scalar fields that say what a task is and whether it is still
// running. Nothing else of an entry (its description, command or prompt) is
// ever read.
type TaskSummary struct {
	ID     string `json:"id,omitempty"`
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
}

// Summary is the decision-relevant, non-sensitive view of a hook payload that
// the opt-in payload capture (PAWL_HOOK_LOG) records. It deliberately has no
// field for tool_input, last_assistant_message, transcript_path or any prompt:
// a command is reduced to what the classifiers say about it and its length.
type Summary struct {
	PayloadKeys     []string      `json:"payload_keys"`
	HookEventName   string        `json:"hook_event_name"`
	AgentID         string        `json:"agent_id"`
	Cwd             string        `json:"cwd"`
	StopHookActive  bool          `json:"stop_hook_active"`
	BackgroundTasks int           `json:"background_tasks"`
	Tasks           []TaskSummary `json:"background_task_entries,omitempty"`
	SessionCrons    int           `json:"session_crons"`
	PawlCommand     bool          `json:"pawl_command"`
	PawlOnly        bool          `json:"pawl_only"`
	VCSMutation     bool          `json:"vcs_mutation"`
	CommandBytes    int           `json:"command_bytes"`
}

// Summarize builds a Summary from a hook's stdin JSON. A payload that is not a
// JSON object gives the zero Summary, and a field of an unexpected type reads
// as its zero value: it never fails, because a capture must not be able to
// break a hook call. It is pure: no I/O, no clock.
func Summarize(data []byte) Summary {
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil || top == nil {
		return Summary{}
	}
	str := func(raw json.RawMessage) string {
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return ""
		}
		return v
	}
	var s Summary
	for k := range top {
		s.PayloadKeys = append(s.PayloadKeys, k)
	}
	sort.Strings(s.PayloadKeys)
	if len(s.PayloadKeys) > maxSummaryKeys {
		s.PayloadKeys = s.PayloadKeys[:maxSummaryKeys]
	}
	s.HookEventName = str(top["hook_event_name"])
	s.AgentID = str(top["agent_id"])
	s.Cwd = str(top["cwd"])
	_ = json.Unmarshal(top["stop_hook_active"], &s.StopHookActive)

	var tasks []json.RawMessage
	_ = json.Unmarshal(top["background_tasks"], &tasks)
	s.BackgroundTasks = len(tasks)
	for i, raw := range tasks {
		if i >= maxSummaryTasks {
			break
		}
		var entry map[string]json.RawMessage
		if json.Unmarshal(raw, &entry) != nil {
			continue
		}
		ts := TaskSummary{ID: str(entry["id"]), Type: str(entry["type"]), Status: str(entry["status"])}
		if ts != (TaskSummary{}) {
			s.Tasks = append(s.Tasks, ts)
		}
	}
	var crons []json.RawMessage
	_ = json.Unmarshal(top["session_crons"], &crons)
	s.SessionCrons = len(crons)

	if str(top["tool_name"]) == "Bash" {
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(top["tool_input"], &in)
		s.CommandBytes = len(in.Command)
		s.PawlCommand = IsPawlCommand(in.Command)
		s.PawlOnly = IsPawlOnlyCommand(in.Command)
		s.VCSMutation = IsVCSMutation(in.Command)
	}
	return s
}
