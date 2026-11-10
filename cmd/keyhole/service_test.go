package main

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/assertion"
	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/envelope"
	"github.com/VBCDX/keyhole/internal/seal"
	"github.com/VBCDX/keyhole/internal/service/servicetest"
	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

func TestServiceCSRCommand(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	k, c := filepath.Join(dir, "k"), filepath.Join(dir, "c")
	if err := run(context.Background(), []string{"service", "csr", "--key-out", k, "--csr-out", c}, nil, &out, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "services add keyhole") {
		t.Fatalf("output %q", out.String())
	}
	for _, args := range [][]string{{"service"}, {"service", "csr", "--key-out", k}, {"service", "csr", "--key-out", k, "--csr-out", c}} {
		if err := run(context.Background(), args, nil, io.Discard, nil); err == nil {
			t.Errorf("%v succeeded", args)
		}
	}
}

// TestForwardServerEndToEnd runs the forward listener as serve does: a
// fake control tunnel listener serves the signing keys, and a client with
// vbcdx-control's certificate resolves over HTTP/2 mTLS.
func TestForwardServerEndToEnd(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	ca := servicetest.NewCA(t)
	certFile, keyFile := servicetest.Enrolled(t, ca)
	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, ca.PEM, 0o644); err != nil {
		t.Fatal(err)
	}
	kekFile := filepath.Join(dir, "kek")
	if err := envelope.GenerateKEK(kekFile); err != nil {
		t.Fatal(err)
	}
	kek, _ := envelope.LoadKEK(kekFile)

	b := storetest.Backends(t)[0]
	st := storetest.Open(t, b)
	f, _ := os.Open(litellmFixture)
	fx, err := store.ParseFixture(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	fx.Tables = append(fx.Tables, store.FixtureTable{Table: "workspace_memberships", Rows: []map[string]any{{
		"id": "wm_usr_e2e", "org_id": "org_team", "workspace_id": "ws_team", "member_kind": "user", "member_id": "usr_e2e",
		"role": "member", "added_at": "@now", "added_by_kind": "system", "added_by_id": "t", "updated_at": "@now", "rev": json.Number("0"),
	}}})
	if _, err := st.LoadFixture(ctx, fx, time.Now()); err != nil {
		t.Fatal(err)
	}
	const canary = "sk-CANARY-e2e-41c9"
	if _, err := st.PutLocalValue(ctx, kek, "org_team", "k_litellm_test", []byte(canary), "t", time.Now()); err != nil {
		t.Fatal(err)
	}

	// The control plane's tunnel listener: its server certificate from the
	// same CA, our service certificate required, the signing keys.
	pub, sk, _ := ed25519.GenerateKey(rand.Reader)
	b64 := base64.RawURLEncoding
	jwks, _ := json.Marshal(map[string]any{"data": map[string]any{"keys": []any{map[string]any{"kty": "OKP", "crv": "Ed25519", "kid": "k1", "x": b64.EncodeToString(pub)}}}})
	ctlCert, _ := ca.Issue(t, servicetest.Leaf{IPs: servicetest.Loopback, EKU: servicetest.ServerAuth})
	cln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	keysCalls := 0
	ctl := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/control/v1/keys" && len(r.TLS.PeerCertificates) > 0 && r.TLS.PeerCertificates[0].URIs[0].String() == "vbcdx:service/keyhole" {
			keysCalls++
			_, _ = w.Write(jwks)
			return
		}
		http.NotFound(w, r)
	}), TLSConfig: &tls.Config{Certificates: []tls.Certificate{ctlCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: ca.Pool, MinVersion: tls.VersionTLS13, NextProtos: []string{"h2"}}}
	go ctl.ServeTLS(cln, "", "")
	t.Cleanup(func() { ctl.Close() })

	logs := &bytes.Buffer{}
	fs, fln, err := forwardServer(ctx, config.Service{
		ListenAddr: "127.0.0.1:0", CertFile: certFile, KeyFile: keyFile, ControlCAFile: caFile,
		ControlURL: "https://" + cln.Addr().String(), KEKFile: kekFile,
	}, st, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	go fs.Serve(fln)
	t.Cleanup(func() { fs.Close() })
	if keysCalls != 1 {
		t.Fatalf("the keys were fetched %d times at start, want 1", keysCalls)
	}

	client := func(cert tls.Certificate) *http.Client {
		return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{ForceAttemptHTTP2: true,
			TLSClientConfig: &tls.Config{RootCAs: ca.Pool, ServerName: "127.0.0.1", Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2"}}}}
	}
	now := time.Now().Unix()
	h, _ := json.Marshal(map[string]any{"alg": "EdDSA", "typ": assertion.Typ, "kid": "k1"})
	p, _ := json.Marshal(map[string]any{"iss": assertion.Issuer, "aud": "keyhole", "iat": now, "exp": now + 60, "jti": "req_e2e",
		"sub": "usr_e2e", "kind": "user", "sidecar": "sc_e2e", "org": map[string]any{"id": "org_team", "role": "member"}})
	in := b64.EncodeToString(h) + "." + b64.EncodeToString(p)
	jws := in + "." + b64.EncodeToString(ed25519.Sign(sk, []byte(in)))
	eph, _ := ecdh.X25519().GenerateKey(rand.Reader)
	body, _ := json.Marshal(map[string]string{"tool": "t_litellm", "slot": "litellm_key", "eph_pub": b64.EncodeToString(eph.PublicKey().Bytes())})
	url := "https://" + fln.Addr().String() + "/orgs/org_team/resolve"
	post := func(c *http.Client) (*http.Response, error) {
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Vbcdx-Assertion", jws)
		return c.Do(req)
	}

	// A sidecar's certificate can't even connect.
	sidecar, _ := ca.Issue(t, servicetest.Leaf{URIs: []string{"vbcdx:sidecar/sc_e2e"}, EKU: servicetest.ClientAuth})
	if res, err := post(client(sidecar)); err == nil {
		res.Body.Close()
		t.Fatalf("a sidecar certificate got status %d", res.StatusCode)
	}

	control, _ := ca.Issue(t, servicetest.Leaf{URIs: []string{"vbcdx:control"}, EKU: servicetest.ClientAuth})
	res, err := post(client(control))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || res.Proto != "HTTP/2.0" {
		t.Fatalf("status %d over %s: %s", res.StatusCode, res.Proto, raw)
	}
	var rep struct {
		Sealed seal.Sealed `json:"sealed"`
		TTL    int         `json:"ttl"`
		// The tool definition as the sidecar reads it off the wire: the v2
		// seal binds exactly this.
		Tool struct {
			Version   int    `json:"version"`
			BaseURL   string `json:"base_url"`
			TimeoutMs int    `json:"timeout_ms"`
			Slot      struct {
				Location string `json:"location"`
				Field    string `json:"field"`
				Scheme   string `json:"scheme"`
			} `json:"slot"`
			PrivateAllow []string `json:"private_allow"`
			Actions      []struct {
				Method string `json:"method"`
				Path   string `json:"path"`
			} `json:"actions"`
		} `json:"tool"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	def := seal.ToolDef{Version: rep.Tool.Version, BaseURL: rep.Tool.BaseURL, TimeoutMs: rep.Tool.TimeoutMs,
		SlotLocation: rep.Tool.Slot.Location, SlotField: rep.Tool.Slot.Field, SlotScheme: rep.Tool.Slot.Scheme,
		PrivateAllow: rep.Tool.PrivateAllow}
	for _, a := range rep.Tool.Actions {
		def.Actions = append(def.Actions, seal.Action{Method: a.Method, Path: a.Path})
	}
	got, err := seal.Open(eph, rep.Sealed, seal.AAD("req_e2e", "t_litellm", "litellm_key", def))
	if err != nil || string(got) != canary || rep.TTL != 300 {
		t.Fatalf("opened %q (ttl %d), %v", got, rep.TTL, err)
	}
	if strings.Contains(string(raw)+logs.String(), "CANARY") {
		t.Fatal("the value reached the reply in clear or the log")
	}
}
