package migrate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// chain IDs reused across the history tests (timestamp-style, per the convention).
const (
	idBaseline = int64(20260528000001)
	idAddMime  = int64(20260601090000)
	idRemap    = int64(20260601100000)
	idRetype   = int64(20260601110000)
	idThumb    = int64(20260601105500)
)

func linearChain() []Migration {
	return []Migration{
		{ID: idRetype, After: idRemap, Name: "retype_size_to_int"},
		{ID: idBaseline, After: 0, Name: "baseline", Steps: []Step{{Name: "ensure_schema"}}},
		{ID: idAddMime, After: idBaseline, Name: "add_mime_category", Steps: []Step{{Name: "alter"}, {Name: "backfill"}}},
		{ID: idRemap, After: idAddMime, Name: "remap_archive_status"},
	}
}

func TestHistory_LinearAnnotatesAppliedInChainOrder(t *testing.T) {
	f := newFakeStore()
	f.seedMigration(idBaseline, "baseline", "sum1")
	f.seedMigration(idAddMime, "add_mime_category", "sum2")

	res, err := history(context.Background(), f, linearChain())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Valid {
		t.Fatalf("expected a valid chain")
	}

	wantOrder := []int64{idBaseline, idAddMime, idRemap, idRetype}
	if len(res.Entries) != len(wantOrder) {
		t.Fatalf("got %d entries, want %d", len(res.Entries), len(wantOrder))
	}
	for i, e := range res.Entries {
		if e.ID != wantOrder[i] {
			t.Fatalf("entry[%d].ID = %d, want %d", i, e.ID, wantOrder[i])
		}
	}
	if !res.Entries[0].IsRoot {
		t.Errorf("first entry should be the root")
	}
	if res.Entries[1].IsRoot {
		t.Errorf("non-root entry flagged IsRoot")
	}
	if !res.Entries[0].Applied || !res.Entries[1].Applied {
		t.Errorf("seeded migrations should be Applied")
	}
	if res.Entries[2].Applied || res.Entries[3].Applied {
		t.Errorf("unseeded migrations should be pending")
	}
	if got := res.Entries[1].Steps; len(got) != 2 || got[0] != "alter" {
		t.Errorf("step names not captured: %v", got)
	}
}

func TestHistory_DivergentSurfacesTypedError(t *testing.T) {
	migs := []Migration{
		{ID: idBaseline, After: 0, Name: "baseline"},
		{ID: idAddMime, After: idBaseline, Name: "add_mime_category"},
		{ID: idRemap, After: idAddMime, Name: "remap_archive_status"},
		{ID: idThumb, After: idAddMime, Name: "add_thumbnail_predicate"},
	}
	res, err := history(context.Background(), newFakeStore(), migs)

	var de *ErrDivergentHistory
	if !errors.As(err, &de) {
		t.Fatalf("want *ErrDivergentHistory, got %v", err)
	}
	if res.Valid {
		t.Errorf("divergent chain should not be Valid")
	}
	if len(res.Entries) != len(migs) {
		t.Errorf("entries should still be populated for rendering: got %d", len(res.Entries))
	}
}

func TestRenderHistory_Linear(t *testing.T) {
	f := newFakeStore()
	f.seedMigration(idBaseline, "baseline", "sum1")
	f.seedMigration(idAddMime, "add_mime_category", "sum2")
	res, _ := history(context.Background(), f, linearChain())

	out := RenderHistory(res, false, false)
	for _, want := range []string{
		"Migrations (4)", "root → head",
		"[x] 20260528000001  baseline", "(root)",
		"[ ] 20260601110000", "after 20260601100000",
		"head 20260601110000", "applied 20260601090000", "2 pending",
		"[x] applied",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("linear render missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderHistory_LinearVerboseShowsSteps(t *testing.T) {
	res, _ := history(context.Background(), newFakeStore(), linearChain())
	out := RenderHistory(res, false, true)
	if !strings.Contains(out, "2 step(s): alter, backfill") {
		t.Errorf("verbose render missing step detail\n---\n%s", out)
	}
}

func TestRenderHistory_DivergentList(t *testing.T) {
	migs := []Migration{
		{ID: idBaseline, After: 0, Name: "baseline"},
		{ID: idAddMime, After: idBaseline, Name: "add_mime_category"},
		{ID: idRemap, After: idAddMime, Name: "remap_archive_status"},
		{ID: idThumb, After: idAddMime, Name: "add_thumbnail_predicate"},
	}
	res, _ := history(context.Background(), newFakeStore(), migs)

	out := RenderHistory(res, false, false)
	for _, want := range []string{
		"✘ DIVERGENT HISTORY",
		"predecessor 20260601090000 has 2 children",
		"remap_archive_status",
		"add_thumbnail_predicate",
		"Re-point one migration's After",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("divergent render missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderHistory_TreeMarksFork(t *testing.T) {
	migs := []Migration{
		{ID: idBaseline, After: 0, Name: "baseline"},
		{ID: idAddMime, After: idBaseline, Name: "add_mime_category"},
		{ID: idRemap, After: idAddMime, Name: "remap_archive_status"},
		{ID: idThumb, After: idAddMime, Name: "add_thumbnail_predicate"},
	}
	res, _ := history(context.Background(), newFakeStore(), migs)

	out := RenderHistory(res, true, false)
	for _, want := range []string{"└── ", "├── ", "✘ 2 children — divergent"} {
		if !strings.Contains(out, want) {
			t.Errorf("tree render missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderHistory_TreeLinearSpine(t *testing.T) {
	res, _ := history(context.Background(), newFakeStore(), linearChain())
	out := RenderHistory(res, true, false)
	// A sound chain is a single spine: a root with no fork markers.
	if strings.Contains(out, "divergent") {
		t.Errorf("linear chain should not be flagged divergent\n---\n%s", out)
	}
	if !strings.Contains(out, "└── ") {
		t.Errorf("tree render missing chain connectors\n---\n%s", out)
	}
}
