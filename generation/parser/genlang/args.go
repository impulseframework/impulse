package genlang

import (
	"slices"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
)

// ArgSpec describes one keyword invocation's argument shape: how many leading
// positional values it takes, and which named (key: value) arguments it
// accepts or requires. It is the shared argument-parsing layer for keywords
// with structured argument lists — one place for arity, unknown-key, and
// duplicate-key errors instead of per-keyword string splitting.
type ArgSpec struct {
	// Positional is the exact number of leading positional values, or their
	// maximum when OptionalPositional is set.
	Positional int

	// OptionalPositional admits fewer positional values than Positional, none
	// included: a keyword whose one positional value has a default.
	OptionalPositional bool

	// Keys is the set of accepted named-argument keys.
	Keys []string

	// Required is the subset of Keys that must appear.
	Required []string

	// Multi is the subset of Keys that accept a value list: bare values
	// following the key attach to it (`from: a, b` reads as from = [a, b])
	// until the next named argument.
	Multi []string

	// Expressions is the subset of Keys whose value is an expression of the
	// condition language, not a name: the commas inside its parentheses and
	// its single-quoted strings are its own (`where: Level IN ('view', 'edit')`).
	// An invocation of a spec that names one is split on the commas outside
	// parentheses and outside quotes of either kind.
	Expressions []string
}

// ScanFlags returns the flags a keyword whose arguments the spec parses is scanned
// with, to be joined to the keyword's own: Expressions where the spec names an
// expression key. A keyword table that takes them from the spec has the scanner
// end the keyword's annotation by the quotes the spec splits its arguments by.
func (s *ArgSpec) ScanFlags() keywordFlag {
	if s.takesExpressions() {
		return Expressions
	}

	return 0
}

// takesExpressions reports whether an argument of the spec may be an expression of
// the condition language.
func (s *ArgSpec) takesExpressions() bool {
	return len(s.Expressions) > 0
}

// NamedArgs is one keyword invocation's parsed argument list: the leading
// positional values in order, then the named arguments.
type NamedArgs struct {
	Positional []string
	named      map[string][]string
}

// Named returns the value of a named argument and whether it appeared. For a
// multi-valued key it returns the first value; use List for all of them.
func (n NamedArgs) Named(key string) (string, bool) {
	values, ok := n.named[key]
	if !ok || len(values) == 0 {
		return "", false
	}

	return values[0], true
}

// List returns every value of a named argument, in declaration order.
func (n NamedArgs) List(key string) []string {
	return n.named[key]
}

// ParseInvocations parses every invocation of a keyword against spec — one
// NamedArgs per use of the keyword on the same subject. Positional values
// come first, named arguments (key: value) after; an empty value, an unknown
// or duplicate key, a missing required key, or a wrong positional count is an
// error.
func (a Arg) ParseInvocations(spec *ArgSpec) ([]NamedArgs, error) {
	invocations := make([]NamedArgs, 0, a.Count())
	for invocation := range a.Seq() {
		parsed, err := parseInvocation(invocation, spec)
		if err != nil {
			return nil, err
		}
		invocations = append(invocations, parsed)
	}

	return invocations, nil
}

func parseInvocation(invocation string, spec *ArgSpec) (NamedArgs, error) {
	args := NamedArgs{named: make(map[string][]string)}

	parts, err := splitArguments(invocation, spec.takesExpressions())
	if err != nil {
		return NamedArgs{}, err
	}

	var lastKey string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return NamedArgs{}, errors.Newf("empty argument in %q", invocation)
		}

		key, value, isNamed := cutNamed(part)
		if !isNamed {
			value, err := unquote(part, invocation)
			if err != nil {
				return NamedArgs{}, err
			}
			if len(args.named) > 0 {
				// A bare value after a named argument continues the preceding
				// key's list when that key is multi-valued.
				if lastKey != "" && slices.Contains(spec.Multi, lastKey) {
					args.named[lastKey] = append(args.named[lastKey], value)

					continue
				}

				return NamedArgs{}, errors.Newf("positional argument %q after a named argument in %q", part, invocation)
			}
			args.Positional = append(args.Positional, value)

			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch {
		case key == "":
			return NamedArgs{}, errors.Newf("named argument with no key in %q", invocation)
		case value == "":
			return NamedArgs{}, errors.Newf("named argument %q with no value in %q", key, invocation)
		case !slices.Contains(spec.Keys, key):
			return NamedArgs{}, errors.Newf("unknown argument %q in %q (accepted: %s)", key, invocation, strings.Join(spec.Keys, ", "))
		}
		if _, dup := args.named[key]; dup {
			return NamedArgs{}, errors.Newf("argument %q given twice in %q", key, invocation)
		}
		value, err := unquote(value, invocation)
		if err != nil {
			return NamedArgs{}, err
		}
		args.named[key] = []string{value}
		lastKey = key
	}

	switch {
	case spec.OptionalPositional && len(args.Positional) > spec.Positional:
		return NamedArgs{}, errors.Newf("expected at most %d positional argument(s), found %d in %q", spec.Positional, len(args.Positional), invocation)
	case !spec.OptionalPositional && len(args.Positional) != spec.Positional:
		return NamedArgs{}, errors.Newf("expected %d positional argument(s), found %d in %q", spec.Positional, len(args.Positional), invocation)
	}
	for _, required := range spec.Required {
		if _, ok := args.named[required]; !ok {
			return NamedArgs{}, errors.Newf("missing required argument %q in %q", required, invocation)
		}
	}

	return args, nil
}

// splitArguments splits an invocation on the commas outside double quotes, so a
// quoted value may carry a comma (`from: "a,b"`). An unclosed quote is an error.
//
// With expressions set, an argument may be an expression of the condition
// language, and the commas inside its parentheses and its single-quoted strings
// stay with it: `where: Level IN ('view', 'edit'), value: Team` is two arguments.
// What is quoted is decided by quotes, which the scanner ends an annotation by too.
func splitArguments(invocation string, expressions bool) ([]string, error) {
	var (
		parts []string
		start int
		depth int
	)
	quoted := newQuotes(expressions)
	for i := range len(invocation) {
		switch c := invocation[i]; {
		case quoted.read(c):
		case expressions && c == '(':
			depth++
		case expressions && c == ')':
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, invocation[start:i])
			start = i + 1
		}
	}
	if _, _, open := quoted.unclosed(); open {
		return nil, errors.Newf("unclosed quote in %q", invocation)
	}

	return append(parts, invocation[start:]), nil
}

// cutNamed splits a `key: value` argument at its first colon. A part that opens with a
// quote is a quoted positional value, whatever it contains.
func cutNamed(part string) (key, value string, isNamed bool) {
	if strings.HasPrefix(part, `"`) {
		return "", "", false
	}

	return strings.Cut(part, ":")
}

// unquote reads a value written in double quotes (`"geojson"`, `"@scope/pkg"`) as the
// string inside them, so a value may carry characters the bare syntax would read as
// structure; a bare value is returned as written.
func unquote(value, invocation string) (string, error) {
	if !strings.HasPrefix(value, `"`) {
		return value, nil
	}
	unquoted, err := strconv.Unquote(value)
	if err != nil {
		return "", errors.Newf("malformed quoted value %s in %q: %v", value, invocation, err)
	}

	return unquoted, nil
}
