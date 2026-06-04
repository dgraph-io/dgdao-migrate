package migrate

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	dg "github.com/dolan-in/dgman/v2"

	mg "github.com/matthewmcneely/modusgraph"
)

// MarshalSchema renders struct templates to a canonical Dgraph Schema Definition
// Language string — the frozen counterpart to a SchemaChange.Ensure list.
//
// Capture the result once at authoring time and store it in
// SchemaChange.EnsureSchema. Because the string is then applied and checksummed
// verbatim, the migration's schema never drifts as the live struct definitions
// evolve — unlike Ensure, which re-derives both at run time. This is what makes
// a baseline (or any additive schema in a shipped migration) reproducible and
// immutable.
//
// The output is deterministic: predicates and types are sorted into a fixed
// order so it is stable across runs regardless of Go map iteration order, and it
// is byte-identical to the rendering the runner checksums for an equivalent
// Ensure step at the same moment. Models are unwrapped (mg.UnwrapSchema) to match
// the schema client.UpdateSchema derives, so a frozen EnsureSchema and the live
// Ensure it was captured from apply the same predicates.
//
// MarshalSchema returns an error when two structs declare the same predicate
// with different directives. Dgraph stores a single definition per predicate —
// directives like @index and @reverse are properties of the predicate, not of
// an individual edge — so every struct that declares a predicate must give it
// identical directives. Rather than silently pick one declaration (which would
// hide the disagreement and make a later edit to the "losing" struct invisible),
// MarshalSchema reports the conflict and names the structs on each side.
func MarshalSchema(models ...any) (string, error) {
	merged := dg.NewTypeSchema()

	// variantsByPredicate maps a predicate to each distinct declaration string
	// seen for it, and the struct names that produced that declaration.
	variantsByPredicate := make(map[string]map[string][]string)

	for _, m := range models {
		name := modelName(m)
		ts := dg.NewTypeSchema()
		ts.Marshal("", mg.UnwrapSchema(m))

		for pred, s := range ts.Schema {
			decl := strings.TrimSpace(s.String())
			if variantsByPredicate[pred] == nil {
				variantsByPredicate[pred] = make(map[string][]string)
			}
			variantsByPredicate[pred][decl] = append(variantsByPredicate[pred][decl], name)
			merged.Schema[pred] = s
		}
		for typeName, preds := range ts.Types {
			if merged.Types[typeName] == nil {
				merged.Types[typeName] = make(dg.SchemaMap, len(preds))
			}
			for pn, s := range preds {
				merged.Types[typeName][pn] = s
			}
		}
	}

	if err := schemaConflictError(variantsByPredicate); err != nil {
		return "", err
	}
	return canonicalTypeSchema(merged), nil
}

// schemaConflictError returns a non-nil error when any predicate has more than
// one distinct declaration. The message names each conflicting predicate, lists
// every declaration variant, and names the structs that produced it.
func schemaConflictError(variantsByPredicate map[string]map[string][]string) error {
	var conflicted []string
	for pred, variants := range variantsByPredicate {
		if len(variants) > 1 {
			conflicted = append(conflicted, pred)
		}
	}
	if len(conflicted) == 0 {
		return nil
	}
	sort.Strings(conflicted)

	var b strings.Builder
	b.WriteString("migrate: schema declarations must agree across structs:")
	for _, pred := range conflicted {
		fmt.Fprintf(&b, "\n  predicate %q is declared inconsistently:", pred)
		variants := variantsByPredicate[pred]
		decls := make([]string, 0, len(variants))
		for d := range variants {
			decls = append(decls, d)
		}
		sort.Strings(decls)
		for _, d := range decls {
			structs := append([]string(nil), variants[d]...)
			sort.Strings(structs)
			fmt.Fprintf(&b, "\n    %s  (in %s)", d, strings.Join(structs, ", "))
		}
	}
	return errors.New(b.String())
}

// modelName returns the struct type name behind a schema model value (deref'ing
// pointers), for naming structs in a conflict error. It falls back to the Go
// type string when there is no simple name.
func modelName(m any) string {
	t := reflect.TypeOf(m)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return "?"
	}
	if t.Name() != "" {
		return t.Name()
	}
	return t.String()
}
