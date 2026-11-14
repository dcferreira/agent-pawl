package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/dcferreira/agent-pawl/internal/hook"
	"github.com/dcferreira/agent-pawl/internal/journal"
)

// The opt-in hook payload capture: with PAWL_HOOK_LOG=<absolute dir> set,
// `pawl hook pre|stop|subagent-stop` appends one JSON line per call to
// <dir>/<session_id>.jsonl, so an unexpected Stop block or deny can be
// diagnosed from the payload Claude Code actually sent. It is a diagnostic:
// the variable and the line shape are not frozen (docs/compatibility.md).
//
// What is written is fixed by hook.Summary (pure, allowlisted) plus pawl's own
// view of the call. A command is never written, nor tool_input,
// last_assistant_message, transcript_path or a prompt. The capture can never
// change a hook call: hookLog runs after the decision is made, swallows every
// error and recovers from a panic.
const (
	envHookLog      = "PAWL_HOOK_LOG"
	hookLogMaxLine  = 4096    // bytes per line, newline included
	hookLogRotateAt = 1 << 20 // bytes; one older generation (.1) is kept
)

// hookLogAppend writes one line to the session's log file; tests replace it.
var hookLogAppend = appendHookLog

type hookLogRun struct {
	RunID       string `json:"run_id"`
	Workflow    string `json:"workflow"`
	CursorStep  string `json:"cursor_step"`
	CursorKind  string `json:"cursor_kind"`
	Blocked     bool   `json:"blocked"`
	Opaque      bool   `json:"opaque,omitempty"`
	DriverMatch bool   `json:"driver_match"`
}

type hookLogMarker struct {
	ID    string `json:"id"`
	AgeMS int64  `json:"age_ms"`
}

// hookLogRecord is one log line. The embedded hook.Summary is flattened into
// it; every field below it is pawl's own side of the call.
type hookLogRecord struct {
	TS        string `json:"ts"`
	Event     string `json:"event"`
	SessionID string `json:"session_id"`
	hook.Summary
	Runs          []hookLogRun    `json:"runs"`
	Markers       []hookLogMarker `json:"markers,omitempty"`
	Consumed      []string        `json:"consumed,omitempty"`
	MarkerWritten bool            `json:"marker_written,omitempty"`
	Decision      string          `json:"decision"`
	Reason        string          `json:"reason,omitempty"`
	GuardID       string          `json:"guard_id"`
}

// hookLog records one hook call when PAWL_HOOK_LOG is set. data is the raw
// payload, p its parsed form, markers the SubagentStop markers the Stop
// decision was given, markerWritten whether a subagent-stop recorded one, and
// d the decision about to be written.
func hookLog(stderr io.Writer, event string, data []byte, p hook.Payload, runs []hook.LiveRun, markers []hook.StopMarker, markerWritten bool, d hook.Decision) {
	dir := os.Getenv(envHookLog)
	if dir == "" {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			printLine(stderr, "pawl hook "+event+":", "payload capture:", fmt.Sprintf("recovered from a panic: %v", r))
		}
	}()
	if !filepath.IsAbs(dir) {
		printLine(stderr, "pawl hook "+event+":", envHookLog, "must be an absolute directory; not capturing")
		return
	}
	if !journal.SafeFileName(p.SessionID) {
		return
	}
	now := nowFunc()
	rec := hookLogRecord{
		TS:            now.UTC().Format(time.RFC3339Nano),
		Event:         event,
		SessionID:     p.SessionID,
		Summary:       hook.Summarize(data),
		Runs:          []hookLogRun{},
		MarkerWritten: markerWritten,
		GuardID:       d.GuardID,
	}
	switch {
	case event == "subagent-stop":
		rec.Decision = "none" // subagent-stop never decides
	case d.Allow:
		rec.Decision = "allow"
	case event == "pre":
		rec.Decision = "deny"
	default:
		rec.Decision = "block"
	}
	if !d.Allow {
		rec.Reason = d.Reason
	}
	for _, r := range runs {
		rec.Runs = append(rec.Runs, hookLogRun{
			RunID: r.RunID, Workflow: r.WorkflowID, CursorStep: r.CursorStep, CursorKind: r.CursorKind,
			Blocked: r.Blocked, Opaque: r.Opaque, DriverMatch: r.DriverSession != "" && r.DriverSession == p.SessionID,
		})
	}
	for _, m := range markers {
		rec.Markers = append(rec.Markers, hookLogMarker{ID: m.ID, AgeMS: now.Sub(m.Time).Milliseconds()})
	}
	for _, c := range d.Consumed {
		rec.Consumed = append(rec.Consumed, c.ID)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		printLine(stderr, "pawl hook "+event+":", "payload capture:", err.Error())
		return
	}
	// A line over the cap loses optional fields whole, least useful first, so
	// what is written is always complete JSON; the counts and the decision stay.
	for _, drop := range []func(){
		func() { rec.Tasks = nil },
		func() { rec.Markers = nil },
		func() { rec.Runs = []hookLogRun{} },
		func() { rec.PayloadKeys = nil },
		func() { rec.Reason = "" },
		func() { rec.Cwd = "" },
	} {
		if len(line) < hookLogMaxLine {
			break
		}
		drop()
		if line, err = json.Marshal(rec); err != nil {
			printLine(stderr, "pawl hook "+event+":", "payload capture:", err.Error())
			return
		}
	}
	if len(line) >= hookLogMaxLine {
		printLine(stderr, "pawl hook "+event+":", "payload capture: line over 4096 bytes even without its optional fields; not written")
		return
	}
	if err := hookLogAppend(dir, p.SessionID, line); err != nil {
		printLine(stderr, "pawl hook "+event+":", "payload capture:", err.Error())
	}
}

// appendHookLog appends line and a newline to dir/<session>.jsonl. The
// directory is created 0700 and the file 0600; a file at hookLogRotateAt bytes
// is first renamed to <session>.jsonl.1, replacing an older one, so a session
// holds at most about twice that.
func appendHookLog(dir, session string, line []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, session+".jsonl")
	if fi, err := os.Lstat(path); err == nil {
		// Never open a FIFO or device named like a log file: it could block the hook.
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		// Two calls racing past the cap can both rename; the second then moves a
		// fresh small file over .1. Acceptable for a diagnostic.
		if fi.Size() >= hookLogRotateAt {
			if err := os.Rename(path, path+".1"); err != nil {
				return err
			}
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
