package migrate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetypePredicate_ExpandsToFiveIrreversibleSteps(t *testing.T) {
	steps := RetypePredicate(RetypeSpec{
		Predicate: "height", To: Int, Index: "int",
		Convert: func(old string) (any, error) { return old, nil },
	})
	require.Len(t, steps, 5)
	assert.Equal(t, []string{
		"height_retype_stage", "height_retype_verify", "height_retype_swap",
		"height_retype_copy", "height_retype_cleanup",
	}, []string{steps[0].Name, steps[1].Name, steps[2].Name, steps[3].Name, steps[4].Name})
	assert.Equal(t, "height__retype_staging: int @index(int) .", steps[0].Schema.Alter, "stage declares the staging predicate")
	for i, s := range steps {
		assert.Nil(t, s.Down, "step %d (%s) must be irreversible", i, s.Name)
	}
}

func TestRetypePredicate_PanicsOnNilConvert(t *testing.T) {
	assert.PanicsWithValue(t, "migrate: RetypeSpec.Convert must not be nil", func() {
		RetypePredicate(RetypeSpec{Predicate: "height", To: Int, Index: "int"})
	}, "a nil Convert must fail loudly at construction, not deep in the stage step")
}
