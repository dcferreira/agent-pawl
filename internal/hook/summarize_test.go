package hook

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

func TestSummarize_StopPayload(t *testing.T) {
	s := Summarize([]byte(`{"session_id":"s1","cwd":"/w","hook_event_name":"Stop","stop_hook_active":true,
		"last_assistant_message":"LAST_MSG_SENTINEL","transcript_path":"/home/u/TRANSCRIPT_SENTINEL.jsonl",
		"background_tasks":[{"id":"t1","type":"subagent","status":"running","description":"DESC_SENTINEL","command":"CMD_SENTINEL"},
			{"id":"t2","type":"shell","status":"completed"}],
		"session_crons":[{"id":"c1"}]}`))
	if s.HookEventName != "Stop" || s.Cwd != "/w" || !s.StopHookActive || s.AgentID != "" {
		t.Fatalf("scalars wrong: %+v", s)
	}
	if s.BackgroundTasks != 2 || s.SessionCrons != 1 {
		t.Fatalf("counts wrong: %+v", s)
	}
	want := []TaskSummary{{ID: "t1", Type: "subagent", Status: "running"}, {ID: "t2", Type: "shell", Status: "completed"}}
	if !reflect.DeepEqual(s.Tasks, want) {
		t.Fatalf("tasks = %+v, want %+v", s.Tasks, want)
	}
	wantKeys := []string{"background_tasks", "cwd", "hook_event_name", "last_assistant_message", "session_crons", "session_id", "stop_hook_active", "transcript_path"}
	if !reflect.DeepEqual(s.PayloadKeys, wantKeys) {
		t.Fatalf("payload_keys = %v, want %v", s.PayloadKeys, wantKeys)
	}
	if s.PawlCommand || s.PawlOnly || s.VCSMutation || s.CommandBytes != 0 {
		t.Fatalf("a Stop payload has no command: %+v", s)
	}
}

func TestSummarize_NeverCarriesForbiddenContent(t *testing.T) {
	data := `{"session_id":"s1","cwd":"/w","hook_event_name":"PreToolUse","tool_name":"Bash",
		"tool_input":{"command":"curl -H 'Authorization: Bearer CMD_SENTINEL' x && git push"},
		"last_assistant_message":"LAST_MSG_SENTINEL","transcript_path":"/x/TRANSCRIPT_SENTINEL.jsonl",
		"prompt":"PROMPT_SENTINEL","agent_id":"a7",
		"background_tasks":[{"id":"t1","type":"subagent","status":"running","description":"DESC_SENTINEL","prompt":"TASKPROMPT_SENTINEL"}]}`
	s := Summarize([]byte(data))
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range []string{"CMD_SENTINEL", "LAST_MSG_SENTINEL", "TRANSCRIPT_SENTINEL", "PROMPT_SENTINEL", "DESC_SENTINEL", "TASKPROMPT_SENTINEL", "Bearer", "curl"} {
		if strings.Contains(string(out), sentinel) {
			t.Errorf("summary leaks %q: %s", sentinel, out)
		}
	}
	if s.AgentID != "a7" || !s.VCSMutation || s.PawlCommand || s.CommandBytes == 0 {
		t.Fatalf("pre summary wrong: %+v", s)
	}
}

func TestSummarize_CommandBooleans(t *testing.T) {
	type tcase struct {
		cmd string

		pawl, only, vcs bool
	}
	for _, tc := range []tcase{
		{"pawl status", true, true, false},
		{"git push origin main", false, false, true},
		{"ls", false, false, false},
	} {
		data, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]string{"command": tc.cmd}})
		s := Summarize(data)
		if s.PawlCommand != tc.pawl || s.PawlOnly != tc.only || s.VCSMutation != tc.vcs || s.CommandBytes != len(tc.cmd) {
			t.Errorf("%q: got %+v", tc.cmd, s)
		}
	}
}

func TestSummarize_UnknownShapesAreCountedNotRead(t *testing.T) {
	s := Summarize([]byte(`{"background_tasks":["a",7,{"id":5,"type":["x"],"status":"ok"}],"session_crons":"nope","stop_hook_active":"yes"}`))
	if s.BackgroundTasks != 3 {
		t.Fatalf("count = %d", s.BackgroundTasks)
	}
	if want := []TaskSummary{{Status: "ok"}}; len(s.Tasks) != 1 || s.Tasks[0] != want[0] {
		t.Fatalf("only the string scalars of an object entry are kept: %+v", s.Tasks)
	}
	if s.SessionCrons != 0 || s.StopHookActive {
		t.Fatalf("a field of the wrong type reads as zero: %+v", s)
	}
}

func TestSummarize_GarbageIsZero(t *testing.T) {
	for _, in := range []string{"", "not json", "[1]", "null"} {
		if s := Summarize([]byte(in)); !reflect.DeepEqual(s, Summary{}) {
			t.Errorf("Summarize(%q) = %+v, want zero", in, s)
		}
	}
}

func TestSummarize_CapsEntries(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"background_tasks":[`)
	for i := 0; i < 60; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"id":"t","type":"x","status":"y"}`)
	}
	b.WriteString(`]}`)
	s := Summarize([]byte(b.String()))
	if s.BackgroundTasks != 60 || len(s.Tasks) != 20 {
		t.Fatalf("count=%d entries=%d, want 60 counted and 20 kept", s.BackgroundTasks, len(s.Tasks))
	}
}

func TestDecidePre_GuardDenyNamesGuardID(t *testing.T) {
	g := table(t, spec.GuardDecl{ID: "push-in-commit", Match: "git push", OnlyIn: []string{}})
	runs := []LiveRun{{RunID: "r1", WorkflowID: "w", CursorStep: "fix", CursorKind: "agentic", ActiveSteps: []string{"fix"}, Guards: g}}
	d := DecidePre(runs, Payload{ToolName: "Bash", Command: "git push"})
	if d.Allow || d.GuardID != "push-in-commit" {
		t.Fatalf("decision = %+v, want a deny carrying GuardID", d)
	}
	if d := DecidePre(runs, Payload{ToolName: "Bash", Command: "ls"}); !d.Allow || d.GuardID != "" {
		t.Fatalf("allow must carry no GuardID: %+v", d)
	}
}
