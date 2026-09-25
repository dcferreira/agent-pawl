package hook

import "testing"

func TestParsePayload_PreToolUseSubagent(t *testing.T) {
	p, err := ParsePayload([]byte(`{"session_id":"s1","cwd":"/w","hook_event_name":"PreToolUse",
		"tool_name":"Bash","tool_input":{"command":"git push"},"agent_id":"a7","agent_type":"general-purpose"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Payload{SessionID: "s1", Cwd: "/w", HookEventName: "PreToolUse", ToolName: "Bash", Command: "git push", AgentID: "a7"}
	if p != want {
		t.Fatalf("got %+v, want %+v", p, want)
	}
}

func TestParsePayload_Stop(t *testing.T) {
	p, err := ParsePayload([]byte(`{"session_id":"s1","cwd":"/w","hook_event_name":"Stop","stop_hook_active":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p.StopHookActive || p.AgentID != "" || p.Command != "" {
		t.Fatalf("got %+v", p)
	}
}

func TestParsePayload_Garbage(t *testing.T) {
	if _, err := ParsePayload([]byte("not json")); err == nil {
		t.Fatal("want error")
	}
}

func TestParsePayload_BackgroundTasksAndSessionCrons(t *testing.T) {
	p, err := ParsePayload([]byte(`{"session_id":"s1","cwd":"/w","hook_event_name":"Stop",
		"background_tasks":[{"id":"t1","type":"subagent","status":"running","description":"fix tests"}],
		"session_crons":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.BackgroundTasks != 1 || p.SessionCrons != 0 {
		t.Fatalf("got %+v", p)
	}
}

func TestParsePayload_BackgroundTasksAbsent(t *testing.T) {
	p, err := ParsePayload([]byte(`{"session_id":"s1","cwd":"/w","hook_event_name":"Stop"}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.BackgroundTasks != 0 || p.SessionCrons != 0 {
		t.Fatalf("got %+v", p)
	}
}

func TestParsePayload_BackgroundTasksNull(t *testing.T) {
	p, err := ParsePayload([]byte(`{"session_id":"s1","cwd":"/w","hook_event_name":"Stop",
		"background_tasks":null,"session_crons":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.BackgroundTasks != 0 || p.SessionCrons != 0 {
		t.Fatalf("got %+v", p)
	}
}

func TestParsePayload_BackgroundTasksUnexpectedShape(t *testing.T) {
	// An entry shape pawl doesn't model must still parse rather than fail
	// the whole payload.
	p, err := ParsePayload([]byte(`{"session_id":"s1","cwd":"/w","hook_event_name":"Stop",
		"background_tasks":[{"unexpected_field":123,"nested":{"a":[1,2,3]}}],
		"session_crons":[{"id":"c1","schedule":"*/5 * * * *"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.BackgroundTasks != 1 || p.SessionCrons != 1 {
		t.Fatalf("got %+v", p)
	}
}
