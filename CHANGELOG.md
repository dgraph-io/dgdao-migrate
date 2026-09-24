# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.2.5] - 2026-09-23

### Added

- `migrate diff` and `migrate create` now compare type membership. A type that
  was added, removed, or gained or lost a field is reported, and `diff --check`
  fails when only type membership has drifted. `create` writes the full current
  definition of each added or changed type into the migration's `.schema`, after
  the predicate lines, because Dgraph replaces a whole type on alter. A removed
  type is flagged in the SCAFFOLD NOTES block and never dropped automatically,
  matching how removed predicates are handled. A field dropped from a changed
  type is also named in the notes, since the emitted definition removes it from
  the type.
- `Verify` reports a type the structs declare that the live schema lacks
  (`Drift.MissingTypes`) and each field missing from a live type
  (`Drift.MissingTypeFields`, as `Type.field`). Both checks work against the
  embedded engine.

### Fixed

- Adding an already-declared predicate to a type no longer produces an empty
  delta. Previously `diff --check` passed, `create` wrote an empty stub
  migration, and the desired-state snapshot absorbed the change, so the type
  change never reached the database.
- `Verify` no longer passes when a live type is missing a field. It previously
  merged every type's fields into one set, so a field counted as present if any
  type or predicate declaration named it.

## [0.2.4] - 2026-07-19

### Changed

- Bump the `github.com/dgraph-io/dgdao` dependency to v0.9.0, which renames the
  transaction surface (`Txn`/`ClientTxn`), the consume operations
  (`GetOrInsert`/`GetAndDelete`), and the record bridge (`AsRecord`). Schema rendering
  follows the `AsRecord` rename; no behavior change in migrate itself.

## [0.2.3] - 2026-07-17

### Changed

- Bump the `github.com/dgraph-io/dgdao` dependency to v0.8.0, which adds the transaction-scoped
  `Client.InTxn` client and moves the Client-level write methods off `TxnContext`. No behavior
  change here; the test stub gains the new `InTxn`/`NewTxnContext` interface methods.

## [0.2.2] - 2026-07-16

### Changed

- Bump the `github.com/dgraph-io/dgdao` dependency to v0.6.1, which adds the `Defaulter`
  hook (before-validation defaulting on writes) and caches the per-write schema check.

## [0.2.1] - 2026-07-09

### Changed

- Bump the `github.com/dgraph-io/dgdao` dependency to v0.5.4, which pins Dgraph to the
  released v25.3.8 tag rather than a pre-release pseudo-version.

## [0.2.0] - 2026-07-08

### Added

- Initial extraction of the schema versioning and migration engine (`migrate`,
  `migrate/migratecli`) from dgdao
  (https://github.com/dgraph-io/dgdao). Includes the revision chain, phased
  resumable migrations, content-checksum immutability, retyping, struct-snapshot
  scaffolding, history, and drift verification.
