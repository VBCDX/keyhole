package service_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/service"
	"github.com/VBCDX/keyhole/internal/service/servicetest"
)

func TestWriteCSR(t *testing.T) {
	dir := t.TempDir()
	k, c := filepath.Join(dir, "service.key"), filepath.Join(dir, "service.csr")
	if err := service.WriteCSR(k, c); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(k)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %04o", fi.Mode().Perm())
	}
	raw, _ := os.ReadFile(c)
	b, _ := pem.Decode(raw)
	if b == nil || b.Type != "CERTIFICATE REQUEST" {
		t.Fatalf("CSR PEM: %q", raw)
	}
	csr, err := x509.ParseCertificateRequest(b.Bytes)
	if err != nil || csr.CheckSignature() != nil || csr.PublicKeyAlgorithm != x509.Ed25519 {
		t.Fatalf("CSR: %v, alg %v", err, csr.PublicKeyAlgorithm)
	}
	if err := service.WriteCSR(k, filepath.Join(dir, "other.csr")); err == nil {
		t.Fatal("service.WriteCSR overwrote an existing key")
	}
	if _, err := os.Stat(filepath.Join(dir, "other.csr")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused service.WriteCSR still wrote a CSR")
	}
	// A CSR path that can't be created leaves no key behind.
	k2 := filepath.Join(dir, "k2")
	if err := service.WriteCSR(k2, c); err == nil {
		t.Fatal("service.WriteCSR overwrote an existing CSR")
	}
	if _, err := os.Stat(k2); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed CSR write left its key behind")
	}
}

func TestLoadIdentity(t *testing.T) {
	ca := servicetest.NewCA(t)
	c, k := servicetest.Enrolled(t, ca)
	id, err := service.LoadIdentity(c, k)
	if err != nil || id.Leaf == nil || id.Leaf.URIs[0].String() != service.ServiceURI {
		t.Fatalf("service.LoadIdentity = %v", err)
	}
	if err := os.Chmod(k, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoadIdentity(c, k); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("a group-readable key: %v", err)
	}
	for name, uris := range map[string][]string{
		"dispatch's name": {"vbcdx:service/dispatch"},
		"a sidecar":       {"vbcdx:sidecar/sc_1"},
		"two names":       {service.ServiceURI, "vbcdx:sidecar/sc_1"},
	} {
		c, k := servicetest.Enrolled(t, ca, uris...)
		if _, err := service.LoadIdentity(c, k); err == nil || !strings.Contains(err.Error(), "does not name") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestIsControl(t *testing.T) {
	ca := servicetest.NewCA(t)
	cases := map[string]struct {
		l    servicetest.Leaf
		want bool
	}{
		"control":         {servicetest.Leaf{URIs: []string{service.ControlURI}, EKU: servicetest.ClientAuth}, true},
		"sidecar":         {servicetest.Leaf{URIs: []string{"vbcdx:sidecar/sc_1"}, EKU: servicetest.ClientAuth}, false},
		"a service":       {servicetest.Leaf{URIs: []string{"vbcdx:service/dispatch"}, EKU: servicetest.ClientAuth}, false},
		"two URIs":        {servicetest.Leaf{URIs: []string{service.ControlURI, "vbcdx:sidecar/sc_1"}, EKU: servicetest.ClientAuth}, false},
		"plus a DNS name": {servicetest.Leaf{URIs: []string{service.ControlURI}, DNS: []string{"x"}, EKU: servicetest.ClientAuth}, false},
		"plus an IP":      {servicetest.Leaf{URIs: []string{service.ControlURI}, IPs: servicetest.Loopback, EKU: servicetest.ClientAuth}, false},
		"prefix":          {servicetest.Leaf{URIs: []string{"vbcdx:controlx"}, EKU: servicetest.ClientAuth}, false},
		"no SAN":          {servicetest.Leaf{EKU: servicetest.ClientAuth}, false},
	}
	for name, c := range cases {
		_, cert := ca.Issue(t, c.l)
		if got := service.IsControl(cert); got != c.want {
			t.Errorf("%s: service.IsControl = %v, want %v", name, got, c.want)
		}
	}
}

// serve runs service.ServerConfig on a servicetest.Loopback listener and reports each
// handshake's error.
func serve(t *testing.T, cfg *tls.Config) string {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.(*tls.Conn).Handshake()
				_, _ = c.Write([]byte("ok"))
			}()
		}
	}()
	return ln.Addr().String()
}

func TestServerConfigAcceptsOnlyControl(t *testing.T) {
	ca, other := servicetest.NewCA(t), servicetest.NewCA(t)
	c, k := servicetest.Enrolled(t, ca)
	id, err := service.LoadIdentity(c, k)
	if err != nil {
		t.Fatal(err)
	}
	cfg := service.ServerConfig(id, ca.Pool)
	// Required, not just verified if given: the name check behind it would
	// refuse a missing certificate too, so the setting is pinned itself.
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert || cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("ClientAuth %v, MinVersion %x", cfg.ClientAuth, cfg.MinVersion)
	}
	addr := serve(t, cfg)
	control, _ := ca.Issue(t, servicetest.Leaf{URIs: []string{service.ControlURI}, EKU: servicetest.ClientAuth})
	dial := func(certs []tls.Certificate, maxVer uint16) error {
		cfg := &tls.Config{RootCAs: ca.Pool, ServerName: "127.0.0.1", Certificates: certs, NextProtos: []string{"h2"}, MaxVersion: maxVer}
		conn, err := tls.Dial("tcp", addr, cfg)
		if err != nil {
			return err
		}
		defer conn.Close()
		// TLS 1.3 sends the client cert verdict after the handshake: read it.
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		b := make([]byte, 2)
		if _, err := io.ReadFull(conn, b); err != nil {
			return err
		}
		if conn.ConnectionState().NegotiatedProtocol != "h2" {
			return errors.New("not h2")
		}
		return nil
	}
	if err := dial([]tls.Certificate{control}, 0); err != nil {
		t.Fatalf("vbcdx-control's certificate was refused: %v", err)
	}
	sidecar, _ := ca.Issue(t, servicetest.Leaf{URIs: []string{"vbcdx:sidecar/sc_1"}, EKU: servicetest.ClientAuth})
	twoSAN, _ := ca.Issue(t, servicetest.Leaf{URIs: []string{service.ControlURI, "vbcdx:sidecar/sc_1"}, EKU: servicetest.ClientAuth})
	serverOnly, _ := ca.Issue(t, servicetest.Leaf{URIs: []string{service.ControlURI}, EKU: servicetest.ServerAuth})
	foreign, _ := other.Issue(t, servicetest.Leaf{URIs: []string{service.ControlURI}, EKU: servicetest.ClientAuth})
	for name, c := range map[string]struct {
		certs  []tls.Certificate
		maxVer uint16
	}{
		"no client cert":       {nil, 0},
		"a sidecar":            {[]tls.Certificate{sidecar}, 0},
		"two SANs":             {[]tls.Certificate{twoSAN}, 0},
		"server auth only":     {[]tls.Certificate{serverOnly}, 0},
		"another CA":           {[]tls.Certificate{foreign}, 0},
		"TLS 1.2, the control": {[]tls.Certificate{control}, tls.VersionTLS12},
	} {
		if err := dial(c.certs, c.maxVer); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// controlServer is a fake tunnel listener that requires our service cert.
func controlServer(t *testing.T, ca servicetest.CA, h http.HandlerFunc) (string, tls.Certificate) {
	t.Helper()
	srvCert, _ := ca.Issue(t, servicetest.Leaf{DNS: []string{"vbcdx-control"}, IPs: servicetest.Loopback, EKU: servicetest.ServerAuth})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &http.Server{Handler: h, TLSConfig: &tls.Config{
		Certificates: []tls.Certificate{srvCert}, ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs: ca.Pool, MinVersion: tls.VersionTLS13, NextProtos: []string{"h2"},
	}}
	go s.ServeTLS(ln, "", "")
	t.Cleanup(func() { s.Close() })
	c, k := servicetest.Enrolled(t, ca)
	id, err := service.LoadIdentity(c, k)
	if err != nil {
		t.Fatal(err)
	}
	return "https://" + ln.Addr().String(), id
}

func TestControlKeys(t *testing.T) {
	ca := servicetest.NewCA(t)
	var sawPath, sawProto string
	u, id := controlServer(t, ca, func(w http.ResponseWriter, r *http.Request) {
		sawPath, sawProto = r.URL.Path, r.Proto
		_, _ = io.WriteString(w, `{"data":{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k"}]},"request_id":"r"}`)
	})
	c, err := service.NewControl(u, "", id, ca.Pool)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Keys(context.Background())
	if err != nil || string(got) != `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k"}]}` {
		t.Fatalf("Keys = %s, %v", got, err)
	}
	if sawPath != "/control/v1/keys" || sawProto != "HTTP/2.0" {
		t.Fatalf("request %s over %s", sawPath, sawProto)
	}
}

func TestControlKeysRefusals(t *testing.T) {
	ca := servicetest.NewCA(t)
	for name, h := range map[string]http.HandlerFunc{
		"not 200":   func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) },
		"no data":   func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"error":"x"}`) },
		"null data": func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"data":null}`) },
		"not json":  func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `nope`) },
		// Valid JSON exactly one byte over the cap: only the cap refuses it.
		"one byte over": func(w http.ResponseWriter, r *http.Request) {
			pre, post := `{"data":"`, `"}`
			_, _ = io.WriteString(w, pre+strings.Repeat("x", service.MaxKeysBody+1-len(pre)-len(post))+post)
		},
	} {
		t.Run(name, func(t *testing.T) {
			u, id := controlServer(t, ca, h)
			c, err := service.NewControl(u, "", id, ca.Pool)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := c.Keys(context.Background()); err == nil {
				t.Fatalf("Keys = %s, want an error", got)
			}
		})
	}
}

func TestControlRefusesAnotherCA(t *testing.T) {
	ca, other := servicetest.NewCA(t), servicetest.NewCA(t)
	u, id := controlServer(t, other, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"keys":[]}}`)
	})
	c, err := service.NewControl(u, "", id, ca.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Keys(context.Background()); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("a server from another CA: %v", err)
	}
	// The right CA but the wrong name.
	u2, id2 := controlServer(t, ca, func(w http.ResponseWriter, r *http.Request) {})
	c2, _ := service.NewControl(u2, "not-vbcdx-control", id2, ca.Pool)
	if _, err := c2.Keys(context.Background()); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("a server certificate for another name: %v", err)
	}
}

func TestControlIgnoresProxyAndRedirects(t *testing.T) {
	ca := servicetest.NewCA(t)
	// Nothing listens on :1, so a proxied dial would fail. (Set in a loop:
	// these are the environment's, not Keyhole's config.)
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY"} {
		t.Setenv(k, "http://127.0.0.1:1")
	}
	u, id := controlServer(t, ca, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			_, _ = io.WriteString(w, `{"data":{"followed":true}}`)
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})
	c, err := service.NewControl(u, "", id, ca.Pool)
	if err != nil {
		t.Fatal(err)
	}
	// Go never proxies a loopback target, so the env alone can't show this.
	if !service.UsesNoProxy(c) {
		t.Fatal("the control client has a proxy function")
	}
	got, err := c.Keys(context.Background())
	if err == nil || strings.Contains(string(got), "followed") || !strings.Contains(err.Error(), "302") {
		t.Fatalf("a redirect: %s, %v; want status 302 refused, not followed", got, err)
	}
}

func TestNewControlRefusesBadURLs(t *testing.T) {
	ca := servicetest.NewCA(t)
	c, k := servicetest.Enrolled(t, ca)
	id, _ := service.LoadIdentity(c, k)
	for _, u := range []string{"http://127.0.0.1:8443", "https://", "https://u:p@127.0.0.1", "https://127.0.0.1/x", "https://127.0.0.1?q=1", "127.0.0.1:8443", "https://127.0.0.1#f"} {
		if _, err := service.NewControl(u, "", id, ca.Pool); err == nil {
			t.Errorf("service.NewControl(%q) accepted", u)
		}
	}
	if _, err := service.NewControl("https://127.0.0.1:8443/", "", id, ca.Pool); err != nil {
		t.Errorf("a trailing slash: %v", err)
	}
}

// The refusal shows scheme://host only: a credential put in the userinfo,
// path, query or fragment never appears, whether the URL parses or not.
func TestNewControlNeverEchoesCredentials(t *testing.T) {
	ca := servicetest.NewCA(t)
	c, k := servicetest.Enrolled(t, ca)
	id, _ := service.LoadIdentity(c, k)
	for _, u := range []string{"https://u:CANARY-1@127.0.0.1:8443", "https://CANARY-2@127.0.0.1:8443", "https://u:CANARY-3@[::1",
		"https://127.0.0.1:8443/CANARY-4", "https://127.0.0.1:8443?token=CANARY-5", "https://127.0.0.1:8443#CANARY-6"} {
		_, err := service.NewControl(u, "", id, ca.Pool)
		if err == nil || strings.Contains(err.Error(), "CANARY") {
			t.Errorf("NewControl(%q): %v", u, err)
		}
	}
	if _, err := service.NewControl("http://127.0.0.1:8443", "", id, ca.Pool); err == nil || !strings.Contains(err.Error(), `"http://127.0.0.1:8443"`) {
		t.Errorf("a URL with no userinfo is no longer shown: %v", err)
	}
}

func TestControlRefusesTLS12(t *testing.T) {
	ca := servicetest.NewCA(t)
	srvCert, _ := ca.Issue(t, servicetest.Leaf{IPs: servicetest.Loopback, EKU: servicetest.ServerAuth})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"keys":[]}}`)
	}), TLSConfig: &tls.Config{Certificates: []tls.Certificate{srvCert}, MaxVersion: tls.VersionTLS12}}
	go s.ServeTLS(ln, "", "")
	t.Cleanup(func() { s.Close() })
	c, k := servicetest.Enrolled(t, ca)
	id, _ := service.LoadIdentity(c, k)
	ctl, err := service.NewControl("https://"+ln.Addr().String(), "", id, ca.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ctl.Keys(context.Background()); err == nil {
		t.Fatalf("a TLS 1.2 control server answered: %s", got)
	}
}

// The key is opened once, never through a symlink, and must be a regular
// file: every check is on what was opened.
func TestLoadIdentityRefusesASymlinkedOrIrregularKey(t *testing.T) {
	ca := servicetest.NewCA(t)
	c, k := servicetest.Enrolled(t, ca)
	if _, err := service.LoadIdentity(c, k); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "link.key")
	if err := os.Symlink(k, link); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoadIdentity(c, link); err == nil || !strings.Contains(err.Error(), "a symlink is refused") {
		t.Errorf("a symlinked key: %v", err)
	}
	sub := filepath.Join(dir, "d")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoadIdentity(c, sub); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a directory: %v", err)
	}
}
