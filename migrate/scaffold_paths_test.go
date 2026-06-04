package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDir_Success(t *testing.T) {
	root, dir := tempProject(t)
	absDir, pkg, err := ResolveDir(root, "")
	if err != nil {
		t.Fatalf("resolveDir: %v", err)
	}
	if absDir != dir {
		t.Errorf("absDir = %q, want %q", absDir, dir)
	}
	if pkg != "migrations" {
		t.Errorf("pkg = %q, want %q (from the package clause)", pkg, "migrations")
	}
}

func TestResolveDir_NoGoModFails(t *testing.T) {
	root := t.TempDir() // no go.mod
	_, _, err := ResolveDir(root, "")
	var e *ErrNotProjectRoot
	if !errors.As(err, &e) {
		t.Fatalf("want *ErrNotProjectRoot, got %v", err)
	}
	if !strings.Contains(err.Error(), "is not a Go project root (no go.mod found)") {
		t.Errorf("message not actionable: %v", err)
	}
}

func TestResolveDir_MissingMigrationsDirFails(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/x\n\ngo 1.21\n")
	// no migrations/ directory created
	_, _, err := ResolveDir(root, "")
	var e *ErrMigrationsDirNotFound
	if !errors.As(err, &e) {
		t.Fatalf("want *ErrMigrationsDirNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "migrations directory not found at") {
		t.Errorf("message not actionable: %v", err)
	}
	// Failure must not have created the directory.
	if _, statErr := os.Stat(filepath.Join(root, "migrations")); statErr == nil {
		t.Errorf("resolveDir must not create the migrations directory on failure")
	}
}
