package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestRouteSetIsPinned lists every pattern the module registers on a mux
// and compares the set exactly. Writing a key value, a tool or a grant is
// an admin CLI job on the Keyhole host, so a new route,
// above all a write route, must be a deliberate change to this list.
func TestRouteSetIsPinned(t *testing.T) {
	want := []string{
		"/",
		"/api/",
		"GET /api/auth/config",
		"GET /api/me",
		"GET /api/v1/orgs",
		"GET /api/v1/orgs/{orgID}",
		"GET /api/v1/orgs/{orgID}/stores",
		"GET /api/v1/orgs/{orgID}/stores/{storeID}",
		"GET /auth/oidc/callback",
		"GET /auth/oidc/login",
		"GET /healthz",
		"GET /readyz",
		"POST /api/auth/login",
		"POST /api/auth/logout",
		"POST /auth/oidc/backchannel-logout",
		// The forward listener's one route (resolve.go), served only on
		// SERVICE_LISTEN_ADDR with vbcdx-control's certificate required.
		"POST /orgs/{orgID}/resolve",
	}
	// Every non-test Go file in the module, not only this package, so a
	// route registered elsewhere (cmd/keyhole, a new package) is caught too.
	var files []string
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Test-helper packages (oidctest's fake provider, storetest) are
		// imported only by tests and never linked into the server.
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "testdata" ||
			(strings.HasSuffix(d.Name(), "test") && path != "../..")) {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(files, filepath.Join("..", "..", "cmd", "keyhole", "main.go")) || len(files) < 20 {
		t.Fatalf("the walk found %d Go files and not cmd/keyhole/main.go; it isn't covering the module", len(files))
	}
	var got []string
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") || len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: a %s pattern that isn't a string literal can't be pinned", fset.Position(call.Pos()), sel.Sel.Name)
				return true
			}
			p, _ := strconv.Unquote(lit.Value)
			got = append(got, p)
			return true
		})
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("registered routes changed:\n got  %q\n want %q", got, want)
	}
}
