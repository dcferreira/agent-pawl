package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// foreachBase is a valid foreach workflow; each rule test mutates one piece
// of it (see mutate) and asserts exactly which errors result.
const foreachBase = `workflow: fe
start: p
state:
  items: {type: json}
  out: {type: json}
  res: {type: string}
steps:
  - id: p
    kind: parallel
    foreach: {over: items, body: b, collect: out}
    outcomes: {success: done, partial: done, failure: blocked}
  - id: b
    kind: deterministic
    run: echo "res=${item}"
    emits: pairs
    writes: [res]
terminal: {done: {status: ok}}
`

// validateYAML loads src from a temp file and returns Validate's errors.
func validateYAML(t *testing.T, src string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "w.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	report, err := Validate(w)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// Strip the temp path so assertions read like the golden messages.
	out := make([]string, len(report.Errors))
	for i, e := range report.Errors {
		out[i] = strings.TrimPrefix(e, path+": ")
	}
	return out
}

// mutate replaces old with new in foreachBase, failing if old is absent.
func mutate(t *testing.T, old, new string) string {
	t.Helper()
	if !strings.Contains(foreachBase, old) {
		t.Fatalf("mutate: %q not in foreachBase", old)
	}
	return strings.Replace(foreachBase, old, new, 1)
}

// wantErrs asserts got is exactly the want list, in order.
func wantErrs(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Errors =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestForeach_ValidBase(t *testing.T) {
	wantErrs(t, validateYAML(t, foreachBase))
}

func TestForeach_ValidWithRetryPostconditionMaxItems(t *testing.T) {
	src := mutate(t, "collect: out}", "collect: out, max_items: 5}")
	src = strings.Replace(src, "    writes: [res]\n", "    writes: [res]\n    retry: {max_attempts: 3, backoff: 1s}\n    postcondition: {all_set: [res]}\n", 1)
	wantErrs(t, validateYAML(t, src))
}

func TestForeach_ValidItemIndexAndCatchRoutedPartial(t *testing.T) {
	src := mutate(t, "outcomes: {success: done, partial: done, failure: blocked}",
		"next: done\n    catch: [{on: partial, next: done}]")
	src = strings.Replace(src, `echo "res=${item}"`, `echo "res=${item}-${item_index}"`, 1)
	wantErrs(t, validateYAML(t, src))
}

func TestForeach_MaxItemsOrDefault(t *testing.T) {
	if got := (&Foreach{}).MaxItemsOrDefault(); got != 20 {
		t.Errorf("default = %d, want 20", got)
	}
	if got := (&Foreach{MaxItems: 7}).MaxItemsOrDefault(); got != 7 {
		t.Errorf("got %d, want 7", got)
	}
}

func TestForeach_LoadsFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.yaml")
	if err := os.WriteFile(path, []byte(foreachBase), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f := w.StepByID("p").Foreach
	if f == nil || f.Over != "items" || f.Body != "b" || f.Collect != "out" || f.MaxItems != 0 {
		t.Fatalf("Foreach = %+v", f)
	}
}

// extraStep appends a step to foreachBase (before terminal:).
func extraStep(src, step string) string {
	return strings.Replace(src, "terminal:", step+"terminal:", 1)
}

const stepC = "  - id: c\n    kind: deterministic\n    run: echo hi\n    emits: pairs\n"

func TestForeach_Rule22_BothBranchesAndForeach(t *testing.T) {
	src := mutate(t, "    foreach:", "    branches: [b, c]\n    foreach:")
	src = extraStep(src, stepC)
	wantErrs(t, validateYAML(t, src),
		`step "p": rule 22: kind: parallel declares both branches: and foreach:; keep exactly one`)
}

func TestForeach_Rule22_NeitherBranchesNorForeach(t *testing.T) {
	src := mutate(t, "    foreach: {over: items, body: b, collect: out}\n", "")
	wantErrs(t, validateYAML(t, src),
		`step "p": rule 22: kind: parallel requires exactly one of branches: or foreach:; add branches: [a, b] or foreach: {over: <key>, body: <step>, collect: <key>}`,
		`step "b": rule 2: is unreachable from start: "p"; add an edge to it or remove it`,
		`step "b": rule 3: has no next: or outcomes:; add one naming its successor step or a terminal`,
		`step "b": rule 27: run uses ${item}, which is only valid in a foreach body's own fields; remove it or move the work into the body`)
}

func TestForeach_Rule22_ForeachOnOtherKind(t *testing.T) {
	src := mutate(t, "  - id: b\n    kind: deterministic\n", "  - id: b\n    kind: deterministic\n    foreach: {over: items, body: b, collect: out}\n")
	wantErrs(t, validateYAML(t, src),
		`step "b": rule 22: foreach: is only valid on kind: parallel, not kind: deterministic; remove the foreach: block`)
}

func TestForeach_Rule22_RequiredFields(t *testing.T) {
	src := mutate(t, "foreach: {over: items, body: b, collect: out}", "foreach: {}")
	wantErrs(t, validateYAML(t, src),
		`step "p": rule 22: foreach.over: is required; add over: <json state key holding the array>`,
		`step "p": rule 22: foreach.body: is required; add body: <id of the step run once per item>`,
		`step "p": rule 22: foreach.collect: is required; add collect: <json state key receiving the per-item results>`,
		`step "b": rule 2: is unreachable from start: "p"; add an edge to it or remove it`,
		`step "b": rule 3: has no next: or outcomes:; add one naming its successor step or a terminal`,
		`step "b": rule 27: run uses ${item}, which is only valid in a foreach body's own fields; remove it or move the work into the body`)
}

func TestForeach_Rule22_UnknownKey(t *testing.T) {
	src := mutate(t, "collect: out}", "collect: out, max_item: 3}")
	wantErrs(t, validateYAML(t, src),
		`step "p": rule 22: foreach: uses unknown key(s) max_item; use only over, body, collect and max_items`)
}

func TestForeach_Rule22_MaxItems(t *testing.T) {
	for _, n := range []string{"0", "-3"} {
		src := mutate(t, "collect: out}", "collect: out, max_items: "+n+"}")
		wantErrs(t, validateYAML(t, src),
			`step "p": rule 22: foreach.max_items: `+n+` is less than 1; use a value of 1 or more, or omit it for the default of 20`)
	}
}

func TestForeach_Rule23_OverMustBeDeclaredJSONState(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"undeclared", mutate(t, "over: items", "over: nope"),
			`step "p": rule 23: foreach.over: "nope" does not name a declared state: key; declare state: {nope: {type: json}}`},
		{"arg", strings.Replace(mutate(t, "over: items", "over: ar"), "state:", "args:\n  ar: {type: json}\nstate:", 1),
			`step "p": rule 23: foreach.over: "ar" is declared under args:, but foreach.over must name a state: key; declare it under state: {ar: {type: json}}`},
		{"wrong type", mutate(t, "  items: {type: json}", "  items: {type: string}"),
			`step "p": rule 23: foreach.over: "items" has type string, but foreach.over must be type: json (an array)`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { wantErrs(t, validateYAML(t, c.src), c.want) })
	}
}

func TestForeach_Rule23_CollectMustBeDeclaredJSONState(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"undeclared", mutate(t, "collect: out", "collect: nope"),
			`step "p": rule 23: foreach.collect: "nope" does not name a declared state: key; declare state: {nope: {type: json}}`},
		{"arg", strings.Replace(mutate(t, "collect: out", "collect: ar"), "state:", "args:\n  ar: {type: json}\nstate:", 1),
			`step "p": rule 23: foreach.collect: "ar" is declared under args:, but foreach.collect must name a state: key; declare it under state: {ar: {type: json}}`},
		{"wrong type", mutate(t, "  out: {type: json}", "  out: {type: string}"),
			`step "p": rule 23: foreach.collect: "out" has type string, but foreach.collect must be type: json (an array)`},
		{"same as over", mutate(t, "collect: out", "collect: items"),
			`step "p": rule 23: foreach.collect: "items" is the same key as foreach.over; use a different key to receive the results`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { wantErrs(t, validateYAML(t, c.src), c.want) })
	}
}

func TestForeach_Rule23_CollectWrittenElsewhere(t *testing.T) {
	src := extraStep(mutate(t, "outcomes: {success: done, partial: done, failure: blocked}", "outcomes: {success: c, partial: done, failure: blocked}"),
		"  - id: c\n    kind: deterministic\n    run: echo out=1\n    emits: pairs\n    writes: [out]\n    next: done\n")
	wantErrs(t, validateYAML(t, src),
		`step "c": rule 23: writes: "out", but "out" is the collect: key of foreach step "p", which is its only writer; use a different key`)
}

func TestForeach_Rule24_Body(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"undeclared", mutate(t, "body: b", "body: nope"), []string{
			`step "p": rule 24: foreach.body: "nope" does not name a declared step; declare step "nope" or fix the typo`,
			`step "b": rule 2: is unreachable from start: "p"; add an edge to it or remove it`,
			`step "b": rule 3: has no next: or outcomes:; add one naming its successor step or a terminal`,
			`step "b": rule 27: run uses ${item}, which is only valid in a foreach body's own fields; remove it or move the work into the body`}},
		{"agentic", strings.Replace(mutate(t, "    kind: deterministic\n    run: echo \"res=${item}\"\n    emits: pairs\n", "    kind: agentic\n    description: do ${item}\n"), "    writes: [res]\n", "    writes: {res: {type: string}}\n    postcondition: {all_set: [res]}\n", 1), []string{
			`step "p": rule 24: foreach.body: step "b" is kind: agentic; agentic foreach bodies are not yet supported (a later milestone) — use a deterministic body`}},
		{"nested", extraStep(mutate(t, "body: b", "body: w"), "  - id: w\n    kind: wait\n    poll: echo hi\n    timeout: 1m\n"), []string{
			`step "p": rule 24: foreach.body: step "w" has kind "wait", but a foreach body must be deterministic (no nesting)`,
			`step "b": rule 2: is unreachable from start: "p"; add an edge to it or remove it`,
			`step "b": rule 3: has no next: or outcomes:; add one naming its successor step or a terminal`,
			`step "b": rule 27: run uses ${item}, which is only valid in a foreach body's own fields; remove it or move the work into the body`}},
		{"start", strings.Replace(mutate(t, "start: p", "start: b"), "terminal:", "terminal:", 1), []string{
			`step "p": rule 24: foreach.body: step "b" is start:, but a foreach body may not be the workflow's start step`,
			`step "p": rule 2: is unreachable from start: "b"; add an edge to it or remove it`}},
		{"also a branch", extraStep(strings.Replace(mutate(t, "outcomes: {success: done, partial: done, failure: blocked}", "outcomes: {success: q, partial: done, failure: blocked}"), "terminal:", "  - id: q\n    kind: parallel\n    branches: [b, c]\n    next: done\nterminal:", 1), stepC), []string{
			`step "q": branches[0]: step "b" is already claimed as the foreach body of parallel step "p"; a step may be owned by only one parallel step`}},
		{"body of two foreach", extraStep(strings.Replace(mutate(t, "outcomes: {success: done, partial: done, failure: blocked}", "outcomes: {success: q, partial: done, failure: blocked}"), "terminal:", "  - id: q\n    kind: parallel\n    foreach: {over: items, body: b, collect: out2}\n    outcomes: {success: done, partial: done, failure: blocked}\nterminal:", 1), ""), []string{
			`step "q": rule 24: foreach.body: step "b" is already claimed by parallel step "p"; a step may be owned by only one parallel step`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := c.src
			if c.name == "body of two foreach" {
				src = strings.Replace(src, "  res: {type: string}", "  res: {type: string}\n  out2: {type: json}", 1)
			}
			wantErrs(t, validateYAML(t, src), c.want...)
		})
	}
}

func TestForeach_Rule24_BodyForbiddenFields(t *testing.T) {
	for _, f := range []struct{ field, line string }{
		{"next:", "    next: done\n"},
		{"outcomes:", "    outcomes: {success: done}\n"},
		{"catch:", "    catch: [{on: failure, next: done}]\n"},
		{"attempts:", "    attempts: 2\n"},
		{"attempt_key:", "    attempt_key: x\n"},
		{"max_visits:", "    max_visits: 3\n"},
	} {
		t.Run(f.field, func(t *testing.T) {
			src := strings.Replace(foreachBase, "    writes: [res]\n", "    writes: [res]\n"+f.line, 1)
			wantErrs(t, validateYAML(t, src),
				`step "b": rule 24: declares `+f.field+`, but it is the foreach body of parallel step "p", which owns routing for every item; remove `+f.field)
		})
	}
}

func TestForeach_Rule25_BodyWritesNotGlobal(t *testing.T) {
	routed := mutate(t, "outcomes: {success: done, partial: done, failure: blocked}", "outcomes: {success: c, partial: done, failure: blocked}")
	t.Run("read by another step", func(t *testing.T) {
		src := extraStep(routed, "  - id: c\n    kind: deterministic\n    run: echo ${res}\n    next: done\n")
		wantErrs(t, validateYAML(t, src),
			`step "c": rule 25: run uses ${res}, but "res" is written only by foreach body "b" and captured per item into collect: key "out", never into global state; read "out" after the join instead`)
	})
	t.Run("written by another step", func(t *testing.T) {
		src := extraStep(routed, "  - id: c\n    kind: deterministic\n    run: echo res=1\n    emits: pairs\n    writes: [res]\n    next: done\n")
		wantErrs(t, validateYAML(t, src),
			`step "c": rule 25: writes: "res", which is also written by foreach body "b"; body writes are captured per item into collect:, so no other step may write the same key — use a different key`)
	})
	t.Run("read by terminal message", func(t *testing.T) {
		src := strings.Replace(foreachBase, "terminal: {done: {status: ok}}", "terminal: {done: {status: ok, message: \"${res}\"}}", 1)
		wantErrs(t, validateYAML(t, src),
			`terminal "done": rule 25: message uses ${res}, but "res" is written only by foreach body "b" and captured per item into collect: key "out", never into global state; read "out" after the join instead`)
	})
	t.Run("body may read its own writes", func(t *testing.T) {
		src := strings.Replace(foreachBase, "    writes: [res]\n", "    writes: [res]\n    postcondition: {command: \"test -n ${res}\"}\n", 1)
		wantErrs(t, validateYAML(t, src))
	})
}

func TestForeach_Rule26_PartialMustBeRouted(t *testing.T) {
	t.Run("next only", func(t *testing.T) {
		src := mutate(t, "outcomes: {success: done, partial: done, failure: blocked}", "next: done")
		wantErrs(t, validateYAML(t, src),
			`step "p": rule 26: a foreach step resolves to success, partial or failure; route partial explicitly — add outcomes: {success: ..., partial: <step-or-terminal>, failure: ...} or catch: [{on: partial, next: ...}]`)
	})
	t.Run("outcomes without partial", func(t *testing.T) {
		src := mutate(t, "partial: done, ", "")
		wantErrs(t, validateYAML(t, src),
			`step "p": rule 26: a foreach step resolves to success, partial or failure; route partial explicitly — add outcomes: {success: ..., partial: <step-or-terminal>, failure: ...} or catch: [{on: partial, next: ...}]`)
	})
	t.Run("branches parallel may not route partial", func(t *testing.T) {
		src := `workflow: w
start: p
steps:
  - id: p
    kind: parallel
    branches: [b1, b2]
    outcomes: {success: done, partial: done, failure: done}
  - id: b1
    kind: deterministic
    run: echo hi
  - id: b2
    kind: deterministic
    run: echo hi
terminal: {done: {status: ok}}
`
		wantErrs(t, validateYAML(t, src),
			`step "p": rule 26: kind: parallel with branches: resolves only to success or failure, so it cannot route partial; remove the partial route (only foreach: produces partial)`)
	})
}

func TestForeach_Rule27_ItemKeysScopedAndReserved(t *testing.T) {
	routed := mutate(t, "outcomes: {success: done, partial: done, failure: blocked}", "outcomes: {success: c, partial: done, failure: blocked}")
	t.Run("item outside the body", func(t *testing.T) {
		src := extraStep(routed, "  - id: c\n    kind: deterministic\n    run: echo ${item} ${item_index}\n    next: done\n")
		wantErrs(t, validateYAML(t, src),
			`step "c": rule 27: run uses ${item}, which is only valid in a foreach body's own fields; remove it or move the work into the body`,
			`step "c": rule 27: run uses ${item_index}, which is only valid in a foreach body's own fields; remove it or move the work into the body`)
	})
	t.Run("item in terminal message", func(t *testing.T) {
		src := strings.Replace(foreachBase, "terminal: {done: {status: ok}}", "terminal: {done: {status: ok, message: \"${item}\"}}", 1)
		wantErrs(t, validateYAML(t, src),
			`terminal "done": rule 27: message uses ${item}, which is only valid in a foreach body's own fields; remove it or move the work into the body`)
	})
	t.Run("not declarable as state", func(t *testing.T) {
		src := mutate(t, "  res: {type: string}", "  res: {type: string}\n  item: {type: string}")
		wantErrs(t, validateYAML(t, src),
			`rule 27: state: key "item" is reserved for foreach bodies; rename it`)
	})
	t.Run("not declarable as arg", func(t *testing.T) {
		src := strings.Replace(foreachBase, "state:", "args:\n  item_index: {type: string}\nstate:", 1)
		wantErrs(t, validateYAML(t, src),
			`rule 27: args: key "item_index" is reserved for foreach bodies; rename it`)
	})
}

func TestForeach_Rule28_InjectionLint(t *testing.T) {
	for _, tc := range []struct{ name, run string }{
		{"sh -c item", "sh -c ${item}"},
		{"bash -c item", "bash -c ${item}"},
		{"quoted", `bash -c "${item}"`},
		{"item_index", "sh -c ${item_index}"},
		{"combined flags", "bash -ec ${item}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.Replace(foreachBase, `run: echo "res=${item}"`, "run: '"+strings.ReplaceAll(tc.run, "'", "''")+"'", 1)
			got := validateYAML(t, src)
			if len(got) != 1 || !strings.HasPrefix(got[0], `step "b": rule 28: run runs ${`) || !strings.Contains(got[0], "never be executed as a script") {
				t.Fatalf("Errors = %v", got)
			}
		})
	}
	t.Run("postcondition command", func(t *testing.T) {
		src := strings.Replace(foreachBase, "    writes: [res]\n", "    writes: [res]\n    postcondition: {command: \"sh -c ${item}\"}\n", 1)
		got := validateYAML(t, src)
		if len(got) != 1 || !strings.HasPrefix(got[0], `step "b": rule 28: postcondition.command runs ${item}`) {
			t.Fatalf("Errors = %v", got)
		}
	})
	t.Run("plain use is fine", func(t *testing.T) {
		src := strings.Replace(foreachBase, `run: echo "res=${item}"`, `run: ./process.sh ${item} --index ${item_index}`, 1)
		wantErrs(t, validateYAML(t, src))
	})
}
