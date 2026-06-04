package migrate

import (
	"context"
	"testing"

	mg "github.com/matthewmcneely/modusgraph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newEmbeddedClient opens an embedded (file://) modusGraph client backed by a
// fresh temp dir. Each test gets its own directory so the per-process singleton
// Engine is fully torn down (client.Close then mg.Shutdown) before the next test
// opens a new one. Do not call t.Parallel() in integration tests.
func newEmbeddedClient(t *testing.T) mg.Client {
	t.Helper()
	// t.TempDir() creates the directory, so os.Stat inside NewClient succeeds.
	dir := t.TempDir()
	c, err := mg.NewClient("file://"+dir, mg.WithAutoSchema(true))
	require.NoError(t, err, "open embedded modusgraph")
	t.Cleanup(func() {
		c.Close()
		// Shutdown clears activeEngine and resets the singleton flag so the
		// next test's NewEngine call succeeds. client.Close already resets the
		// singleton via engine.Close, but Shutdown additionally nils activeEngine.
		mg.Shutdown()
	})
	return c
}

type tree struct {
	UID    string   `json:"uid,omitempty"`
	DType  []string `json:"dgraph.type,omitempty" dgraph:"Tree"`
	Height int      `json:"height,omitempty" dgraph:"predicate=height index=int"`
}

func TestIntegration_RunRecordsAndResumes(t *testing.T) {
	c := newEmbeddedClient(t)
	s := newDgraphStore(c)
	var calls []string
	m := makeMigration(20260101000000, "first", []string{"a", "b"}, &calls)

	require.NoError(t, run(ctx, c, s, []Migration{m}))

	migs, err := s.loadAppliedMigrations(ctx)
	require.NoError(t, err)
	require.Len(t, migs, 1)
	assert.Equal(t, migrationChecksum(m), migs[0].Checksum, "stored checksum round-trips")

	// Re-running is a no-op (idempotent at the migration level).
	calls = nil
	require.NoError(t, run(ctx, c, s, []Migration{m}))
	assert.Empty(t, calls, "re-run applies nothing")
}

func TestIntegration_HybridSchemaThenBackfill(t *testing.T) {
	c := newEmbeddedClient(t)
	s := newDgraphStore(c)

	// A migration that ensures the Tree schema, then backfills one node.
	m := Migration{ID: 20260101000000, Name: "tree_baseline", Steps: []Step{
		{Name: "ensure_tree", Schema: SchemaChange{Ensure: []any{&tree{}}}},
		{Name: "seed_one", Up: func(ctx context.Context, cl mg.Client) error {
			return cl.Insert(ctx, &tree{DType: []string{"Tree"}, Height: 5730})
		}},
	}}

	require.NoError(t, run(ctx, c, s, []Migration{m}))

	raw, err := c.QueryRaw(ctx, `{ q(func: type(Tree)) { height } }`, nil)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"height":5730`, "backfilled node present with int height")
}

func TestIntegration_ResumeAfterSimulatedPartialApply(t *testing.T) {
	c := newEmbeddedClient(t)
	s := newDgraphStore(c)
	require.NoError(t, s.bootstrap(ctx))

	var calls []string
	m := makeMigration(20260101000000, "first", []string{"a", "b", "c"}, &calls)
	// Simulate a crash that completed step 0 only: record it directly.
	require.NoError(t, s.saveStep(ctx, m.ID, "a", 0, stepChecksum(0, m.Steps[0]), 1))

	require.NoError(t, run(ctx, c, s, []Migration{m}))
	assert.Equal(t, []string{"20260101000000/b", "20260101000000/c"}, calls, "resumes at step b")

	migs, _ := s.loadAppliedMigrations(ctx)
	assert.Len(t, migs, 1)
}

func TestIntegration_DownRoundTrip(t *testing.T) {
	c := newEmbeddedClient(t)
	s := newDgraphStore(c)
	var calls []string
	m := makeMigration(20260101000000, "first", []string{"a", "b"}, &calls)

	require.NoError(t, run(ctx, c, s, []Migration{m}))
	require.NoError(t, down(ctx, c, s, []Migration{m}, 0))

	v, err := version(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, int64(0), v, "version back to 0 after full down")

	// up again succeeds (no leftover records block it).
	calls = nil
	require.NoError(t, run(ctx, c, s, []Migration{m}))
	assert.Equal(t, []string{"20260101000000/a", "20260101000000/b"}, calls)
}
