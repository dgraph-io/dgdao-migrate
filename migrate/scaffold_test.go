package migrate

import (
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The two fixture struct sets differ by exactly one change per classification:
//
//	diff_mime   — present only in current        → Added
//	diff_title  — index term → exact             → IndexChanged
//	diff_size   — type int → string              → TypeChanged
//	diff_legacy — present only in prev           → Removed
type diffPrev struct {
	UID    string   `json:"uid,omitempty"`
	DType  []string `json:"dgraph.type,omitempty" dgraph:"DiffDoc"`
	Title  string   `json:"title,omitempty" dgraph:"predicate=diff_title index=term"`
	Size   int      `json:"size,omitempty" dgraph:"predicate=diff_size"`
	Legacy string   `json:"legacy,omitempty" dgraph:"predicate=diff_legacy"`
}

type diffCurrent struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty" dgraph:"DiffDoc"`
	Title string   `json:"title,omitempty" dgraph:"predicate=diff_title index=exact"`
	Size  string   `json:"size,omitempty" dgraph:"predicate=diff_size"`
	Mime  string   `json:"mime,omitempty" dgraph:"predicate=diff_mime index=exact"`
}

func bucketHas(bucket []string, substr string) bool {
	for _, s := range bucket {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func TestDiffSchema_ClassifiesEachChange(t *testing.T) {
	prev := mustMarshalSchema(t, &diffPrev{})
	current := mustMarshalSchema(t, &diffCurrent{})

	d := diffSchema(prev, current)

	if !bucketHas(d.Added, "diff_mime") {
		t.Errorf("diff_mime should be Added; got %v", d.Added)
	}
	if !bucketHas(d.IndexChanged, "diff_title") {
		t.Errorf("diff_title should be IndexChanged; got %v", d.IndexChanged)
	}
	if !bucketHas(d.TypeChanged, "diff_size") {
		t.Errorf("diff_size should be TypeChanged; got %v", d.TypeChanged)
	}
	if !bucketHas(d.Removed, "diff_legacy") {
		t.Errorf("diff_legacy should be Removed; got %v", d.Removed)
	}

	// Each change lands in exactly one bucket.
	if bucketHas(d.Added, "diff_size") || bucketHas(d.IndexChanged, "diff_size") {
		t.Errorf("type change leaked into an additive bucket: Added=%v Index=%v", d.Added, d.IndexChanged)
	}
	if bucketHas(d.Added, "diff_title") {
		t.Errorf("index change misclassified as Added: %v", d.Added)
	}

	// The type change is destructive and must not be emittable schema.
	if d.HasAdditive() && bucketHas(d.Additive(), "diff_size") {
		t.Errorf("retype must never be emitted as additive schema")
	}
	if !d.HasFlagged() {
		t.Errorf("expected flagged changes (type change + removal)")
	}
}

func TestDiffSchema_IdenticalIsEmpty(t *testing.T) {
	s := mustMarshalSchema(t, &diffCurrent{})
	if d := diffSchema(s, s); !d.Empty() {
		t.Errorf("identical schemas should produce an empty delta; got %+v", d)
	}
}

func TestDiffSchema_EmptyPrevIsAllAdded(t *testing.T) {
	current := mustMarshalSchema(t, &diffCurrent{})
	d := diffSchema("", current)
	if len(d.Added) == 0 || d.HasFlagged() {
		t.Errorf("empty prior state should make every predicate Added with no flags; got %+v", d)
	}
	if !bucketHas(d.Added, "diff_title") || !bucketHas(d.Added, "diff_mime") {
		t.Errorf("expected all current predicates in Added; got %v", d.Added)
	}
}

// --- Scaffold / Snapshot fixtures -------------------------------------------

type scafBase struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty" dgraph:"ScafDoc"`
	Name  string   `json:"name,omitempty" dgraph:"predicate=scaf_name index=term"`
}

type scafAdded struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty" dgraph:"ScafDoc"`
	Name  string   `json:"name,omitempty" dgraph:"predicate=scaf_name index=term"`
	Mime  string   `json:"mime,omitempty" dgraph:"predicate=scaf_mime index=exact"`
}

type retypeBefore struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty" dgraph:"RetypeDoc"`
	Size  int      `json:"size,omitempty" dgraph:"predicate=scaf_size"`
}

type retypeAfter struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty" dgraph:"RetypeDoc"`
	Size  string   `json:"size,omitempty" dgraph:"predicate=scaf_size"`
}

// tempProject builds a minimal Go project (go.mod + migrations/ + an empty All
// registry) and returns its root and migrations directory.
func tempProject(t *testing.T) (root, dir string) {
	t.Helper()
	root = t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/test\n\ngo 1.21\n")
	dir = filepath.Join(root, "migrations")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "migrations.go"),
		"package migrations\n\nimport \"github.com/dgraph-io/dgdao-migrate/migrate\"\n\nvar All = []migrate.Migration{}\n")
	return root, dir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// --- renderMigrationFile (template) -----------------------------------------

func TestRenderMigrationFile_AddOnly(t *testing.T) {
	delta := Delta{Added: []string{"scaf_mime: string @index(exact) ."}}
	goSrc, schema := renderMigrationFile("migrations", "add_mime", 20260601090000, 20260528000001, delta)

	if _, err := format.Source([]byte(goSrc)); err != nil {
		t.Fatalf("generated source is not gofmt-valid: %v\n%s", err, goSrc)
	}
	for _, want := range []string{
		"package migrations",
		"//go:embed 20260601090000_add_mime.schema",
		"var addMimeSchema string",
		"var addMime20260601090000 = migrate.Migration{",
		"After: 20260528000001,",
		"EnsureSchema: addMimeSchema",
	} {
		if !strings.Contains(goSrc, want) {
			t.Errorf("add-only source missing %q\n---\n%s", want, goSrc)
		}
	}
	if schema != "scaf_mime: string @index(exact) .\n" {
		t.Errorf("unexpected schema content: %q", schema)
	}
}

func TestRenderMigrationFile_FlaggedOnly(t *testing.T) {
	delta := Delta{
		TypeChanged: []string{"scaf_size: int → string"},
		Removed:     []string{"scaf_legacy: string ."},
	}
	goSrc, schema := renderMigrationFile("migrations", "retype_size", 20260601100000, 20260601090000, delta)

	if _, err := format.Source([]byte(goSrc)); err != nil {
		t.Fatalf("generated source is not gofmt-valid: %v\n%s", err, goSrc)
	}
	if schema != "" {
		t.Errorf("flagged-only delta must produce an empty schema; got %q", schema)
	}
	for _, want := range []string{"SCAFFOLD NOTES", "TYPE CHANGE: scaf_size", "RetypePredicate", "REMOVED: scaf_legacy"} {
		if !strings.Contains(goSrc, want) {
			t.Errorf("flagged source missing %q\n---\n%s", want, goSrc)
		}
	}
	for _, notWant := range []string{"SchemaChange{EnsureSchema", "//go:embed"} {
		if strings.Contains(goSrc, notWant) {
			t.Errorf("flagged-only source must not contain %q\n---\n%s", notWant, goSrc)
		}
	}
}

// --- Scaffold / Snapshot ----------------------------------------------------

func TestScaffold_AddOnlyWiresEnsureStepAndAdvancesState(t *testing.T) {
	_, dir := tempProject(t)
	mustWrite(t, filepath.Join(dir, schemaStateFile), mustMarshalSchema(t, &scafBase{}))

	report, err := Scaffold(ScaffoldParams{
		Migrations: []Migration{{ID: 20260528000001, After: 0, Name: "baseline"}},
		Models:     []any{&scafAdded{}},
		Dir:        dir,
		Package:    "migrations",
		Name:       "Add Mime!",
		Now:        time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC),
		Register:   true,
	})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	if report.MigrationID != 20260601090000 {
		t.Errorf("deterministic ID = %d, want 20260601090000", report.MigrationID)
	}
	if report.After != 20260528000001 {
		t.Errorf("After = %d, want baseline head", report.After)
	}
	if !report.HasDelta {
		t.Errorf("add-only scaffold should report a delta")
	}
	if !report.Registered {
		t.Errorf("expected registration into the All slice")
	}

	goSrc := readFile(t, report.GoFile)
	if !strings.Contains(goSrc, "EnsureSchema") {
		t.Errorf("generated migration missing EnsureSchema step:\n%s", goSrc)
	}
	if s := readFile(t, report.SchemaFile); !strings.Contains(s, "scaf_mime") {
		t.Errorf("schema file missing the added predicate: %q", s)
	}

	// State advanced to the current structs → a follow-up diff is empty.
	if d, err := Diff(dir, []any{&scafAdded{}}); err != nil || !d.Empty() {
		t.Errorf("state not advanced; follow-up diff err=%v shows %+v", err, d)
	}
	// The new var was appended to All.
	if reg := readFile(t, filepath.Join(dir, "migrations.go")); !strings.Contains(reg, "addMime20260601090000") {
		t.Errorf("All slice not updated:\n%s", reg)
	}
}

func TestScaffold_FlaggedOnlyEmitsStubAndNotes(t *testing.T) {
	_, dir := tempProject(t)
	mustWrite(t, filepath.Join(dir, schemaStateFile), mustMarshalSchema(t, &retypeBefore{}))

	report, err := Scaffold(ScaffoldParams{
		Migrations: []Migration{{ID: 20260528000001, After: 0, Name: "baseline"}},
		Models:     []any{&retypeAfter{}},
		Dir:        dir,
		Package:    "migrations",
		Name:       "retype_size",
		Now:        time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	if report.HasDelta {
		t.Errorf("flagged-only scaffold should report no additive delta")
	}
	if len(report.TypeChanged) == 0 {
		t.Errorf("expected a flagged type change in the report")
	}
	if s := readFile(t, report.SchemaFile); s != "" {
		t.Errorf("flagged-only schema file must be empty; got %q", s)
	}
	goSrc := readFile(t, report.GoFile)
	if strings.Contains(goSrc, "SchemaChange{EnsureSchema") {
		t.Errorf("flagged-only migration must not emit an EnsureSchema step:\n%s", goSrc)
	}
	if !strings.Contains(goSrc, "SCAFFOLD NOTES") {
		t.Errorf("flagged-only migration missing SCAFFOLD NOTES:\n%s", goSrc)
	}
}

func TestScaffold_RejectsTestSuffixName(t *testing.T) {
	_, dir := tempProject(t)
	_, err := Scaffold(ScaffoldParams{
		Migrations: []Migration{{ID: 20260528000001, After: 0, Name: "baseline"}},
		Models:     []any{&scafBase{}},
		Dir:        dir,
		Package:    "migrations",
		Name:       "smoke_test",
		Now:        time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("a name producing a _test.go file must be rejected")
	}
	if !strings.Contains(err.Error(), "_test") {
		t.Errorf("error should explain the _test.go problem: %v", err)
	}
	// Rejection happens before any write.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "smoke_test") {
			t.Errorf("rejected scaffold must not write files: %s", e.Name())
		}
	}
}

func TestSnapshot_RewritesStateOnly(t *testing.T) {
	_, dir := tempProject(t)
	path, err := Snapshot(ScaffoldParams{Models: []any{&scafAdded{}}, Dir: dir})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got := readFile(t, path); got != mustMarshalSchema(t, &scafAdded{}) {
		t.Errorf("snapshot did not write the current schema")
	}
	if d, err := Diff(dir, []any{&scafAdded{}}); err != nil || !d.Empty() {
		t.Errorf("diff after snapshot should be empty; err=%v got %+v", err, d)
	}
	// Snapshot writes no migration files.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") && e.Name() != "migrations.go" {
			t.Errorf("snapshot unexpectedly wrote a migration file: %s", e.Name())
		}
	}
}
