package journal

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

func TestNormalizeFormat(t *testing.T) {
	if got := NormalizeFormat(0); got != 1 {
		t.Errorf("NormalizeFormat(0) = %d, want 1", got)
	}
	if got := NormalizeFormat(2); got != 2 {
		t.Errorf("NormalizeFormat(2) = %d, want 2", got)
	}
}

func TestFormatError(t *testing.T) {
	var err error = &FormatError{Format: Format + 1}
	if !errors.Is(err, ErrNewerFormat) {
		t.Errorf("errors.Is(FormatError, ErrNewerFormat) = false")
	}
	want := "journal: written by a newer pawl (format 3; this pawl reads formats 1-2)"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	var fe *FormatError
	if !errors.As(err, &fe) || fe.Format != Format+1 {
		t.Errorf("errors.As failed: %v", fe)
	}
}

func TestReplay_NewerFormatRefusedImmediately(t *testing.T) {
	// A gapped seq after the RUN_START would be a different error; the
	// format check must fire first.
	events := []Event{
		{Kind: KindRunStart, Seq: 0, Format: Format + 1},
		{Kind: KindStepEnter, Seq: 7, Step: "a", Attempt: 1},
	}
	_, err := Replay(events)
	if !errors.Is(err, ErrNewerFormat) {
		t.Fatalf("Replay err = %v, want ErrNewerFormat", err)
	}
}

func TestReplay_CurrentAndLegacyFormatAccepted(t *testing.T) {
	for _, f := range []int{0, 1, Format} {
		events := []Event{{Kind: KindRunStart, Seq: 0, Format: f}}
		if _, err := Replay(events); err != nil {
			t.Errorf("format %d: Replay err = %v", f, err)
		}
	}
}

func TestEvent_FormatOmittedWhenZero(t *testing.T) {
	data, _ := json.Marshal(Event{Kind: KindStepEnter})
	if strings.Contains(string(data), "format") {
		t.Errorf("zero Format should be omitted: %s", data)
	}
	data, _ = json.Marshal(Event{Kind: KindRunStart, Format: Format})
	if !strings.Contains(string(data), `"format":2`) {
		t.Errorf("Format not serialised: %s", data)
	}
}

func TestWritePlan_StampsFormat(t *testing.T) {
	dir := t.TempDir()
	w := &spec.Workflow{Workflow: "x", Start: "done"}
	if _, err := WritePlan(dir, w); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "plan.json"))
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["format"] != float64(Format) {
		t.Errorf("plan.json format = %v, want %d", m["format"], Format)
	}
	p, err := ReadPlan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Format != Format {
		t.Errorf("Plan.Format = %d, want %d", p.Format, Format)
	}
}

func TestReadPlan_NewerFormatRefused(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "plan.json"), []byte(`{"format":99,"workflow":{"workflow":"x"},"digest":"d"}`), 0o644)
	_, err := ReadPlan(dir)
	if !errors.Is(err, ErrNewerFormat) {
		t.Fatalf("ReadPlan err = %v, want ErrNewerFormat", err)
	}
	var fe *FormatError
	if !errors.As(err, &fe) || fe.Format != 99 {
		t.Errorf("FormatError = %+v", fe)
	}
}
