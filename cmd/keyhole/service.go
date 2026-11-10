package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/VBCDX/keyhole/internal/assertion"
	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/envelope"
	"github.com/VBCDX/keyhole/internal/server"
	"github.com/VBCDX/keyhole/internal/service"
	"github.com/VBCDX/keyhole/internal/store"
)

func serviceCmd(args []string, stdout io.Writer) error {
	const use = "usage: keyhole service csr --key-out FILE --csr-out FILE"
	if len(args) == 0 || args[0] != "csr" {
		return usageErr("%s", use)
	}
	fs := flag.NewFlagSet("service csr", flag.ContinueOnError)
	keyOut := fs.String("key-out", "", "the new private key (created 0600; must not exist)")
	csrOut := fs.String("csr-out", "", "the CSR to pass to `vbcdx-control services add keyhole --csr`")
	if err := parseFlags(fs, args[1:], use); err != nil {
		return err
	}
	if *keyOut == "" || *csrOut == "" || fs.NArg() > 0 {
		return usageErr("%s", use)
	}
	if err := service.WriteCSR(*keyOut, *csrOut); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote the key to %s and the CSR to %s\nnext: vbcdx-control services add keyhole --url https://%s --csr %s --cert-out CERT\n",
		*keyOut, *csrOut, "127.0.0.1:PORT", *csrOut)
	return nil
}

// forwardServer is the listener vbcdx-control forwards resolves to: mTLS
// with its vbcdx:control certificate required, HTTP/2, the resolve route
// only.
func forwardServer(ctx context.Context, c config.Service, st *store.Store, log *slog.Logger) (*http.Server, net.Listener, error) {
	id, err := service.LoadIdentity(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, nil, err
	}
	ca, err := service.LoadCA(c.ControlCAFile)
	if err != nil {
		return nil, nil, err
	}
	kek, err := envelope.LoadKEK(c.KEKFile)
	if err != nil {
		return nil, nil, err
	}
	ctl, err := service.NewControl(c.ControlURL, c.ControlServerName, id, ca)
	if err != nil {
		return nil, nil, err
	}
	// The keys at start; if control isn't up yet, the verifier fetches them
	// on the first assertion it can't place.
	keysCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	jwks, err := ctl.Keys(keysCtx)
	cancel()
	if err != nil {
		log.Warn("the control plane's signing keys aren't available yet; fetching on first use", "err", err)
		jwks = []byte(`{"keys":[]}`)
	}
	// The store types values come from: today the built-in Local store only
	// (docs/stores.md says how another one is added).
	stores, err := store.NewStoreTypes(st.LocalType(kek))
	if err != nil {
		return nil, nil, err
	}
	rv := &server.Resolver{Store: st, Stores: stores, Verifier: assertion.New("keyhole", jwks, ctl.Keys), Log: log, Now: time.Now}
	hs := newHTTPServer(c.ListenAddr, rv.Handler())
	hs.TLSConfig = service.ServerConfig(id, ca)
	ln, err := net.Listen("tcp", c.ListenAddr)
	if err != nil {
		return nil, nil, err
	}
	return hs, tls.NewListener(ln, hs.TLSConfig), nil
}
