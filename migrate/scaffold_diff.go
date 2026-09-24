package migrate

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// diffSchema compares two canonical schema strings (MarshalSchema output) and
// classifies every predicate and type difference. Both sides are already sorted
// and deterministic, so this is a keyed set difference rather than a semantic
// graph diff. Type membership is compared separately from predicate
// declarations, because Dgraph stores each type's field list on its own.
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

	diffTypes(&d, parseTypes(prevState), parseTypes(current))

	sort.Strings(d.Added)
	sort.Strings(d.IndexChanged)
	sort.Strings(d.TypeChanged)
	sort.Strings(d.Removed)
	return d
}

// diffTypes fills the type buckets of d. Added and changed types carry their
// full current definition, since Dgraph replaces a whole type on alter.
func diffTypes(d *Delta, prev, cur map[string][]string) {
	for name, fields := range cur {
		prevFields, ok := prev[name]
		switch {
		case !ok:
			d.TypesAdded = append(d.TypesAdded, typeDefinition(name, fields))
		case !slices.Equal(fields, prevFields):
			d.TypesChanged = append(d.TypesChanged, typeDefinition(name, fields))
			for _, f := range prevFields {
				if !slices.Contains(fields, f) {
					d.TypeFieldsRemoved = append(d.TypeFieldsRemoved, name+"."+f)
				}
			}
		}
	}
	for name := range prev {
		if _, ok := cur[name]; !ok {
			d.TypesRemoved = append(d.TypesRemoved, name)
		}
	}
	sort.Strings(d.TypesAdded)
	sort.Strings(d.TypesChanged)
	sort.Strings(d.TypesRemoved)
	sort.Strings(d.TypeFieldsRemoved)
}

// parseTypes maps each "type T { ... }" block to its sorted field names. It
// accepts both the canonical rendering and the live one (tab-indented fields,
// reverse edges possibly wrapped in angle brackets).
func parseTypes(schema string) map[string][]string {
	out := make(map[string][]string)
	current := ""
	inType := false
	for _, raw := range strings.Split(schema, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "type ") && strings.HasSuffix(line, "{"):
			current = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "type "), "{"))
			out[current] = nil
			inType = true
		case line == "}":
			if inType {
				sort.Strings(out[current])
			}
			inType = false
		case inType:
			if fields := strings.Fields(line); len(fields) > 0 {
				f := strings.TrimSuffix(strings.TrimPrefix(fields[0], "<"), ">")
				out[current] = append(out[current], f)
			}
		}
	}
	return out
}

// typeDefinition renders a type block exactly as canonicalTypeSchema does.
func typeDefinition(name string, fields []string) string {
	var b strings.Builder
	b.WriteString("type " + name + " {")
	for _, f := range fields {
		b.WriteString("\n" + f)
	}
	b.WriteString("\n}")
	return b.String()
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
