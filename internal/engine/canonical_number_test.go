package engine

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/render"
)

func TestCanonicalNumber(t *testing.T) {
	cases := []struct {
		name   string
		in     any
		want   string
		wantOK bool
	}{
		{"int", 1000000, "1000000", true},
		{"int negative", -5, "-5", true},
		{"int64", int64(1000000), "1000000", true},
		{"uint64 max", uint64(18446744073709551615), "18446744073709551615", true},
		{"float64 integral", float64(1000000), "1000000", true},
		{"float64 fractional", 2.5, "2.5", true},
		{"float64 negative zero", func() any { z := 0.0; return -z }(), "0", true},
		{"json plain", json.Number("1000000"), "1000000", true},
		{"json exponent", json.Number("1e6"), "1000000", true},
		{"json decimal", json.Number("1000000.0"), "1000000", true},
		{"json negative zero", json.Number("-0"), "0", true},
		{"json zero decimal", json.Number("0.0"), "0", true},
		{"json big exact", json.Number("9007199254740993"), "9007199254740993", true},
		{"json huge int exact", json.Number("123456789012345678901234567890"), "123456789012345678901234567890", true},
		{"json negative big", json.Number("-9007199254740993"), "-9007199254740993", true},
		{"json fractional", json.Number("2.50"), "2.5", true},
		{"json not a number", json.Number("abc"), "", false},
		{"string is not numeric", "5", "", false},
		{"bool is not numeric", true, "", false},
		{"nil is not numeric", nil, "", false},
	}
	for _, c := range cases {
		got, ok := canonicalNumber(c.in)
		if ok != c.wantOK || got != c.want {
			t.Errorf("%s: canonicalNumber(%#v) = %q, %v; want %q, %v", c.name, c.in, got, ok, c.want, c.wantOK)
		}
	}
}

// Hostile exponents from step stdout must neither hang nor panic.
func TestCanonicalNumber_HostileExponentsReturnQuickly(t *testing.T) {
	for _, s := range []string{"1e999999999", "-1e999999999", "1e-999999999", "0e999999999", "1e99999999999999999999"} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			canonicalNumber(json.Number(s))
			isEmptyValue(json.Number(s))
			evaluateEquals(map[string]any{"n": 1}, map[string]any{"n": json.Number(s)}, render.Values{}, false)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("canonicalNumber(%q) did not return within 2s", s)
		}
	}
}
