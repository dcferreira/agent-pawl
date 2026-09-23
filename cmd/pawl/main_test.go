package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantExit      int
		wantStdout    string
		wantStderrHas string
	}{
		{
			name:       "version",
			args:       []string{"pawl", "version"},
			wantExit:   0,
			wantStdout: "pawl " + Version + "\n",
		},
		{
			name:          "no subcommand",
			args:          []string{"pawl"},
			wantExit:      2,
			wantStderrHas: "pawl run",
		},
		{
			name:          "unknown subcommand",
			args:          []string{"pawl", "bogus"},
			wantExit:      2,
			wantStderrHas: "pawl validate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			gotExit := run(tt.args, &stdout, &stderr)

			if gotExit != tt.wantExit {
				t.Errorf("exit = %d, want %d", gotExit, tt.wantExit)
			}
			if tt.wantStdout != "" && stdout.String() != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderrHas != "" && !strings.Contains(stderr.String(), tt.wantStderrHas) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tt.wantStderrHas)
			}
		})
	}
}

func TestUsageListsAllMilestone1Commands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	run([]string{"pawl"}, &stdout, &stderr)

	for _, cmd := range []string{"run", "validate", "status", "abandon", "list", "update"} {
		if !strings.Contains(stderr.String(), cmd) {
			t.Errorf("usage missing command %q; stderr = %q", cmd, stderr.String())
		}
	}
}
