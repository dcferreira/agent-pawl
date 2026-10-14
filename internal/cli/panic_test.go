package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunIO_PanicInCommandExits5(t *testing.T) {
	commandHook = func(name string) {
		if name == "list" {
			panic("boom")
		}
	}
	t.Cleanup(func() { commandHook = nil })

	var out, errb bytes.Buffer
	code := RunIO([]string{"pawl", "list"}, strings.NewReader(""), &out, &errb)
	if code != 5 {
		t.Fatalf("exit = %d, want 5; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "pawl: internal error: boom") {
		t.Errorf("stderr missing internal error line: %q", errb.String())
	}
	if !strings.Contains(errb.String(), "goroutine") {
		t.Errorf("stderr missing stack trace: %q", errb.String())
	}
}

func TestRunIO_HookPanicStillFailsOpen(t *testing.T) {
	commandHook = func(name string) {
		if name == "hook" {
			panic("hook boom")
		}
	}
	t.Cleanup(func() { commandHook = nil })

	for _, tc := range []struct {
		event string
		want  int
	}{{"pre", 1}, {"stop", 0}, {"subagent-stop", 0}} {
		var out, errb bytes.Buffer
		code := RunIO([]string{"pawl", "hook", tc.event}, strings.NewReader("{}"), &out, &errb)
		if code != tc.want {
			t.Errorf("hook %s: exit = %d, want %d (fail open); stderr=%q", tc.event, code, tc.want, errb.String())
		}
		if strings.Contains(errb.String(), "internal error") {
			t.Errorf("hook %s: outer recover ran: %q", tc.event, errb.String())
		}
	}
}
