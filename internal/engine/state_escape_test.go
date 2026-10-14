package engine

import (
	"reflect"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/emit"
)

func TestCoerceAgenticValue_StateEscaping(t *testing.T) {
	cases := []struct {
		name, declType string
		in, want       any
	}{
		{"string LF/TAB kept", "string", "a\nb\tc", "a\nb\tc"},
		{"string ESC escaped", "string", "a\x1bb", `a\u001bb`},
		{"string CR escaped", "string", "a\rb", `a\u000db`},
		{"string NUL escaped", "string", "a\x00b", `a\u0000b`},
		{"string DEL escaped", "string", "a\x7fb", `a\u007fb`},
		{"json nested value", "json", map[string]any{"a": []any{"x\ny\x1b"}}, map[string]any{"a": []any{"x\ny" + `\u001b`}}},
		{"json map key", "json", map[string]any{"a\nb\x1b": 1.5}, map[string]any{"a\nb" + `\u001b`: 1.5}},
		{"json scalar string", "json", "a\tb\r", "a\tb" + `\u000d`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := coerceAgenticValue("k", tc.in, tc.declType)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestSubmit_MaxLengthCountsLFAsOneCodePoint(t *testing.T) {
	for _, tc := range []struct {
		max int
		ok  bool
	}{{3, true}, {2, false}} {
		s, err := coerceAgenticValue("k", "a\nb", "string")
		if err != nil {
			t.Fatal(err)
		}
		err = emit.CheckMaxLength("k", s, tc.max)
		if (err == nil) != tc.ok {
			t.Errorf("max %d: CheckMaxLength err = %v, want ok=%v", tc.max, err, tc.ok)
		}
	}
}
