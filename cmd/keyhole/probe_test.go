package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// healthz serves /healthz on 127.0.0.1 and returns its listen address.
func healthz(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// The healthcheck reaches the server's own loopback listener, and refuses an
// explicit non-loopback bind at the dial: it never leaves the host.
func TestHealthcheckProbesOnlyLoopback(t *testing.T) {
	t.Setenv("LISTEN_ADDR", healthz(t))
	if err := healthcheck(context.Background()); err != nil {
		t.Fatalf("healthcheck on loopback: %v", err)
	}
	for _, addr := range []string{"192.0.2.1:80", "[2001:db8::1]:80"} {
		t.Setenv("LISTEN_ADDR", addr)
		if err := healthcheck(context.Background()); !errors.Is(err, errNotLoopback) {
			t.Errorf("LISTEN_ADDR=%s: healthcheck = %v, want errNotLoopback", addr, err)
		}
	}
}

// probeClient dials only when every address of the host is loopback,
// whatever URL the request names.
func TestProbeClientDialsLoopbackOnly(t *testing.T) {
	addr := healthz(t)
	_, port, _ := net.SplitHostPort(addr)
	resolve := func(m map[string][]string) func(context.Context, string, string) ([]netip.Addr, error) {
		return func(_ context.Context, _, host string) ([]netip.Addr, error) {
			var out []netip.Addr
			for _, s := range m[host] {
				out = append(out, netip.MustParseAddr(s))
			}
			return out, nil
		}
	}
	lookup := resolve(map[string][]string{
		"127.0.0.1":    {"127.0.0.1"},
		"self.test":    {"127.0.0.1"},
		"mapped.test":  {"::ffff:127.0.0.1"},
		"evil.example": {"203.0.113.5"},
		"mixed.test":   {"127.0.0.1", "203.0.113.5"},
		"private.test": {"10.0.0.5"},
		"zero.test":    {"0.0.0.0"},
		"mapped4.test": {"::ffff:1.2.3.4"},
		"empty.test":   nil,
	})
	for host, ok := range map[string]bool{
		"127.0.0.1": true, "self.test": true, "mapped.test": true,
		"evil.example": false, "mixed.test": false, "private.test": false, "empty.test": false,
		"zero.test": false, "mapped4.test": false,
	} {
		res, err := probeClient(lookup).Get("http://" + net.JoinHostPort(host, port) + "/healthz")
		if ok {
			if err != nil {
				t.Errorf("%s: %v", host, err)
				continue
			}
			res.Body.Close()
			if res.StatusCode != http.StatusOK {
				t.Errorf("%s: %s", host, res.Status)
			}
		} else if !errors.Is(err, errNotLoopback) {
			t.Errorf("%s: err = %v, want errNotLoopback", host, err)
		}
	}
	// No redirect is followed, so a 30x can't send the probe elsewhere.
	srv := httptest.NewServer(http.RedirectHandler("http://evil.example/", http.StatusFound))
	defer srv.Close()
	res, err := probeClient(net.DefaultResolver.LookupNetIP).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Errorf("redirect followed: %s", res.Status)
	}
}
