// Package migratecli provides reusable Kong command structs for schema
// migrations. Mount MigrateCmd in any Kong CLI to get
// "migrate up / down / status / version".
//
// This package intentionally does NOT import Kong: the command structs carry
// only inert Kong struct tags, and dispatch happens through the consumer's own
// kong.New(..., kong.Bind(provider)) call. That keeps the dgdao library
// free of a CLI-framework dependency.
package migratecli

import (
	"context"
	"fmt"

	mg "github.com/dgraph-io/dgdao"
	"github.com/dgraph-io/dgdao-migrate/migrate"
)

// Provider supplies a Client, the registered migration list, and the current
// schema model set at runtime. Implement this in your application and bind it
// via kong.Bind so each sub-command's Run method receives it.
//
// Runtime commands (up/down/status/version/history) use Client and Migrations.
// The authoring commands (create/diff/snapshot/verify) use Models — one zero
// value per schema entity type, typically the generated schema.Models().
type Provider interface {
	Client() mg.Client
	Migrations() []migrate.Migration
	Models() []any
}

// MigrateCmd is the parent command. Mount it in your CLI struct:
//
//	type CLI struct {
//	    Migrate migratecli.MigrateCmd `cmd:"" help:"Manage schema migrations."`
//	}
//
// then run kong.New(&cli, kong.Bind(provider)).
type MigrateCmd struct {
	Up      UpCmd      `cmd:"" help:"Apply all pending migrations."`
	Down    DownCmd    `cmd:"" help:"Roll back migrations to a target version."`
	Status  StatusCmd  `cmd:"" help:"Show applied and pending migrations."`
	Version VersionCmd `cmd:"" help:"Print the current migration version."`
	History HistoryCmd `cmd:"" help:"Show the migration chain and flag a broken history."`

	Create   CreateCmd   `cmd:"" help:"Scaffold a new migration from the current structs."`
	Diff     DiffCmd     `cmd:"" help:"Show (or check) the schema drift the next migration would capture."`
	Snapshot SnapshotCmd `cmd:"" help:"Re-sync the desired-state snapshot to the current structs."`
	Verify   VerifyCmd   `cmd:"" help:"Check the live schema against the current structs."`
}

// UpCmd applies pending migrations.
type UpCmd struct{}

func (c *UpCmd) Run(p Provider) error {
	return migrate.Run(context.Background(), p.Client(), p.Migrations())
}

// DownCmd rolls back to a target version ID.
type DownCmd struct {
	To int64 `arg:"" name:"version" help:"Roll back migrations applied after this version ID (0 = roll back all)."`
}

func (c *DownCmd) Run(p Provider) error {
	return migrate.Down(context.Background(), p.Client(), p.Migrations(), c.To)
}

// StatusCmd prints applied and pending migration state.
type StatusCmd struct{}

func (c *StatusCmd) Run(p Provider) error {
	result, err := migrate.Status(context.Background(), p.Client(), p.Migrations())
	if err != nil {
		return err
	}
	fmt.Printf("Applied (%d):\n", len(result.Applied))
	for _, e := range result.Applied {
		drift := ""
		if e.HasDrift {
			drift = " *** SCHEMA DRIFT DETECTED ***"
		}
		fmt.Printf("  [x] %d  %s%s\n", e.ID, e.Name, drift)
	}
	fmt.Printf("In progress (%d):\n", len(result.InProgress))
	for _, e := range result.InProgress {
		fmt.Printf("  [~] %d  %s (%d/%d steps applied — resumes on next up)\n", e.ID, e.Name, e.StepsApplied, e.StepsTotal)
	}
	fmt.Printf("Pending (%d):\n", len(result.Pending))
	for _, e := range result.Pending {
		fmt.Printf("  [ ] %d  %s\n", e.ID, e.Name)
	}
	return nil
}

// HistoryCmd prints the migration chain in order, annotated with applied state.
// It renders even when the chain is broken — showing the fork is the point — and
// returns the structural error so the process exits non-zero, doubling as a CI
// chain lint.
type HistoryCmd struct {
	Tree    bool `help:"Render the chain as a tree, marking forks."`
	Verbose bool `help:"Show each migration's step count and names."`
}

func (c *HistoryCmd) Run(p Provider) error {
	res, err := migrate.History(context.Background(), p.Client(), p.Migrations())
	if len(res.Entries) > 0 {
		fmt.Print(migrate.RenderHistory(res, c.Tree, c.Verbose))
	}
	return err
}

// VersionCmd prints the highest applied migration ID.
type VersionCmd struct{}

func (c *VersionCmd) Run(p Provider) error {
	v, err := migrate.Version(context.Background(), p.Client())
	if err != nil {
		return err
	}
	if v == 0 {
		fmt.Println("no migrations applied")
	} else {
		fmt.Printf("version: %d\n", v)
	}
	return nil
}

// CreateCmd scaffolds a new migration: it diffs the current structs against the
// desired-state snapshot, writes the .go + .schema pair, advances the snapshot,
// and (by default) registers the new variable in the All slice.
type CreateCmd struct {
	Name        string `arg:"" help:"Human name for the migration (sanitized to snake_case)."`
	Register    bool   `default:"true" negatable:"" help:"Append the new migration to the All slice."`
	ProjectRoot string `help:"Project root (defaults to the current directory)." type:"path"`
	Dir         string `name:"migrations-dir" help:"Migrations directory (defaults to <root>/migrations)." type:"path"`
}

func (c *CreateCmd) Run(p Provider) error {
	dir, pkg, err := migrate.ResolveDir(c.ProjectRoot, c.Dir)
	if err != nil {
		return err
	}
	report, err := migrate.Scaffold(migrate.ScaffoldParams{
		Migrations: p.Migrations(),
		Models:     p.Models(),
		Dir:        dir,
		Package:    pkg,
		Name:       c.Name,
		Register:   c.Register,
	})
	if err != nil {
		return err
	}
	printReport(report, c.Register)
	return nil
}

// DiffCmd previews the delta the next migration would capture. With --check it
// writes nothing and exits non-zero when a delta exists — the gofmt -l idiom,
// for an offline CI gate.
type DiffCmd struct {
	Check       bool   `help:"Exit non-zero if the structs have drifted from the snapshot."`
	ProjectRoot string `help:"Project root (defaults to the current directory)." type:"path"`
	Dir         string `name:"migrations-dir" help:"Migrations directory (defaults to <root>/migrations)." type:"path"`
}

func (c *DiffCmd) Run(p Provider) error {
	dir, _, err := migrate.ResolveDir(c.ProjectRoot, c.Dir)
	if err != nil {
		return err
	}
	delta, err := migrate.Diff(dir, p.Models())
	if err != nil {
		return err
	}
	printDelta(delta)
	if c.Check && !delta.Empty() {
		return fmt.Errorf("migrate diff: schema drift detected — run `migrate create <name>` to capture it")
	}
	return nil
}

// SnapshotCmd re-syncs the desired-state snapshot to the current structs without
// writing a migration — use it after hand-authoring a migration create cannot
// generate (a retype or drop).
type SnapshotCmd struct {
	ProjectRoot string `help:"Project root (defaults to the current directory)." type:"path"`
	Dir         string `name:"migrations-dir" help:"Migrations directory (defaults to <root>/migrations)." type:"path"`
}

func (c *SnapshotCmd) Run(p Provider) error {
	dir, _, err := migrate.ResolveDir(c.ProjectRoot, c.Dir)
	if err != nil {
		return err
	}
	path, err := migrate.Snapshot(migrate.ScaffoldParams{Models: p.Models(), Dir: dir})
	if err != nil {
		return err
	}
	fmt.Printf("snapshot: wrote %s\n", path)
	return nil
}

// VerifyCmd checks the live schema against the current structs and exits
// non-zero on drift — the DB-backed CI gate, run after `migrate up`.
type VerifyCmd struct{}

func (c *VerifyCmd) Run(p Provider) error {
	drift, err := migrate.Verify(context.Background(), p.Client(), p.Models())
	if err != nil {
		return err
	}
	if drift.Clean() {
		fmt.Println("verify: live schema satisfies the current models")
		return nil
	}
	for _, m := range drift.Missing {
		fmt.Printf("  MISSING:  %s\n", m)
	}
	for _, m := range drift.Mismatched {
		fmt.Printf("  MISMATCH: %s\n", m)
	}
	return fmt.Errorf("migrate verify: live schema drift — %d missing, %d mismatched",
		len(drift.Missing), len(drift.Mismatched))
}

// printReport summarizes a scaffolded migration for the operator.
func printReport(r migrate.ScaffoldReport, wantRegister bool) {
	fmt.Printf("created migration %d (after %d)\n", r.MigrationID, r.After)
	fmt.Printf("  %s\n  %s\n", r.GoFile, r.SchemaFile)
	printChanges("added", r.Added)
	printChanges("index changed", r.IndexChanged)
	printChanges("TYPE CHANGED (needs RetypePredicate)", r.TypeChanged)
	printChanges("REMOVED (not auto-dropped)", r.Removed)
	if !r.HasDelta && len(r.TypeChanged)+len(r.Removed) == 0 {
		fmt.Println("  no schema delta — empty stub migration")
	}
	switch {
	case r.Registered:
		fmt.Println("  registered in the All slice")
	case wantRegister:
		fmt.Println("  NOT registered — add the new var to All in your migrations registry")
	}
}

// printDelta prints the classified drift for `migrate diff`.
func printDelta(d migrate.Delta) {
	if d.Empty() {
		fmt.Println("no drift: structs match the desired-state snapshot")
		return
	}
	printChanges("added", d.Added)
	printChanges("index changed", d.IndexChanged)
	printChanges("TYPE CHANGED (needs RetypePredicate)", d.TypeChanged)
	printChanges("REMOVED (not auto-dropped)", d.Removed)
}

func printChanges(label string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Printf("  %s (%d):\n", label, len(items))
	for _, it := range items {
		fmt.Printf("    %s\n", it)
	}
}
