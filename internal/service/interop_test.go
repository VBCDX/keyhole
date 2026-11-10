package service

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
	"time"
)

func readPEM(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(raw)
	if b == nil {
		t.Fatalf("%s: no PEM block", name)
	}
	return b.Bytes
}

// TestRustIssuedCertificates checks Keyhole's name and chain rules against
// what vbcdx-control's CA really issues (testdata/README.md).
func TestRustIssuedCertificates(t *testing.T) {
	ca, err := x509.ParseCertificate(readPEM(t, "rust-ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := x509.ParseCertificate(readPEM(t, "rust-service.pem"))
	if err != nil {
		t.Fatal(err)
	}
	ctl, err := x509.ParseCertificate(readPEM(t, "rust-control.pem"))
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.ParseCertificateRequest(readPEM(t, "go-service.csr"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	at := svc.NotBefore.Add(time.Hour)

	// The CA signed our Go CSR: the same public key.
	pk, _ := x509.MarshalPKIXPublicKey(csr.PublicKey)
	if !bytes.Equal(pk, svc.RawSubjectPublicKeyInfo) {
		t.Fatal("the service certificate is not for the Go CSR's key")
	}
	if !namesService(svc) || IsControl(svc) {
		t.Fatalf("service cert URIs %v: namesService %v, IsControl %v", svc.URIs, namesService(svc), IsControl(svc))
	}
	if _, err := svc.Verify(x509.VerifyOptions{Roots: pool, CurrentTime: at, DNSName: "127.0.0.1",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatalf("the service certificate as a server for 127.0.0.1: %v", err)
	}
	if !IsControl(ctl) || namesService(ctl) {
		t.Fatalf("control cert URIs %v DNS %v IPs %v", ctl.URIs, ctl.DNSNames, ctl.IPAddresses)
	}
	if _, err := ctl.Verify(x509.VerifyOptions{Roots: pool, CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("the control certificate as a client: %v", err)
	}
	// It is a client certificate only: not usable as a server.
	if _, err := ctl.Verify(x509.VerifyOptions{Roots: pool, CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil {
		t.Fatal("the control certificate verified for server auth")
	}
}
