package spec

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func decodeWrites(t *testing.T, doc string) Writes {
	t.Helper()
	var w Writes
	if err := yaml.Unmarshal([]byte(doc), &w); err != nil {
		t.Fatalf("unmarshal %q: %v", doc, err)
	}
	return w
}

func TestWrites_List(t *testing.T) {
	w := decodeWrites(t, "[branch, title]\n")
	if w.IsTyped {
		t.Fatalf("expected untyped, got %+v", w)
	}
	if !reflect.DeepEqual(w.Keys, []string{"branch", "title"}) {
		t.Fatalf("got %+v", w.Keys)
	}
}

func TestWrites_TypedMap(t *testing.T) {
	w := decodeWrites(t, "comment_count: {type: integer}\nfindings: {type: json}\n")
	if !w.IsTyped {
		t.Fatalf("expected typed, got %+v", w)
	}
	if w.Types["comment_count"] != "integer" || w.Types["findings"] != "json" {
		t.Fatalf("got %+v", w.Types)
	}
}
