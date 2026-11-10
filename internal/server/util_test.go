package server

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func readAll(t testing.TB, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func (h *harness) idpURL() string {
	if h.idp == nil {
		return "\x00no-idp"
	}
	return h.idp.URL
}
