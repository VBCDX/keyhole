// Package service is Keyhole's side of the link with vbcdx-control.
//
//   - Keyhole makes its own Ed25519 key and a CSR ([WriteCSR]); an admin
//     enrolls it with `vbcdx-control services add keyhole --csr …`, and the
//     control CA signs a certificate naming `vbcdx:service/keyhole` plus the
//     host vbcdx-control dials. The key never leaves this host.
//   - vbcdx-control forwards a sidecar's request to Keyhole over HTTP/2 and
//     TLS 1.3, presenting its client certificate `vbcdx:control`.
//     [ServerConfig] requires exactly that certificate, as
//     vbcdx-core's `service_server_config` does.
//   - Keyhole fetches the assertion-signing keys from vbcdx-control the
//     other way, over the tunnel listener with its service certificate
//     ([Control.Keys]: GET /control/v1/keys).
//
// The control plane is the only peer: this package never dials a
// consuming service (rule 4).
package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// The URI SANs vbcdx-control's CA writes (vbcdx-core crates/vbcdx-tunnel/src/pki.rs).
const (
	ControlURI    = "vbcdx:control"
	ServiceURI    = "vbcdx:service/keyhole"
	keysPath      = "/control/v1/keys"
	maxKeysBody   = 64 << 10
	clientTimeout = 10 * time.Second
)

// WriteCSR makes a new Ed25519 key and a CSR for it. The key file is
// created 0600 and must not exist yet; the CSR is public.
func WriteCSR(keyPath, csrPath string) error {
	_, sk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(sk)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	defer clear(keyPEM)
	defer clear(der)
	csr, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: "keyhole"}}, sk)
	if err != nil {
		return err
	}
	if err := writeNew(keyPath, keyPEM, 0o600); err != nil {
		return err
	}
	if err := writeNew(csrPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}), 0o644); err != nil {
		_ = os.Remove(keyPath)
		return err
	}
	return nil
}

func writeNew(path string, b []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("service: create %s: %w", path, err)
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("service: write %s: %w", path, errors.Join(werr, cerr))
	}
	return nil
}

// LoadIdentity reads the enrolled certificate (the chain from `services
// add --cert-out`) and the key from WriteCSR. The key file must have no
// group or other permission bits, and the certificate must name
// vbcdx:service/keyhole.
func LoadIdentity(certPath, keyPath string) (tls.Certificate, error) {
	// The key is opened once, without following a symlink, and every check
	// is made on what was opened (fstat), as for the KEK: a file swapped in
	// between a check and a reopen can't skip them.
	kf, err := os.OpenFile(keyPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("service: key file (a symlink is refused): %w", err)
	}
	defer kf.Close()
	fi, err := kf.Stat()
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("service: key file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return tls.Certificate{}, fmt.Errorf("service: key file %s is not a regular file", keyPath)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return tls.Certificate{}, fmt.Errorf("service: key file %s has mode %04o; it must be 0600 or stricter", keyPath, fi.Mode().Perm())
	}
	// Like the KEK: the key belongs to the user Keyhole runs as.
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return tls.Certificate{}, fmt.Errorf("service: key file %s is not owned by this user (uid %d)", keyPath, os.Geteuid())
	}
	keyPEM, err := io.ReadAll(io.LimitReader(kf, 64<<10))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("service: key file: %w", err)
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("service: identity: %w", err)
	}
	c, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("service: identity: %w", err)
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("service: identity: %w", err)
	}
	if !namesService(leaf) {
		return tls.Certificate{}, fmt.Errorf("service: %s does not name %s", certPath, ServiceURI)
	}
	c.Leaf = leaf
	return c, nil
}

// LoadCA reads the control CA certificate (PEM).
func LoadCA(path string) (*x509.CertPool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("service: control CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		return nil, fmt.Errorf("service: %s holds no PEM certificate", path)
	}
	return pool, nil
}

// IsControl reports whether a certificate names vbcdx:control and nothing
// else: one URI SAN, and no DNS, IP or email SAN.
func IsControl(c *x509.Certificate) bool {
	return len(c.URIs) == 1 && c.URIs[0].String() == ControlURI &&
		len(c.DNSNames) == 0 && len(c.IPAddresses) == 0 && len(c.EmailAddresses) == 0
}

// namesService reports whether a certificate's only URI SAN is
// vbcdx:service/keyhole (its host SANs are where it is dialed).
func namesService(c *x509.Certificate) bool {
	return len(c.URIs) == 1 && c.URIs[0].String() == ServiceURI
}

// ServerConfig is the forward listener's TLS: TLS 1.3, h2, our enrolled
// certificate, and a client certificate required that chains to the
// control CA, is for client auth, and names exactly vbcdx:control.
func ServerConfig(id tls.Certificate, controlCA *x509.CertPool) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2"},
		Certificates: []tls.Certificate{id},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    controlCA,
		// Runs after the chain is verified for client auth.
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 || !IsControl(cs.PeerCertificates[0]) {
				return errors.New("service: the client certificate is not vbcdx-control's")
			}
			return nil
		},
	}
}

// Control is the client for vbcdx-control's tunnel listener, as the
// enrolled service.
type Control struct {
	base   *url.URL
	client *http.Client
}

// NewControl returns a client for controlURL (https://HOST[:PORT]) that
// presents id and accepts only a server certificate chaining to the
// control CA for serverName (the URL's host when empty).
func NewControl(controlURL, serverName string, id tls.Certificate, controlCA *x509.CertPool) (*Control, error) {
	u, err := url.Parse(controlURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		// Only scheme://host is shown: a userinfo, path, query or fragment may
		// carry a credential.
		got := "(not shown: it does not parse)"
		if err == nil {
			got = u.Scheme + "://" + u.Host
		}
		return nil, fmt.Errorf("service: CONTROL_URL must be https://HOST[:PORT], got %q", got)
	}
	u.Path = ""
	if serverName == "" {
		serverName = u.Hostname()
	}
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2"},
		Certificates: []tls.Certificate{id},
		RootCAs:      controlCA,
		ServerName:   serverName,
	}
	tr := &http.Transport{
		TLSClientConfig:     cfg,
		ForceAttemptHTTP2:   true,
		Proxy:               nil, // never through a proxy: the peer is the control plane
		DialContext:         (&net.Dialer{Timeout: clientTimeout}).DialContext,
		TLSHandshakeTimeout: clientTimeout,
		MaxIdleConns:        2,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Control{base: u, client: &http.Client{
		Transport: tr,
		Timeout:   clientTimeout,
		// The control plane never redirects; following one could leave it.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Keys fetches the JWK Set the assertion verifier needs
// (GET /control/v1/keys, which answers {"data": <JWK Set>}).
func (c *Control) Keys(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base.String()+keysPath, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("service: control keys: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxKeysBody+1))
	if err != nil {
		return nil, fmt.Errorf("service: control keys: %w", err)
	}
	if res.StatusCode != http.StatusOK || len(body) > maxKeysBody {
		return nil, fmt.Errorf("service: control keys: status %d, %d bytes", res.StatusCode, len(body))
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil || len(env.Data) == 0 || strings.TrimSpace(string(env.Data)) == "null" {
		return nil, errors.New("service: control keys: no data in the reply")
	}
	return env.Data, nil
}
