package discovery

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A discovery must change nothing on the Machine it reads. These are the only
// packages this one may import, whatever the platform: none of the rest can
// touch a file at all.
var allowedImports = map[string]bool{
	"context":       true,
	"errors":        true,
	"fmt":           true,
	"log/slog":      true,
	"os":            true,
	"os/exec":       true,
	"path/filepath": true,
	"slices":        true,
	"strconv":       true,
	"strings":       true,
}

// And these are the only ways it may use the two that can. Nothing here opens
// a path, so no file handle exists to write to; nothing creates, renames,
// removes or changes the mode of anything; and the only program it runs is
// looked up once and given a context.
var allowedCalls = map[string]bool{
	"os.ReadFile":         true,
	"os.ReadDir":          true,
	"os.Executable":       true,
	"os.ErrNotExist":      true,
	"exec.LookPath":       true,
	"exec.CommandContext": true,
	"exec.ExitError":      true,
}

// TestPackageOnlyReads is the mechanical half of the promise this package
// makes. It reads the package's own source, on every platform's files at once,
// so that a reviewer can check the two lists above instead of every function,
// and so that a write cannot be added here without deleting this test.
func TestPackageOnlyReads(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list the package: %v", err)
	}

	sources := 0

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}

		sources++

		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}

		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("%s: import path %s: %v", file, imported.Path.Value, err)
			}

			if !allowedImports[path] {
				t.Errorf("%s imports %q, which is not one of the packages a read-only discovery needs", file, path)
			}
		}

		ast.Inspect(parsed, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			pkg, ok := selector.X.(*ast.Ident)
			if !ok || (pkg.Name != "os" && pkg.Name != "exec") {
				return true
			}

			if call := pkg.Name + "." + selector.Sel.Name; !allowedCalls[call] {
				t.Errorf("%s uses %s, and this package may only read", file, call)
			}

			return true
		})
	}

	if sources < 4 {
		t.Fatalf("checked %d source files, want the whole package including the platform-specific paths", sources)
	}
}
