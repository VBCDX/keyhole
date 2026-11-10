// Package egress holds the guard for VBCDX architecture rule 4: the Keyhole
// server never calls a consuming service. Service calls leave from the agent
// host through the sidecar, which attaches the granted credential; the server
// only issues and revokes grants.
//
// It has two parts:
//
//   - Module source. Every Go file of every package the server binary
//     (`./cmd/keyhole`) builds, in all build-tag variants, is parsed. Each
//     place that can reach the network or another process is listed: watched
//     standard-library identifiers, banned imports, and any import of a
//     third-party package that can reach the network. The list must equal
//     `allowed` exactly, counted per entry.
//   - The dependency graph. Every third-party package the binary links that
//     can reach the network (it is, or transitively imports, one of
//     netRoots) must be in depAllowed, with the reason it may.
//
// Out of scope: assembly (.s) and C (.c) files, and what an allowed
// dependency does inside itself. The healthcheck's own client is pinned at
// runtime instead (cmd/keyhole probe_test.go: loopback only).
package egress

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// allowed is every outbound path in the module's own code: file, function,
// what. A path listed twice may occur twice.
var allowed = []string{
	// The container healthcheck: a client that dials loopback only (runtime
	// pin: TestHealthcheckProbesOnlyLoopback, TestProbeClientDialsLoopbackOnly).
	"cmd/keyhole/main.go healthcheck net.DefaultResolver",
	"cmd/keyhole/main.go probeClient net.Dialer",
	"cmd/keyhole/main.go probeClient http.Client", // the return type
	"cmd/keyhole/main.go probeClient http.Client", // the client
	"cmd/keyhole/main.go probeClient http.Transport",
	// Sign-in through the configured OIDC issuer. Temporary: these move out of
	// Keyhole when vbcdx-control owns OIDC.
	"internal/auth/oidc.go - import github.com/coreos/go-oidc/v3/oidc",
	"internal/auth/oidc.go - import golang.org/x/oauth2",
	"internal/auth/oidc.go NewOIDC http.Client",
	"internal/auth/oidc.go - http.Client",
	// RP-initiated logout verifies the ID token hint against the issuer's
	// keys. Temporary, as above.
	"internal/auth/logout.go - import github.com/coreos/go-oidc/v3/oidc",
	// The control plane, as Keyhole's enrolled service: GET /control/v1/keys
	// over the tunnel listener for the assertion-signing keys. The only peer is CONTROL_URL with a server certificate
	// from the control CA: no proxy, no redirect (runtime pins in
	// internal/service: TestControlIgnoresProxyAndRedirects,
	// TestControlRefusesAnotherCA). Not a consuming service, so rule 4 holds.
	"internal/service/service.go NewControl net.Dialer",
	"internal/service/service.go NewControl http.Transport",
	"internal/service/service.go NewControl http.Client",
	"internal/service/service.go - http.Client", // the Control struct field
	// Keyhole's own database (control-plane internal): the Postgres driver
	// and the pure-Go SQLite driver, registered in one place.
	"internal/store/store.go - import github.com/jackc/pgx/v5/stdlib",
	"internal/store/store.go - import modernc.org/sqlite",
	// The two connections: Postgres and the SQLite file. sql.Open with the
	// registered "pgx" driver dials whatever URL it's given, so it is
	// pinned here like a client.
	"internal/store/store.go open sql.Open",
	"internal/store/store.go open sql.Open",
}

// netRoots are the packages that reach the network or start a process. A
// third-party package that is or transitively imports one can reach out.
var netRoots = []string{"net", "net/http", "crypto/tls", "os/exec", "log/syslog", "golang.org/x/crypto/ssh"}

// depAllowed are the third-party modules the binary may link although they
// can reach the network, each with why. A module's sub-packages are covered.
// Who may import them from module code is pinned by `allowed`.
var depAllowed = map[string]string{
	"github.com/coreos/go-oidc/v3":  "OIDC sign-in client; temporary until vbcdx-control owns OIDC (A1/A2)",
	"golang.org/x/oauth2":           "OIDC code exchange; temporary, as go-oidc",
	"github.com/go-jose/go-jose/v4": "ID-token and JWK parsing for go-oidc; uses net for certificate IP fields, dials nothing",
	"github.com/jackc/pgx/v5":       "the Postgres driver for Keyhole's own database (control-plane internal)",
	"github.com/google/uuid":        "linked by modernc libc; uses net for the MAC-based node id, dials nothing",
	"modernc.org/libc":              "the transpiled C library under SQLite; net and os/exec back libc functions",
	"modernc.org/sqlite":            "the SQLite driver for Keyhole's own database file",
}

// watched lists, per package, the identifiers that make, carry or configure
// an outbound connection or a process. Any use counts: a call, a type in a
// var, field, parameter or embedding, a conversion, a method value.
var watched = map[string][]string{
	"net/http": {"Get", "Head", "Post", "PostForm", "DefaultClient", "DefaultTransport", "Client", "Transport"},
	"net": {"Dial", "DialTimeout", "DialTCP", "DialUDP", "DialIP", "DialUnix", "Dialer", "Resolver", "DefaultResolver",
		"ListenPacket", "ListenUDP", "ListenIP", "ListenMulticastUDP", "ListenUnixgram", "FileConn", "FilePacketConn",
		"LookupHost", "LookupIP", "LookupAddr", "LookupCNAME", "LookupMX", "LookupNS", "LookupPort", "LookupSRV", "LookupTXT",
		"ResolveTCPAddr", "ResolveUDPAddr", "ResolveIPAddr"},
	"crypto/tls":        {"Dial", "DialWithDialer", "Dialer", "Client"},
	"net/http/httputil": {"NewSingleHostReverseProxy", "ReverseProxy", "NewProxyClientConn", "NewClientConn"},
	"net/smtp":          {"Dial", "SendMail", "NewClient", "Client"},
	"net/rpc":           {"Dial", "DialHTTP", "DialHTTPPath", "NewClient", "NewClientWithCodec", "Client"},
	"net/rpc/jsonrpc":   {"Dial", "NewClient", "NewClientCodec"},
	"net/textproto":     {"Dial"},
	// Signals and the like stay usable; raw sockets and process starts don't.
	"syscall":               {"Connect", "Socket", "Sendto", "Sendmsg", "SendmsgN", "Syscall", "Syscall6", "RawSyscall", "RawSyscall6", "ForkExec", "Exec", "StartProcess"},
	"golang.org/x/sys/unix": {"Connect", "Socket", "Sendto", "Sendmsg", "SendmsgN", "Syscall", "Syscall6", "RawSyscall", "RawSyscall6", "ForkExec", "Exec"},
	"os":                    {"StartProcess"},
	// A registered driver dials through these with no driver import.
	"database/sql": {"Open", "OpenDB", "Register"},
}

// banned are imports that exist to call out or run something; importing one
// (or a sub-package) is a finding. "C" is cgo. Third-party network packages
// are caught by the graph (deps) as well; these also name standard-library
// ones and packages not yet in go.mod.
var banned = []string{
	"C", "os/exec", "plugin", "net/http/httptest", "log/syslog",
	"golang.org/x/crypto/ssh", "golang.org/x/sys/execabs", "golang.org/x/net/icmp",
	"github.com/coreos/go-oidc", "golang.org/x/oauth2",
	"golang.org/x/net/http2", "golang.org/x/net/proxy", "golang.org/x/net/websocket", "golang.org/x/net/context/ctxhttp",
	"github.com/gorilla/websocket", "nhooyr.io/websocket", "github.com/coder/websocket",
	"google.golang.org/grpc", "github.com/go-resty/resty",
	"github.com/hashicorp/vault/api", "github.com/openbao/openbao/api",
	"google.golang.org/api", "cloud.google.com/go", "github.com/aws/aws-sdk-go", "github.com/aws/aws-sdk-go-v2",
	"github.com/Azure/azure-sdk-for-go",
}

// graph is what the scan knows about import paths: the module path, the
// module's test-helper packages, and for each third-party package in the
// build graph whether it can reach the network.
type graph struct {
	mod     string
	helpers []string
	netDeps map[string]bool
}

// thirdParty reports whether path is neither standard library nor the
// module's own: its first element has a dot.
func (g graph) thirdParty(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return strings.Contains(first, ".") && path != g.mod && !strings.HasPrefix(path, g.mod+"/")
}

// findings lists the outbound paths in one parsed file as "file func what".
func findings(rel string, f *ast.File, g graph) []string {
	names := map[string]string{} // local package name → import path
	var out []string
	for _, im := range f.Imports {
		path, _ := strconv.Unquote(im.Path.Value)
		name := path[strings.LastIndex(path, "/")+1:]
		if im.Name != nil {
			name = im.Name.Name
		}
		if name == "." {
			// A dot import hides every selector; refuse it outright.
			out = append(out, rel+" - dot import "+path)
		}
		names[name] = path
		isBanned := slices.ContainsFunc(banned, func(p string) bool { return path == p || strings.HasPrefix(path, p+"/") })
		reaches, known := g.netDeps[path]
		switch {
		case isBanned || reaches:
			out = append(out, rel+" - import "+path)
		case g.thirdParty(path) && !known:
			out = append(out, rel+" - import "+path+" (not in the build graph)")
		}
		if slices.Contains(g.helpers, path) {
			out = append(out, rel+" - import test helper "+path)
		}
	}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "//go:linkname") {
				out = append(out, rel+" - go:linkname")
			}
		}
	}
	for _, d := range f.Decls {
		fn := "-"
		if fd, ok := d.(*ast.FuncDecl); ok {
			fn = fd.Name.Name
		}
		ast.Inspect(d, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if path, ok := names[id.Name]; ok && slices.Contains(watched[path], sel.Sel.Name) {
				out = append(out, rel+" "+fn+" "+path[strings.LastIndex(path, "/")+1:]+"."+sel.Sel.Name)
			}
			return true
		})
	}
	return out
}

// diff compares got and want as multisets: what got has beyond want (each
// surplus occurrence), and what want has that got lacks.
func diff(got, want []string) (extra, missing []string) {
	count := map[string]int{}
	for _, w := range want {
		count[w]++
	}
	for _, g := range got {
		if count[g] > 0 {
			count[g]--
		} else {
			extra = append(extra, g)
		}
	}
	for _, w := range want {
		if count[w] > 0 {
			count[w]--
			missing = append(missing, w)
		}
	}
	return extra, missing
}

// pkg is the part of `go list -json` the guard reads.
type pkg struct {
	ImportPath string
	Standard   bool
	Deps       []string
}

// list runs `go list -deps -json` on pattern in root.
func list(t *testing.T, root, pattern string) []pkg {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-json", pattern)
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps -json %s: %v", pattern, err)
	}
	var pkgs []pkg
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p pkg
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// reaches reports whether p is or transitively imports a net root.
func reaches(p pkg) bool {
	return slices.Contains(netRoots, p.ImportPath) || slices.ContainsFunc(p.Deps, func(d string) bool { return slices.Contains(netRoots, d) })
}

// depPolicy returns the linked third-party packages that can reach the
// network but aren't covered by allow, and the allow entries nothing uses.
func depPolicy(g graph, linked []pkg, allow map[string]string) (bad, stale []string) {
	used := map[string]bool{}
	for _, p := range linked {
		if p.Standard || !g.thirdParty(p.ImportPath) || !reaches(p) {
			continue
		}
		hit := false
		for m := range allow {
			if p.ImportPath == m || strings.HasPrefix(p.ImportPath, m+"/") {
				used[m], hit = true, true
			}
		}
		if !hit {
			bad = append(bad, p.ImportPath)
		}
	}
	for m := range allow {
		if !used[m] {
			stale = append(stale, m)
		}
	}
	slices.Sort(bad)
	slices.Sort(stale)
	return bad, stale
}

// scan returns the module-source findings for the server binary of the module
// at root: every non-test .go file, whatever its build tags, in every module
// package that `go list -deps ./cmd/keyhole` reports, plus every other module
// package except test helpers (packages the binary doesn't link whose every
// file imports "testing"). It also returns how many files it parsed, the
// linked packages the walk didn't reach (each one a gap in the scan), and the
// binary's link graph.
func scan(t *testing.T, root string) (got []string, files int, unwalked []string, linkedPkgs []pkg, g graph) {
	t.Helper()
	g = graph{mod: modulePath(t, root), netDeps: map[string]bool{}}
	linkedPkgs = list(t, root, "./cmd/keyhole")
	linked := map[string]bool{}
	for _, p := range linkedPkgs {
		if p.ImportPath == g.mod || strings.HasPrefix(p.ImportPath, g.mod+"/") {
			linked[p.ImportPath] = true
		}
	}
	if !linked[g.mod+"/cmd/keyhole"] {
		t.Fatalf("go list didn't report %s/cmd/keyhole", g.mod)
	}
	// Network reach of every third-party package any module package uses.
	for _, p := range list(t, root, "./...") {
		if !p.Standard && g.thirdParty(p.ImportPath) {
			g.netDeps[p.ImportPath] = reaches(p)
		}
	}

	// Every directory holding non-test Go files, parsed once.
	type file struct {
		rel string
		f   *ast.File
	}
	pkgs := map[string][]file{} // import path → files
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			// Only these exact top-level paths, which hold no server code.
			if slices.Contains([]string{".git", "web/node_modules", "mocks"}, rel) {
				return filepath.SkipDir
			}
			// A nested module is not this module's code (Go excludes it too);
			// if the binary uses it, it is a dependency and depPolicy sees it.
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		p := g.mod
		if dir := filepath.ToSlash(filepath.Dir(rel)); dir != "." {
			p += "/" + dir
		}
		pkgs[p] = append(pkgs[p], file{rel, f})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for p := range linked {
		if _, ok := pkgs[p]; !ok {
			unwalked = append(unwalked, p)
		}
	}
	for p, fs := range pkgs {
		if linked[p] {
			continue
		}
		all := true
		for _, f := range fs {
			all = all && slices.ContainsFunc(f.f.Imports, func(im *ast.ImportSpec) bool { return im.Path.Value == `"testing"` })
		}
		if all {
			g.helpers = append(g.helpers, p)
		}
	}
	for p, fs := range pkgs {
		if slices.Contains(g.helpers, p) {
			continue
		}
		for _, f := range fs {
			files++
			got = append(got, findings(f.rel, f.f, g)...)
		}
	}
	slices.Sort(got)
	slices.Sort(unwalked)
	return got, files, unwalked, linkedPkgs, g
}

func modulePath(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if m, ok := strings.CutPrefix(strings.TrimSpace(l), "module "); ok {
			return strings.TrimSpace(m)
		}
	}
	t.Fatal("go.mod has no module line")
	return ""
}

func parse(t *testing.T, src string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestServerHasNoEgress: the module's outbound paths are exactly the allowed
// ones, counted, and every network-capable dependency the binary links is
// allowed with a reason.
func TestServerHasNoEgress(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	got, files, unwalked, linked, g := scan(t, root)
	for _, p := range unwalked {
		t.Errorf("the binary links %s, but the walk skipped it", p)
	}
	if files < 20 {
		t.Fatalf("only %d server files parsed; is the walk rooted at the repo?", files)
	}
	extra, missing := diff(got, allowed)
	for _, x := range extra {
		t.Errorf("outbound path not allowed (rule 4: service calls leave from the sidecar, never the server): %s", x)
	}
	for _, w := range missing {
		t.Errorf("allowed entry no longer exists; drop it from the list: %s", w)
	}
	bad, stale := depPolicy(g, linked, depAllowed)
	for _, p := range bad {
		t.Errorf("the binary links %s, which can reach the network, and it isn't in depAllowed (rule 4)", p)
	}
	for _, m := range stale {
		t.Errorf("depAllowed entry %s is no longer linked; drop it", m)
	}
}

// TestGuardCatchesEgress: each way of calling out is found, and plain server
// code is not.
func TestGuardCatchesEgress(t *testing.T) {
	g := graph{
		mod:     "example.com/m",
		helpers: []string{"example.com/m/internal/h"},
		netDeps: map[string]bool{"example.org/dial": true, "example.org/pure": false},
	}
	cases := []struct{ name, src, want string }{
		{"http.Get", `package p; import "net/http"; func h() { http.Get("https://api.github.com") }`, "x.go h http.Get"},
		{"aliased http.Post", `package p; import web "net/http"; func h() { web.Post("u", "", nil) }`, "x.go h http.Post"},
		{"DefaultClient.Do", `package p; import "net/http"; func h(r *http.Request) { http.DefaultClient.Do(r) }`, "x.go h http.DefaultClient"},
		{"client literal", `package p; import "net/http"; var c = &http.Client{}`, "x.go - http.Client"},
		{"new(http.Client)", `package p; import "net/http"; func h() { _ = new(http.Client) }`, "x.go h http.Client"},
		{"var of client type", `package p; import "net/http"; func h() { var c http.Client; c.Get("u") }`, "x.go h http.Client"},
		{"embedded client", `package p; import "net/http"; type X struct{ http.Client }`, "x.go - http.Client"},
		{"client parameter", `package p; import "net/http"; func h(c *http.Client) {}`, "x.go h http.Client"},
		{"transport var", `package p; import "net/http"; var t http.Transport`, "x.go - http.Transport"},
		{"dot import", `package p; import . "net/http"; func h() { Get("u") }`, "x.go - dot import net/http"},
		{"reverse proxy", `package p; import "net/http/httputil"; func h() { httputil.NewSingleHostReverseProxy(nil) }`, "x.go h httputil.NewSingleHostReverseProxy"},
		{"proxy literal", `package p; import "net/http/httputil"; func h() { _ = &httputil.ReverseProxy{} }`, "x.go h httputil.ReverseProxy"},
		{"net.Dial", `package p; import "net"; func h() { net.Dial("tcp", "x:443") }`, "x.go h net.Dial"},
		{"net.Dialer var", `package p; import "net"; func h() { var d net.Dialer; d.Dial("tcp", "x") }`, "x.go h net.Dialer"},
		{"udp send", `package p; import "net"; func h() { net.ListenUDP("udp", nil) }`, "x.go h net.ListenUDP"},
		{"dns lookup", `package p; import "net"; func h() { net.LookupHost("x") }`, "x.go h net.LookupHost"},
		{"ResolveTCPAddr", `package p; import "net"; func h() { net.ResolveTCPAddr("tcp", "x.evil.example:53") }`, "x.go h net.ResolveTCPAddr"},
		{"ResolveUDPAddr", `package p; import "net"; func h() { net.ResolveUDPAddr("udp", "x.evil.example:53") }`, "x.go h net.ResolveUDPAddr"},
		{"ResolveIPAddr", `package p; import "net"; func h() { net.ResolveIPAddr("ip", "x.evil.example") }`, "x.go h net.ResolveIPAddr"},
		{"tls.Dial", `package p; import "crypto/tls"; func h() { tls.Dial("tcp", "x:443", nil) }`, "x.go h tls.Dial"},
		{"tls.Client", `package p; import "crypto/tls"; func h() { tls.Client(nil, nil) }`, "x.go h tls.Client"},
		{"smtp.SendMail", `package p; import "net/smtp"; func h() { smtp.SendMail("", nil, "", nil, nil) }`, "x.go h smtp.SendMail"},
		{"smtp.NewClient", `package p; import "net/smtp"; func h() { smtp.NewClient(nil, "") }`, "x.go h smtp.NewClient"},
		{"rpc.Client", `package p; import "net/rpc"; var c *rpc.Client`, "x.go - rpc.Client"},
		{"textproto.Dial", `package p; import "net/textproto"; func h() { textproto.Dial("tcp", "x") }`, "x.go h textproto.Dial"},
		{"syslog", `package p; import "log/syslog"; func h() { syslog.Dial("tcp", "evil.example:514", syslog.LOG_INFO, "x") }`, "x.go - import log/syslog"},
		{"os/exec", `package p; import "os/exec"; func h() { exec.Command("curl", "https://x").Run() }`, "x.go - import os/exec"},
		{"execabs", `package p; import "golang.org/x/sys/execabs"; func h() { execabs.Command("curl").Run() }`, "x.go - import golang.org/x/sys/execabs"},
		{"cgo", `package p; import "C"`, "x.go - import C"},
		{"syscall.Connect", `package p; import "syscall"; func h() { syscall.Connect(0, nil) }`, "x.go h syscall.Connect"},
		{"unix.Connect", `package p; import "golang.org/x/sys/unix"; func h() { unix.Connect(0, nil) }`, "x.go h unix.Connect"},
		{"os.StartProcess", `package p; import "os"; func h() { os.StartProcess("curl", nil, nil) }`, "x.go h os.StartProcess"},
		{"ssh", `package p; import "golang.org/x/crypto/ssh"; func h() { ssh.Dial("tcp", "evil.example:22", nil) }`, "x.go - import golang.org/x/crypto/ssh"},
		{"icmp", `package p; import _ "golang.org/x/net/icmp"`, "x.go - import golang.org/x/net/icmp"},
		{"http2", `package p; import _ "golang.org/x/net/http2"`, "x.go - import golang.org/x/net/http2"},
		{"httptest", `package p; import "net/http/httptest"; func h() { httptest.NewServer(nil).Client() }`, "x.go - import net/http/httptest"},
		{"vault client", `package p; import _ "github.com/openbao/openbao/api/v2"`, "x.go - import github.com/openbao/openbao/api/v2"},
		{"websocket", `package p; import _ "github.com/gorilla/websocket"`, "x.go - import github.com/gorilla/websocket"},
		// A third-party package that reaches the network (pgx-style): the
		// graph says so, whatever its name.
		{"network-capable dependency", `package p; import "example.org/dial"; func h() { dial.Connect("postgres://evil.example/db") }`, "x.go - import example.org/dial"},
		{"dependency outside the graph", `package p; import _ "example.org/elsewhere"`, "x.go - import example.org/elsewhere (not in the build graph)"},
		{"sql.Open", `package p; import "database/sql"; func h() { sql.Open("pgx", "postgres://evil.example/db") }`, "x.go h sql.Open"},
		{"sql.OpenDB", `package p; import "database/sql"; func h() { sql.OpenDB(nil) }`, "x.go h sql.OpenDB"},
		{"sql.Register", `package p; import "database/sql"; func h() { sql.Register("x", nil) }`, "x.go h sql.Register"},
		{"go:linkname", "package p\nimport _ \"unsafe\"\n//go:linkname dial net.Dial\nfunc dial()", "x.go - go:linkname"},
		// r1's exemption is gone: a non-test file importing "testing" is scanned.
		{"testing import", `package p; import ("net/http"; _ "testing"); func h() { http.Get("u") }`, "x.go h http.Get"},
		{"test helper import", `package p; import _ "example.com/m/internal/h"`, "x.go - import test helper example.com/m/internal/h"},
	}
	for _, c := range cases {
		got := findings("x.go", parse(t, c.src), g)
		if !slices.Contains(got, c.want) {
			t.Errorf("%s: not caught; got %q, want %q", c.name, got, c.want)
		}
	}
	clean := `package p
import ("database/sql"; "net"; "net/http"; "os"; "os/signal"; "syscall"; "example.org/pure"; "example.com/m/internal/store")
func h(w http.ResponseWriter, r *http.Request) { _, _ = net.SplitHostPort(r.RemoteAddr); http.Error(w, "x", 400) }
func s() { _ = http.Server{}; _ = http.NewServeMux(); _, _ = net.Listen("tcp", ":0"); _ = net.ParseIP("::1") }
func g() { signal.Notify(nil, os.Interrupt, syscall.SIGTERM); _ = os.Args; pure.F(); store.F() }
func q(db *sql.DB) { _, _ = db.Query("SELECT 1"); var _ sql.NullString }`
	if got := findings("x.go", parse(t, clean), g); len(got) != 0 {
		t.Errorf("server-side code flagged: %q", got)
	}

	// Counted: a second allowed call in the same function is a surplus.
	extra, missing := diff([]string{"a", "a", "new"}, []string{"a", "gone"})
	if !slices.Equal(extra, []string{"a", "new"}) || !slices.Equal(missing, []string{"gone"}) {
		t.Errorf("diff: extra %q, missing %q", extra, missing)
	}

	// The dependency policy: a new network-capable dependency fails, an
	// allowed one (and its sub-packages) passes, pure ones and the standard
	// library are ignored, and an unused allowance is stale.
	linked := []pkg{
		{ImportPath: "net", Standard: true},
		{ImportPath: "example.com/m/internal/x", Deps: []string{"net"}},
		{ImportPath: "example.org/dial", Deps: []string{"crypto/tls", "net"}},
		{ImportPath: "example.org/db/sub", Deps: []string{"net"}},
		{ImportPath: "example.org/pure", Deps: []string{"strings"}},
		{ImportPath: "example.org/run", Deps: []string{"os/exec"}},
		{ImportPath: "golang.org/x/crypto/ssh", Deps: []string{"bytes"}},
		{ImportPath: "example.org/dbx", Deps: []string{"net"}}, // not covered by example.org/db
		{ImportPath: "example.org/logs", Deps: []string{"log/syslog"}},
	}
	bad, stale := depPolicy(g, linked, map[string]string{"example.org/db": "why", "example.org/gone": "why"})
	if !slices.Equal(bad, []string{"example.org/dbx", "example.org/dial", "example.org/logs", "example.org/run", "golang.org/x/crypto/ssh"}) || !slices.Equal(stale, []string{"example.org/gone"}) {
		t.Errorf("depPolicy: bad %q, stale %q", bad, stale)
	}
}

// TestScanCoversTheBinary: on a fixture module, the scan reaches server
// packages in directories named like the skipped top-level ones, scans a
// non-test file that imports "testing", skips an unlinked test helper and
// flags importing it, and the dependency policy catches a new third-party
// package that dials out.
func TestScanCoversTheBinary(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.22\n\nrequire (\n\texample.org/dial v0.0.0\n\texample.org/pure v0.0.0\n)\n\nreplace example.org/dial => ./third/dial\n\nreplace example.org/pure => ./third/pure\n")
	write("third/pure/go.mod", "module example.org/pure\n\ngo 1.22\n")
	write("third/pure/pure.go", `package pure; func F() {}`)
	write("third/dial/go.mod", "module example.org/dial\n\ngo 1.22\n")
	write("third/dial/dial.go", `package dial; import "net"; func Connect(a string) { net.Dial("tcp", a) }`)
	write("cmd/keyhole/main.go", `package main; import ("example.com/m/internal/web"; "example.com/m/internal/mocks"); func main() { web.H(); mocks.H() }`)
	write("internal/web/a.go", `package web; import ("net/http"; "example.org/dial"); func H() { http.Get("https://api.github.com"); dial.Connect("evil.example:5432") }`)
	write("internal/mocks/a.go", `package mocks; import ("net"; _ "testing"); func H() { net.Dial("tcp", "x:1") }`)
	write("internal/h/h.go", `package h; import ("net/http"; "testing"); func H(*testing.T) { http.Get("u") }`)
	// Unlinked, not a helper: scanned; its pure dependency is in the
	// module's graph although the binary doesn't link it.
	write("internal/u/u.go", `package u; import (_ "example.com/m/internal/h"; "example.org/pure"); func F() { pure.F() }`)
	write("mocks/x.go", `package x; import "net/http"; func H() { http.Get("u") }`)
	got, _, unwalked, linked, g := scan(t, root)
	if len(unwalked) != 0 {
		t.Errorf("unwalked = %q", unwalked)
	}
	want := []string{
		"internal/mocks/a.go H net.Dial",
		"internal/u/u.go - import test helper example.com/m/internal/h",
		"internal/web/a.go - import example.org/dial",
		"internal/web/a.go H http.Get",
	}
	if !slices.Equal(got, want) {
		t.Errorf("scan = %q, want %q", got, want)
	}
	if bad, _ := depPolicy(g, linked, nil); !slices.Equal(bad, []string{"example.org/dial"}) {
		t.Errorf("depPolicy bad = %q, want the new dialing dependency", bad)
	}

	// A linked package under a skipped top-level path is reported, not missed.
	write("mocks/x.go", `package mocks; func H() {}`)
	write("cmd/keyhole/main.go", `package main; import "example.com/m/mocks"; func main() { mocks.H() }`)
	if _, _, unwalked, _, _ := scan(t, root); !slices.Equal(unwalked, []string{"example.com/m/mocks"}) {
		t.Errorf("unwalked = %q, want the linked mocks package", unwalked)
	}
}
