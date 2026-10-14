package journal

import (
	"reflect"
	"strings"
	"testing"
)

// frozenDigestFields is the field set digestWorkflow and digestStep had when
// the digest was re-keyed to snake_case json tags. It is deliberately a
// closed list: a field added later MUST carry omitempty, so a workflow that
// does not use the new field serialises, and therefore digests, exactly as it
// did before the field existed (the same trick Foreach uses). Without that, a
// new always-emitted key silently changes every existing workflow's digest.
// Do not add names here to make the test pass; add `,omitempty` to the new
// field's tag instead.
var frozenDigestFields = map[string]bool{
	// digestWorkflow
	"Workflow": true, "Description": true, "Start": true, "MaxStepsRaw": true, "Args": true,
	"State": true, "Guards": true, "Invariants": true, "Steps": true, "Terminal": true,
	// digestStep
	"ID": true, "Kind": true, "Run": true, "Emits": true, "Context": true, "SubagentArgs": true,
	"Poll": true, "Every": true, "Timeout": true, "Question": true, "Options": true,
	"OptionsFrom": true, "Multi": true, "Writes": true, "Postcondition": true,
	"AttemptsRaw": true, "AttemptKey": true, "MaxVisitsRaw": true, "Retry": true,
	"Catch": true, "Next": true, "Outcomes": true, "Branches": true, "Foreach": true,
	"UnknownFields": true,
}

func TestDigestStructs_NewFieldsMustBeOmitempty(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(digestWorkflow{}), reflect.TypeOf(digestStep{})} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			tag := f.Tag.Get("json")
			if tag == "" || strings.HasPrefix(tag, ",") {
				t.Errorf("%s.%s has no explicit json name (%q)", typ.Name(), f.Name, tag)
			}
			if frozenDigestFields[f.Name] {
				continue
			}
			if !strings.Contains(tag, ",omitempty") {
				t.Errorf("%s.%s is not in the frozen field set and lacks omitempty: adding it would change every existing digest", typ.Name(), f.Name)
			}
		}
	}
}
