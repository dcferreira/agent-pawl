package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// hookLogDir returns a not-yet-created log directory under a temp dir and sets
// PAWL_HOOK_LOG to it, so a test also proves the directory is created by pawl.
func hookLogDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "logs", "pawl")
	t.Setenv("PAWL_HOOK_LOG", dir)
	return dir
}

// readHookLog returns the decoded lines of dir/<session>.jsonl.
func readHookLog(t *testing.T, dir, session string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, session+".jsonl"))
	if err != nil {
		t.Fatalf("reading the hook log: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("hook log line is not JSON: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func startDrivenHookRun(t *testing.T) (root, runID string) {
	t.Helper()
	root, runID = startHookRun(t)
	if err := journal.WriteDriver(journal.RunDir(root, "hookwf", runID), "s1", nowFunc()); err != nil {
		t.Fatal(err)
	}
	return root, runID
}

func TestHookLog_OffByDefaultWritesNothing(t *testing.T) {
	root, _ := startDrivenHookRun(t)
	t.Setenv("PAWL_HOOK_LOG", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	code, out, errs := hookCall(t, "stop", raceStopPayload(root, "s1"))
	if _, ok := stopBlockReason(t, code, out); !ok || errs != "" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "pawl", "hook-log")); !os.IsNotExist(err) {
		t.Fatalf("nothing may be created when PAWL_HOOK_LOG is empty: %v", err)
	}
}

func TestHookLog_PreGuardDeny(t *testing.T) {
	root, runID := startDrivenHookRun(t)
	dir := hookLogDir(t)
	cmd := "git push origin main"
	code, out, _ := hookCall(t, "pre", prePayload(root, "s1", cmd, ""))
	if _, ok := preDenyReason(t, code, out); !ok {
		t.Fatalf("precondition: want a deny; code=%d out=%q", code, out)
	}
	lines := readHookLog(t, dir, "s1")
	if len(lines) != 1 {
		t.Fatalf("lines = %v", lines)
	}
	l := lines[0]
	if l["event"] != "pre" || l["decision"] != "deny" || l["guard_id"] != "push-in-commit" || l["session_id"] != "s1" {
		t.Fatalf("line = %v", l)
	}
	if l["pawl_command"] != false || l["vcs_mutation"] != true || l["command_bytes"] != float64(len(cmd)) {
		t.Fatalf("command booleans wrong: %v", l)
	}
	if r, _ := l["reason"].(string); !strings.Contains(r, "push-in-commit") {
		t.Fatalf("reason = %q", r)
	}
	if ts, _ := l["ts"].(string); ts == "" {
		t.Fatalf("missing ts: %v", l)
	}
	runs, _ := l["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("runs = %v", l["runs"])
	}
	r := runs[0].(map[string]any)
	if r["run_id"] != runID || r["cursor_step"] != "fix" || r["cursor_kind"] != "agentic" || r["driver_match"] != true || r["blocked"] != false {
		t.Fatalf("run summary = %v", r)
	}
	if strings.Contains(fmt.Sprint(l), cmd) {
		t.Fatalf("the command text must never be logged: %v", l)
	}
}

func TestHookLog_StopBlockAndMarkerExcuse(t *testing.T) {
	root, _ := startDrivenHookRun(t)
	dir := hookLogDir(t)

	hookCall(t, "subagent-stop", subagentStopPayload(root, "s1", "a1"))
	// The first Stop is excused by the marker; the second finds it spent.
	hookCall(t, "stop", raceStopPayload(root, "s1"))
	hookCall(t, "stop", raceStopPayload(root, "s1"))
	lines := readHookLog(t, dir, "s1")
	if len(lines) != 3 {
		t.Fatalf("lines = %v", lines)
	}
	sub, excused, blocked := lines[0], lines[1], lines[2]
	if sub["event"] != "subagent-stop" || sub["marker_written"] != true || sub["decision"] != "none" || sub["agent_id"] != "a1" {
		t.Fatalf("subagent-stop line = %v", sub)
	}
	if excused["event"] != "stop" || excused["decision"] != "allow" || excused["stop_hook_active"] != false {
		t.Fatalf("excused line = %v", excused)
	}
	if m, _ := excused["markers"].([]any); len(m) != 1 || m[0].(map[string]any)["id"] != "a1" {
		t.Fatalf("markers = %v", excused["markers"])
	}
	if c, _ := excused["consumed"].([]any); len(c) != 1 || c[0] != "a1" {
		t.Fatalf("consumed = %v", excused["consumed"])
	}
	if blocked["decision"] != "block" || blocked["background_tasks"] != float64(0) {
		t.Fatalf("blocked line = %v", blocked)
	}
	if r, _ := blocked["reason"].(string); !strings.Contains(r, "awaiting `pawl submit`") {
		t.Fatalf("reason = %q", r)
	}
	if _, ok := blocked["markers"]; ok {
		t.Fatalf("no markers were left: %v", blocked)
	}
}

func TestHookLog_DecisionIdenticalOnAndOff(t *testing.T) {
	root, _ := startDrivenHookRun(t)
	cases := map[string]struct{ event, payload string }{
		"pre deny":      {"pre", prePayload(root, "s1", "git push", "")},
		"pre allow":     {"pre", prePayload(root, "s1", "ls", "")},
		"pre pawl":      {"pre", prePayload(root, "s1", "pawl status", "")},
		"stop block":    {"stop", raceStopPayload(root, "s1")},
		"stop active":   {"stop", stopPayload(root, "s1", true)},
		"stop other":    {"stop", stopPayload(root, "s2", false)},
		"garbage stop":  {"stop", "not json"},
		"garbage pre":   {"pre", "not json"},
		"subagent-stop": {"subagent-stop", "not json"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PAWL_HOOK_LOG", "")
			offCode, offOut, offErr := hookCall(t, c.event, c.payload)
			hookLogDir(t)
			onCode, onOut, onErr := hookCall(t, c.event, c.payload)
			if offCode != onCode || offOut != onOut || offErr != onErr {
				t.Fatalf("logging changed the call:\n off: %d %q %q\n on:  %d %q %q", offCode, offOut, offErr, onCode, onOut, onErr)
			}
		})
	}
}

func TestHookLog_NeverCapturesForbiddenContent(t *testing.T) {
	root, _ := startDrivenHookRun(t)
	dir := hookLogDir(t)
	pre := fmt.Sprintf(`{"session_id":"s1","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash",`+
		`"tool_input":{"command":"echo CMD_SENTINEL"},"last_assistant_message":"LAST_MSG_SENTINEL",`+
		`"transcript_path":"/x/TRANSCRIPT_SENTINEL.jsonl","prompt":"PROMPT_SENTINEL"}`, root)
	stop := fmt.Sprintf(`{"session_id":"s1","cwd":%q,"hook_event_name":"Stop","stop_hook_active":false,`+
		`"last_assistant_message":"LAST_MSG_SENTINEL","transcript_path":"/x/TRANSCRIPT_SENTINEL.jsonl",`+
		`"background_tasks":[{"id":"t1","type":"subagent","status":"running","description":"DESC_SENTINEL","command":"CMD_SENTINEL"}]}`, root)
	hookCall(t, "pre", pre)
	hookCall(t, "stop", stop)
	data, err := os.ReadFile(filepath.Join(dir, "s1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"CMD_SENTINEL", "LAST_MSG_SENTINEL", "TRANSCRIPT_SENTINEL", "PROMPT_SENTINEL", "DESC_SENTINEL"} {
		if strings.Contains(string(data), s) {
			t.Errorf("the log leaks %q: %s", s, data)
		}
	}
	if !strings.Contains(string(data), `"background_tasks":1`) {
		t.Errorf("the task count must still be logged: %s", data)
	}
}

func TestHookLog_Permissions(t *testing.T) {
	root, _ := startDrivenHookRun(t)
	dir := hookLogDir(t)
	hookCall(t, "pre", prePayload(root, "s1", "ls", ""))
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "s1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("dir %v, file %v; want 0700 and 0600", di.Mode().Perm(), fi.Mode().Perm())
	}
}

func TestHookLog_BadSessionIDLogsNothing(t *testing.T) {
	root, _ := startDrivenHookRun(t)
	dir := hookLogDir(t)
	for _, sid := range []string{"../evil", "a/b", ""} {
		hookCall(t, "pre", prePayload(root, sid, "ls", ""))
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) != 0 {
		t.Fatalf("an unsafe session id must write nothing: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("path traversal: %v", err)
	}
}

func TestHookLog_RelativePathIsRefusedWithOneLine(t *testing.T) {
	root, _ := startDrivenHookRun(t)
	t.Setenv("PAWL_HOOK_LOG", "relative/dir")
	cwd, _ := os.Getwd()
	code, out, errs := hookCall(t, "stop", raceStopPayload(root, "s1"))
	if _, ok := stopBlockReason(t, code, out); !ok {
		t.Fatalf("the decision must not change: code=%d out=%q", code, out)
	}
	if n := strings.Count(strings.TrimSuffix(errs, "\n"), "\n"); !strings.Contains(errs, "PAWL_HOOK_LOG") || n != 0 {
		t.Fatalf("want exactly one stderr line naming PAWL_HOOK_LOG, got %q", errs)
	}
	if _, err := os.Stat(filepath.Join(cwd, "relative")); !os.IsNotExist(err) {
		t.Fatalf("a relative path must create nothing: %v", err)
	}
}

func TestHookLog_UnwritableLocationFailsOpenWithSameDecision(t *testing.T) {
	root, _ := startDrivenHookRun(t)
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAWL_HOOK_LOG", file) // a file where a directory is needed
	code, out, errs := hookCall(t, "pre", prePayload(root, "s1", "git push", ""))
	if _, ok := preDenyReason(t, code, out); !ok {
		t.Fatalf("a deny must stay a deny: code=%d out=%q", code, out)
	}
	if strings.Count(strings.TrimSuffix(errs, "\n"), "\n") != 0 {
		t.Fatalf("at most one stderr line, got %q", errs)
	}
	code, out, _ = hookCall(t, "stop", raceStopPayload(root, "s1"))
	if _, ok := stopBlockReason(t, code, out); !ok {
		t.Fatalf("a block must stay a block: code=%d out=%q", code, out)
	}
}

func TestHookLog_PanicInLoggerKeepsDecision(t *testing.T) {
	root, _ := startDrivenHookRun(t)
	hookLogDir(t)
	old := hookLogAppend
	hookLogAppend = func(string, string, []byte) error { panic("boom") }
	t.Cleanup(func() { hookLogAppend = old })
	code, out, _ := hookCall(t, "pre", prePayload(root, "s1", "git push", ""))
	if _, ok := preDenyReason(t, code, out); !ok {
		t.Fatalf("a panicking logger must not change the deny: code=%d out=%q", code, out)
	}
	code, out, _ = hookCall(t, "stop", raceStopPayload(root, "s1"))
	if _, ok := stopBlockReason(t, code, out); !ok {
		t.Fatalf("a panicking logger must not change the block: code=%d out=%q", code, out)
	}
}

func TestHookLog_RotatesAtOneMiB(t *testing.T) {
	root, _ := startDrivenHookRun(t)
	dir := hookLogDir(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("x"), 1<<20)
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl.1"), []byte("older"), 0o600); err != nil {
		t.Fatal(err)
	}
	hookCall(t, "pre", prePayload(root, "s1", "ls", ""))
	old, err := os.ReadFile(filepath.Join(dir, "s1.jsonl.1"))
	if err != nil || len(old) != 1<<20 {
		t.Fatalf("the full file must replace the older .1: len=%d err=%v", len(old), err)
	}
	if lines := readHookLog(t, dir, "s1"); len(lines) != 1 {
		t.Fatalf("the new file starts with the new line: %v", lines)
	}
}

func TestHookLog_LineIsCappedAndStaysValidJSON(t *testing.T) {
	root, _ := startDrivenHookRun(t)
	dir := hookLogDir(t)
	var tasks []string
	for i := 0; i < 40; i++ {
		tasks = append(tasks, fmt.Sprintf(`{"id":%q,"type":"subagent","status":"running"}`, strings.Repeat("i", 200)))
	}
	payload := fmt.Sprintf(`{"session_id":"s1","cwd":%q,"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[%s]}`,
		root, strings.Join(tasks, ","))
	hookCall(t, "stop", payload)
	data, err := os.ReadFile(filepath.Join(dir, "s1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 4096+1 {
		t.Fatalf("line is %d bytes, cap is 4096", len(data))
	}
	var l map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &l); err != nil {
		t.Fatalf("a capped line must still be whole JSON: %v", err)
	}
	if l["background_tasks"] != float64(40) || l["decision"] == nil {
		t.Fatalf("the count and decision survive the cap: %v", l)
	}
	if _, ok := l["background_task_entries"]; ok {
		t.Fatalf("optional fields are dropped whole: %v", l)
	}
}

// The recorded Summary must flatten into the line: payload_keys is the field
// a future capture uses to spot a payload field pawl does not model.
func TestHookLog_RecordsPayloadKeys(t *testing.T) {
	root, _ := startDrivenHookRun(t)
	dir := hookLogDir(t)
	hookCall(t, "stop", fmt.Sprintf(`{"session_id":"s1","cwd":%q,"hook_event_name":"Stop","brand_new_field":1}`, root))
	l := readHookLog(t, dir, "s1")[0]
	keys, _ := l["payload_keys"].([]any)
	found := false
	for _, k := range keys {
		found = found || k == "brand_new_field"
	}
	if !found {
		t.Fatalf("payload_keys = %v", l["payload_keys"])
	}
}
