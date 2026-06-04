// Package migrate runs versioned, run-once schema-and-data migrations against a
// modusgraph.Client.
//
// # Model
//
// A Migration is an ordered list of named Steps, identified by a UTC timestamp
// ID (YYYYMMDDHHMMSS). Each Step carries an optional SchemaChange (Ensure for
// additive struct-derived schema, EnsureSchema for a frozen additive schema
// string, or Alter for raw DQL: rename/drop/retype) and an optional Up data
// transform. On up, the runner applies a step's schema, runs its Up, then
// records a checkpoint; on a later run it skips already-recorded steps and
// resumes at the next one.
//
// # Freezing schema
//
// Ensure derives its schema and checksum from the live struct definitions every
// time the binary runs. That is convenient for bootstrapping but unsafe for a
// migration that must survive its structs evolving: once the structs change, an
// applied Ensure step's checksum drifts (ErrChecksumMismatch) and a fresh-DB
// replay no longer matches the schema as of authoring time. For anything that
// ships — a baseline especially — capture the schema once with MarshalSchema and
// store the returned string in EnsureSchema. The frozen string is applied and
// checksummed verbatim, so it is reproducible and immutable for the life of the
// database.
//
// # The one rule that matters
//
// Every Step must be idempotent. A Dgraph Alter auto-commits and cannot share a
// transaction with the step's data work or its checkpoint write, so a crash can
// leave a step's effect applied but unrecorded. On resume the step runs again —
// it must converge to the same end state. Dgraph's add-predicate, add-index,
// set-type, and drop-predicate are naturally idempotent; author your Up funcs
// (upsert by UID, not blind insert) to be the same.
//
// # Reversal
//
// Down is optional per step. Down(toVersion) rolls back applied migrations with
// ID > toVersion in descending order, running each migration's steps' Down in
// reverse step order. If any step in the range has a nil Down, the whole range
// is refused with ErrIrreversible before any work.
//
// Migrations are immutable once applied: editing a shipped migration's schema
// portion trips ErrChecksumMismatch. Correct mistakes by writing a new migration.
package migrate
