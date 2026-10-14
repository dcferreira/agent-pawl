package journal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

// decodePlan decodes plan.json bytes of any readable format. A plan with a
// top-level "format" key is current-shaped (explicit snake_case tags) and
// decodes directly; one without is format 1, whose workflow object used the
// Go field names of the untagged spec types and goes through
// renameLegacyKeys first.
func decodePlan(data []byte) (*Plan, error) {
	var probe struct {
		Format *int `json:"format"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("journal: parsing plan.json: %w", err)
	}
	if probe.Format != nil {
		if err := checkFormat(*probe.Format); err != nil {
			return nil, err
		}
		var plan Plan
		if err := unmarshalUseNumber(data, &plan); err != nil {
			return nil, fmt.Errorf("journal: parsing plan.json: %w", err)
		}
		return &plan, nil
	}

	var generic any
	if err := unmarshalUseNumber(data, &generic); err != nil {
		return nil, fmt.Errorf("journal: parsing plan.json: %w", err)
	}
	top, ok := generic.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("journal: parsing plan.json: expected an object")
	}
	if wf, ok := top["workflow"]; ok {
		top["workflow"] = renameLegacyKeys(wf, reflect.TypeOf(spec.Workflow{}))
	}
	converted, err := json.Marshal(top)
	if err != nil {
		return nil, fmt.Errorf("journal: re-encoding legacy plan.json: %w", err)
	}
	var plan Plan
	if err := unmarshalUseNumber(converted, &plan); err != nil {
		return nil, fmt.Errorf("journal: parsing legacy plan.json: %w", err)
	}
	plan.Format = NormalizeFormat(0)
	return &plan, nil
}

// unmarshalUseNumber decodes data into v keeping numbers in interface-typed
// fields as json.Number, so integers above 2^53 survive the plan.json round
// trip instead of being rounded through float64 (which would make Digest of
// the pinned workflow differ from Digest of the unchanged file).
func unmarshalUseNumber(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("unexpected data after top-level value")
	}
	return nil
}

// renameLegacyKeys walks the generic JSON value v alongside the Go type t it
// will decode into, renaming every object key that equals a STRUCT FIELD's Go
// name to that field's json tag name.
//
// Why a reflection walk and not a global key rewrite: format-1 plans carry
// user data both as map keys (state/arg/terminal names, outcomes, writes
// Types/MaxLength, postcondition Equals keys) and as interface values
// (SubagentArgs, defaults, Equals values). A user's state key may legitimately
// be spelled "Path" or "Attempts". So only keys that sit directly in a
// struct-typed object are candidates; map keys are never touched, and
// interface-typed values are returned untouched without being walked.
//
// encoding/json's case-insensitive matching already decodes single-word
// fields (ID, Kind...), but a multi-word field such as MaxStepsRaw would
// silently decode to its zero value under the snake_case tag; this walk is
// what prevents that. A key that already equals the tag name (GuardDecl was
// tagged in format 1 too) is left as is.
func renameLegacyKeys(v any, t reflect.Type) any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Interface:
		return v
	case reflect.Struct:
		obj, ok := v.(map[string]any)
		if !ok {
			return v
		}
		type field struct {
			tag string
			typ reflect.Type
		}
		byGoName := map[string]field{}
		byTag := map[string]field{}
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			tag := strings.Split(sf.Tag.Get("json"), ",")[0]
			if tag == "-" {
				continue
			}
			if tag == "" {
				tag = sf.Name
			}
			f := field{tag: tag, typ: sf.Type}
			byGoName[sf.Name] = f
			byTag[tag] = f
		}
		out := make(map[string]any, len(obj))
		for k, val := range obj {
			if f, ok := byGoName[k]; ok {
				out[f.tag] = renameLegacyKeys(val, f.typ)
			} else if f, ok := byTag[k]; ok {
				out[k] = renameLegacyKeys(val, f.typ)
			} else {
				out[k] = val
			}
		}
		return out
	case reflect.Slice, reflect.Array:
		arr, ok := v.([]any)
		if !ok {
			return v
		}
		out := make([]any, len(arr))
		for i, e := range arr {
			out[i] = renameLegacyKeys(e, t.Elem())
		}
		return out
	case reflect.Map:
		obj, ok := v.(map[string]any)
		if !ok {
			return v
		}
		out := make(map[string]any, len(obj))
		for k, e := range obj {
			out[k] = renameLegacyKeys(e, t.Elem())
		}
		return out
	}
	return v
}
