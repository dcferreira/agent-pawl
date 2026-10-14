package journal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// OpaqueRun describes a run directory that has recorded events but whose
// journal this build cannot replay: written by a newer pawl, or corrupt. It
// is kept in the run listings (RunRef.Opaque, with a nil State) instead of
// being skipped, so the run stays visible to resume, the hooks, status and
// abandon rather than silently vanishing.
type OpaqueRun struct {
	// Format is the normalised format stamped on RUN_START, or 0 when it
	// could not be read (unknown).
	Format int
	// Err is the ReadEvents/Replay error. errors.Is(Err, ErrNewerFormat)
	// distinguishes the newer-format case.
	Err error
	// RunID is the run id found on the run's events (empty if none decoded).
	RunID string
	// LastSeq is the highest seq among the decodable events.
	LastSeq int
	// Ended reports that the last decodable event is a RUN_END whose status
	// is not "blocked" (a blocked run is paused, still live).
	Ended bool
}

// Newer reports whether the run is opaque because a newer pawl wrote it.
func (o *OpaqueRun) Newer() bool { return errors.Is(o.Err, ErrNewerFormat) }

// Terminal reports whether the run is over: for an opaque run, whether the
// lenient scan saw a final non-blocked RUN_END; otherwise State.Terminal().
func (r RunRef) Terminal() bool {
	if r.Opaque != nil {
		return r.Opaque.Ended
	}
	return r.State.Terminal()
}

// StatusText is the run's status as shown to a user: "newer-format" or
// "unreadable" for an opaque run, else State.Status().
func (r RunRef) StatusText() string {
	if r.Opaque != nil {
		if r.Opaque.Newer() {
			return "newer-format"
		}
		return "unreadable"
	}
	return r.State.Status()
}

// opaqueScan is the result of a lenient scan of events.jsonl.
type opaqueScan struct {
	RunID   string
	LastSeq int
	Format  int // normalised; 0 when no RUN_START decoded
	Ended   bool
	Events  int // number of decodable lines
}

// scanEvents reads dir's events.jsonl line by line, decoding only the fields
// every format shares and skipping lines that do not decode. An unterminated
// final line is a torn append and is ignored, as ParseEvents ignores it. The
// RUN_END shape (kind/run_id/seq/time/status/reason) is frozen across
// formats, which is what makes Ended trustworthy for a format this build
// cannot replay.
func scanEvents(dir string) (opaqueScan, error) {
	var s opaqueScan
	data, err := os.ReadFile(filepath.Join(dir, eventsFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, fmt.Errorf("journal: reading events.jsonl: %w", err)
	}
	if i := bytes.LastIndexByte(data, '\n'); i < 0 {
		return s, nil
	} else {
		data = data[:i+1]
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), len(data)+1)
	for sc.Scan() {
		var e struct {
			Kind   string `json:"kind"`
			RunID  string `json:"run_id"`
			Seq    int    `json:"seq"`
			Status string `json:"status"`
			Format int    `json:"format"`
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || json.Unmarshal(line, &e) != nil {
			continue
		}
		s.Events++
		if e.RunID != "" && s.RunID == "" {
			s.RunID = e.RunID
		}
		if e.Seq > s.LastSeq {
			s.LastSeq = e.Seq
		}
		if e.Kind == string(KindRunStart) && s.Format == 0 {
			s.Format = NormalizeFormat(e.Format)
		}
		s.Ended = e.Kind == string(KindRunEnd) && e.Status != "blocked"
	}
	return s, sc.Err()
}

// opaqueRef builds the OpaqueRun for a run directory whose replay failed with
// cause, or reports false when the directory holds no decodable event.
func opaqueRef(dir string, cause error) (*OpaqueRun, bool) {
	s, err := scanEvents(dir)
	if err != nil || s.Events == 0 {
		return nil, false
	}
	return &OpaqueRun{Format: s.Format, Err: cause, RunID: s.RunID, LastSeq: s.LastSeq, Ended: s.Ended}, true
}

// AppendRunEndRaw appends one RUN_END event to dir's events.jsonl without
// replaying the journal, so a run this build cannot read (see OpaqueRun) can
// still be ended. It uses Log.Append's durable write path (O_APPEND, one
// encoding/json line, fsync) and, like OpenLog, first truncates a torn
// (unterminated) tail so the record starts on a fresh line. The caller
// supplies seq (OpaqueRun.LastSeq+1).
func AppendRunEndRaw(dir, runID string, seq int, status, reason string) error {
	path := filepath.Join(dir, eventsFileName)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("journal: reading events.jsonl: %w", err)
	}
	if n := bytes.LastIndexByte(data, '\n') + 1; n < len(data) {
		if err := os.Truncate(path, int64(n)); err != nil {
			return fmt.Errorf("journal: truncating torn tail of events.jsonl: %w", err)
		}
	}
	line, err := json.Marshal(Event{Kind: KindRunEnd, RunID: runID, Seq: seq, Time: time.Now().UTC(), Status: status, Reason: reason})
	if err != nil {
		return fmt.Errorf("journal: marshalling event: %w", err)
	}
	line = append(line, '\n')
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("journal: opening events.jsonl: %w", err)
	}
	if _, err := f.Write(line); err != nil {
		f.Close()
		return fmt.Errorf("journal: appending event: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("journal: fsyncing events.jsonl: %w", err)
	}
	return f.Close()
}
