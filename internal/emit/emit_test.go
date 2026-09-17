package emit_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dcferreira/agentic-workflow-fsm/internal/emit"
	"github.com/dcferreira/agentic-workflow-fsm/internal/spec"
)

func namedStep(id string, outcomes map[string]string, writesKeys []string, emits string) *spec.Step {
	return &spec.Step{
		ID:       id,
		Emits:    emits,
		Outcomes: outcomes,
		Writes:   spec.Writes{Keys: writesKeys},
	}
}

func typedStep(id string, outcomes map[string]string, types map[string]string, emits string) *spec.Step {
	keys := make([]string, 0, len(types))
	for k := range types {
		keys = append(keys, k)
	}
	return &spec.Step{
		ID:       id,
		Emits:    emits,
		Outcomes: outcomes,
		Writes:   spec.Writes{Keys: keys, Types: types, IsTyped: true},
	}
}

func TestParse_NonZeroExitIsAlwaysFailure(t *testing.T) {
	cases := []struct {
		name     string
		exitCode int
		stdout   string
		step     *spec.Step
	}{
		{"exit 1, no outcomes declared", 1, "anything at all", namedStep("s", nil, nil, "json")},
		{"exit 2, printed a valid token", 2, "FRESH", namedStep("s", map[string]string{"FRESH": "next"}, nil, "json")},
		{"exit 127, empty stdout", 127, "", namedStep("s", nil, nil, "json")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := emit.Parse(tc.stdout, tc.exitCode, tc.step, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Outcome != "failure" {
				t.Errorf("Outcome = %q, want failure", res.Outcome)
			}
			if res.Writes != nil {
				t.Errorf("Writes = %v, want nil", res.Writes)
			}
		})
	}
}

func TestParse_NoAuthorNamedOutcomes_WholeLineIsPayload(t *testing.T) {
	step := namedStep("s", map[string]string{"success": "next", "failure": "blocked"}, []string{"branch"}, "pairs")
	res, err := emit.Parse("branch=main", 0, step, map[string]spec.StateDecl{"branch": {Type: "string"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != "success" {
		t.Errorf("Outcome = %q, want success", res.Outcome)
	}
	if got := res.Writes["branch"]; got != "main" {
		t.Errorf("Writes[branch] = %v, want main", got)
	}
}

func TestParse_TokenOnlyLine_ValidUnderBothModes_NoWrites(t *testing.T) {
	for _, emitsMode := range []string{"json", "pairs"} {
		t.Run(emitsMode, func(t *testing.T) {
			step := namedStep("s", map[string]string{"FRESH": "a", "EXISTING": "b"}, []string{"branch"}, emitsMode)
			res, err := emit.Parse("FRESH", 0, step, map[string]spec.StateDecl{"branch": {Type: "string"}})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Outcome != "FRESH" {
				t.Errorf("Outcome = %q, want FRESH", res.Outcome)
			}
			if len(res.Writes) != 0 {
				t.Errorf("Writes = %v, want empty", res.Writes)
			}
		})
	}
}

func TestParse_TokenWithJSONPayload(t *testing.T) {
	step := namedStep("s", map[string]string{"FRESH": "a"}, []string{"branch", "title"}, "json")
	decls := map[string]spec.StateDecl{"branch": {Type: "string"}, "title": {Type: "string"}}
	res, err := emit.Parse(`FRESH {"branch":"main","title":"x"}`, 0, step, decls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != "FRESH" {
		t.Errorf("Outcome = %q, want FRESH", res.Outcome)
	}
	want := map[string]any{"branch": "main", "title": "x"}
	if !reflect.DeepEqual(res.Writes, want) {
		t.Errorf("Writes = %v, want %v", res.Writes, want)
	}
}

func TestParse_TokenNotRouted_IsError(t *testing.T) {
	step := namedStep("s", map[string]string{"FRESH": "a", "EXISTING": "b"}, nil, "json")
	_, err := emit.Parse("BOGUS", 0, step, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, emit.ErrParse) {
		t.Errorf("error does not wrap ErrParse: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"BOGUS", "EXISTING", "FRESH"} {
		if !contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

func TestParse_LastNonEmptyLine_TrailingNewlines(t *testing.T) {
	step := namedStep("s", nil, []string{"branch"}, "pairs")
	decls := map[string]spec.StateDecl{"branch": {Type: "string"}}
	res, err := emit.Parse("branch=main\n\n\n", 0, step, decls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Writes["branch"] != "main" {
		t.Errorf("Writes[branch] = %v, want main", res.Writes["branch"])
	}
}

func TestParse_LastNonEmptyLine_WhitespaceOnlyLineIsEmpty(t *testing.T) {
	step := namedStep("s", nil, []string{"branch"}, "pairs")
	decls := map[string]spec.StateDecl{"branch": {Type: "string"}}
	res, err := emit.Parse("branch=main\n   \n", 0, step, decls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Writes["branch"] != "main" {
		t.Errorf("Writes[branch] = %v, want main", res.Writes["branch"])
	}
}

func TestParse_LastNonEmptyLine_IgnoresEarlierLines(t *testing.T) {
	step := namedStep("s", nil, []string{"branch"}, "pairs")
	decls := map[string]spec.StateDecl{"branch": {Type: "string"}}
	res, err := emit.Parse("branch=first\nsome log noise\nbranch=second", 0, step, decls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Writes["branch"] != "second" {
		t.Errorf("Writes[branch] = %v, want second", res.Writes["branch"])
	}
}

func TestParse_CRLF(t *testing.T) {
	step := namedStep("s", nil, []string{"branch"}, "pairs")
	decls := map[string]spec.StateDecl{"branch": {Type: "string"}}
	res, err := emit.Parse("noise\r\nbranch=main\r\n", 0, step, decls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Writes["branch"] != "main" {
		t.Errorf("Writes[branch] = %v, want main", res.Writes["branch"])
	}
}

func TestParse_JSON_KeyNotInWrites_IsError(t *testing.T) {
	step := namedStep("s", nil, []string{"branch"}, "json")
	_, err := emit.Parse(`{"branch":"main","evil":"x"}`, 0, step, map[string]spec.StateDecl{"branch": {Type: "string"}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, emit.ErrParse) {
		t.Errorf("error does not wrap ErrParse: %v", err)
	}
	if !contains(err.Error(), "evil") {
		t.Errorf("error %q does not name the offending key", err.Error())
	}
}

func TestParse_JSON_NotAnObject_IsError(t *testing.T) {
	cases := []string{`["a","b"]`, `"just a string"`, `42`, `true`}
	step := namedStep("s", nil, []string{"branch"}, "json")
	for _, payload := range cases {
		t.Run(payload, func(t *testing.T) {
			_, err := emit.Parse(payload, 0, step, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, emit.ErrParse) {
				t.Errorf("error does not wrap ErrParse: %v", err)
			}
		})
	}
}

func TestParse_JSON_Malformed_IsError(t *testing.T) {
	step := namedStep("s", nil, []string{"branch"}, "json")
	_, err := emit.Parse(`{"branch": `, 0, step, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, emit.ErrParse) {
		t.Errorf("error does not wrap ErrParse: %v", err)
	}
}

func TestParse_IntegerCoercion(t *testing.T) {
	step := namedStep("s", nil, []string{"count"}, "json")
	decls := map[string]spec.StateDecl{"count": {Type: "integer"}}

	t.Run("string \"3\" coerces to integer", func(t *testing.T) {
		res, err := emit.Parse(`{"count":"3"}`, 0, step, decls)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, ok := res.Writes["count"].(int64)
		if !ok || got != 3 {
			t.Errorf("Writes[count] = %#v, want int64(3)", res.Writes["count"])
		}
	})

	t.Run("JSON number 3 coerces to integer", func(t *testing.T) {
		res, err := emit.Parse(`{"count":3}`, 0, step, decls)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, ok := res.Writes["count"].(int64)
		if !ok || got != 3 {
			t.Errorf("Writes[count] = %#v, want int64(3)", res.Writes["count"])
		}
	})

	t.Run("\"banana\" is rejected loudly", func(t *testing.T) {
		_, err := emit.Parse(`{"count":"banana"}`, 0, step, decls)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, emit.ErrParse) {
			t.Errorf("error does not wrap ErrParse: %v", err)
		}
		if !contains(err.Error(), "banana") {
			t.Errorf("error %q does not mention the offending value", err.Error())
		}
	})
}

func TestParse_PairsCoercion(t *testing.T) {
	step := namedStep("s", nil, []string{"count", "ok", "ratio", "label"}, "pairs")
	decls := map[string]spec.StateDecl{
		"count": {Type: "integer"},
		"ok":    {Type: "boolean"},
		"ratio": {Type: "number"},
		"label": {Type: "string"},
	}
	res, err := emit.Parse("count=3 ok=true ratio=1.5 label=main", 0, step, decls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := res.Writes["count"]; got != int64(3) {
		t.Errorf("count = %#v, want int64(3)", got)
	}
	if got := res.Writes["ok"]; got != true {
		t.Errorf("ok = %#v, want true", got)
	}
	if got := res.Writes["ratio"]; got != 1.5 {
		t.Errorf("ratio = %#v, want 1.5", got)
	}
	if got := res.Writes["label"]; got != "main" {
		t.Errorf("label = %#v, want main", got)
	}
}

func TestParse_Pairs_Malformed_IsError(t *testing.T) {
	step := namedStep("s", nil, []string{"branch"}, "pairs")
	_, err := emit.Parse("branch", 0, step, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, emit.ErrParse) {
		t.Errorf("error does not wrap ErrParse: %v", err)
	}
}

func TestParse_C0ByteInsideJSONStringValue(t *testing.T) {
	step := namedStep("s", nil, []string{"msg"}, "json")
	decls := map[string]spec.StateDecl{"msg": {Type: "string"}}
	payload := "{\"msg\":\"line1\x01line2\"}"
	res, err := emit.Parse(payload, 0, step, decls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := res.Writes["msg"].(string)
	if strings.ContainsRune(got, 0x01) {
		t.Errorf("Writes[msg] = %q still contains a raw C0 byte", got)
	}
	const want = `line1\u0001line2`
	if got != want {
		t.Errorf("Writes[msg] = %q, want %q", got, want)
	}
}

func TestParse_C0ByteInPairsValue(t *testing.T) {
	step := namedStep("s", nil, []string{"msg"}, "pairs")
	decls := map[string]spec.StateDecl{"msg": {Type: "string"}}
	res, err := emit.Parse("msg=a\x01b", 0, step, decls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := res.Writes["msg"].(string)
	if contains(got, "\x01") {
		t.Errorf("Writes[msg] = %q still contains a raw C0 byte", got)
	}
}

func TestParse_TypedWritesMap_UsedOverDecls(t *testing.T) {
	step := typedStep("s", nil, map[string]string{"count": "integer"}, "json")
	// decls disagrees; the step's own typed writes: must win.
	decls := map[string]spec.StateDecl{"count": {Type: "string"}}
	res, err := emit.Parse(`{"count":"5"}`, 0, step, decls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := res.Writes["count"]; got != int64(5) {
		t.Errorf("count = %#v, want int64(5)", got)
	}
}

func TestParse_UndeclaredType_IsError(t *testing.T) {
	step := namedStep("s", nil, []string{"branch"}, "json")
	_, err := emit.Parse(`{"branch":"main"}`, 0, step, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, emit.ErrParse) {
		t.Errorf("error does not wrap ErrParse: %v", err)
	}
}

func TestParse_NoTokenWhenOutcomesDeclared_IsError(t *testing.T) {
	step := namedStep("s", map[string]string{"FRESH": "a"}, nil, "json")
	_, err := emit.Parse("", 0, step, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, emit.ErrParse) {
		t.Errorf("error does not wrap ErrParse: %v", err)
	}
}

// TestParse_TokenRoutedOnlyByCatch_IsAccepted is finding I4's round-2
// end-to-end fix: a TOKEN named only by a catch: entry (no matching
// outcomes: key at all) must be accepted, not rejected as unintelligible —
// design/format-spec.md §D defines catch: as an ordered list routing any
// outcome, not just the two reserved ones, so the routable set Parse checks
// a TOKEN against must be outcomes: keys ∪ catch[].on values.
func TestParse_TokenRoutedOnlyByCatch_IsAccepted(t *testing.T) {
	step := namedStep("s", map[string]string{"good": "done"}, nil, "json")
	step.Catch = []spec.CatchRule{{On: "bad", Next: "blocked"}}

	result, err := emit.Parse("bad", 0, step, nil)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if result.Outcome != "bad" {
		t.Errorf("Outcome = %q, want %q", result.Outcome, "bad")
	}
}

// TestParse_TokenInNeitherOutcomesNorCatch_IsError pins that the "keep the
// unroutable-token-is-ErrParse behaviour for tokens in neither set" half of
// finding I4's ruling still holds: a token that isn't in outcomes: or
// catch: is still unintelligible stdout.
func TestParse_TokenInNeitherOutcomesNorCatch_IsError(t *testing.T) {
	step := namedStep("s", map[string]string{"good": "done"}, nil, "json")
	step.Catch = []spec.CatchRule{{On: "bad", Next: "blocked"}}

	_, err := emit.Parse("worse", 0, step, nil)
	if !errors.Is(err, emit.ErrParse) {
		t.Errorf("Parse(%q): error = %v, want it to wrap ErrParse", "worse", err)
	}
}

func TestEscapeC0(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain text", "plain text"},
		{"a\x01b", `a\u0001b`},
		{"a\nb", `a\u000ab`},
		{"a\tb", `a\u0009b`},
		{"", ""},
	}
	for _, tc := range cases {
		if got := emit.EscapeC0(tc.in); got != tc.want {
			t.Errorf("EscapeC0(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// --- Fix round 1 regressions (F1-F6) ---

func TestParse_F1_TabFormattedJSONParses(t *testing.T) {
	step := namedStep("s", nil, []string{"a"}, "json")
	decls := map[string]spec.StateDecl{"a": {Type: "string"}}

	t.Run("no token", func(t *testing.T) {
		payload := "{\"a\":\t\"x\"}"
		res, err := emit.Parse(payload, 0, step, decls)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Writes["a"] != "x" {
			t.Errorf("Writes[a] = %#v, want \"x\"", res.Writes["a"])
		}
	})

	t.Run("with token", func(t *testing.T) {
		tokStep := namedStep("s", map[string]string{"OK": "next"}, []string{"a"}, "json")
		payload := "OK\t{\"a\":\t\"x\"}"
		res, err := emit.Parse(payload, 0, tokStep, decls)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Outcome != "OK" {
			t.Errorf("Outcome = %q, want OK", res.Outcome)
		}
		if res.Writes["a"] != "x" {
			t.Errorf("Writes[a] = %#v, want \"x\"", res.Writes["a"])
		}
	})
}

func TestParse_F2_JSONTypedValue_MapKeysEscaped(t *testing.T) {
	step := namedStep("s", nil, []string{"data"}, "json")
	decls := map[string]spec.StateDecl{"data": {Type: "json"}}
	// A raw 0x01 byte, not a newline: Parse already isolates a single line
	// before payload parsing ever runs, so a JSON payload never legitimately
	// contains a raw newline; 0x01 exercises the same map-key escaping path
	// without also exercising line-splitting.
	payload := "{\"data\":{\"k\x01ey\":\"v\"}}"
	res, err := emit.Parse(payload, 0, step, decls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := res.Writes["data"].(map[string]any)
	if !ok {
		t.Fatalf("Writes[data] = %#v, want map[string]any", res.Writes["data"])
	}
	for k := range m {
		if strings.ContainsRune(k, 0x01) {
			t.Errorf("map key %q still contains a raw control byte", k)
		}
	}
	if _, ok := m[`k\u0001ey`]; !ok {
		t.Errorf("expected escaped key %q, got keys %v", `k\u0001ey`, m)
	}
}

func TestEscapeC0_F3_InvalidUTF8IsPreservedNotMangled(t *testing.T) {
	// EscapeC0 is byte-oriented: an invalid UTF-8 byte (0xff is never valid
	// in UTF-8) that is not itself a C0 control byte or DEL must survive
	// completely unchanged, not be decoded (and thereby lossily replaced
	// with U+FFFD) as if it were a rune.
	in := "a\xffb"
	got := emit.EscapeC0(in)
	if got != in {
		t.Errorf("EscapeC0(%q) = %q, want unchanged (byte-for-byte)", in, got)
	}
}

func TestParse_F4_NoWritesDeclared_ProseIsIgnored(t *testing.T) {
	for _, emitsMode := range []string{"json", "pairs"} {
		t.Run(emitsMode, func(t *testing.T) {
			step := namedStep("s", nil, nil, emitsMode)
			res, err := emit.Parse("All 42 tests passed.", 0, step, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Outcome != "success" {
				t.Errorf("Outcome = %q, want success", res.Outcome)
			}
			if len(res.Writes) != 0 {
				t.Errorf("Writes = %v, want empty", res.Writes)
			}
		})
	}
}

func TestParse_F5_BadTypeErrorNamesTheFix(t *testing.T) {
	step := namedStep("s", nil, []string{"count"}, "json")
	decls := map[string]spec.StateDecl{"count": {Type: "integer"}}
	_, err := emit.Parse("{\"count\":\"banana\"}", 0, step, decls)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"change", "state:", "writes:"} {
		if !contains(msg, want) {
			t.Errorf("error %q does not name the fix (missing %q)", msg, want)
		}
	}
}

func TestParse_F6_JSONNumberCoercesToString(t *testing.T) {
	step := namedStep("s", nil, []string{"b"}, "json")
	decls := map[string]spec.StateDecl{"b": {Type: "string"}}
	res, err := emit.Parse("{\"b\":1}", 0, step, decls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Writes["b"] != "1" {
		t.Errorf("Writes[b] = %#v, want \"1\"", res.Writes["b"])
	}
}
