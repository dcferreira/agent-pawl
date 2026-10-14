package emit_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/emit"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

func TestEscapeStateC0(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "plain text", "plain text"},
		{"LF kept", "a\nb", "a\nb"},
		{"TAB kept", "a\tb", "a\tb"},
		{"ESC escaped", "a\x1bb", `a\u001bb`},
		{"CR escaped", "a\rb", `a\u000db`},
		{"NUL escaped", "a\x00b", `a\u0000b`},
		{"DEL escaped", "a\x7fb", `a\u007fb`},
		{"empty", "", ""},
		{"invalid utf8 preserved", "a\xffb", "a\xffb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emit.EscapeStateC0(tc.in); got != tc.want {
				t.Errorf("EscapeStateC0(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEscapeStateValue(t *testing.T) {
	in := map[string]any{
		"k\x01\n": []any{"a\nb\x1b", float64(1.5), true, nil, map[string]any{"x\t\r": "v\x00"}},
	}
	want := map[string]any{
		"k\\u0001\n": []any{"a\nb\\u001b", float64(1.5), true, nil, map[string]any{"x\t\\u000d": `v\u0000`}},
	}
	if got := emit.EscapeStateValue(in); !reflect.DeepEqual(got, want) {
		t.Errorf("EscapeStateValue = %#v, want %#v", got, want)
	}
}

func TestParse_StateKeepsLFAndTab(t *testing.T) {
	strDecl := map[string]spec.StateDecl{"k": {Type: "string"}}
	jsonDecl := map[string]spec.StateDecl{"k": {Type: "json"}}
	cases := []struct {
		name   string
		emits  string
		decls  map[string]spec.StateDecl
		stdout string
		want   any
	}{
		{"json string LF/TAB", "json", strDecl, `{"k":"a\nb\tc"}`, "a\nb\tc"},
		{"json string ESC", "json", strDecl, `{"k":"a\u001bb"}`, `a\u001bb`},
		{"json string CR", "json", strDecl, `{"k":"a\rb"}`, `a\u000db`},
		{"json string NUL", "json", strDecl, `{"k":"a\u0000b"}`, `a\u0000b`},
		{"json string DEL", "json", strDecl, `{"k":"a\u007fb"}`, `a\u007fb`},
		{"json string raw ctrl byte", "json", strDecl, "{\"k\":\"a\x01b\"}", `a\u0001b`},
		{"json-typed nested value", "json", jsonDecl, `{"k":{"a":["x\ny\u001b"]}}`, map[string]any{"a": []any{`x` + "\n" + `y\u001b`}}},
		{"json-typed map key", "json", jsonDecl, `{"k":{"a\nb\u001b":1}}`, nil},
		{"pairs string ctrl byte", "pairs", strDecl, "k=a\x1bb", `a\u001bb`},
		{"pairs json LF", "pairs", jsonDecl, `k="a\nb\u001b"`, "a\nb" + `\u001b`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step := namedStep("s", nil, []string{"k"}, tc.emits)
			res, err := emit.Parse(tc.stdout, 0, step, tc.decls)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if tc.name == "json-typed map key" {
				m, _ := res.Writes["k"].(map[string]any)
				if _, ok := m["a\nb"+`\u001b`]; !ok || len(m) != 1 {
					t.Errorf("Writes[k] = %#v, want single key %q", res.Writes["k"], "a\nb"+`\u001b`)
				}
				return
			}
			if !reflect.DeepEqual(res.Writes["k"], tc.want) {
				t.Errorf("Writes[k] = %#v, want %#v", res.Writes["k"], tc.want)
			}
		})
	}
}

func TestParse_MaxLengthCountsLFAsOneCodePoint(t *testing.T) {
	// Only json carries an LF (a pairs string value is a single raw line).
	for _, tc := range []struct {
		max int
		ok  bool
	}{{3, true}, {2, false}} {
		decls := map[string]spec.StateDecl{"k": {Type: "string", MaxLength: tc.max}}
		step := namedStep("s", nil, []string{"k"}, "json")
		_, err := emit.Parse(`{"k":"a\nb"}`, 0, step, decls)
		if tc.ok && err != nil {
			t.Errorf("max %d: unexpected error %v", tc.max, err)
		}
		if !tc.ok && !errors.Is(err, emit.ErrParse) {
			t.Errorf("max %d: err = %v, want ErrParse", tc.max, err)
		}
	}
}
