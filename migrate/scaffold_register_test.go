package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendToAll_AppendsIdentifier(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "migrations.go")
	mustWrite(t, path,
		"package migrations\n\nimport \"github.com/mlwelles/modusGraph-migrate/migrate\"\n\nvar All = []migrate.Migration{\n\tbaseline20260528000001,\n}\n")

	if err := appendToAll(path, "addMime20260601090000"); err != nil {
		t.Fatalf("appendToAll: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "addMime20260601090000") {
		t.Errorf("identifier not appended:\n%s", got)
	}
	if !strings.Contains(got, "baseline20260528000001") {
		t.Errorf("existing element lost:\n%s", got)
	}
	// The rewrite must remain parseable (idempotent re-append proves it parses).
	if err := appendToAll(path, "another20260601100000"); err != nil {
		t.Fatalf("second appendToAll on rewritten file: %v", err)
	}
}

func TestAppendToAll_NotFoundReturnsSentinel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "other.go")
	mustWrite(t, path, "package migrations\n\nvar somethingElse = 1\n")

	before := readFile(t, path)
	err := appendToAll(path, "x20260601090000")
	if !errors.Is(err, errAllSliceNotFound) {
		t.Fatalf("want errAllSliceNotFound, got %v", err)
	}
	if after := readFile(t, path); after != before {
		t.Errorf("file modified despite missing All slice")
	}
	_ = os.Remove(path)
}
