package migrate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type checksumWidget struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty"`
	Name  string   `json:"name,omitempty" dgraph:"index=exact"`
}

func TestStepChecksum_StableAndDistinct(t *testing.T) {
	s := Step{Name: "add_name", Schema: SchemaChange{Alter: "name: string @index(exact) ."}}
	c1 := stepChecksum(0, s)
	require.Len(t, c1, 64, "SHA-256 hex is 64 chars")
	assert.Equal(t, c1, stepChecksum(0, s), "same step+index → same checksum")

	assert.NotEqual(t, c1, stepChecksum(1, s), "index is part of identity")

	other := Step{Name: "add_name", Schema: SchemaChange{Alter: "age: int ."}}
	assert.NotEqual(t, c1, stepChecksum(0, other), "different Alter → different checksum")

	renamed := Step{Name: "rename", Schema: s.Schema}
	assert.NotEqual(t, c1, stepChecksum(0, renamed), "step name is part of identity")
}

func TestStepChecksum_Ensure(t *testing.T) {
	s := Step{Name: "ensure_widget", Schema: SchemaChange{Ensure: []any{&checksumWidget{}}}}
	c1 := stepChecksum(0, s)
	require.Len(t, c1, 64)
	assert.Equal(t, c1, stepChecksum(0, s))
	assert.NotEqual(t, c1, stepChecksum(0, Step{Name: "ensure_widget"}), "zero SchemaChange differs")
}

// checksumMultiPredicate has several indexed predicates so the dgman TypeSchema
// carries multiple map entries — the case where TypeSchema.String()'s randomized
// map iteration would otherwise produce an unstable hash.
type checksumMultiPredicate struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty"`
	Name  string   `json:"name,omitempty" dgraph:"index=exact"`
	Email string   `json:"email,omitempty" dgraph:"index=hash"`
	Age   int      `json:"age,omitempty" dgraph:"index=int"`
	City  string   `json:"city,omitempty" dgraph:"index=term"`
}

// TestStepChecksum_EnsureDeterministicAcrossCalls guards the canonicalTypeSchema
// fix: an Ensure step's checksum must be identical across many calls, otherwise
// a struct-derived migration falsely reports drift and fails the immutability
// guard on re-run.
func TestStepChecksum_EnsureDeterministicAcrossCalls(t *testing.T) {
	s := Step{Name: "ensure_multi", Schema: SchemaChange{Ensure: []any{&checksumMultiPredicate{}}}}
	first := stepChecksum(0, s)
	for i := 0; i < 50; i++ {
		assert.Equal(t, first, stepChecksum(0, s), "Ensure checksum must be stable across calls (call %d)", i)
	}
}

func TestMigrationChecksum_OrderSensitive(t *testing.T) {
	a := Step{Name: "a", Schema: SchemaChange{Alter: "a: string ."}}
	b := Step{Name: "b", Schema: SchemaChange{Alter: "b: string ."}}
	m1 := Migration{ID: 1, Steps: []Step{a, b}}
	m2 := Migration{ID: 1, Steps: []Step{b, a}}
	require.Len(t, migrationChecksum(m1), 64)
	assert.NotEqual(t, migrationChecksum(m1), migrationChecksum(m2), "step order changes the checksum")
}
