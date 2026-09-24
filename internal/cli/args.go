package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

// runFlags holds pawl run's own flags, as opposed to key=value arg bindings
// (design/format-spec.md §I).
type runFlags struct {
	Fresh         bool
	Force         bool
	RunID         string
	NoEnforcement bool
}

// parseRunArgs splits pawl run's trailing arguments into key=value bindings
// and flags.
func parseRunArgs(args []string) (raw map[string]string, flags runFlags, err error) {
	raw = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--fresh":
			flags.Fresh = true
		case a == "--force":
			flags.Force = true
		case a == "--no-enforcement":
			flags.NoEnforcement = true
		case a == "--run":
			i++
			if i >= len(args) {
				return nil, flags, fmt.Errorf("pawl run: --run needs a value")
			}
			flags.RunID = args[i]
		case strings.HasPrefix(a, "--run="):
			flags.RunID = strings.TrimPrefix(a, "--run=")
		case strings.Contains(a, "="):
			kv := strings.SplitN(a, "=", 2)
			raw[kv[0]] = kv[1]
		default:
			return nil, flags, fmt.Errorf("pawl run: unrecognised argument %q", a)
		}
	}
	return raw, flags, nil
}

// argsUsageError carries a one-line reason plus the args: declarations that
// produced it, so the caller renders the full multi-line usage message
// through a guarded blockWriter (writeArgsUsage) rather than a
// pre-formatted string with embedded newlines. Fix round 5: this used to be
// a single fmt.Errorf whose %s-interpolated arg names, types and defaults —
// all workflow-declared, hence author-controlled, never validated for
// format — reached stderr with no guard of any kind, via a bare
// fmt.Fprintln(stderr, err).
type argsUsageError struct {
	Reason string
	Decls  map[string]spec.ArgDecl
}

func (e *argsUsageError) Error() string { return e.Reason }

// bindArgs coerces raw key=value strings to the types args: declares and
// refuses when a required: arg is missing, returning an *argsUsageError so
// the caller can print a usage line naming every arg, its type and its
// default (design/format-spec.md §B.9). A key=value on the command line
// that names no declared arg is also a refusal: silently ignoring it would
// hide a typo.
func bindArgs(decls map[string]spec.ArgDecl, raw map[string]string) (map[string]any, error) {
	out := map[string]any{}
	var missing []string
	for name, decl := range decls {
		v, ok := raw[name]
		if !ok {
			if decl.Required {
				missing = append(missing, name)
			}
			continue
		}
		coerced, err := coerceArgValue(v, decl.Type)
		if err != nil {
			return nil, fmt.Errorf("pawl run: arg %q: %w", name, err)
		}
		out[name] = coerced
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, &argsUsageError{
			Reason: fmt.Sprintf("pawl run: missing required arg(s): %s", strings.Join(missing, ", ")),
			Decls:  decls,
		}
	}
	for k := range raw {
		if _, ok := decls[k]; !ok {
			return nil, &argsUsageError{
				Reason: fmt.Sprintf("pawl run: %q is not a declared arg", k),
				Decls:  decls,
			}
		}
	}
	return out, nil
}

func coerceArgValue(v, declType string) (any, error) {
	switch declType {
	case "integer":
		i, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("value %q does not fit declared type %q", v, declType)
		}
		return i, nil
	case "number":
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("value %q does not fit declared type %q", v, declType)
		}
		return f, nil
	case "boolean":
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("value %q does not fit declared type %q", v, declType)
		}
		return b, nil
	case "json", "string":
		return v, nil
	default:
		return v, nil
	}
}

// formatArgsKV renders a run's bound args as sorted "key=value" pairs, for
// the refusal to silently rebind them on resume (finding I1).
func formatArgsKV(args map[string]any) string {
	if len(args) == 0 {
		return "(no args)"
	}
	names := make([]string, 0, len(args))
	for k := range args {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%s=%v", n, args[n])
	}
	return strings.Join(parts, " ")
}

// writeArgsUsage appends every declared arg, its type and its default
// (design/format-spec.md §B.9's "put what do I pass in at the top of the
// file") to w, for the missing-required-arg/unknown-arg refusal. Each
// pre-formatted "name (type, ...)" string is handed to blockWriter.line as
// a single part, so it is neutralised as a whole regardless of which
// piece — the arg's own name, or its author-declared default — carried
// anything hostile.
func writeArgsUsage(w *blockWriter, decls map[string]spec.ArgDecl) {
	names := make([]string, 0, len(decls))
	for n := range decls {
		names = append(names, n)
	}
	sort.Strings(names)
	w.line(0, "args:")
	for _, n := range names {
		d := decls[n]
		switch {
		case d.Required:
			w.line(1, fmt.Sprintf("%s (%s, required)", n, d.Type))
		case d.Default != nil:
			w.line(1, fmt.Sprintf("%s (%s, default %v)", n, d.Type, *d.Default))
		default:
			w.line(1, fmt.Sprintf("%s (%s)", n, d.Type))
		}
	}
}
