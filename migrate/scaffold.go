package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// schemaStateFile is the checked-in desired-state snapshot the diff compares
// against. create advances it; snapshot re-syncs it.
const schemaStateFile = "schema_state.schema"

// Delta is the classified difference between two canonical schema strings — the
// checked-in desired state and the schema derived from the current structs.
// Added and IndexChanged are additive and safe to emit verbatim into an
// EnsureSchema step. TypeChanged and Removed are destructive or ambiguous: the
// scaffolder only flags them as action-required comments, never as schema.
type Delta struct {
	Added        []string // full predicate declaration lines new since the last state
	IndexChanged []string // full predicate declaration lines whose index/directives changed
	TypeChanged  []string // "predicate: oldType → newType" notes; needs RetypePredicate
	Removed      []string // full predicate declaration lines absent from the current structs
}

// Additive returns the declaration lines safe to apply via EnsureSchema (the
// Added and IndexChanged buckets), sorted for a deterministic schema file.
func (d Delta) Additive() []string {
	out := make([]string, 0, len(d.Added)+len(d.IndexChanged))
	out = append(out, d.Added...)
	out = append(out, d.IndexChanged...)
	sort.Strings(out)
	return out
}

// HasAdditive reports whether the delta contains anything to emit as schema.
func (d Delta) HasAdditive() bool { return len(d.Added)+len(d.IndexChanged) > 0 }

// HasFlagged reports whether the delta contains a destructive/ambiguous change
// that the scaffolder will flag rather than emit.
func (d Delta) HasFlagged() bool { return len(d.TypeChanged)+len(d.Removed) > 0 }

// Empty reports whether the two schemas are identical (no drift).
func (d Delta) Empty() bool { return !d.HasAdditive() && !d.HasFlagged() }

// ScaffoldParams configures a single create/snapshot. Migrations is the
// registered chain (for head/After and history validation); Models is the
// current struct set (Provider.Models()). Now is injected so tests get a
// deterministic timestamp ID.
type ScaffoldParams struct {
	Migrations []Migration
	Models     []any
	Dir        string    // absolute, already-validated migrations directory
	Package    string    // generated-file package
	Name       string    // human name, sanitized to snake_case
	Now        time.Time // injected clock; zero value means time.Now()
	Register   bool      // append the new var to the All slice (best-effort)
}

// ScaffoldReport summarizes what Scaffold wrote, for the CLI to print.
type ScaffoldReport struct {
	MigrationID  int64
	After        int64
	GoFile       string
	SchemaFile   string
	Added        []string
	IndexChanged []string
	TypeChanged  []string
	Removed      []string
	Registered   bool
	HasDelta     bool
}

// Diff computes the delta the next migration would capture: the current structs'
// schema against the checked-in desired-state snapshot. It writes nothing and
// needs no database, so it backs both `migrate diff` and the offline CI gate.
// It returns an error when the structs declare a predicate inconsistently (see
// MarshalSchema).
func Diff(dir string, models []any) (Delta, error) {
	current, err := MarshalSchema(models...)
	if err != nil {
		return Delta{}, err
	}
	prev, _ := os.ReadFile(filepath.Join(dir, schemaStateFile)) // missing → empty
	return diffSchema(string(prev), current), nil
}

// Snapshot re-syncs the desired-state snapshot to the current structs without
// writing a migration. It returns the snapshot file's path.
func Snapshot(p ScaffoldParams) (string, error) {
	schema, err := MarshalSchema(p.Models...)
	if err != nil {
		return "", err
	}
	statePath := filepath.Join(p.Dir, schemaStateFile)
	if err := os.WriteFile(statePath, []byte(schema), 0o644); err != nil {
		return "", err
	}
	return statePath, nil
}

// Scaffold writes a new migration: it validates the chain, diffs the current
// structs against the desired state, emits the .go + .schema pair (additive
// changes as an EnsureSchema step; destructive changes as flagged notes),
// advances the desired-state snapshot, and best-effort registers the new
// variable. A broken chain aborts before any write.
func Scaffold(p ScaffoldParams) (ScaffoldReport, error) {
	name := sanitizeName(p.Name)
	if name == "" {
		return ScaffoldReport{}, errors.New("migrate: migration name is empty after sanitizing")
	}

	// A new migration must never extend a broken history.
	var after int64
	if len(p.Migrations) > 0 {
		ordered, err := buildChain(p.Migrations)
		if err != nil {
			return ScaffoldReport{}, err
		}
		after = ordered[len(ordered)-1].ID
	}

	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}
	id := timestampID(now)

	// Go excludes *_test.go files from the package build, so a migration whose
	// file would be named that way silently fails to compile. Reject it before
	// writing anything rather than emit an uncompilable migration.
	if strings.HasSuffix(fmt.Sprintf("%d_%s.go", id, name), "_test.go") {
		return ScaffoldReport{}, fmt.Errorf("migrate: migration name %q produces the Go test file %d_%s.go, which Go excludes from the package build; choose a name that does not end in \"test\"", name, id, name)
	}

	current, err := MarshalSchema(p.Models...)
	if err != nil {
		return ScaffoldReport{}, err
	}
	statePath := filepath.Join(p.Dir, schemaStateFile)
	prev, _ := os.ReadFile(statePath) // missing → empty
	delta := diffSchema(string(prev), current)

	goSrc, schema := renderMigrationFile(p.Package, name, id, after, delta)
	goFile := filepath.Join(p.Dir, fmt.Sprintf("%d_%s.go", id, name))
	schemaFile := filepath.Join(p.Dir, fmt.Sprintf("%d_%s.schema", id, name))

	if err := os.WriteFile(goFile, []byte(goSrc), 0o644); err != nil {
		return ScaffoldReport{}, err
	}
	if err := os.WriteFile(schemaFile, []byte(schema), 0o644); err != nil {
		return ScaffoldReport{}, err
	}
	if err := os.WriteFile(statePath, []byte(current), 0o644); err != nil {
		return ScaffoldReport{}, err
	}

	registered := false
	if p.Register {
		reg, err := registerInDir(p.Dir, varNameFor(name, id))
		if err != nil {
			return ScaffoldReport{}, err
		}
		registered = reg
	}

	return ScaffoldReport{
		MigrationID:  id,
		After:        after,
		GoFile:       goFile,
		SchemaFile:   schemaFile,
		Added:        delta.Added,
		IndexChanged: delta.IndexChanged,
		TypeChanged:  delta.TypeChanged,
		Removed:      delta.Removed,
		Registered:   registered,
		HasDelta:     delta.HasAdditive(),
	}, nil
}

// registerInDir appends varName to the All slice in whichever non-test .go file
// in dir defines it. A missing slice is not an error (registration is
// best-effort): it returns (false, nil) so the caller prints a manual hint.
func registerInDir(dir, varName string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		err := appendToAll(filepath.Join(dir, e.Name()), varName)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, errAllSliceNotFound):
			continue
		default:
			return false, err
		}
	}
	return false, nil
}

// timestampID renders a time as the UTC YYYYMMDDHHMMSS migration ID.
func timestampID(t time.Time) int64 {
	id, _ := strconv.ParseInt(t.UTC().Format("20060102150405"), 10, 64)
	return id
}
