package server

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

const maxJSONBody = 8 << 10

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Error: code, Message: msg})
}

// readJSON decodes a small body holding exactly one JSON value. Requiring
// application/json also means a cross-site HTML form cannot submit to the
// endpoint.
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return errors.New("Content-Type must be application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("body must hold a single JSON value")
	}
	return nil
}

// clientIP is the address used for rate limiting. It is the TCP peer
// unless the peer is one of TRUSTED_PROXIES. Then X-Forwarded-For is read
// from the right: each trusted proxy appends the address it received the
// request from, so hops are skipped while they are trusted proxies, and the
// first address that is not one is the client. Everything further left was
// written by the client and is ignored. An entry that is not an address
// stops the walk at the last proxy reached.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	client, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	client = client.Unmap()
	if !s.trustedProxy(client) {
		return client.String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := parseHop(hops[i])
		if !ok {
			break
		}
		client = hop
		if !s.trustedProxy(hop) {
			break
		}
	}
	return client.String()
}

// limitKey is the part of a client address that sign-in limits count
// against. An IPv6 client usually holds a whole /64, so it is limited per
// /64; anything else is limited per address.
func limitKey(client string) string {
	ip, err := netip.ParseAddr(client)
	if err != nil || !ip.Is6() {
		return client
	}
	return netip.PrefixFrom(ip, 64).Masked().String()
}

func (s *Server) trustedProxy(ip netip.Addr) bool {
	for _, p := range s.cfg.TrustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// parseHop reads one X-Forwarded-For entry, with or without a port.
func parseHop(v string) (netip.Addr, bool) {
	v = strings.TrimSpace(v)
	if ip, err := netip.ParseAddr(v); err == nil && ip.Zone() == "" {
		return ip.Unmap(), true
	}
	if ap, err := netip.ParseAddrPort(v); err == nil && ap.Addr().Zone() == "" {
		return ap.Addr().Unmap(), true
	}
	return netip.Addr{}, false
}
