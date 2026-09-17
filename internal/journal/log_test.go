package journal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLog_AppendAndReadEvents(t *testing.T) {
	dir := t.TempDir()

	l, err := OpenLog(dir)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}

	e1, err := l.Append(Event{Kind: KindRunStart, RunID: "r1", Args: map[string]any{"a": 1.0}})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if e1.Seq != 0 {
		t.Errorf("first event Seq = %d, want 0", e1.Seq)
	}
	if e1.Time.IsZero() {
		t.Error("Append did not stamp Time")
	}

	e2, err := l.Append(Event{Kind: KindStepEnter, RunID: "r1", Step: "s1", Attempt: 1})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if e2.Seq != 1 {
		t.Errorf("second event Seq = %d, want 1", e2.Seq)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events, err := ReadEvents(dir)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("ReadEvents returned %d events, want 2", len(events))
	}
	if events[0].Kind != KindRunStart || events[1].Kind != KindStepEnter {
		t.Errorf("ReadEvents order/kinds wrong: %+v", events)
	}

	// Re-opening continues the sequence rather than restarting it.
	l2, err := OpenLog(dir)
	if err != nil {
		t.Fatalf("OpenLog (reopen): %v", err)
	}
	defer l2.Close()
	e3, err := l2.Append(Event{Kind: KindRunEnd, RunID: "r1", Status: "ok"})
	if err != nil {
		t.Fatalf("Append after reopen: %v", err)
	}
	if e3.Seq != 2 {
		t.Errorf("third event Seq = %d, want 2 (continuing the sequence across opens)", e3.Seq)
	}
}

func TestLog_OneRecordPerLine_IsGrepable(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLog(dir)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	// A control byte arriving through captured stdout must never break the
	// one-record-per-line invariant: the caller is expected to have already
	// run it through emit.EscapeC0, but Text/Writes containing an
	// unescaped C0 byte must still not produce a newline inside the record,
	// because encoding/json escapes control characters in string values (and
	// map keys) regardless.
	if _, err := l.Append(Event{Kind: KindPostcondition, RunID: "r1", Step: "s", Text: "line1\nline2\x00tail"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("events.jsonl has %d lines, want 1 (embedded control bytes must not create new lines): %q", len(lines), string(data))
	}

	events, err := ReadEvents(dir)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if events[0].Text != "line1\nline2\x00tail" {
		t.Errorf("round-tripped Text = %q, want original preserved", events[0].Text)
	}
}

func TestReadEvents_MissingFile(t *testing.T) {
	dir := t.TempDir()
	events, err := ReadEvents(dir)
	if err != nil {
		t.Fatalf("ReadEvents on a dir with no events.jsonl: %v", err)
	}
	if events != nil {
		t.Errorf("ReadEvents = %v, want nil", events)
	}
}

func TestReadEvents_SkipsBlankLines(t *testing.T) {
	dir := t.TempDir()
	content := "{\"kind\":\"RUN_START\",\"run_id\":\"r1\",\"seq\":0,\"time\":\"2026-01-01T00:00:00Z\"}\n\n{\"kind\":\"RUN_END\",\"run_id\":\"r1\",\"seq\":1,\"time\":\"2026-01-01T00:00:01Z\",\"status\":\"ok\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	events, err := ReadEvents(dir)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("ReadEvents = %d events, want 2", len(events))
	}
}

func TestReadEvents_CorruptLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEvents(dir); err == nil {
		t.Fatal("ReadEvents: want error on a corrupt line")
	}
}

// TestReadEvents_TornTail is C3: a crash between Write and a complete fsync
// can leave the file's last line partially written. That must not make the
// whole run permanently unresumable — it must be dropped, silently, exactly
// like the record was never appended (which, from the caller's point of
// view, it wasn't: Append had not yet returned success for it).
func TestReadEvents_TornTail(t *testing.T) {
	good := `{"kind":"RUN_START","run_id":"r1","seq":0,"time":"2026-01-01T00:00:00Z"}` + "\n"
	torn := `{"kind":"STEP_ENTER","run_id":"r1","seq":1,"step":"s1"` // no closing brace, no trailing newline
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(good+torn), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := ReadEvents(dir)
	if err != nil {
		t.Fatalf("ReadEvents: want a torn tail dropped silently, got error: %v", err)
	}
	if len(events) != 1 || events[0].Kind != KindRunStart {
		t.Fatalf("ReadEvents = %+v, want just the one good record", events)
	}
}

// TestReadEvents_TornTail_ValidJSONWithoutNewline is round-2 finding N1: the
// commonest torn-write shape of all is a write that lost only its trailing
// newline byte — the JSON itself parses fine. This must still be dropped
// like any other torn tail: a complete Append writes its JSON and its '\n'
// in one Write call, so an unterminated line was never a committed record,
// whether or not it happens to parse. Getting this wrong recreates exactly
// the wedge C3 was raised to close, from the other side: OpenLog would not
// truncate it, and the next Append would concatenate onto it, corrupting the
// file permanently.
func TestReadEvents_TornTail_ValidJSONWithoutNewline(t *testing.T) {
	good := `{"kind":"RUN_START","run_id":"r1","seq":0,"time":"2026-01-01T00:00:00Z"}` + "\n"
	validButUnterminated := `{"kind":"STEP_ENTER","run_id":"r1","seq":1,"step":"s1","attempt":1}` // valid JSON, no trailing \n
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(good+validButUnterminated), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := ReadEvents(dir)
	if err != nil {
		t.Fatalf("ReadEvents: want the unterminated (but valid) line dropped silently, got error: %v", err)
	}
	if len(events) != 1 || events[0].Kind != KindRunStart {
		t.Fatalf("ReadEvents = %+v, want just the one newline-terminated record", events)
	}
}

// TestOpenLog_TruncatesValidButUnterminatedTail is N1's end-to-end path: a
// valid-JSON-but-unterminated tail must be truncated by OpenLog exactly like
// a garbled one, so the next Append starts a fresh line instead of
// concatenating onto it.
func TestOpenLog_TruncatesValidButUnterminatedTail(t *testing.T) {
	good := `{"kind":"RUN_START","run_id":"r1","seq":0,"time":"2026-01-01T00:00:00Z"}` + "\n"
	validButUnterminated := `{"kind":"STEP_ENTER","run_id":"r1","seq":1,"step":"s1","attempt":1}`
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, []byte(good+validButUnterminated), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := OpenLog(dir)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	e, err := l.Append(Event{Kind: KindWrites, RunID: "r1", Step: "s1", Attempt: 1})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if e.Seq != 1 {
		t.Errorf("Append after recovering a valid-but-unterminated tail: Seq = %d, want 1 (the dropped tail must not have counted)", e.Seq)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	events, err := ReadEvents(dir)
	if err != nil {
		t.Fatalf("ReadEvents after recovery: %v", err)
	}
	if len(events) != 2 || events[1].Kind != KindWrites {
		t.Fatalf("ReadEvents after recovery = %+v, want [RUN_START, WRITES] with no corruption", events)
	}
}

// TestReadEvents_CorruptMidFile is C3's other half: a corrupt line that is
// NOT the unterminated tail (either it has a trailing newline, or later
// lines follow it) is real corruption, not a torn write, and must remain a
// hard error.
func TestReadEvents_CorruptMidFile(t *testing.T) {
	content := `{"kind":"RUN_START","run_id":"r1","seq":0,"time":"2026-01-01T00:00:00Z"}` + "\n" +
		"not json\n" +
		`{"kind":"RUN_END","run_id":"r1","seq":1,"time":"2026-01-01T00:00:01Z","status":"ok"}` + "\n"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEvents(dir); err == nil {
		t.Fatal("ReadEvents: want a hard error for corruption that is not the unterminated tail")
	}
}

// TestOpenLog_TruncatesTornTailAndContinuesAppending is C3's end-to-end
// path: OpenLog must not merely tolerate a torn tail when reading, it must
// truncate it off the file so a subsequent Append starts a fresh line
// rather than concatenating onto garbage bytes.
func TestOpenLog_TruncatesTornTailAndContinuesAppending(t *testing.T) {
	good := `{"kind":"RUN_START","run_id":"r1","seq":0,"time":"2026-01-01T00:00:00Z"}` + "\n"
	torn := `{"kind":"STEP_ENTER","run_id":"r1","seq":1,"step":"s1"`
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, []byte(good+torn), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := OpenLog(dir)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	// The next assigned Seq must continue from the one good record (seq 0),
	// not from the dropped torn one — it never counted.
	e, err := l.Append(Event{Kind: KindStepEnter, RunID: "r1", Step: "s1", Attempt: 1})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if e.Seq != 1 {
		t.Errorf("Append after a torn-tail recovery: Seq = %d, want 1", e.Seq)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("events.jsonl has %d lines after recovery, want 2 (torn tail purged, one clean append): %q", len(lines), string(data))
	}

	events, err := ReadEvents(dir)
	if err != nil {
		t.Fatalf("ReadEvents after recovery: %v", err)
	}
	if len(events) != 2 || events[1].Attempt != 1 {
		t.Fatalf("ReadEvents after recovery = %+v", events)
	}
}
