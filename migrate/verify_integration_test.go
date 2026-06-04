package migrate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type verifyV1 struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty" dgraph:"VerifyDoc"`
	Name  string   `json:"name,omitempty" dgraph:"predicate=verify_name index=term"`
}

type verifyV2 struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty" dgraph:"VerifyDoc"`
	Name  string   `json:"name,omitempty" dgraph:"predicate=verify_name index=term"`
	Extra string   `json:"extra,omitempty" dgraph:"predicate=verify_extra index=exact"`
}

func TestVerify_CleanThenReportsMissing(t *testing.T) {
	c := newEmbeddedClient(t)
	ctx := context.Background()

	require.NoError(t, c.AlterSchema(ctx, mustMarshalSchema(t, &verifyV1{})))

	// The live schema matches the structs it was built from.
	clean, err := Verify(ctx, c, []any{&verifyV1{}})
	require.NoError(t, err)
	assert.True(t, clean.Clean(), "expected no drift, got %+v", clean)

	// A struct set with an extra predicate the database has not applied drifts.
	drift, err := Verify(ctx, c, []any{&verifyV2{}})
	require.NoError(t, err)
	assert.False(t, drift.Clean(), "expected drift for the unapplied predicate")
	assert.True(t, bucketHas(drift.Missing, "verify_extra"),
		"verify_extra should be reported Missing; got %+v", drift)
	// The already-applied predicate must not be flagged.
	assert.False(t, bucketHas(drift.Missing, "verify_name"))
}
