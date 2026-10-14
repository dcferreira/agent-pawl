package journal

import (
	"encoding/json"
	"strings"
	"testing"
)

const bigInt = "9007199254740993" // 2^53 + 1: not representable as a float64

func TestParseEvents_NumbersReplayAsJSONNumber(t *testing.T) {
	data := `{"kind":"RUN_START","run_id":"r","seq":0,"time":"2026-01-01T00:00:00Z","args":{"n":` + bigInt + `}}` + "\n" +
		`{"kind":"WRITES","run_id":"r","seq":1,"time":"2026-01-01T00:00:00Z","step":"a","writes":{"n":` + bigInt + `,"m":1000000}}` + "\n" +
		`{"kind":"DISPATCH","run_id":"r","seq":2,"time":"2026-01-01T00:00:00Z","step":"a","items":[` + bigInt + `]}` + "\n"
	events, err := ParseEvents([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if got := events[0].Args["n"]; got != json.Number(bigInt) {
		t.Errorf("Args[n] = %#v, want json.Number(%s)", got, bigInt)
	}
	if got := events[1].Writes["n"]; got != json.Number(bigInt) {
		t.Errorf("Writes[n] = %#v, want json.Number(%s)", got, bigInt)
	}
	if got := events[1].Writes["m"]; got != json.Number("1000000") {
		t.Errorf("Writes[m] = %#v, want json.Number(1000000)", got)
	}
	if got := events[2].Items[0]; got != json.Number(bigInt) {
		t.Errorf("Items[0] = %#v, want json.Number(%s)", got, bigInt)
	}
}

func TestReplay_BigIntStateExact(t *testing.T) {
	data := `{"kind":"RUN_START","run_id":"r","seq":0,"time":"2026-01-01T00:00:00Z","args":{"a":` + bigInt + `}}` + "\n" +
		`{"kind":"STEP_ENTER","run_id":"r","seq":1,"time":"2026-01-01T00:00:00Z","step":"s","attempt":1}` + "\n" +
		`{"kind":"WRITES","run_id":"r","seq":2,"time":"2026-01-01T00:00:00Z","step":"s","attempt":1,"writes":{"n":` + bigInt + `}}` + "\n"
	events, err := ParseEvents([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if rs.State["n"] != json.Number(bigInt) {
		t.Errorf("State[n] = %#v, want json.Number(%s)", rs.State["n"], bigInt)
	}
	if rs.Args["a"] != json.Number(bigInt) {
		t.Errorf("Args[a] = %#v, want json.Number(%s)", rs.Args["a"], bigInt)
	}
}

func TestParseEvents_RejectsTrailingGarbageAndCorrupt(t *testing.T) {
	good := `{"kind":"RUN_START","run_id":"r","seq":0,"time":"2026-01-01T00:00:00Z"}`
	for name, line := range map[string]string{
		"trailing garbage": good + ` xyz`,
		"second object":    good + good,
		"corrupt":          `{"kind":"RUN_START",`,
		"not an object":    `[1,2]`,
	} {
		_, err := ParseEvents([]byte(line + "\n"))
		if err == nil || !strings.Contains(err.Error(), "corrupt record") {
			t.Errorf("%s: err = %v, want a corrupt-record error", name, err)
		}
	}
}
