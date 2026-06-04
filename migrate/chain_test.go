package migrate

import (
	"errors"
	"testing"
)

func TestBuildChain_Linear(t *testing.T) {
	ms := []Migration{
		{ID: 3, After: 2, Name: "c"}, {ID: 1, After: 0, Name: "a"}, {ID: 2, After: 1, Name: "b"},
	}
	got, err := buildChain(ms)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	want := []int64{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d", len(got), len(want))
	}
	for i, m := range got {
		if m.ID != want[i] {
			t.Fatalf("order[%d]=%d want %d", i, m.ID, want[i])
		}
	}
}

func TestBuildChain_DuplicateID(t *testing.T) {
	ms := []Migration{{ID: 1, After: 0, Name: "a"}, {ID: 1, After: 0, Name: "b"}}
	_, err := buildChain(ms)
	var e *ErrDuplicateID
	if !errors.As(err, &e) {
		t.Fatalf("want ErrDuplicateID, got %v", err)
	}
}

func TestBuildChain_NoRoot(t *testing.T) {
	ms := []Migration{{ID: 2, After: 1}}
	_, err := buildChain(ms)
	var e *ErrUnknownPredecessor
	if !errors.As(err, &e) {
		t.Fatalf("want ErrUnknownPredecessor, got %v", err)
	}
}

func TestBuildChain_MultipleRoots(t *testing.T) {
	ms := []Migration{{ID: 1, After: 0}, {ID: 2, After: 0}}
	_, err := buildChain(ms)
	var e *ErrMultipleRoots
	if !errors.As(err, &e) {
		t.Fatalf("want ErrMultipleRoots, got %v", err)
	}
}

func TestBuildChain_Divergent(t *testing.T) {
	ms := []Migration{{ID: 1, After: 0}, {ID: 2, After: 1}, {ID: 3, After: 1}}
	_, err := buildChain(ms)
	var e *ErrDivergentHistory
	if !errors.As(err, &e) {
		t.Fatalf("want ErrDivergentHistory, got %v", err)
	}
}

func TestBuildChain_Cycle(t *testing.T) {
	// One root (id 1), plus a 2<->3 cycle unreachable from the root.
	ms := []Migration{{ID: 1, After: 0}, {ID: 2, After: 3}, {ID: 3, After: 2}}
	_, err := buildChain(ms)
	var e *ErrCycle
	if !errors.As(err, &e) {
		t.Fatalf("want ErrCycle, got %v", err)
	}
}

func TestBuildChain_EmptyEdgeCases(t *testing.T) {
	if _, err := buildChain(nil); err == nil {
		t.Fatal("empty set should error (no root)")
	}
	if got, err := buildChain([]Migration{{ID: 1, After: 0}}); err != nil || len(got) != 1 {
		t.Fatalf("single root should be valid: got %v err %v", got, err)
	}
}
