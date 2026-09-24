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
