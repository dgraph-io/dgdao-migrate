package migratecli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mg "github.com/dgraph-io/dgdao"
	"github.com/dgraph-io/dgdao-migrate/migrate"
)

// cmdBase/cmdAdded differ by one predicate so a diff against a cmdBase snapshot
// reports cmdAdded's extra predicate as drift.
type cmdBase struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty" dgraph:"CmdDoc"`
	Name  string   `json:"name,omitempty" dgraph:"predicate=cmd_name index=term"`
}

type cmdAdded struct {
	UID   string   `json:"uid,omitempty"`
	DType []string `json:"dgraph.type,omitempty" dgraph:"CmdDoc"`
	Name  string   `json:"name,omitempty" dgraph:"predicate=cmd_name index=term"`
	Extra string   `json:"extra,omitempty" dgraph:"predicate=cmd_extra index=exact"`
}

// fakeProvider satisfies Provider for the authoring commands. Client is unused by
// create/diff/snapshot and returns nil.
type fakeProvider struct {
	migs   []migrate.Migration
	models []any
}

func (f *fakeProvider) Client() mg.Client               { return nil }
func (f *fakeProvider) Migrations() []migrate.Migration { return f.migs }
func (f *fakeProvider) Models() []any                   { return f.models }

// tempProject builds a minimal Go project with a seeded desired-state snapshot.
func tempProject(t *testing.T, state string) (root, dir string) {
	t.Helper()
	root = t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/test\n\ngo 1.21\n")
	dir = filepath.Join(root, "migrations")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "migrations.go"),
		"package migrations\n\nimport \"github.com/dgraph-io/dgdao-migrate/migrate\"\n\nvar All = []migrate.Migration{}\n")
	write(t, filepath.Join(dir, "schema_state.schema"), state)
	return root, dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustSchema(t *testing.T, models ...any) string {
	t.Helper()
	s, err := migrate.MarshalSchema(models...)
	if err != nil {
		t.Fatalf("MarshalSchema: %v", err)
	}
	return s
}

func TestCreateCmd_WritesMigrationFiles(t *testing.T) {
	root, dir := tempProject(t, mustSchema(t, &cmdBase{}))
	p := &fakeProvider{
		migs:   []migrate.Migration{{ID: 20260528000001, After: 0, Name: "baseline"}},
		models: []any{&cmdAdded{}},
	}

	cmd := &CreateCmd{Name: "add extra", Register: true, ProjectRoot: root}
	if err := cmd.Run(p); err != nil {
		t.Fatalf("CreateCmd.Run: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	var goFiles, schemaFiles int
	for _, e := range entries {
		switch {
		case e.Name() == "migrations.go":
		case strings.HasSuffix(e.Name(), ".go"):
			goFiles++
		case strings.HasSuffix(e.Name(), ".schema") && e.Name() != "schema_state.schema":
			schemaFiles++
		}
	}
	if goFiles != 1 || schemaFiles != 1 {
		t.Fatalf("expected one .go and one .schema migration file; got go=%d schema=%d", goFiles, schemaFiles)
	}

	// The new var was registered.
	reg, _ := os.ReadFile(filepath.Join(dir, "migrations.go"))
	if !strings.Contains(string(reg), "addExtra") {
		t.Errorf("migration not registered in All:\n%s", reg)
	}
}

func TestDiffCmd_CheckExitsNonZeroOnDrift(t *testing.T) {
	root, _ := tempProject(t, mustSchema(t, &cmdBase{}))

	// Models carry an extra predicate absent from the snapshot → drift.
	drifted := &fakeProvider{models: []any{&cmdAdded{}}}
	if err := (&DiffCmd{Check: true, ProjectRoot: root}).Run(drifted); err == nil {
		t.Errorf("diff --check should exit non-zero when the structs have drifted")
	}

	// Models matching the snapshot → no drift, no error.
	clean := &fakeProvider{models: []any{&cmdBase{}}}
	if err := (&DiffCmd{Check: true, ProjectRoot: root}).Run(clean); err != nil {
		t.Errorf("diff --check should pass when structs match the snapshot: %v", err)
	}
}
