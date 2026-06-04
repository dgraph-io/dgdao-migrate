package migrate

import (
	"fmt"
	"sort"
	"strings"
)

// diffSchema compares two canonical schema strings (MarshalSchema output) and
// classifies every predicate-level difference. Both sides are already sorted and
// deterministic, so this is a line-keyed set difference rather than a semantic
// graph diff. Type blocks ("type T { ... }") are ignored: predicate membership
// is implied by the predicate declarations themselves.
func diffSchema(prevState, current string) Delta {
	prev := parsePredicates(prevState)
	cur := parsePredicates(current)

	var d Delta
	for pred, line := range cur {
		prevLine, ok := prev[pred]
		if !ok {
			d.Added = append(d.Added, line)
			continue
		}
		if line == prevLine {
			continue
		}
		// A scalar retype is destructive (flag, never emit); any other
		// declaration change is additively re-appliable via EnsureSchema.
		if predType(line) != predType(prevLine) {
			d.TypeChanged = append(d.TypeChanged, fmt.Sprintf("%s: %s → %s", pred, predType(prevLine), predType(line)))
		} else {
			d.IndexChanged = append(d.IndexChanged, line)
		}
	}
	for pred, line := range prev {
		if _, ok := cur[pred]; !ok {
			d.Removed = append(d.Removed, line)
		}
	}

	sort.Strings(d.Added)
	sort.Strings(d.IndexChanged)
	sort.Strings(d.TypeChanged)
	sort.Strings(d.Removed)
	return d
}

// parsePredicates maps each predicate name to its full declaration line. A
// predicate line is "<name>: <type> [@directives] ."; the name is the token
// before the first ':'. Type blocks and their member lines carry no ':' and are
// skipped.
func parsePredicates(schema string) map[string]string {
	out := make(map[string]string)
	for _, raw := range strings.Split(schema, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		pred := line[:colon]
		if strings.ContainsAny(pred, " \t{}") {
			continue // not a "<name>:" predicate declaration
		}
		out[pred] = line
	}
	return out
}

// predType returns the scalar/edge type token: the first field after the ':'.
// e.g. "size: int ." -> "int", "friends: [uid] @reverse ." -> "[uid]".
func predType(line string) string {
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return ""
	}
	fields := strings.Fields(line[colon+1:])
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
