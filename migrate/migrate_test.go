package migrate

import (
	"context"
	"errors"
	"fmt"
	"testing"

	mg "github.com/matthewmcneely/modusgraph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var ctx = context.Background()

// makeMigration builds a migration whose steps each record their execution
// order into *calls when run, so tests can assert what actually ran.
func makeMigration(id int64, name string, stepNames []string, calls *[]string) Migration {
	m := Migration{ID: id, Name: name}
	for _, sn := range stepNames {
		sn := sn
		m.Steps = append(m.Steps, Step{
			Name:   sn,
			Schema: SchemaChange{Alter: sn + ": string ."},
			Up: func(_ context.Context, _ mg.Client) error {
				*calls = append(*calls, fmt.Sprintf("%d/%s", id, sn))
				return nil
			},
			Down: func(_ context.Context, _ mg.Client) error {
				*calls = append(*calls, fmt.Sprintf("down %d/%s", id, sn))
				return nil
			},
		})
	}
	return m
}

func TestRun_AppliesStepsInOrderAndRecords(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	m := makeMigration(20260101000000, "first", []string{"a", "b", "c"}, &calls)

	require.NoError(t, run(ctx, c, s, []Migration{m}))

	assert.Equal(t, []string{"20260101000000/a", "20260101000000/b", "20260101000000/c"}, calls)
	steps, _ := s.loadStepRecords(ctx, m.ID)
	assert.Len(t, steps, 3, "all steps recorded")
	migs, _ := s.loadAppliedMigrations(ctx)
	assert.Len(t, migs, 1, "migration record written after all steps")
}

func TestRun_ResumeSkipsCompletedSteps(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	m := makeMigration(20260101000000, "first", []string{"a", "b", "c"}, &calls)
	// Steps 0 and 1 already done (with correct checksums); migration not yet recorded.
	s.seedStep(m.ID, "a", 0, stepChecksum(0, m.Steps[0]))
	s.seedStep(m.ID, "b", 1, stepChecksum(1, m.Steps[1]))

	require.NoError(t, run(ctx, c, s, []Migration{m}))

	assert.Equal(t, []string{"20260101000000/c"}, calls, "only the un-applied step runs")
	migs, _ := s.loadAppliedMigrations(ctx)
	assert.Len(t, migs, 1, "migration record written once all steps complete")
}

func TestRun_SkipsFullyAppliedMigration(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	m := makeMigration(20260101000000, "first", []string{"a"}, &calls)
	s.seedMigration(m.ID, m.Name, migrationChecksum(m))

	require.NoError(t, run(ctx, c, s, []Migration{m}))
	assert.Empty(t, calls, "no steps run for an already-applied migration")
}

func TestRun_FailsOnMigrationChecksumMismatch(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	m := makeMigration(20260101000000, "first", []string{"a"}, &calls)
	s.seedMigration(m.ID, m.Name, "deadbeefdeadbeef")

	err := run(ctx, c, s, []Migration{m})
	var mm *ErrChecksumMismatch
	require.ErrorAs(t, err, &mm)
	assert.Equal(t, m.ID, mm.ID)
}

func TestRun_FailsOnInFlightStepChecksumMismatch(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	m := makeMigration(20260101000000, "first", []string{"a", "b"}, &calls)
	s.seedStep(m.ID, "a", 0, "tamperedchecksum") // step recorded with wrong checksum

	err := run(ctx, c, s, []Migration{m})
	var mm *ErrChecksumMismatch
	require.ErrorAs(t, err, &mm, "editing an in-flight migration is caught on resume")
	assert.Empty(t, calls, "nothing runs when a recorded step's checksum drifted")
}

func TestRun_FailsWhenRecordedStepRemoved(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	m := makeMigration(20260101000000, "first", []string{"a"}, &calls)
	// A step at index 1 was recorded, but the migration now has only one step:
	// a shipped step was deleted.
	s.seedStep(m.ID, "b", 1, "anychecksum")

	err := run(ctx, c, s, []Migration{m})
	var removed *ErrStepRemoved
	require.ErrorAs(t, err, &removed, "deleting a shipped step is caught on resume")
	assert.Equal(t, m.ID, removed.MigrationID)
	assert.Equal(t, 1, removed.Index)
	assert.Equal(t, "b", removed.Name)
	assert.Empty(t, calls, "nothing runs when a recorded step was removed")
}

func TestRun_FailsWhenRecordedStepRenamed(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	m := makeMigration(20260101000000, "first", []string{"a", "b"}, &calls)
	// A step recorded at index 0 under a different name than the registered step
	// at that ordinal: the step was renamed in place.
	s.seedStep(m.ID, "renamed", 0, stepChecksum(0, m.Steps[0]))

	err := run(ctx, c, s, []Migration{m})
	var mm *ErrChecksumMismatch
	require.ErrorAs(t, err, &mm, "renaming a step at a fixed ordinal is caught on resume")
	assert.Empty(t, calls, "nothing runs when a recorded step's name drifted")
}

func TestRun_FailsOnMissingApplied(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	s.seedMigration(20260101000000, "deleted", "anychecksum")
	other := makeMigration(20260102000000, "other", []string{"a"}, &calls)

	err := run(ctx, c, s, []Migration{other})
	var missing *ErrMissingApplied
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, int64(20260101000000), missing.ID)
}

func TestRun_StopsOnStepFailureLeavingForwardProgress(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	boom := errors.New("step b exploded")
	m := Migration{ID: 20260101000000, Name: "first", Steps: []Step{
		{Name: "a", Schema: SchemaChange{Alter: "a: string ."}},
		{Name: "b", Up: func(context.Context, mg.Client) error { return boom }},
		{Name: "c", Schema: SchemaChange{Alter: "c: string ."}},
	}}

	err := run(ctx, c, s, []Migration{m})
	var sf *ErrStepFailed
	require.ErrorAs(t, err, &sf)
	assert.Equal(t, 1, sf.Index)
	assert.Equal(t, "b", sf.StepName)

	steps, _ := s.loadStepRecords(ctx, m.ID)
	assert.Len(t, steps, 1, "step a recorded; b and c not")
	migs, _ := s.loadAppliedMigrations(ctx)
	assert.Empty(t, migs, "migration not recorded when a step failed")
}

func TestRun_ReturnsErrLockedWhenHeld(t *testing.T) {
	s := newFakeStore()
	s.locked = true
	err := run(ctx, &stubClient{}, s, nil)
	require.ErrorIs(t, err, ErrLocked)
}

func TestRun_MultiVersionAppliesAllInOrder(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	m1 := makeMigration(20260101000000, "one", []string{"a"}, &calls)
	m2 := makeMigration(20260102000000, "two", []string{"b"}, &calls)
	m2.After = m1.ID
	m3 := makeMigration(20260103000000, "three", []string{"c"}, &calls)
	m3.After = m2.ID
	// Register out of order to prove the runner orders by the After chain, not slice order.
	require.NoError(t, run(ctx, c, s, []Migration{m3, m1, m2}))

	assert.Equal(t, []string{"20260101000000/a", "20260102000000/b", "20260103000000/c"}, calls)
	migs, _ := s.loadAppliedMigrations(ctx)
	assert.Len(t, migs, 3)
}

func TestDown_ReversesStepsInReverseOrderAndRemovesRecords(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	m := makeMigration(20260101000000, "first", []string{"a", "b"}, &calls)
	require.NoError(t, run(ctx, c, s, []Migration{m}))
	calls = nil // drop the up calls

	require.NoError(t, down(ctx, c, s, []Migration{m}, 0))

	assert.Equal(t, []string{"down 20260101000000/b", "down 20260101000000/a"}, calls, "steps reverse in reverse order")
	migs, _ := s.loadAppliedMigrations(ctx)
	assert.Empty(t, migs, "migration record removed")
	steps, _ := s.loadStepRecords(ctx, m.ID)
	assert.Empty(t, steps, "step records removed")
}

func TestDown_RefusesIrreversibleBeforeAnyWork(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	m := Migration{ID: 20260101000000, Name: "first", Steps: []Step{
		{Name: "a", Down: func(context.Context, mg.Client) error { calls = append(calls, "a"); return nil }},
		{Name: "b"}, // nil Down => irreversible
	}}
	s.seedStep(m.ID, "a", 0, stepChecksum(0, m.Steps[0]))
	s.seedStep(m.ID, "b", 1, stepChecksum(1, m.Steps[1]))
	s.seedMigration(m.ID, m.Name, migrationChecksum(m))

	err := down(ctx, c, s, []Migration{m}, 0)
	var ir *ErrIrreversible
	require.ErrorAs(t, err, &ir)
	assert.Empty(t, calls, "no Down runs when the range is irreversible")
	migs, _ := s.loadAppliedMigrations(ctx)
	assert.Len(t, migs, 1, "nothing removed")
}

func TestDown_MultiVersionToMiddleLeavesCorrectSubset(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	m1 := makeMigration(20260101000000, "one", []string{"a"}, &calls)
	m2 := makeMigration(20260102000000, "two", []string{"b"}, &calls)
	m2.After = m1.ID
	m3 := makeMigration(20260103000000, "three", []string{"c"}, &calls)
	m3.After = m2.ID
	require.NoError(t, run(ctx, c, s, []Migration{m1, m2, m3}))
	calls = nil

	// Roll back everything newer than m1.
	require.NoError(t, down(ctx, c, s, []Migration{m1, m2, m3}, 20260101000000))

	assert.Equal(t, []string{"down 20260103000000/c", "down 20260102000000/b"}, calls, "reverse chain order")
	migs, _ := s.loadAppliedMigrations(ctx)
	require.Len(t, migs, 1)
	assert.Equal(t, int64(20260101000000), migs[0].ID, "only m1 remains")
}

func TestStatus_ReportsAppliedInProgressPending(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	applied := makeMigration(20260101000000, "applied", []string{"a"}, &calls)
	require.NoError(t, run(ctx, c, s, []Migration{applied}))

	inProgress := makeMigration(20260102000000, "wip", []string{"x", "y"}, &calls)
	inProgress.After = applied.ID
	s.seedStep(inProgress.ID, "x", 0, stepChecksum(0, inProgress.Steps[0]))
	pending := makeMigration(20260103000000, "pending", []string{"z"}, &calls)
	pending.After = inProgress.ID

	res, err := status(ctx, s, []Migration{applied, inProgress, pending})
	require.NoError(t, err)
	require.Len(t, res.Applied, 1)
	require.Len(t, res.InProgress, 1)
	require.Len(t, res.Pending, 1)
	assert.Equal(t, 1, res.InProgress[0].StepsApplied)
	assert.Equal(t, 2, res.InProgress[0].StepsTotal)
}

func TestVersion_ReturnsHighestApplied(t *testing.T) {
	s := newFakeStore()
	c := &stubClient{}
	var calls []string
	one := makeMigration(20260101000000, "one", []string{"a"}, &calls)
	two := makeMigration(20260102000000, "two", []string{"b"}, &calls)
	two.After = one.ID
	require.NoError(t, run(ctx, c, s, []Migration{one, two}))
	v, err := version(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, int64(20260102000000), v)
}
