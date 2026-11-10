package config

import (
	"net"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	c, err := load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthMode != AuthLocal || c.ListenAddr != "localhost:8080" || c.DatabaseURL != "sqlite://data/keyhole.db" ||
		c.SessionTTL != 24*time.Hour || c.SecureCookies() ||
		c.Origin() != "http://localhost:8080" || c.GAMeasurementID != "" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

// Analytics is off unless a well-formed GA4 measurement id is configured;
// a typo fails at start-up instead of silently sending nothing.
func TestGAMeasurementID(t *testing.T) {
	c, err := load(env(map[string]string{"GA_MEASUREMENT_ID": "G-ABC123DEF4"}))
	if err != nil || c.GAMeasurementID != "G-ABC123DEF4" {
		t.Fatalf("valid GA id: %+v, %v", c, err)
	}
	for _, bad := range []string{"UA-123456-1", "G-", "G-abc", "abc123", "G-ABC 123"} {
		if _, err := load(env(map[string]string{"GA_MEASUREMENT_ID": bad})); err == nil {
			t.Errorf("GA_MEASUREMENT_ID=%q: accepted", bad)
		}
	}
}

// The default bind and the default public URL name the same host, so the
// documented http://localhost:8080 reaches the process however the host
// resolves localhost; and the default is still loopback-only.
func TestDefaultListenHostMatchesPublicURL(t *testing.T) {
	c, err := load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	host, _, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		t.Fatal(err)
	}
	if host != c.PublicURL.Hostname() {
		t.Errorf("default listen host %q != default PUBLIC_URL host %q", host, c.PublicURL.Hostname())
	}
	if !isLoopback(host) {
		t.Errorf("default listen host %q must be loopback", host)
	}
}

// ListenAddrFromEnv and ProbeAddr are what `keyhole healthcheck` resolves its
// probe address with.
func TestListenAddrFromEnvAndProbeAddr(t *testing.T) {
	for in, want := range map[string]string{"": DefaultListenAddr, "  ": DefaultListenAddr, ":9": ":9", "0.0.0.0:9": "0.0.0.0:9"} {
		if got := ListenAddrFromEnv(env(map[string]string{"LISTEN_ADDR": in})); got != want {
			t.Errorf("ListenAddrFromEnv(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{":8080": "127.0.0.1:8080", "0.0.0.0:9": "127.0.0.1:9", "[::]:9": "[::1]:9", "localhost:8080": "localhost:8080", "10.0.0.5:9": "10.0.0.5:9", "[::1]:9": "[::1]:9"} {
		if got := ProbeAddr(in); got != want {
			t.Errorf("ProbeAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

// LISTEN_ADDR is passed through verbatim: binding every interface is an
// explicit choice (:8080, 0.0.0.0:8080), never the default.
func TestListenAddrOverride(t *testing.T) {
	for _, want := range []string{":8080", "0.0.0.0:8080", "127.0.0.1:9000", "localhost:9000"} {
		c, err := load(env(map[string]string{"LISTEN_ADDR": want}))
		if err != nil || c.ListenAddr != want {
			t.Errorf("LISTEN_ADDR=%q: ListenAddr = %q, %v; want %q", want, c.ListenAddr, err, want)
		}
	}
}

func TestZitadelModeNeedsClientSettings(t *testing.T) {
	_, err := load(env(map[string]string{"AUTH_MODE": "zitadel", "PUBLIC_URL": "https://keyhole.example.com"}))
	if err == nil || !strings.Contains(err.Error(), "OIDC_ISSUER") || !strings.Contains(err.Error(), "OIDC_CLIENT_SECRET") {
		t.Fatalf("err = %v; want missing OIDC settings named", err)
	}
	c, err := load(env(map[string]string{
		"AUTH_MODE": "both", "PUBLIC_URL": "https://keyhole.example.com/",
		"OIDC_ISSUER": "https://auth.example.com/", "OIDC_CLIENT_ID": "id", "OIDC_CLIENT_SECRET": "secret",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.OIDC.Issuer != "https://auth.example.com" ||
		c.OIDC.RedirectURL != "https://keyhole.example.com/auth/oidc/callback" ||
		c.OIDC.PostLogoutRedirectURL != "https://keyhole.example.com/" || !c.SecureCookies() ||
		!c.AuthMode.Local() || !c.AuthMode.Zitadel() {
		t.Fatalf("unexpected config: %+v", c)
	}
}

// Single sign-on is optional: local mode (the default) boots with every
// OIDC_* blank, and never reads them, so a stray value cannot point it at a
// provider.
func TestLocalModeNeedsNoOIDC(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"nothing set":         {},
		"local, OIDC blank":   {"AUTH_MODE": "local"},
		"local, stray issuer": {"AUTH_MODE": "local", "OIDC_ISSUER": "not a url", "OIDC_CLIENT_ID": "id"},
	} {
		c, err := load(env(m))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if c.AuthMode != AuthLocal || c.AuthMode.Zitadel() || c.OIDC.Issuer != "" || c.OIDC.ClientID != "" {
			t.Fatalf("%s: auth mode %q, OIDC %+v; want local with no OIDC settings", name, c.AuthMode, c.OIDC)
		}
	}
}

func TestRejectsBadValues(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"unknown auth mode":       {"AUTH_MODE": "ldap"},
		"plain http off loopback": {"PUBLIC_URL": "http://keyhole.example.com"},
		"relative public url":     {"PUBLIC_URL": "/keyhole"},
		"short session secret":    {"SESSION_SECRET": "short"},
		"tiny session ttl":        {"SESSION_TTL": "5s"},
		"bootstrap email only":    {"BOOTSTRAP_ADMIN_EMAIL": "a@example.com"},
		"bootstrap password only": {"BOOTSTRAP_ADMIN_PASSWORD": "a long password"},
		"unparseable session ttl": {"SESSION_TTL": "forever"},
		"seed demo data not bool": {"SEED_DEMO_DATA": "yes please"},
	} {
		if _, err := load(env(m)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoopbackHTTPAllowed(t *testing.T) {
	for _, u := range []string{"http://localhost:8080", "http://127.0.0.1:9000", "http://[::1]:8080"} {
		if _, err := load(env(map[string]string{"PUBLIC_URL": u})); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}

// The Zitadel callback is served at the root and its flow cookie is scoped
// to /auth/oidc/, so a public URL with a path, or a redirect URL anywhere
// else, would make every sign-in fail. Both are refused at startup.
func TestRedirectURLMatchesWhatIsServed(t *testing.T) {
	zitadel := func(extra map[string]string) map[string]string {
		m := map[string]string{
			"AUTH_MODE": "zitadel", "PUBLIC_URL": "https://keyhole.example.com",
			"OIDC_ISSUER": "https://auth.example.com", "OIDC_CLIENT_ID": "id", "OIDC_CLIENT_SECRET": "secret",
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for name, m := range map[string]map[string]string{
		"public url with a path":         {"PUBLIC_URL": "https://keyhole.example.com/keyhole"},
		"public url with a query":        {"PUBLIC_URL": "https://keyhole.example.com/?a=b"},
		"redirect on another path":       zitadel(map[string]string{"OIDC_REDIRECT_URL": "https://keyhole.example.com/callback"}),
		"redirect on another host":       zitadel(map[string]string{"OIDC_REDIRECT_URL": "https://other.example.com/auth/oidc/callback"}),
		"redirect with a trailing slash": zitadel(map[string]string{"OIDC_REDIRECT_URL": "https://keyhole.example.com/auth/oidc/callback/"}),
	} {
		if _, err := load(env(m)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, m := range []map[string]string{
		zitadel(nil),
		zitadel(map[string]string{"PUBLIC_URL": "https://keyhole.example.com/"}),
		zitadel(map[string]string{"OIDC_REDIRECT_URL": "https://keyhole.example.com/auth/oidc/callback"}),
	} {
		c, err := load(env(m))
		if err != nil {
			t.Fatalf("%v: %v", m, err)
		}
		if c.OIDC.RedirectURL != "https://keyhole.example.com/auth/oidc/callback" {
			t.Fatalf("redirect URL = %q", c.OIDC.RedirectURL)
		}
	}
}

func TestTrustedProxies(t *testing.T) {
	c, err := load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.TrustedProxies) != 0 {
		t.Fatalf("default trusted proxies = %v, want none", c.TrustedProxies)
	}
	c, err = load(env(map[string]string{"TRUSTED_PROXIES": "10.0.1.0/24, 192.168.1.34  ::1,fd00::/8,10.0.2.7/16"}))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.1.0/24", "192.168.1.34/32", "::1/128", "fd00::/8", "10.0.0.0/16"}
	if len(c.TrustedProxies) != len(want) {
		t.Fatalf("trusted proxies = %v, want %v", c.TrustedProxies, want)
	}
	for i, p := range c.TrustedProxies {
		if p.String() != want[i] {
			t.Errorf("trusted proxy %d = %s, want %s", i, p, want[i])
		}
	}
	for _, bad := range []string{"proxy.example.com", "10.0.0.0/33", "0.0.0.0/0", "::/0", "fe80::1%eth0"} {
		if _, err := load(env(map[string]string{"TRUSTED_PROXIES": bad})); err == nil {
			t.Errorf("TRUSTED_PROXIES=%q was accepted", bad)
		}
	}
}

func TestSeedDemoData(t *testing.T) {
	for v, want := range map[string]bool{"": false, "false": false, "true": true, " TRUE ": true, "1": true, "0": false} {
		c, err := load(env(map[string]string{"SEED_DEMO_DATA": v}))
		if err != nil || c.SeedDemoData != want {
			t.Errorf("SEED_DEMO_DATA=%q: SeedDemoData = %v, %v; want %v", v, c != nil && c.SeedDemoData, err, want)
		}
	}
}

// With SEED_DEMO_DATA on, every sign-in without an organization becomes an
// Owner of the demo organization, so the flag is refused off loopback.
func TestSeedDemoDataRequiresLoopback(t *testing.T) {
	for _, u := range []string{"http://localhost:8080", "http://127.0.0.1:9000", "http://[::1]:8080", "https://localhost"} {
		c, err := load(env(map[string]string{"SEED_DEMO_DATA": "true", "PUBLIC_URL": u}))
		if err != nil || !c.SeedDemoData {
			t.Errorf("SEED_DEMO_DATA=true on %s: SeedDemoData = %v, %v; want on", u, c != nil && c.SeedDemoData, err)
		}
	}
	for _, u := range []string{"https://keyhole.example.com", "https://192.168.1.10:8080", "https://[2001:db8::1]:8080"} {
		if _, err := load(env(map[string]string{"SEED_DEMO_DATA": "true", "PUBLIC_URL": u})); err == nil || !strings.Contains(err.Error(), "SEED_DEMO_DATA") {
			t.Errorf("SEED_DEMO_DATA=true on %s: err = %v; want a refusal naming SEED_DEMO_DATA", u, err)
		}
	}
	if _, err := load(env(map[string]string{"SEED_DEMO_DATA": "false", "PUBLIC_URL": "https://keyhole.example.com"})); err != nil {
		t.Errorf("SEED_DEMO_DATA=false on a non-loopback URL: %v", err)
	}
}

func TestSuiteApps(t *testing.T) {
	c, err := load(env(nil))
	if err != nil || len(c.SuiteApps) != 0 {
		t.Fatalf("unset SUITE_APPS = %+v, %v; want none", c.SuiteApps, err)
	}
	c, err = load(env(map[string]string{"SUITE_APPS": " Dispatch=https://dispatch.vbcdx.com , Keyhole=https://keyhole.vbcdx.com,, Local=http://localhost:9090"}))
	if err != nil {
		t.Fatal(err)
	}
	want := []SuiteApp{{"Dispatch", "https://dispatch.vbcdx.com"}, {"Keyhole", "https://keyhole.vbcdx.com"}, {"Local", "http://localhost:9090"}}
	if len(c.SuiteApps) != len(want) {
		t.Fatalf("SuiteApps = %+v", c.SuiteApps)
	}
	for i := range want {
		if c.SuiteApps[i] != want[i] {
			t.Fatalf("SuiteApps[%d] = %+v, want %+v", i, c.SuiteApps[i], want[i])
		}
	}
	for _, bad := range []string{
		"Dispatch", "=https://x.example", "Dispatch=", "Evil=javascript:alert(1)", "Rel=/dispatch",
		"Plain=http://dispatch.vbcdx.com", "Creds=https://u:p@x.example", "NoHost=https://",
	} {
		if _, err := load(env(map[string]string{"SUITE_APPS": bad})); err == nil || !strings.Contains(err.Error(), "SUITE_APPS") {
			t.Errorf("SUITE_APPS=%q: err = %v; want a SUITE_APPS error", bad, err)
		}
	}
}

func TestServiceLink(t *testing.T) {
	full := map[string]string{
		"SERVICE_LISTEN_ADDR": "127.0.0.1:8444", "SERVICE_CERT_FILE": "/c", "SERVICE_KEY_FILE": "/k",
		"CONTROL_CA_FILE": "/ca", "CONTROL_URL": "https://127.0.0.1:8443", "KEK_FILE": "/kek",
	}
	c, err := load(env(nil))
	if err != nil || c.Service.ListenAddr != "" {
		t.Fatalf("off by default: %+v, %v", c.Service, err)
	}
	c, err = load(env(full))
	if err != nil || c.Service.ControlURL != "https://127.0.0.1:8443" || c.Service.KEKFile != "/kek" {
		t.Fatalf("all set: %+v, %v", c.Service, err)
	}
	for _, addr := range []string{"[::1]:8444", "localhost:8444"} {
		m := map[string]string{}
		for k, v := range full {
			m[k] = v
		}
		m["SERVICE_LISTEN_ADDR"] = addr
		if _, err := load(env(m)); err != nil {
			t.Errorf("%s: %v", addr, err)
		}
	}
	for _, addr := range []string{":8444", "0.0.0.0:8444", "192.168.10.92:8444", "127.0.0.1"} {
		m := map[string]string{}
		for k, v := range full {
			m[k] = v
		}
		m["SERVICE_LISTEN_ADDR"] = addr
		if _, err := load(env(m)); err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Errorf("SERVICE_LISTEN_ADDR=%s: %v, want a loopback refusal", addr, err)
		}
	}
	for _, key := range []string{"SERVICE_CERT_FILE", "SERVICE_KEY_FILE", "CONTROL_CA_FILE", "CONTROL_URL", "KEK_FILE"} {
		m := map[string]string{}
		for k, v := range full {
			if k != key {
				m[k] = v
			}
		}
		if _, err := load(env(m)); err == nil || !strings.Contains(err.Error(), key+" is required") {
			t.Errorf("without %s: %v", key, err)
		}
	}
}

// A refused PUBLIC_URL or SUITE_APPS URL is named by scheme://host only: a
// credential in its userinfo, path, query or fragment never reaches the error
// (and so neither keyhole check's output nor a unit's log).
func TestURLErrorsNeverEchoCredentials(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"public: http off loopback, userinfo": {"PUBLIC_URL": "http://u:CANARY-1@keyhole.example.com"},
		"public: a path":                      {"PUBLIC_URL": "https://keyhole.example.com/CANARY-2"},
		"public: a query":                     {"PUBLIC_URL": "https://keyhole.example.com/?t=CANARY-3"},
		"public: a fragment":                  {"PUBLIC_URL": "https://keyhole.example.com/#CANARY-4"},
		"public: not absolute":                {"PUBLIC_URL": "ftp://u:CANARY-5@x"},
		"public: does not parse":              {"PUBLIC_URL": "https://u:CANARY-6@[::1"},
		"suite: userinfo":                     {"SUITE_APPS": "Docs=https://u:CANARY-7@docs.example.com"},
		"suite: no name":                      {"SUITE_APPS": "https://u:CANARY-8@docs.example.com"},
		"suite: does not parse":               {"SUITE_APPS": "Docs=https://u:CANARY-9@[::1"},
		// AC1: a nameless entry whose credentialed URL has "=" in its
		// query would make the text before that "=" the "name".
		"suite: nameless, = in the query": {"SUITE_APPS": "https://u:CANARY-10@docs.example.com/?a=b"},
		// AC2: OIDC_REDIRECT_URL, and a PUBLIC_URL with userinfo
		// behind the expected value.
		"oidc redirect: userinfo": {"AUTH_MODE": "zitadel", "PUBLIC_URL": "https://keyhole.example.com",
			"OIDC_ISSUER": "https://auth.example.com", "OIDC_CLIENT_ID": "id", "OIDC_CLIENT_SECRET": "secret",
			"OIDC_REDIRECT_URL": "https://u:CANARY-11@keyhole.example.com/auth/oidc/callback?t=CANARY-12"},
		"oidc redirect: public url userinfo": {"AUTH_MODE": "zitadel", "PUBLIC_URL": "https://u:CANARY-13@keyhole.example.com",
			"OIDC_ISSUER": "https://auth.example.com", "OIDC_CLIENT_ID": "id", "OIDC_CLIENT_SECRET": "secret",
			"OIDC_REDIRECT_URL": "https://keyhole.example.com/elsewhere"},
		"oidc redirect: does not parse": {"AUTH_MODE": "zitadel", "PUBLIC_URL": "https://keyhole.example.com",
			"OIDC_ISSUER": "https://auth.example.com", "OIDC_CLIENT_ID": "id", "OIDC_CLIENT_SECRET": "secret",
			"OIDC_REDIRECT_URL": "https://u:CANARY-14@[::1"},
	} {
		_, err := load(env(m))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "CANARY") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A plain name is still named; a name that could be a URL fragment is an entry number.
	_, err := load(env(map[string]string{"SUITE_APPS": "Billing Portal=http://billing.example.com"}))
	if err == nil || !strings.Contains(err.Error(), "SUITE_APPS: Billing Portal's URL") {
		t.Errorf("err %v", err)
	}
	_, err = load(env(map[string]string{"SUITE_APPS": "Docs=https://docs.example.com,https://u:x@h/?a=b"}))
	if err == nil || !strings.Contains(err.Error(), "SUITE_APPS: entry 2 URL") {
		t.Errorf("err %v", err)
	}
	// The host part is still named, so the error says which URL is wrong.
	_, err = load(env(map[string]string{"PUBLIC_URL": "http://keyhole.example.com"}))
	if err == nil || !strings.Contains(err.Error(), `"http://keyhole.example.com"`) {
		t.Errorf("err %v", err)
	}
}
