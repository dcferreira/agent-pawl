package render

import (
	"reflect"
	"strings"
	"testing"
)

func TestRenderShell(t *testing.T) {
	tests := []struct {
		name    string
		tmpl    string
		vals    Values
		want    string
		wantErr string
	}{
		{
			name: "plain substitution",
			tmpl: "echo ${msg}",
			vals: Values{"msg": StringValue("hello")},
			want: "echo 'hello'",
		},
		{
			name: "shell metacharacters come back inert",
			tmpl: "echo ${payload}",
			vals: Values{"payload": StringValue("'; rm -rf /")},
			want: `echo ''\''; rm -rf /'`,
		},
		{
			name: "literal dollar-brace escape consumes no key",
			tmpl: "echo $${not_a_key}",
			vals: Values{},
			want: "echo ${not_a_key}",
		},
		{
			name: "adjacent substitutions",
			tmpl: "${a}${b}",
			vals: Values{"a": StringValue("x"), "b": StringValue("y")},
			want: "'x''y'",
		},
		{
			name:    "unterminated brace is an error",
			tmpl:    "echo ${oops",
			vals:    Values{},
			wantErr: "unterminated",
		},
		{
			name:    "unknown key names itself and lists declared keys",
			tmpl:    "echo ${nope}",
			vals:    Values{"known": StringValue("v")},
			wantErr: "nope",
		},
		{
			name: "json value is compact-encoded then shell-quoted",
			tmpl: "echo ${data}",
			vals: Values{"data": JSONValue(map[string]any{"failures": 2})},
			want: `echo '{"failures":2}'`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RenderShell(tt.tmpl, tt.vals)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("RenderShell() error = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("RenderShell() error = %q, want it to contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("RenderShell() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("RenderShell() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderProse(t *testing.T) {
	tests := []struct {
		name    string
		tmpl    string
		vals    Values
		want    string
		wantErr string
	}{
		{
			name: "raw substitution",
			tmpl: "Findings: ${findings}",
			vals: Values{"findings": StringValue("all green")},
			want: "Findings: all green",
		},
		{
			name: "literal dollar-brace escape",
			tmpl: "literal $${x} here",
			vals: Values{},
			want: "literal ${x} here",
		},
		{
			name: "adjacent substitutions",
			tmpl: "${a}-${b}",
			vals: Values{"a": StringValue("1"), "b": StringValue("2")},
			want: "1-2",
		},
		{
			name:    "unterminated brace is an error",
			tmpl:    "${oops",
			vals:    Values{},
			wantErr: "unterminated",
		},
		{
			name:    "unknown key is never a silent empty string",
			tmpl:    "${missing}",
			vals:    Values{"a": StringValue("1")},
			wantErr: "missing",
		},
		{
			name: "json value is pretty-printed with two-space indent",
			tmpl: "Report:\n${data}",
			vals: Values{"data": JSONValue(map[string]any{"failures": 2})},
			want: "Report:\n{\n  \"failures\": 2\n}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RenderProse(tt.tmpl, tt.vals)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("RenderProse() error = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("RenderProse() error = %q, want it to contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("RenderProse() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("RenderProse() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKeys(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		want []string
	}{
		{
			name: "no keys",
			tmpl: "nothing to see here",
			want: nil,
		},
		{
			name: "single key",
			tmpl: "echo ${msg}",
			want: []string{"msg"},
		},
		{
			name: "order of appearance, deduplicated",
			tmpl: "${b} ${a} ${b}",
			want: []string{"b", "a"},
		},
		{
			name: "escaped dollar-brace consumes no key",
			tmpl: "$${not_a_key} ${real}",
			want: []string{"real"},
		},
		{
			name: "pseudo-keys are ordinary keys to this package",
			tmpl: "${run_id} ${step} ${attempt}",
			want: []string{"run_id", "step", "attempt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Keys(tt.tmpl)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Keys(%q) = %#v, want %#v", tt.tmpl, got, tt.want)
			}
		})
	}
}

func TestEnvFor(t *testing.T) {
	tests := []struct {
		name string
		vals Values
		keys []string
		want []string
	}{
		{
			name: "uppercased WF_ prefix",
			vals: Values{"run_id": StringValue("abc123")},
			keys: []string{"run_id"},
			want: []string{"WF_RUN_ID=abc123"},
		},
		{
			name: "json value exported as compact json",
			vals: Values{"data": JSONValue(map[string]any{"failures": 2})},
			keys: []string{"data"},
			want: []string{`WF_DATA={"failures":2}`},
		},
		{
			name: "keys absent from vals are skipped",
			vals: Values{"present": StringValue("x")},
			keys: []string{"present", "absent"},
			want: []string{"WF_PRESENT=x"},
		},
		{
			name: "preserves given key order",
			vals: Values{"a": StringValue("1"), "b": StringValue("2")},
			keys: []string{"b", "a"},
			want: []string{"WF_B=2", "WF_A=1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EnvFor(tt.vals, tt.keys)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("EnvFor() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
