package journal

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

var update = flag.Bool("update", false, "regenerate the golden files under testdata/golden")

const goldenDir = "testdata/golden"

// loadGolden loads the fixture with Path pinned to a fixed relative value
// (Load records the absolute path it was given, which differs per checkout).
func loadGolden(t *testing.T) *spec.Workflow {
	t.Helper()
	w, err := spec.Load(filepath.Join(goldenDir, "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := spec.Validate(w)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("golden fixture no longer validates: %v", rep.Errors)
	}
	w.Path = "workflow.yaml"
	return w
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join(goldenDir, name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/journal -run Golden -update)", err)
	}
	if string(want) != string(got) {
		t.Errorf("%s differs from the golden file; if the change is intended (it changes plan.json's shape or every digest: bump Format and keep the old decode path), regenerate with -update\n got: %s\nwant: %s", name, got, want)
	}
}

// TestGolden_PlanJSON pins the exact plan.json bytes WritePlan emits: the
// snake_case tags on every spec type are an on-disk format.
func TestGolden_PlanJSON(t *testing.T) {
	w := loadGolden(t)
	dir := t.TempDir()
	if _, err := WritePlan(dir, w); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "plan.golden.json", got)
}

// TestGolden_Digest pins Digest: any change to the digest structs or to the
// spec tags changes every workflow's digest, which is only safe because the
// resume check recomputes both sides; the golden makes such a change a
// conscious one.
func TestGolden_Digest(t *testing.T) {
	d, err := Digest(loadGolden(t))
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "digest.golden.txt", []byte(d+"\n"))
}

// TestGolden_PlanRoundTrips: the golden plan.json decodes back to an
// identical workflow (same digest, same serialisation).
func TestGolden_PlanRoundTrips(t *testing.T) {
	w := loadGolden(t)
	dir := t.TempDir()
	if _, err := WritePlan(dir, w); err != nil {
		t.Fatal(err)
	}
	p, err := ReadPlan(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Compare through JSON: yaml-decoded ints become float64 after a round
	// trip, which DeepEqual would flag but which serialises identically.
	if !jsonEqual(t, w, p.Workflow) {
		t.Errorf("round-tripped workflow differs")
	}
}

// TestGolden_FixtureCoversEveryTaggedField guards the fixture itself: every
// exported field of every tagged spec type must be non-zero somewhere in the
// golden workflow, so a new field cannot be added without the golden
// exercising it.
func TestGolden_FixtureCoversEveryTaggedField(t *testing.T) {
	w := loadGolden(t)
	seen := map[string]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				walk(v.MapIndex(k))
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				f := v.Type().Field(i)
				if !f.IsExported() {
					continue
				}
				if !v.Field(i).IsZero() {
					seen[v.Type().Name()+"."+f.Name] = true
				}
				walk(v.Field(i))
			}
		}
	}
	walk(reflect.ValueOf(w))
	for _, typ := range []reflect.Type{
		reflect.TypeOf(spec.Workflow{}), reflect.TypeOf(spec.Step{}), reflect.TypeOf(spec.ArgDecl{}),
		reflect.TypeOf(spec.StateDecl{}), reflect.TypeOf(spec.GuardDecl{}), reflect.TypeOf(spec.InvariantDecl{}),
		reflect.TypeOf(spec.Terminal{}), reflect.TypeOf(spec.CatchRule{}), reflect.TypeOf(spec.Writes{}),
		reflect.TypeOf(spec.Postcondition{}), reflect.TypeOf(spec.RetryDecl{}), reflect.TypeOf(spec.Foreach{}),
		reflect.TypeOf(spec.ContextEntry{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			// UnknownFields/UnknownKeys only populate on a workflow that
			// fails validation; testdata/legacy/unknowns.yaml covers them.
			if strings.HasPrefix(f.Name, "Unknown") {
				continue
			}
			if f.Tag.Get("json") == "-" {
				continue
			}
			if !seen[typ.Name()+"."+f.Name] {
				t.Errorf("golden fixture never sets %s.%s; extend testdata/golden/workflow.yaml", typ.Name(), f.Name)
			}
		}
	}
}
