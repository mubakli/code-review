package review_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestCorePackagesDoNotDependOnConcreteAdapters(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate architecture test")
	}
	internalRoot := filepath.Dir(filepath.Dir(currentFile))

	assertNoImports(t, filepath.Join(internalRoot, "review"), []string{
		"code-review/internal/ai/providers/",
		"code-review/internal/analyzers/",
		"code-review/internal/config",
		"code-review/internal/git",
		"code-review/internal/output",
		"code-review/internal/providers/",
	})
	assertNoImports(t, filepath.Join(internalRoot, "ai"), []string{
		"code-review/internal/ai/providers/",
		"code-review/internal/analyzers/",
		"code-review/internal/git",
		"code-review/internal/pathfilter",
		"code-review/internal/providers/",
	})
}

func assertNoImports(t *testing.T, directory string, forbidden []string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read package directory %s: %v", directory, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		filePath := filepath.Join(directory, entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), filePath, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse imports from %s: %v", filePath, err)
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("decode import in %s: %v", filePath, err)
			}
			for _, prefix := range forbidden {
				if path == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(path, prefix) {
					t.Errorf("%s imports forbidden dependency %s", filePath, path)
				}
			}
		}
	}
}
