// Package servicetest issues test certificates in the shapes
// vbcdx-control's CA issues them (crates/vbcdx-tunnel/src/pki.rs), for
// tests of Keyhole's service link. Only tests import it.
package servicetest

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/service"
)

// CA is a throwaway CA shaped like vbcdx-control's: Ed25519, and the
// leaf shapes its pki.rs issues.
type CA struct {
	Cert *x509.Certificate
	key  ed25519.PrivateKey
	Pool *x509.CertPool
	PEM  []byte
}

func NewCA(t testing.TB) CA {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "vbcdx test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(c)
	return CA{c, key, pool, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// Leaf is a certificate to issue.
type Leaf struct {
	URIs []string
	DNS  []string
	IPs  []net.IP
	EKU  []x509.ExtKeyUsage
	Pub  ed25519.PublicKey // nil: a fresh key
}

// Issue signs l with ca.
func (ca CA) Issue(t testing.TB, l Leaf) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	pub, key := l.Pub, ed25519.PrivateKey(nil)
	if pub == nil {
		pub, key, _ = ed25519.GenerateKey(rand.Reader)
	}
	var uris []*url.URL
	for _, u := range l.URIs {
		p, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		uris = append(uris, p)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "leaf"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		URIs: uris, DNSNames: l.DNS, IPAddresses: l.IPs, ExtKeyUsage: l.EKU,
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, pub, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der, ca.Cert.Raw}, PrivateKey: key, Leaf: c}, c
}

// The key usages and the loopback address the certificates use.
var (
	ClientAuth = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	ServerAuth = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	BothAuth   = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}
	Loopback   = []net.IP{net.ParseIP("127.0.0.1")}
)

// Enrolled writes our key and CSR, signs it as `services add` would, and
// returns the identity's file paths.
func Enrolled(t testing.TB, ca CA, uris ...string) (certPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()
	keyPath, csrPath := filepath.Join(dir, "service.key"), filepath.Join(dir, "service.csr")
	if err := service.WriteCSR(keyPath, csrPath); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(csrPath)
	b, _ := pem.Decode(raw)
	csr, _ := x509.ParseCertificateRequest(b.Bytes)
	if len(uris) == 0 {
		uris = []string{service.ServiceURI}
	}
	cert, _ := ca.Issue(t, Leaf{URIs: uris, IPs: Loopback, EKU: BothAuth, Pub: csr.PublicKey.(ed25519.PublicKey)})
	var chain []byte
	for _, d := range cert.Certificate {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})...)
	}
	certPath = filepath.Join(dir, "service.crt")
	if err := os.WriteFile(certPath, chain, 0o644); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}
