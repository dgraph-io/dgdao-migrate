package migrate

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"testing"

	mg "github.com/dgraph-io/dgdao"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// metersToMM converts a meters string ("5.73") to integer millimeters (5730).
func metersToMM(old string) (any, error) {
	m, err := strconv.ParseFloat(old, 64)
	if err != nil {
		return nil, fmt.Errorf("non-numeric height %q: %w", old, err)
	}
	return int64(math.Round(m * 1000)), nil
}

// seedStringHeights declares height as a string predicate and inserts raw nodes.
func seedStringHeights(t *testing.T, c mg.Client, values ...string) {
	t.Helper()
	require.NoError(t, c.AlterSchema(context.Background(), "height: string ."))
	rows := make([]map[string]any, 0, len(values))
	for _, v := range values {
		rows = append(rows, map[string]any{"height": v})
	}
	require.NoError(t, mutateRows(context.Background(), c, rows))
}

func retypeMigration() Migration {
	return Migration{ID: 20260601000000, Name: "height_m_to_mm", Steps: RetypePredicate(RetypeSpec{
		Predicate: "height", To: Int, Index: "int", Convert: metersToMM,
	})}
}

func TestRetype_HappyPath(t *testing.T) {
	c := newEmbeddedClient(t)
	s := newDgraphStore(c)
	seedStringHeights(t, c, "5.73", "0.5", "12")

	require.NoError(t, run(ctx, c, s, []Migration{retypeMigration()}))

	// height is now int; values converted to mm; staging gone.
	raw, err := c.QueryRaw(ctx, `{ q(func: has(height), orderasc: height) { height } }`, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"q":[{"height":500},{"height":5730},{"height":12000}]}`, string(raw))

	stc, err := countHas(ctx, c, "height__retype_staging")
	require.NoError(t, err)
	assert.Equal(t, 0, stc, "staging predicate cleaned up")
}

func TestRetype_ResumeAfterCrashAtEachBoundary(t *testing.T) {
	for k := 0; k < 5; k++ {
		t.Run(fmt.Sprintf("crash_after_step_%d", k), func(t *testing.T) {
			c := newEmbeddedClient(t)
			s := newDgraphStore(c)
			seedStringHeights(t, c, "5.73", "0.5")

			m := retypeMigration()
			// Wrap step k+1's Up to fail exactly once, simulating a crash that
			// completed steps 0..k but not k+1.
			if k < 4 {
				orig := m.Steps[k+1].Up
				failed := false
				m.Steps[k+1].Up = func(ctx context.Context, cl mg.Client) error {
					if !failed {
						failed = true
						return fmt.Errorf("injected crash before step %d", k+1)
					}
					return orig(ctx, cl)
				}
			}
			// First run fails (unless k==4, where there's no later step to fail).
			err := run(ctx, c, s, []Migration{m})
			if k < 4 {
				require.Error(t, err, "first run should fail at the injected step")
			} else {
				require.NoError(t, err)
			}
			// Re-run with a clean migration resumes from the recorded checkpoint.
			require.NoError(t, run(ctx, c, s, []Migration{retypeMigration()}))

			raw, err := c.QueryRaw(ctx, `{ q(func: has(height), orderasc: height) { height } }`, nil)
			require.NoError(t, err)
			assert.JSONEq(t, `{"q":[{"height":500},{"height":5730}]}`, string(raw), "converges after resume")
			stc, _ := countHas(ctx, c, "height__retype_staging")
			assert.Equal(t, 0, stc, "staging cleaned up after resume")
		})
	}
}

func TestRetype_DataDependentFailureLeavesSourceUntouched(t *testing.T) {
	c := newEmbeddedClient(t)
	s := newDgraphStore(c)
	seedStringHeights(t, c, "5.73", "not-a-number", "0.5")

	err := run(ctx, c, s, []Migration{retypeMigration()})
	require.Error(t, err, "stage Convert fails on the non-numeric value")

	// Source still string, all 3 values intact; no destructive swap happened.
	sc, err := countHas(ctx, c, "height")
	require.NoError(t, err)
	assert.Equal(t, 3, sc, "source predicate untouched")
	raw, _ := c.QueryRaw(ctx, `{ q(func: has(height)) @filter(eq(height, "not-a-number")) { uid } }`, nil)
	assert.Contains(t, string(raw), "uid", "original string value still present")
}

func TestRetype_IdempotentReRun(t *testing.T) {
	c := newEmbeddedClient(t)
	s := newDgraphStore(c)
	seedStringHeights(t, c, "5.73")

	require.NoError(t, run(ctx, c, s, []Migration{retypeMigration()}))
	require.NoError(t, run(ctx, c, s, []Migration{retypeMigration()}), "second run is a no-op")

	raw, _ := c.QueryRaw(ctx, `{ q(func: has(height)) { height } }`, nil)
	assert.JSONEq(t, `{"q":[{"height":5730}]}`, string(raw))
}
