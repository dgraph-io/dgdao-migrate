package migrate_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mg "github.com/matthewmcneely/modusgraph"
	"github.com/mlwelles/modusGraph-migrate/migrate"
)

type Tree struct {
	UID    string   `json:"uid,omitempty"`
	DType  []string `json:"dgraph.type,omitempty" dgraph:"Tree"`
	Height int      `json:"height,omitempty" dgraph:"predicate=height index=int"`
}

// Example_additive shows the simplest migration: one step that ensures a type's
// schema exists. Ensure is additive and idempotent.
func Example_additive() {
	m := migrate.Migration{
		ID:   20260101000000,
		Name: "create_tree",
		Steps: []migrate.Step{
			{Name: "ensure_tree", Schema: migrate.SchemaChange{Ensure: []any{&Tree{}}}},
		},
	}
	fmt.Println(m.Name, len(m.Steps))
	// Output: create_tree 1
}

// Example_frozenSchema shows the immutable counterpart to Ensure. MarshalSchema
// renders the live structs to a Dgraph schema string once; storing that string
// in EnsureSchema freezes it, so the migration stays reproducible even after the
// Tree struct evolves. Capture the string at authoring time (e.g. paste the
// MarshalSchema output into a const or an embedded file) rather than calling
// MarshalSchema from the migration, which would re-derive it from live structs.
func Example_frozenSchema() {
	frozen, _ := migrate.MarshalSchema(&Tree{})
	m := migrate.Migration{
		ID:   20260104000000,
		Name: "baseline",
		Steps: []migrate.Step{
			{Name: "baseline_schema", Schema: migrate.SchemaChange{EnsureSchema: frozen}},
		},
	}
	fmt.Println(strings.Contains(frozen, "height:"), len(m.Steps))
	// Output: true 1
}

// Example_backfill shows a hybrid migration: a schema step followed by a data
// step. The data step is keyed by UID so re-running it converges.
func Example_backfill() {
	m := migrate.Migration{
		ID:   20260102000000,
		Name: "seed_tree",
		Steps: []migrate.Step{
			{Name: "ensure_tree", Schema: migrate.SchemaChange{Ensure: []any{&Tree{}}}},
			{Name: "seed_one", Up: func(ctx context.Context, c mg.Client) error {
				return c.Insert(ctx, &Tree{DType: []string{"Tree"}, Height: 5730})
			}},
		},
	}
	fmt.Println(len(m.Steps))
	// Output: 2
}

// Example_retypePredicate shows how RetypePredicate expands into five staged,
// checkpointed, idempotent steps. Provide a Convert func that maps each
// existing string-rendered value to its new typed value. The op is irreversible:
// every step's Down is nil.
func Example_retypePredicate() {
	spec := migrate.RetypeSpec{
		Predicate: "height",
		To:        migrate.Int,
		Index:     "int",
		// metersToMM converts a decimal-meters string to integer millimeters.
		Convert: func(old string) (any, error) {
			v, err := strconv.ParseFloat(old, 64)
			if err != nil {
				return nil, err
			}
			return int64(v * 1000), nil
		},
	}
	steps := migrate.RetypePredicate(spec)
	fmt.Println(len(steps))
	// Output: 5
}

// Example_irreversibleDrop shows a destructive step. It omits Down, so any down
// range that includes it is refused with ErrIrreversible.
func Example_irreversibleDrop() {
	m := migrate.Migration{
		ID:   20260103000000,
		Name: "drop_legacy",
		Steps: []migrate.Step{
			{Name: "drop_old", Schema: migrate.SchemaChange{Alter: "drop_predicate_via_alter_here ."}},
		},
	}
	fmt.Println(m.Steps[0].Down == nil)
	// Output: true
}

// Leaf adds a predicate beyond Tree, so scaffolding against a Tree-only snapshot
// produces an additive delta.
type Leaf struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty" dgraph:"Leaf"`
	Color string   `json:"color,omitempty" dgraph:"predicate=color index=term"`
}

// Example_revisionChain shows the ordering model: each migration names the one
// it follows via After, and exactly one migration (the baseline) has After == 0.
// The runner walks this chain rather than sorting by ID.
func Example_revisionChain() {
	chain := []migrate.Migration{
		{ID: 20260528000001, After: 0, Name: "baseline"},
		{ID: 20260601090000, After: 20260528000001, Name: "add_mime"},
		{ID: 20260601100000, After: 20260601090000, Name: "remap_status"},
	}
	for _, m := range chain {
		if m.After == 0 {
			fmt.Printf("%s is the root\n", m.Name)
		} else {
			fmt.Printf("%s follows %d\n", m.Name, m.After)
		}
	}
	// Output:
	// baseline is the root
	// add_mime follows 20260528000001
	// remap_status follows 20260601090000
}

// Example_divergentHistory shows that a fork — two migrations sharing a
// predecessor — is a hard error. Scaffold validates the chain before writing
// anything, so authoring never extends a broken history.
func Example_divergentHistory() {
	divergent := []migrate.Migration{
		{ID: 1, After: 0, Name: "root"},
		{ID: 2, After: 1, Name: "branch_a"},
		{ID: 3, After: 1, Name: "branch_b"},
	}
	_, err := migrate.Scaffold(migrate.ScaffoldParams{
		Migrations: divergent,
		Name:       "next",
		Dir:        ".", // never reached: the chain is validated first
	})
	var de *migrate.ErrDivergentHistory
	fmt.Println(errors.As(err, &de))
	// Output: true
}

// Example_scaffold shows authoring a migration from a struct snapshot. Scaffold
// diffs the current models against the checked-in desired state, writes the
// .go + .schema pair with an EnsureSchema step for the additive delta, and
// advances the snapshot. Now is injected here for a deterministic ID.
func Example_scaffold() {
	root, _ := os.MkdirTemp("", "scaffold-example")
	defer os.RemoveAll(root)
	dir := filepath.Join(root, "migrations")
	_ = os.Mkdir(dir, 0o755)
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.21\n"), 0o644)
	state, _ := migrate.MarshalSchema(&Tree{})
	_ = os.WriteFile(filepath.Join(dir, "schema_state.schema"), []byte(state), 0o644)

	report, err := migrate.Scaffold(migrate.ScaffoldParams{
		Migrations: []migrate.Migration{{ID: 20260528000001, After: 0, Name: "baseline"}},
		Models:     []any{&Tree{}, &Leaf{}},
		Dir:        dir,
		Package:    "migrations",
		Name:       "add_leaf",
		Now:        time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(report.MigrationID, report.After, report.HasDelta)
	// Output: 20260601090000 20260528000001 true
}

// Example_verify shows the live drift gate. After `migrate up`, Verify checks
// that the database satisfies every predicate the current structs declare. Pass
// a client with auto-schema disabled so the check reflects what migrations
// applied, not what an auto-schema client would re-create.
func Example_verify() {
	var c mg.Client // your migrate client (auto-schema off)
	drift, err := migrate.Verify(context.Background(), c, []any{&Tree{}})
	if err != nil {
		return // connection error
	}
	if !drift.Clean() {
		fmt.Println("drift:", drift.Missing, drift.Mismatched)
	}
}
