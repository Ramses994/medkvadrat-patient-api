package repo

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// me.go:Appointments — снять после мержа LK-1a (PR #21).
// motconsu.go:ChangesSince и PollAfter передают строку CONVERT(..., 126) и в списке не состоят.
var allowMSSQLTimeNamed = map[string]bool{
	"me.go:Appointments": true,
}

func TestMSSQLNamedArgsForbidTimeTime(t *testing.T) {
	got := mssqlNamedTimeCalls(t, []string{
		".",
		"github.com/medkvadrat/medkvadrat-patient-api/internal/medialogwrite",
	}, allowMSSQLTimeNamed)
	if len(got) > 0 {
		t.Fatalf("sql.Named(time.Time|*time.Time|sql.NullTime) in MSSQL packages:\n%s", strings.Join(got, "\n"))
	}
}

func TestNamedTimeGuardCatchesIndirectBindings(t *testing.T) {
	got := mssqlNamedTimeCalls(t, []string{"./testdata/namedtime/bad"}, nil)
	want := []string{
		"directTimeBinding",
		"localTimeBinding",
		"returnedTimeBinding",
		"chainedTimeBinding",
		"fieldTimeBinding",
		"nullTimeBinding",
	}
	seen := map[string]bool{}
	for _, line := range got {
		for _, name := range want {
			if strings.Contains(line, ":"+name) || strings.HasSuffix(line, ":"+name) {
				seen[name] = true
			}
		}
	}
	for _, name := range want {
		if !seen[name] {
			t.Errorf("missed %s\n%s", name, strings.Join(got, "\n"))
		}
	}
}

func TestNamedTimeGuardAllowsWallClockString(t *testing.T) {
	got := mssqlNamedTimeCalls(t, []string{"./testdata/namedtime/good"}, nil)
	if len(got) != 0 {
		t.Fatalf("string bound must pass:\n%s", strings.Join(got, "\n"))
	}
}

func mssqlNamedTimeCalls(t *testing.T, patterns []string, allow map[string]bool) []string {
	t.Helper()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:  ".",
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		t.Fatalf("сторож не смог загрузить типы: проверьте версию Go/x/tools: %v", err)
	}
	var out []string
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 || pkg.TypesInfo == nil {
			t.Fatalf("сторож не смог загрузить типы: проверьте версию Go/x/tools: %s: %v", pkg.PkgPath, pkg.Errors)
		}
		for _, file := range pkg.Syntax {
			name := pkg.Fset.Position(file.Pos()).Filename
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 || !isSQLNamed(call.Fun) {
					return true
				}
				tv, ok := pkg.TypesInfo.Types[call.Args[1]]
				if !ok || !forbiddenMedialogTime(tv.Type) {
					return true
				}
				fn := enclosingFunc(file, call.Pos())
				key := filepath.Base(name) + ":" + fn
				if allow[key] {
					return true
				}
				pos := pkg.Fset.Position(call.Pos())
				out = append(out, fmt.Sprintf("%s:%d %s", pos.Filename, pos.Line, key))
				return true
			})
		}
	}
	return out
}

func isSQLNamed(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Named" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "sql"
}

func enclosingFunc(file *ast.File, pos token.Pos) string {
	var name string
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name == nil {
			return true
		}
		if fn.Body.Pos() <= pos && pos <= fn.Body.End() {
			name = fn.Name.Name
			return false
		}
		return true
	})
	if name == "" {
		return "<unknown>"
	}
	return name
}
