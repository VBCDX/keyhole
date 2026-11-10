package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

// Analytics off (the default): the web config says so and the CSP stays strict,
// so nothing about a Keyhole with no GA4 measurement id changes.
func TestAnalyticsOffByDefault(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, nil)
	c := h.client()
	res := h.do(c, http.MethodGet, "/api/auth/config", nil, nil)
	csp := res.Header.Get("Content-Security-Policy")
	if got := decode[authConfigResponse](t, res); got.GAMeasurementID != "" {
		t.Fatalf("ga_measurement_id = %q, want empty", got.GAMeasurementID)
	}
	if strings.Contains(csp, "googletagmanager") || strings.Contains(csp, "google-analytics") {
		t.Fatalf("CSP with analytics off allows GA hosts: %q", csp)
	}
}

// Analytics on: the id is served to the web app and only then do the GA4 hosts
// enter the CSP.
func TestAnalyticsOnServesIDAndAllowsGAHosts(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, nil)
	h.cfg.GAMeasurementID = "G-ABC123DEF4"
	c := h.client()
	res := h.do(c, http.MethodGet, "/api/auth/config", nil, nil)
	csp := res.Header.Get("Content-Security-Policy")
	if got := decode[authConfigResponse](t, res); got.GAMeasurementID != "G-ABC123DEF4" {
		t.Fatalf("ga_measurement_id = %q, want the configured id", got.GAMeasurementID)
	}
	for _, want := range []string{
		"script-src 'self' https://www.googletagmanager.com",
		"connect-src 'self' https://*.google-analytics.com https://*.analytics.google.com https://www.googletagmanager.com",
		"img-src 'self' data: https://*.google-analytics.com https://www.googletagmanager.com",
	} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP with analytics on missing %q: %q", want, csp)
		}
	}
	if strings.Contains(csp, "'unsafe-inline'") || strings.Contains(csp, "'unsafe-eval'") {
		t.Fatalf("CSP with analytics on is weaker than it needs to be: %q", csp)
	}
}
