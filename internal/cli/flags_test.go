package cli

import (
	"bytes"
	"strings"
	"testing"
)

var testCmd = &command{
	name:   "demo",
	usage:  "Usage:\n  pawl demo [--x] [--run <id>] [--reason <t>]\n",
	maxPos: 2,
	flags: []flagDef{
		{name: "x", isBool: true},
		{name: "run"},
		{name: "reason", allowEmpty: true},
	},
}

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string // substring; "" = no error
		help    bool
		check   func(t *testing.T, p *parsedFlags)
	}{
		{name: "space form", args: []string{"--run", "a"}, check: func(t *testing.T, p *parsedFlags) {
			if p.value("run") != "a" || !p.has("run") {
				t.Errorf("run = %q", p.value("run"))
			}
		}},
		{name: "equals form", args: []string{"--run=a=b"}, check: func(t *testing.T, p *parsedFlags) {
			if p.value("run") != "a=b" {
				t.Errorf("run = %q", p.value("run"))
			}
		}},
		{name: "bool", args: []string{"--x"}, check: func(t *testing.T, p *parsedFlags) {
			if !p.boolean("x") || p.boolean("run") {
				t.Errorf("bool wrong")
			}
		}},
		{name: "empty value equals form", args: []string{"--run="}, wantErr: "--run needs a non-empty value"},
		{name: "empty value space form", args: []string{"--run", ""}, wantErr: "--run needs a non-empty value"},
		{name: "empty allowed", args: []string{"--reason", ""}, check: func(t *testing.T, p *parsedFlags) {
			if !p.has("reason") || p.value("reason") != "" {
				t.Errorf("reason not recorded")
			}
		}},
		{name: "empty allowed equals", args: []string{"--reason="}, check: func(t *testing.T, p *parsedFlags) {
			if !p.has("reason") {
				t.Errorf("reason not recorded")
			}
		}},
		{name: "missing value", args: []string{"--run"}, wantErr: "--run needs a value"},
		{name: "repeat", args: []string{"--run", "a", "--run=b"}, wantErr: "--run given more than once"},
		{name: "repeat bool", args: []string{"--x", "--x"}, wantErr: "--x given more than once"},
		{name: "unknown flag", args: []string{"--nope"}, wantErr: `unrecognised argument "--nope"`},
		{name: "single dash", args: []string{"-r", "a"}, wantErr: `unrecognised argument "-r"`},
		{name: "bool with equals", args: []string{"--x=true"}, wantErr: "--x does not take a value"},
		{name: "help anywhere", args: []string{"--nope", "--x=1", "-h"}, help: true},
		{name: "long help", args: []string{"--run", "a", "--help"}, help: true},
		{name: "help as a value is a value", args: []string{"--run", "-h"}, check: func(t *testing.T, p *parsedFlags) {
			if p.value("run") != "-h" {
				t.Errorf("run = %q", p.value("run"))
			}
		}},
		{name: "positionals", args: []string{"a", "--x", "b"}, check: func(t *testing.T, p *parsedFlags) {
			if len(p.pos) != 2 || p.pos[0] != "a" || p.pos[1] != "b" {
				t.Errorf("pos = %v", p.pos)
			}
		}},
		{name: "too many positionals", args: []string{"a", "b", "c"}, wantErr: `unrecognised argument "c"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := testCmd.parse(tt.args)
			if tt.help {
				if err != errHelp {
					t.Fatalf("err = %v, want errHelp", err)
				}
				return
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if tt.check != nil {
				tt.check(t, p)
			}
		})
	}
}

func TestParseFlags_NoPositionals(t *testing.T) {
	c := &command{name: "z", maxPos: 0}
	if _, err := c.parse([]string{"a"}); err == nil || !strings.Contains(err.Error(), `unrecognised argument "a"`) {
		t.Errorf("err = %v", err)
	}
}

func TestCommandRunArgs_UsageErrorShape(t *testing.T) {
	var out, errb bytes.Buffer
	_, code, done := testCmd.parseOrExit([]string{"--run", "a", "--run", "b"}, &out, &errb)
	if !done || code != exitUsage || out.Len() != 0 {
		t.Fatalf("done=%v code=%d out=%q", done, code, out.String())
	}
	if !strings.HasPrefix(errb.String(), "pawl demo: --run given more than once\n") || !strings.Contains(errb.String(), "Usage:") {
		t.Errorf("stderr = %q", errb.String())
	}
	out.Reset()
	errb.Reset()
	_, code, done = testCmd.parseOrExit([]string{"--bogus", "-h"}, &out, &errb)
	if !done || code != exitOK || errb.Len() != 0 || out.String() != testCmd.usage {
		t.Fatalf("help: done=%v code=%d out=%q err=%q", done, code, out.String(), errb.String())
	}
}
