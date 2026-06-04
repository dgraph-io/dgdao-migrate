package migrate

import (
	"bytes"
	"errors"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
)

// errAllSliceNotFound signals that the target file has no top-level All slice in
// the expected composite-literal shape. The caller treats registration as
// best-effort: it prints a manual instruction rather than failing the scaffold.
var errAllSliceNotFound = errors.New("migrate: All slice not found in expected shape")

// appendToAll appends varName as a new element of the top-level
// `All = []migrate.Migration{ ... }` composite literal in the given Go file,
// rewriting it through go/format so existing layout and comments survive. It
// returns errAllSliceNotFound when no such slice is present.
func appendToAll(migrationsGoPath, varName string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, migrationsGoPath, nil, parser.ParseComments)
	if err != nil {
		return err
	}

	lit := findAllCompositeLit(f)
	if lit == nil {
		return errAllSliceNotFound
	}
	lit.Elts = append(lit.Elts, ast.NewIdent(varName))

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return err
	}
	return os.WriteFile(migrationsGoPath, buf.Bytes(), 0o644)
}

// findAllCompositeLit returns the composite literal assigned to a top-level
// variable named All, or nil when the file has no such variable in that shape.
func findAllCompositeLit(f *ast.File) *ast.CompositeLit {
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if name.Name != "All" || i >= len(vs.Values) {
					continue
				}
				if cl, ok := vs.Values[i].(*ast.CompositeLit); ok {
					return cl
				}
			}
		}
	}
	return nil
}
