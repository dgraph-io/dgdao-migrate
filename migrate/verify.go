package migrate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	mg "github.com/dgraph-io/dgdao"
)

// Drift is the result of Verify: predicates the current structs declare that the
// live schema is missing entirely (Missing) or defines differently (Mismatched).
// It is one-directional — predicates present only in the database are ignored,
// since the database always carries system and migration-bookkeeping predicates
// the application structs do not.
//
// Mismatched is only reported when the live schema exposes predicate
// declarations (a real Dgraph cluster). The embedded file:// engine returns only
// type membership, so against it Verify catches Missing predicates but cannot
// compare type/index definitions.
type Drift struct {
	Missing    []string // declarations the structs want but the live schema lacks
	Mismatched []string // "predicate: want <decl> | live <decl>" for differing definitions
}

// Clean reports whether the live schema satisfies every predicate the structs
// declare.
func (d Drift) Clean() bool { return len(d.Missing)+len(d.Mismatched) == 0 }

// Verify compares the schema the given models declare against the database's
// live schema, reporting any predicate the database is missing or defines
// differently. Use it as a post-migration drift gate: after `migrate up`, the
// live schema must satisfy the current structs.
func Verify(ctx context.Context, c mg.Client, models []any) (Drift, error) {
	want, err := MarshalSchema(models...)
	if err != nil {
		return Drift{}, err
	}
	wantDecls, wantNames := schemaPredicates(want)
	live, err := c.GetSchema(ctx)
	if err != nil {
		return Drift{}, fmt.Errorf("migrate: reading live schema: %w", err)
	}
	gotDecls, gotNames := schemaPredicates(live)

	names := make([]string, 0, len(wantNames))
	for p := range wantNames {
		names = append(names, p)
	}
	sort.Strings(names)

	var d Drift
	for _, p := range names {
		if !gotNames[p] {
			if decl, ok := wantDecls[p]; ok {
				d.Missing = append(d.Missing, decl)
			} else {
				d.Missing = append(d.Missing, p)
			}
			continue
		}
		want, wok := wantDecls[p]
		got, gok := gotDecls[p]
		if wok && gok && normalizeDecl(want) != normalizeDecl(got) {
			d.Mismatched = append(d.Mismatched, fmt.Sprintf("%s: want %q | live %q", p, want, got))
		}
	}
	return d, nil
}

// schemaPredicates parses a schema string into its predicate declarations
// (name → full line, present only where the source exposes them) and the full
// set of predicate names (declarations plus type-block members). Splitting the
// two lets Verify work against both a real Dgraph (declarations available) and
// the embedded engine (type membership only).
func schemaPredicates(schema string) (decls map[string]string, names map[string]bool) {
	decls = make(map[string]string)
	names = make(map[string]bool)
	inType := false
	for _, raw := range strings.Split(schema, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "type ") && strings.HasSuffix(line, "{"):
			inType = true
			continue
		case line == "}":
			inType = false
			continue
		}
		if colon := strings.IndexByte(line, ':'); colon > 0 {
			pred := strings.TrimSpace(line[:colon])
			if pred != "" && !strings.ContainsAny(pred, " \t{}") {
				decls[pred] = line
				names[pred] = true
			}
			continue
		}
		if inType {
			if fields := strings.Fields(line); len(fields) > 0 {
				names[fields[0]] = true
			}
		}
	}
	return decls, names
}

// normalizeDecl reduces a predicate declaration to its semantic identity — type,
// sorted index tokenizers, and sorted non-index directives — so that two
// renderings of the same predicate (canonical vs. Dgraph-live) compare equal
// regardless of tokenizer order or directive spacing.
func normalizeDecl(line string) string {
	typ := predType(line)

	var tokenizers []string
	if i := strings.Index(line, "@index("); i >= 0 {
		if j := strings.IndexByte(line[i:], ')'); j >= 0 {
			inner := line[i+len("@index(") : i+j]
			for _, tk := range strings.Split(inner, ",") {
				if tk = strings.TrimSpace(tk); tk != "" {
					tokenizers = append(tokenizers, tk)
				}
			}
		}
	}
	sort.Strings(tokenizers)

	var directives []string
	for _, f := range strings.Fields(line) {
		if !strings.HasPrefix(f, "@") || strings.HasPrefix(f, "@index") {
			continue
		}
		if p := strings.IndexByte(f, '('); p >= 0 {
			f = f[:p]
		}
		directives = append(directives, f)
	}
	sort.Strings(directives)

	return fmt.Sprintf("%s|index(%s)|%s", typ, strings.Join(tokenizers, ","), strings.Join(directives, ","))
}
