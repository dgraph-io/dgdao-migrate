package migrate

import (
	"errors"
	"fmt"
)

// ErrLocked is returned when another runner holds the migration lock.
var ErrLocked = errors.New("migrate: another migration runner holds the lock")

// ErrChecksumMismatch is returned when an applied migration's (or in-flight
// step's) schema portion has been edited since it ran. Migrations are immutable
// once applied; correct mistakes with a new migration.
type ErrChecksumMismatch struct {
	ID       int64
	Name     string
	Stored   string
	Computed string
}

func (e *ErrChecksumMismatch) Error() string {
	return fmt.Sprintf("migrate: checksum mismatch for migration %d (%s): stored=%s computed=%s — migrations are immutable once applied",
		e.ID, e.Name, short(e.Stored), short(e.Computed))
}

// ErrMissingApplied is returned when a migration ID recorded in the database is
// absent from the registered list (a shipped migration was deleted).
type ErrMissingApplied struct{ ID int64 }

func (e *ErrMissingApplied) Error() string {
	return fmt.Sprintf("migrate: applied migration %d is missing from the registered list — never delete a shipped migration", e.ID)
}

// ErrStepRemoved is returned when a recorded step's ordinal no longer exists
// in the registered migration (a shipped step was deleted).
type ErrStepRemoved struct {
	MigrationID int64
	Name        string
	Index       int
}

func (e *ErrStepRemoved) Error() string {
	return fmt.Sprintf("migrate: migration %d step %d (%s) was recorded but no longer exists — never delete a shipped step", e.MigrationID, e.Index, e.Name)
}

// ErrIrreversible is returned when a requested down range includes a migration
// with any step that has a nil Down.
type ErrIrreversible struct {
	ID   int64
	Name string
}

func (e *ErrIrreversible) Error() string {
	return fmt.Sprintf("migrate: migration %d (%s) has a step with no Down — cannot reverse", e.ID, e.Name)
}

// ErrMigrationFailed wraps a migration-level failure (e.g. recording the
// migration after all steps ran) with the offending ID.
type ErrMigrationFailed struct {
	ID   int64
	Name string
	Err  error
}

func (e *ErrMigrationFailed) Error() string {
	return fmt.Sprintf("migrate: migration %d (%s) failed: %v", e.ID, e.Name, e.Err)
}

func (e *ErrMigrationFailed) Unwrap() error { return e.Err }

// ErrStepFailed wraps a step-level failure with the migration ID and the step's
// name and ordinal, so the operator knows exactly where a run stopped.
type ErrStepFailed struct {
	MigrationID int64
	StepName    string
	Index       int
	Err         error
}

func (e *ErrStepFailed) Error() string {
	return fmt.Sprintf("migrate: migration %d step %d (%s) failed: %v", e.MigrationID, e.Index, e.StepName, e.Err)
}

func (e *ErrStepFailed) Unwrap() error { return e.Err }

// ErrVerifyFailed is returned by a RetypePredicate verify step when the staged
// value count does not match the source count. It is raised before any
// destructive swap, leaving the source predicate untouched.
type ErrVerifyFailed struct {
	Predicate    string
	SourceCount  int
	StagingCount int
}

func (e *ErrVerifyFailed) Error() string {
	return fmt.Sprintf("migrate: retype verify failed for %q: source has %d values, staging has %d — aborting before destructive swap",
		e.Predicate, e.SourceCount, e.StagingCount)
}

func short(s string) string {
	if len(s) >= 8 {
		return s[:8]
	}
	return s
}

// ErrDuplicateID is returned when two registered migrations share an ID.
type ErrDuplicateID struct {
	ID    int64
	Names [2]string
}

func (e *ErrDuplicateID) Error() string {
	return fmt.Sprintf("migrate: duplicate migration ID %d — used by %q and %q; IDs must be unique", e.ID, e.Names[0], e.Names[1])
}

// ErrNoRoot is returned when no migration has After == 0.
type ErrNoRoot struct{}

func (e *ErrNoRoot) Error() string {
	return "migrate: no root migration (none has After == 0)"
}

// ErrMultipleRoots is returned when more than one migration has After == 0.
type ErrMultipleRoots struct{ IDs []int64 }

func (e *ErrMultipleRoots) Error() string {
	return fmt.Sprintf("migrate: multiple root migrations (After == 0): %v — exactly one is allowed; set After on all but the baseline", e.IDs)
}

// ErrUnknownPredecessor is returned when a migration's After names no registered migration.
type ErrUnknownPredecessor struct {
	ID    int64
	After int64
}

func (e *ErrUnknownPredecessor) Error() string {
	return fmt.Sprintf("migrate: migration %d has After %d, which is not a registered migration", e.ID, e.After)
}

// ErrDivergentHistory is returned when two or more migrations share a predecessor.
type ErrDivergentHistory struct {
	After    int64
	Children []int64
}

func (e *ErrDivergentHistory) Error() string {
	return fmt.Sprintf("migrate: divergent history — predecessor %d has %d children: %v. Re-point one migration's After to the other to linearize. Run `migrate history --tree` to see the chain.", e.After, len(e.Children), e.Children)
}

// ErrCycle is returned when following After links forms a loop.
type ErrCycle struct{ IDs []int64 }

func (e *ErrCycle) Error() string {
	return fmt.Sprintf("migrate: migration chain has a cycle involving %v", e.IDs)
}
