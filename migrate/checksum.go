package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	dg "github.com/dolan-in/dgman/v2"
)

// stepChecksum hashes a step's identity (name + ordinal) and its schema portion
// only — never the Up/Down closures. Alter and EnsureSchema strings are hashed
// raw; Ensure types are hashed via a canonical (sorted) rendering of the dgman
// schema. The switch order matches applySchemaChange.
func stepChecksum(index int, s Step) string {
	h := sha256.New()
	fmt.Fprintf(h, "name:%s\nindex:%d\n", s.Name, index)
	switch {
	case s.Schema.Alter != "":
		h.Write([]byte("alter:" + s.Schema.Alter))
	case s.Schema.EnsureSchema != "":
		h.Write([]byte("ensureSchema:" + s.Schema.EnsureSchema))
	case len(s.Schema.Ensure) > 0:
		ts := dg.NewTypeSchema()
		ts.Marshal("", s.Schema.Ensure...)
		h.Write([]byte("ensure:" + canonicalTypeSchema(ts)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// migrationChecksum hashes the ordered concatenation of each step's checksum,
// so editing, adding, removing, or reordering steps changes it.
func migrationChecksum(m Migration) string {
	h := sha256.New()
	fmt.Fprintf(h, "id:%d\n", m.ID)
	fmt.Fprintf(h, "after:%d\n", m.After)
	for i, s := range m.Steps {
		h.Write([]byte(stepChecksum(i, s)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalTypeSchema renders a dgman TypeSchema deterministically. dgman's own
// TypeSchema.String() ranges over Go maps (Schema, Types) in randomized order,
// so its output cannot be hashed stably — an Ensure step would otherwise report
// false drift and fail the immutability guard on re-run. This sorts predicates
// and types (and each type's predicate set) into a fixed order.
func canonicalTypeSchema(ts *dg.TypeSchema) string {
	var b strings.Builder

	preds := make([]string, 0, len(ts.Schema))
	for _, s := range ts.Schema {
		preds = append(preds, s.String())
	}
	sort.Strings(preds)
	for _, p := range preds {
		b.WriteString(p)
		b.WriteByte('\n')
	}

	typeNames := make([]string, 0, len(ts.Types))
	for name := range ts.Types {
		typeNames = append(typeNames, name)
	}
	sort.Strings(typeNames)
	for _, name := range typeNames {
		b.WriteString("type ")
		b.WriteString(name)
		b.WriteString(" {")
		predNames := make([]string, 0, len(ts.Types[name]))
		for pn := range ts.Types[name] {
			predNames = append(predNames, pn)
		}
		sort.Strings(predNames)
		for _, pn := range predNames {
			b.WriteByte('\n')
			b.WriteString(pn)
		}
		b.WriteString("\n}\n")
	}
	return b.String()
}
