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

// verifyOther declares verify_extra under its own type, so the predicate exists
// live while VerifyDoc does not list it.
type verifyOther struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty" dgraph:"VerifyOther"`
	Extra string   `json:"extra,omitempty" dgraph:"predicate=verify_extra index=exact"`
}

type verifyNew struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty" dgraph:"VerifyNew"`
	Name  string   `json:"name,omitempty" dgraph:"predicate=verify_name index=term"`
}

func TestVerify_ReportsTypeMissingField(t *testing.T) {
	c := newEmbeddedClient(t)
	ctx := context.Background()

	require.NoError(t, c.AlterSchema(ctx, mustMarshalSchema(t, &verifyV1{}, &verifyOther{})))

	clean, err := Verify(ctx, c, []any{&verifyV1{}, &verifyOther{}})
	require.NoError(t, err)
	assert.True(t, clean.Clean(), "expected no drift, got %+v", clean)

	// verify_extra exists live, but the live VerifyDoc type does not list it.
	drift, err := Verify(ctx, c, []any{&verifyV2{}, &verifyOther{}})
	require.NoError(t, err)
	assert.False(t, drift.Clean(), "expected drift for the missing type field")
	assert.Empty(t, drift.Missing, "no predicate is missing")
	assert.Equal(t, []string{"VerifyDoc.verify_extra"}, drift.MissingTypeFields)
	assert.Empty(t, drift.MissingTypes)
}

func TestVerify_ReportsMissingType(t *testing.T) {
	c := newEmbeddedClient(t)
	ctx := context.Background()

	require.NoError(t, c.AlterSchema(ctx, mustMarshalSchema(t, &verifyV1{})))

	drift, err := Verify(ctx, c, []any{&verifyV1{}, &verifyNew{}})
	require.NoError(t, err)
	assert.False(t, drift.Clean(), "expected drift for the missing type")
	assert.Equal(t, []string{"VerifyNew"}, drift.MissingTypes)
	assert.Empty(t, drift.MissingTypeFields, "fields of a missing type are covered by MissingTypes")
}

type verifyParent struct {
	UID      string         `json:"uid,omitempty"`
	DType    []string       `json:"dgraph.type,omitempty" dgraph:"VerifyParent"`
	Children []*verifyChild `json:"children,omitempty" dgraph:"predicate=verify_children reverse"`
}

type verifyChild struct {
	UID     string          `json:"uid,omitempty"`
	DType   []string        `json:"dgraph.type,omitempty" dgraph:"VerifyChild"`
	Name    string          `json:"name,omitempty" dgraph:"predicate=verify_child_name"`
	Parents []*verifyParent `json:"~verify_children,omitempty" dgraph:"reverse"`
}

func TestVerify_ReverseEdgeTypeIsClean(t *testing.T) {
	c := newEmbeddedClient(t)
	ctx := context.Background()
	models := []any{&verifyParent{}, &verifyChild{}}

	require.NoError(t, c.AlterSchema(ctx, mustMarshalSchema(t, models...)))

	drift, err := Verify(ctx, c, models)
	require.NoError(t, err)
	assert.True(t, drift.Clean(), "expected no drift, got %+v", drift)
}
