package journal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// eventsFileName is events.jsonl's file name within a run directory.
const eventsFileName = "events.jsonl"

// Log is an open handle on a run's events.jsonl, appending new events and
// assigning them consecutive sequence numbers. It is not safe for concurrent
// use by multiple goroutines; the run lock is what serialises access across
// processes.
type Log struct {
	dir  string
	file *os.File
	seq  int
}

// OpenLog opens (creating if absent) dir's events.jsonl for appending. It
// first reads whatever events already exist, both so new appends continue
// the sequence rather than restart it, and to recover from a torn tail — a
// last line left partially written by a crash between Write and a complete
// fsync — by truncating it off the file before appending anything new (see
// ParseEvents).
func OpenLog(dir string) (*Log, error) {
	path := filepath.Join(dir, eventsFileName)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("journal: reading events.jsonl: %w", err)
	}

	events, validLen, err := parseJournal(data)
	if err != nil {
		return nil, err
	}
	if validLen < len(data) {
		if err := os.Truncate(path, int64(validLen)); err != nil {
			return nil, fmt.Errorf("journal: truncating torn tail of events.jsonl: %w", err)
		}
	}

	seq := 0
	for _, e := range events {
		if e.Seq >= seq {
			seq = e.Seq + 1
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("journal: opening events.jsonl: %w", err)
	}
	return &Log{dir: dir, file: f, seq: seq}, nil
}

// Append assigns e the next sequence number (and, if unset, the current
// time), marshals it with encoding/json, writes it as one line and fsyncs
// the file before returning: the caller may treat the event as durable only
// once Append returns a nil error, and must not treat it as durable before
// that (DESIGN.md §4).
//
// The sequence number is reserved before the write is attempted, so that a
// partial-write failure (which may still have put a torn record on disk)
// never causes the next successful Append to reuse the same Seq — Replay
// treats a repeated Seq as corruption.
func (l *Log) Append(e Event) (Event, error) {
	e.Seq = l.seq
	l.seq++
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	data, err := json.Marshal(e)
	if err != nil {
		return Event{}, fmt.Errorf("journal: marshalling event: %w", err)
	}
	data = append(data, '\n')
	if _, err := l.file.Write(data); err != nil {
		return Event{}, fmt.Errorf("journal: appending event: %w", err)
	}
	if err := l.file.Sync(); err != nil {
		return Event{}, fmt.Errorf("journal: fsyncing events.jsonl: %w", err)
	}
	return e, nil
}

// Close closes the underlying file.
func (l *Log) Close() error {
	return l.file.Close()
}

// ReadEvents reads and parses every record in dir's events.jsonl, in file
// order. A dir with no events.jsonl yet returns (nil, nil). A torn tail (see
// ParseEvents) is silently dropped, exactly as OpenLog would drop it, but
// ReadEvents does not truncate the file — only OpenLog does, since only a
// caller about to append is entitled to modify it.
func ReadEvents(dir string) ([]Event, error) {
	data, err := os.ReadFile(filepath.Join(dir, eventsFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("journal: reading events.jsonl: %w", err)
	}
	return ParseEvents(data)
}

// ParseEvents parses a raw events.jsonl byte stream (one JSON object per
// non-empty, newline-terminated line) into Events, in order. It is exposed
// directly so the property test can construct a journal in memory without
// going through the filesystem.
//
// Any trailing data that is not itself newline-terminated is a torn write —
// a crash between Write and a complete fsync of the final record — and is
// silently dropped rather than failing the whole file, whatever it contains.
// This is deliberately unconditional: a completed Append writes its JSON and
// its trailing '\n' in the one Write call, so an unterminated remainder is
// never a legitimate complete record, even when it happens to parse as valid
// JSON (round 2, N1 — the commonest torn-write shape of all is a write that
// lost only its final byte, which parses fine and must still be dropped, or
// the next Append silently concatenates onto it and the file becomes
// permanently unreadable). A line that fails to parse *despite* being
// newline-terminated is real corruption, never torn-write recovery's to fix,
// and remains a hard error naming its byte offset.
func ParseEvents(data []byte) ([]Event, error) {
	events, _, err := parseJournal(data)
	return events, err
}

// parseJournal is ParseEvents' implementation, additionally reporting
// validLen: the byte length of data up to and including the last
// newline-terminated record Replay should trust — i.e. data with any dropped
// torn tail excluded. A caller that owns the file (OpenLog) truncates to
// validLen to purge that tail so future appends start a fresh line rather
// than concatenating onto it.
func parseJournal(data []byte) (events []Event, validLen int, err error) {
	pos := 0
	for pos < len(data) {
		idx := bytes.IndexByte(data[pos:], '\n')
		if idx < 0 {
			break // unterminated remainder: torn, dropped, regardless of content
		}
		next := pos + idx + 1

		trimmed := bytes.TrimSpace(data[pos : pos+idx])
		if len(trimmed) == 0 {
			pos = next
			validLen = pos
			continue
		}

		var e Event
		if uerr := json.Unmarshal(trimmed, &e); uerr != nil {
			return nil, 0, fmt.Errorf("journal: events.jsonl: corrupt record at byte %d: %w", pos, uerr)
		}
		events = append(events, e)
		pos = next
		validLen = pos
	}
	return events, validLen, nil
}
