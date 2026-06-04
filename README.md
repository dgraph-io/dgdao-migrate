# modusgraph-migrate

The `migrate` package applies ordered, run-once schema and data changes to a
modusGraph database. It gives you an explicit revision chain, resumable phased
migrations, content checksums that make applied migrations immutable, and
struct-snapshot scaffolding that writes your next migration.

This guide is consumer-agnostic. Mount the commands in any Kong CLI through
`migratecli`, or call the engine functions directly.

## Install

`modusgraph-migrate` depends on a fork of modusGraph published under a different import
path. Go does not propagate `replace` directives to consumers, so your project must
declare the same one:

```go
// go.mod
require (
    github.com/mlwelles/modusgraph-migrate v0.1.0
    github.com/matthewmcneely/modusgraph v0.0.0-00010101000000-000000000000
)

replace github.com/matthewmcneely/modusgraph => github.com/mlwelles/modusGraph v0.5.0-dev-mlwelles-20260604b
```

The engine lives at `…/modusgraph-migrate/migrate`; the `migratecli` command at
`…/modusgraph-migrate/migrate/migratecli`.

## The model

A **migration** is one run-once change with a timestamp ID. It holds an ordered
list of **steps**; each step makes an optional schema change, then runs an
optional data transform. The runner records each step as it completes, so a
crashed migration resumes at its first unapplied step.

```go
migrate.Migration{
    ID:    20260601090000,
    After: 20260528000001,
    Name:  "add_mime_category",
    Steps: []migrate.Step{
        {Name: "schema", Schema: migrate.SchemaChange{EnsureSchema: frozen}},
        {Name: "backfill", Up: backfillMime},
    },
}
```

Every step must be idempotent. A schema `Alter` auto-commits, and the runner
writes the step's record separately, so re-running a step after a crash must
converge to the same end state.

## The revision chain

Migrations are ordered by an explicit predecessor chain, not by sorting IDs.
Each migration's `After` names the ID of the migration it follows. Exactly one
migration — the baseline — has `After == 0`; it is the root. The runner builds
the chain by walking `After` from the root and applies migrations in that order.

Explicit ordering catches the mistake an ID sort hides. Two developers on
parallel branches each scaffold a migration; on merge, both set `After` to the
same predecessor. That fork is a real ambiguity, so the runner rejects it rather
than guess an order from timestamps.

Chain validation runs before any migration applies and returns a typed error on
the first fault:

| Fault | Error |
|---|---|
| Two migrations share an ID | `*ErrDuplicateID` |
| No migration has `After == 0` | `*ErrNoRoot` |
| More than one migration has `After == 0` | `*ErrMultipleRoots` |
| An `After` names no registered migration | `*ErrUnknownPredecessor` |
| Two migrations share a predecessor | `*ErrDivergentHistory` |
| The `After` links form a loop | `*ErrCycle` |

Resolve divergence by re-pointing one migration's `After` at the other, which
linearizes the two into a sequence. There are no merge nodes.

`After` is structural, so the migration checksum covers it. Editing `After` on a
migration already applied to a database trips `ErrChecksumMismatch`, the same
immutability guard that protects the schema.

## Schema changes

A step's `SchemaChange` carries exactly one of three fields:

- **`Ensure []any`** — struct templates whose derived schema applies additively.
  The runner re-derives both the applied schema and the checksum from the live
  structs at run time, so an `Ensure` step drifts as those structs evolve. Use
  it for throwaway or bootstrap schema.
- **`EnsureSchema string`** — a frozen schema string that applies additively.
  `MarshalSchema` renders the current structs to this string once; stored
  verbatim, it never drifts. Use it for any migration that must stay
  reproducible after it ships, including the baseline.
- **`Alter string`** — a raw Dgraph schema string, for changes the additive
  forms cannot express: rename, drop, retype.

`Ensure` and `EnsureSchema` apply the same predicates; only the freezing
differs. Freeze anything that ships.

### Declare each predicate consistently

A directive like `@index` or `@reverse` is a property of the **predicate**, not
of an individual edge — Dgraph stores one definition per predicate. So when
several structs declare the same predicate (for example, many types holding a
`tenant` edge), every one of them must give it identical directives. If they
disagree, there is no single correct schema to render, so `MarshalSchema` returns
an error naming the predicate and the structs on each side rather than silently
picking one. Picking one would also hide the disagreement: a later edit to the
struct whose declaration "lost" would never change the output. Make the
declarations agree — declare `reverse` on every edge of the predicate, or none.

## Immutability

Each step's checksum covers its identity and its schema portion, never its
`Up`/`Down` closures. Each migration's checksum covers its ID, its `After`, and
its steps' checksums. Editing, reordering, adding, or removing a step in an
already-applied migration changes the checksum, and the runner rejects it with
`ErrChecksumMismatch`. Correct a mistake with a new migration; never edit a
shipped one.

## Commands

Mount `migratecli.MigrateCmd` in a Kong CLI and bind a `Provider`:

```go
type Provider interface {
    Client() mg.Client
    Migrations() []migrate.Migration
    Models() []any
}
```

Runtime commands use `Client` and `Migrations`; authoring commands use `Models`.

| Command | Purpose |
|---|---|
| `up` | Apply all pending migrations, resuming any partial one. |
| `down <version>` | Roll back every migration after `<version>`, head first. |
| `status` | Show applied, in-progress, and pending migrations. |
| `version` | Print the highest applied migration ID. |
| `history [--tree] [--verbose]` | Show the chain and flag a broken history. |
| `create <name>` | Scaffold the next migration from the current structs. |
| `diff [--check]` | Show, or check for, the drift the next migration would capture. |
| `snapshot` | Re-sync the desired-state snapshot to the current structs. |
| `verify` | Check the live schema against the current structs. |

`history` prints even when the chain is broken — showing the fork is its point —
and exits non-zero on any structural fault, so it doubles as a CI chain lint.
`diff --check` and `verify` also exit non-zero on drift.

## Retyping a predicate

`RetypePredicate` changes a predicate's scalar type without data loss. It
expands a `RetypeSpec` into five staged, checkpointed, idempotent steps: stage
the converted values under a temporary predicate, verify the counts, then swap.
The operation is irreversible — every step's `Down` is nil — so the runner
refuses any down range that includes it.

```go
steps := migrate.RetypePredicate(migrate.RetypeSpec{
    Predicate: "size",
    To:        migrate.Int,
    Index:     "int",
    Convert:   func(old string) (any, error) { return strconv.Atoi(old) },
})
```

## Scaffolding

Scaffolding writes the next migration by comparing the current structs against a
checked-in desired-state snapshot.

**The model aggregate.** Tooling needs one value per schema entity type.
modusgraph-gen emits `schema.Models() []any` for exactly this; supply it through
`Provider.Models()`. Because the generator owns the list, it cannot fall out of
sync with the declared types.

**The desired-state file.** `<migrations-dir>/schema_state.schema` holds the
full canonical schema as of the latest scaffold. It is checked in and
tool-managed: `create` advances it, and `snapshot` re-syncs it. Seed it once at
adoption with `snapshot`.

**The diff.** Both sides are canonical `MarshalSchema` output, so the diff is a
stable line-by-line set difference. It classifies each change:

| Change | Condition | Action |
|---|---|---|
| Added | predicate present now, absent in state | emit into the `EnsureSchema` delta |
| Index changed | same predicate, index/directives differ | emit into the delta |
| Type changed | same predicate, scalar type differs | flag for `RetypePredicate`; never emitted |
| Removed | predicate in state, absent now | flag; never auto-dropped |

If the delta is purely flagged, the generated `.schema` is empty and the step is
a stub carrying only the action-required notes. The scaffolder never emits an
unsafe `EnsureSchema`.

**Create.** `create <name>` validates the chain (aborting on a broken history),
diffs the structs against the snapshot, sets `After` to the current head, writes
`<id>_<name>.go` and `<id>_<name>.schema`, advances the snapshot, and appends the
new variable to the `All` slice. It writes nothing until validation passes, and
fails with an actionable message when it runs outside a Go project root or finds
no migrations directory.

**Engine API.**

```go
func Scaffold(p ScaffoldParams) (ScaffoldReport, error) // create
func Snapshot(p ScaffoldParams) (string, error)         // re-sync the snapshot
func Diff(dir string, models []any) Delta               // offline drift
func Verify(ctx context.Context, c mg.Client, models []any) (Drift, error)
```

## Drift gates

Two gates reuse the diff engine and catch different failures.

- **Offline — `diff --check`.** Exits non-zero when the structs have drifted
  from the snapshot. It needs no database and runs in `make check`, the
  `gofmt -l` idiom for migrations.
- **Live — `verify`.** Exits non-zero when the database lacks a predicate the
  structs declare, or — against a real Dgraph — defines one differently. Run it
  after `up` in CI. `verify` is one-directional: it ignores predicates the
  database has but the structs do not. Pass a client with auto-schema disabled,
  so the check reflects what migrations applied rather than what an auto-schema
  client would re-create.
