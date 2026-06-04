package migrate

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotProjectRoot is returned when the resolved root has no go.mod, so the
// scaffolder cannot trust it as the place to write migration files.
type ErrNotProjectRoot struct{ Root string }

func (e *ErrNotProjectRoot) Error() string {
	return fmt.Sprintf("migrate: %q is not a Go project root (no go.mod found). "+
		"Run from your project checkout root, or pass --project-root <path>.", e.Root)
}

// ErrMigrationsDirNotFound is returned when the migrations directory is absent.
// Its absence is treated as a signal that the command is being run in the wrong
// place, so the directory is never auto-created.
type ErrMigrationsDirNotFound struct{ Dir string }

func (e *ErrMigrationsDirNotFound) Error() string {
	return fmt.Sprintf("migrate: migrations directory not found at %q. "+
		"Run from the project checkout root, or pass --project-root <path> "+
		"(and --dir if your migrations live elsewhere).", e.Dir)
}

// ResolveDir locates and validates the migrations directory and infers its Go
// package. Root is projectRoot or the current directory; it must contain go.mod.
// The directory is <root>/migrations unless dir overrides it, and it must
// already exist. ResolveDir performs no writes, so a failure leaves the project
// untouched. The CLI calls it before scaffolding to fail fast with an actionable
// message when run from the wrong place.
func ResolveDir(projectRoot, dir string) (absDir, pkg string, err error) {
	root := projectRoot
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", "", err
		}
		root = cwd
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	if _, statErr := os.Stat(filepath.Join(root, "go.mod")); statErr != nil {
		return "", "", &ErrNotProjectRoot{Root: root}
	}

	switch {
	case dir == "":
		absDir = filepath.Join(root, "migrations")
	case filepath.IsAbs(dir):
		absDir = dir
	default:
		absDir = filepath.Join(root, dir)
	}
	info, statErr := os.Stat(absDir)
	if statErr != nil || !info.IsDir() {
		return "", "", &ErrMigrationsDirNotFound{Dir: absDir}
	}

	return absDir, inferPackage(absDir), nil
}

// inferPackage reads the package clause from the first non-test .go file in dir,
// falling back to the directory's base name when none is found.
func inferPackage(dir string) string {
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			if pkg := packageClause(src); pkg != "" {
				return pkg
			}
		}
	}
	return filepath.Base(dir)
}

// packageClause parses only the package clause of a Go source file.
func packageClause(src []byte) string {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.PackageClauseOnly)
	if err != nil || f.Name == nil {
		return ""
	}
	return f.Name.Name
}
