package linttest

import (
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/go-critic/go-critic/framework/linter"
	"github.com/go-toolsmith/pkgload"
	"golang.org/x/tools/go/packages"
)

var sizes = types.SizesFor("gc", runtime.GOARCH)

func saneCheckersList(t *testing.T) []*linter.CheckerInfo {
	var saneList []*linter.CheckerInfo

	for _, info := range linter.GetCheckersInfo() {
		pkgPath := "github.com/go-critic/go-critic/framework/linttest/testdata/sanity"
		t.Run(info.Name+"/sanity", func(t *testing.T) {
			fset := token.NewFileSet()
			pkgs := newPackages(t, pkgPath, fset)
			for _, pkg := range pkgs {
				ctx := &linter.Context{
					SizesInfo: sizes,
					FileSet:   fset,
					TypesInfo: pkg.TypesInfo,
					Pkg:       pkg.Types,
				}
				c := linter.NewChecker(ctx, info)
				defer func() {
					r := recover()
					if r != nil {
						t.Errorf("unexpected panic: %v\n%s", r, debug.Stack())
					} else {
						saneList = append(saneList, info)
					}
				}()
				for _, f := range pkg.Syntax {
					ctx.SetFileInfo(getFilename(fset, f), f)
					_ = c.Check(f)
				}
			}
		})
	}

	return saneList
}

// IntegrationTest specifies integration test options.
type IntegrationTest struct {
	Main string

	// Dir specifies a path to integration tests.
	Dir string
}

// TestCheckers runs end2end tests over all registered checkers using default options.
//
// TODO(quasilyte): document default options.
// TODO(quasilyte): make it possible to run tests with different options.
func TestCheckers(t *testing.T) {
	for _, info := range saneCheckersList(t) {
		t.Run(info.Name, func(t *testing.T) {
			pkgPath := "./testdata/" + info.Name

			fset := token.NewFileSet()
			pkgs := newPackages(t, pkgPath, fset)
			for _, pkg := range pkgs {
				ctx := &linter.Context{
					SizesInfo: sizes,
					FileSet:   fset,
					TypesInfo: pkg.TypesInfo,
					Pkg:       pkg.Types,
				}
				c := linter.NewChecker(ctx, info)
				for _, f := range pkg.Syntax {
					checkFile(t, c, ctx, f)
				}
			}
		})
	}
}

func checkFile(t *testing.T, c *linter.Checker, ctx *linter.Context, f *ast.File) {
	filename := getFilename(ctx.FileSet, f)
	testFilename := filepath.Join("testdata", c.Info.Name, filename)

	rc, err := os.Open(testFilename)
	if err != nil {
		t.Fatalf("read file %q: %v", testFilename, err)
	}
	defer rc.Close()

	ws, err := newWarnings(rc)
	if err != nil {
		t.Fatal(err)
	}

	stripDirectives(f)
	ctx.SetFileInfo(filename, f)

	matched := make(map[*string]struct{})
	for _, warn := range c.Check(f) {
		line := ctx.FileSet.Position(warn.Node.Pos()).Line

		if w := ws.find(line, warn.Text); w != nil {
			if _, seen := matched[w]; seen {
				t.Errorf("%s:%d: multiple matches for %s",
					testFilename, line, *w)
			}
			matched[w] = struct{}{}
		} else {
			t.Errorf("%s:%d: unexpected warn: %s",
				testFilename, line, warn.Text)
		}
	}

	checkUnmatched(ws, matched, t, testFilename)
}

// stripDirectives replaces "///" comments with empty single-line
// comments, so the checkers that inspect comments see ordinary
// comment groups (with extra newlines, but that's not important).
func stripDirectives(f *ast.File) {
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "/// ") {
				c.Text = "//"
			}
		}
	}
}

func getFilename(fset *token.FileSet, f *ast.File) string {
	// see https://github.com/golang/go/issues/24498
	return filepath.Base(fset.Position(f.Pos()).Filename)
}

func checkUnmatched(ws warnings, matched map[*string]struct{}, t *testing.T, testFilename string) {
	for line, sl := range ws {
		for i, w := range sl {
			if _, ok := matched[&sl[i]]; !ok {
				t.Errorf("%s:%d: unmatched `%s`", testFilename, line, w)
			}
		}
	}
}

func newPackages(t *testing.T, pattern string, fset *token.FileSet) []*packages.Package {
	mode := packages.NeedName |
		packages.NeedFiles |
		packages.NeedCompiledGoFiles |
		packages.NeedImports |
		packages.NeedTypes |
		packages.NeedSyntax |
		packages.NeedTypesInfo |
		packages.NeedTypesSizes
	cfg := packages.Config{
		Mode:  mode,
		Tests: true,
		Fset:  fset,
	}
	pkgs, err := loadPackages(&cfg, []string{pattern})
	if err != nil {
		t.Fatalf("load package: %v", err)
	}
	return pkgs
}

// TODO(quasilyte): copied from check.go. Should it be added to pkgload?
func loadPackages(cfg *packages.Config, patterns []string) ([]*packages.Package, error) {
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}

	result := pkgs[:0]
	pkgload.VisitUnits(pkgs, func(u *pkgload.Unit) {
		if u.ExternalTest != nil {
			result = append(result, u.ExternalTest)
		}

		if u.Test != nil {
			// Prefer tests to the base package, if present.
			result = append(result, u.Test)
		} else {
			result = append(result, u.Base)
		}
	})
	return result, nil
}
