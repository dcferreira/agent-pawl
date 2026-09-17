// Package render implements ${key} substitution for workflow templates, per
// design/format-spec.md §B.2. It has no dependency on any other package in this
// module: callers build a Values map from whatever they parsed and hand it in.
package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Value is one substitutable entry: either a plain string, or a value that
// should be rendered as JSON (pretty-printed in prose mode, compact in shell
// and env-var contexts).
type Value struct {
	str    string
	json   any
	isJSON bool
}

// StringValue wraps a plain string for substitution.
func StringValue(s string) Value {
	return Value{str: s}
}

// JSONValue wraps a value that renders as JSON: pretty-printed (two-space
// indent) in prose mode, compact in shell and env-var contexts.
func JSONValue(v any) Value {
	return Value{json: v, isJSON: true}
}

// Values is the set of substitutable entries available to a template, keyed
// by the name written between "${" and "}". It carries no notion of
// pseudo-keys or declared keys — every entry is ordinary as far as this
// package is concerned.
type Values map[string]Value

func (v Value) compact() (string, error) {
	if !v.isJSON {
		return v.str, nil
	}
	b, err := json.Marshal(v.json)
	if err != nil {
		return "", fmt.Errorf("render: marshaling json value: %w", err)
	}
	return string(b), nil
}

func (v Value) prose() (string, error) {
	if !v.isJSON {
		return v.str, nil
	}
	b, err := json.MarshalIndent(v.json, "", "  ")
	if err != nil {
		return "", fmt.Errorf("render: marshaling json value: %w", err)
	}
	return string(b), nil
}

// shellQuote wraps s as a single shell-quoted token, escaping any embedded
// single quotes. Every value is quoted, even one with no special characters:
// the simplest correct behaviour wins.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func declaredKeys(vals Values) string {
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "(none)"
	}
	return strings.Join(keys, ", ")
}

// render scans tmpl for "${key}" substitutions, formatting each looked-up
// Value with format. "$${" renders a literal "${" and consumes no key.
func render(tmpl string, vals Values, format func(Value) (string, error)) (string, error) {
	var b strings.Builder
	i := 0
	for i < len(tmpl) {
		rest := tmpl[i:]
		if strings.HasPrefix(rest, "$${") {
			b.WriteString("${")
			i += 3
			continue
		}
		if strings.HasPrefix(rest, "${") {
			closeIdx := strings.IndexByte(tmpl[i+2:], '}')
			if closeIdx == -1 {
				return "", fmt.Errorf("render: unterminated \"${\" at offset %d", i)
			}
			key := tmpl[i+2 : i+2+closeIdx]
			v, ok := vals[key]
			if !ok {
				return "", fmt.Errorf("render: unknown key %q (declared keys: %s)", key, declaredKeys(vals))
			}
			s, err := format(v)
			if err != nil {
				return "", err
			}
			b.WriteString(s)
			i += 2 + closeIdx + 1
			continue
		}
		b.WriteByte(tmpl[i])
		i++
	}
	return b.String(), nil
}

// RenderShell substitutes each ${key} in tmpl as one shell-quoted token,
// suitable for interpolation into a command line. It is the only place in
// this codebase that decides how a value reaches a shell command.
func RenderShell(tmpl string, vals Values) (string, error) {
	return render(tmpl, vals, func(v Value) (string, error) {
		s, err := v.compact()
		if err != nil {
			return "", err
		}
		return shellQuote(s), nil
	})
}

// RenderProse substitutes each ${key} in tmpl with the raw value; a
// JSONValue entry is pretty-printed with a two-space indent.
func RenderProse(tmpl string, vals Values) (string, error) {
	return render(tmpl, vals, func(v Value) (string, error) {
		return v.prose()
	})
}

// Keys returns the set of keys tmpl reads via "${key}", in order of first
// appearance. "$${" consumes no key. An unterminated "${" stops the scan at
// that point; RenderShell and RenderProse are what report it as an error.
func Keys(tmpl string) []string {
	var keys []string
	seen := make(map[string]bool)
	i := 0
	for i < len(tmpl) {
		rest := tmpl[i:]
		if strings.HasPrefix(rest, "$${") {
			i += 3
			continue
		}
		if strings.HasPrefix(rest, "${") {
			closeIdx := strings.IndexByte(tmpl[i+2:], '}')
			if closeIdx == -1 {
				break
			}
			key := tmpl[i+2 : i+2+closeIdx]
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
			i += 2 + closeIdx + 1
			continue
		}
		i++
	}
	return keys
}

// EnvFor returns "WF_<UPPERCASED_KEY>=<value>" pairs for each of keys that
// has an entry in vals, in the given key order. A JSONValue entry is
// exported as compact JSON. Keys absent from vals are silently skipped.
func EnvFor(vals Values, keys []string) []string {
	env := make([]string, 0, len(keys))
	for _, k := range keys {
		v, ok := vals[k]
		if !ok {
			continue
		}
		s, err := v.compact()
		if err != nil {
			continue
		}
		env = append(env, fmt.Sprintf("WF_%s=%s", strings.ToUpper(k), s))
	}
	return env
}
