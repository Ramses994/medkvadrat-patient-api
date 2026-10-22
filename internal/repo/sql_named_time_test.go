package repo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LK-1a still binds MOTCONSU datetimes as time.Time. Everything else must use a wall-clock string.
var allowTimeNamedMSSQL = map[string]string{
	"me.go":       "LK-1a MOTCONSU",
	"motconsu.go": "LK-1a MOTCONSU",
}

func TestMSSQLNamedArgsForbidTimeTime(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		base := filepath.Base(path)
		if _, ok := allowTimeNamedMSSQL[base]; ok {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			timeParams := map[string]bool{}
			if fn.Type.Params != nil {
				for _, field := range fn.Type.Params.List {
					if !isTimeTime(field.Type) {
						continue
					}
					for _, name := range field.Names {
						timeParams[name.Name] = true
					}
				}
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Named" {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "sql" {
					return true
				}
				if argIsTimeTime(call.Args[1], timeParams) {
					t.Errorf("%s:%s sql.Named passes time.Time to Medialog; use wall-clock string + CONVERT (docs/MEDIALOG_RULES.md)", path, fn.Name.Name)
				}
				return true
			})
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func isTimeTime(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "time" && sel.Sel.Name == "Time"
}

func argIsTimeTime(expr ast.Expr, timeParams map[string]bool) bool {
	switch v := expr.(type) {
	case *ast.Ident:
		return timeParams[v.Name]
	case *ast.CallExpr:
		if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "time" && sel.Sel.Name == "Now" {
				return true
			}
			if timeParams[identName(sel.X)] && (sel.Sel.Name == "UTC" || sel.Sel.Name == "In" || sel.Sel.Name == "Add" || sel.Sel.Name == "Local") {
				return true
			}
		}
	}
	return false
}

func identName(expr ast.Expr) string {
	id, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name
}
