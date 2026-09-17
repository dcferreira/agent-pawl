package engine

import "testing"

// TestAfterTransition_TerminalMessageSeesRealVisits guards against
// afterTransition hardcoding visits to 0 when rendering a terminal
// message: design/format-spec.md §B.2 lists `visits` as a pseudo-key
// "readable everywhere", explicitly including terminal `message:`, so a
// loop that visits its step N times before reaching a terminal must render
// `${visits}` as N there, not 0 (fix round 1 on Task 8's dogfood example,
// where "Suite green after ${visits} runs." always read 0).
func TestAfterTransition_TerminalMessageSeesRealVisits(t *testing.T) {
	const yaml = `
workflow: visits-terminal
start: loop
steps:
  - id: loop
    kind: deterministic
    run: |
      n=$(cat count.txt 2>/dev/null || echo 0)
      n=$((n+1))
      echo $n > count.txt
      if [ "$n" -lt 3 ]; then echo "continue"; else echo "stop"; fi
    emits: pairs
    max_visits: 5
    outcomes:
      continue: loop
      stop: done
terminal:
  done: {status: ok, message: "finished after ${visits} loops"}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok {
		t.Fatalf("Start returned %T, want Terminal", instr)
	}
	if want := "finished after 3 loops"; term.Message != want {
		t.Errorf("terminal message = %q, want %q", term.Message, want)
	}
}
