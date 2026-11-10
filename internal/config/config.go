// Package config reads Keyhole's settings from the environment. Every
// variable read here is listed in .env.example.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AuthMode selects which sign-in methods are offered.
type AuthMode string

const (
	AuthLocal   AuthMode = "local"
	AuthZitadel AuthMode = "zitadel"
	AuthBoth    AuthMode = "both"
)

// Local reports whether email-and-password sign-in is enabled.
func (m AuthMode) Local() bool { return m == AuthLocal || m == AuthBoth }

// Zitadel reports whether OpenID Connect sign-in is enabled.
func (m AuthMode) Zitadel() bool { return m == AuthZitadel || m == AuthBoth }

// OIDC holds the Zitadel (or any OpenID Connect provider) client settings.
type OIDC struct {
	Issuer                string
	ClientID              string
	ClientSecret          string
	RedirectURL           string
	PostLogoutRedirectURL string
	Scopes                []string
}

// Bootstrap describes a local account to create on first start, when the
// database has no users at all.
type Bootstrap struct {
	Email    string
	Name     string
	Password string
}

// SuiteApp is one VBCDX app in the app switcher.
type SuiteApp struct {
	Name string
	URL  string
}

// DefaultListenAddr is the address Keyhole binds when LISTEN_ADDR is unset:
// loopback only. It uses the name "localhost", the same host as the default
// PUBLIC_URL and the README quickstart, so the bind follows however the host
// resolves localhost (127.0.0.1 or ::1) instead of fixing IPv4 while the
// advertised URL may resolve to IPv6. Go binds the first IPv4 address
// when localhost has both families.
const (
	defaultPort       = "8080"
	DefaultListenAddr = "localhost:" + defaultPort
	DefaultPublicURL  = "http://localhost:" + defaultPort
)

// ListenAddrFromEnv resolves LISTEN_ADDR exactly as load does. It is the one
// place the default lives; `keyhole healthcheck` reads it here instead of
// keeping its own copy.
func ListenAddrFromEnv(getenv func(string) string) string {
	if v := strings.TrimSpace(getenv("LISTEN_ADDR")); v != "" {
		return v
	}
	return DefaultListenAddr
}

// ProbeAddr turns a listen address into one that can be dialed: every
// interface (":port", "0.0.0.0:port", "[::]:port") is probed on loopback. An
// explicit host is returned as given; the healthcheck's client then refuses it
// unless it is loopback.
func ProbeAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	switch host {
	case "", "0.0.0.0":
		return net.JoinHostPort("127.0.0.1", port)
	case "::":
		return net.JoinHostPort("::1", port)
	}
	return listen
}

// Config is the full runtime configuration.
type Config struct {
	ListenAddr     string
	PublicURL      *url.URL
	DatabaseURL    string
	DataDir        string
	AuthMode       AuthMode
	OIDC           OIDC
	SessionTTL     time.Duration
	SessionSecret  string
	TrustedProxies []netip.Prefix
	Bootstrap      Bootstrap
	LogLevel       string
	// SeedDemoData loads the demo organization into a database with no
	// organizations at start-up, and makes anyone who signs in without an
	// organization an Owner of it. For demos and development only.
	SeedDemoData bool
	// SuiteApps are the VBCDX apps the app switcher links to, in order.
	// Empty hides the switcher.
	SuiteApps []SuiteApp
	// GAMeasurementID is the GA4 measurement id for the Keyhole web data
	// stream. Empty disables analytics: the web app loads no tag and
	// the CSP stays strict. Public (it is served to the browser).
	GAMeasurementID string
	// Service is the link with vbcdx-control. Off
	// (ListenAddr empty) unless SERVICE_LISTEN_ADDR is set.
	Service Service
}

// Service configures the forward listener for vbcdx-control and the
// client Keyhole uses to fetch its signing keys. All fields are required
// when ListenAddr is set.
type Service struct {
	ListenAddr        string // SERVICE_LISTEN_ADDR, a loopback host:port
	CertFile          string // SERVICE_CERT_FILE, from `vbcdx-control services add keyhole --cert-out`
	KeyFile           string // SERVICE_KEY_FILE, from `keyhole service csr`
	ControlCAFile     string // CONTROL_CA_FILE, the control CA certificate (PEM)
	ControlURL        string // CONTROL_URL, the tunnel listener: https://127.0.0.1:8443
	ControlServerName string // CONTROL_SERVER_NAME, optional: the name control's server cert carries
	KEKFile           string // KEK_FILE, from `keyhole kek generate`
}

// SecureCookies reports whether cookies get the Secure attribute. They do
// whenever the public URL is https; plain http is only accepted on loopback.
func (c *Config) SecureCookies() bool { return c.PublicURL.Scheme == "https" }

// Origin is the scheme://host[:port] browsers send in the Origin header.
func (c *Config) Origin() string { return c.PublicURL.Scheme + "://" + c.PublicURL.Host }

// FromEnv loads the configuration from environment variables.
func FromEnv() (*Config, error) { return load(os.Getenv) }

func load(getenv func(string) string) (*Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}

	c := &Config{
		// Loopback by default (DefaultListenAddr): a direct `keyhole` run must not serve sign-in
		// over plain http on a routable address. `:8080` or `0.0.0.0:8080`
		// bind every interface for container and reverse-proxy setups.
		ListenAddr:    ListenAddrFromEnv(getenv),
		DataDir:       get("DATA_DIR", "data"),
		AuthMode:      AuthMode(strings.ToLower(get("AUTH_MODE", "local"))),
		SessionSecret: getenv("SESSION_SECRET"),
		LogLevel:      get("LOG_LEVEL", "info"),
	}
	c.DatabaseURL = get("DATABASE_URL", "sqlite://"+c.DataDir+"/keyhole.db")

	var errs []error

	proxies, err := parseTrustedProxies(getenv("TRUSTED_PROXIES"))
	if err != nil {
		errs = append(errs, err)
	}
	c.TrustedProxies = proxies

	switch c.AuthMode {
	case AuthLocal, AuthZitadel, AuthBoth:
	default:
		errs = append(errs, fmt.Errorf("AUTH_MODE must be zitadel, local or both, got %q", c.AuthMode))
	}

	pub, err := url.Parse(get("PUBLIC_URL", DefaultPublicURL))
	switch {
	case err != nil:
		errs = append(errs, errors.New("PUBLIC_URL does not parse as a URL (not shown: it may hold a credential)"))
	case pub.Scheme != "https" && pub.Scheme != "http", pub.Host == "":
		errs = append(errs, fmt.Errorf("PUBLIC_URL must be an absolute http(s) URL, got %s", shownURL(pub)))
	case pub.Scheme == "http" && !isLoopback(pub.Hostname()):
		errs = append(errs, fmt.Errorf("PUBLIC_URL must use https unless the host is localhost, got %s", shownURL(pub)))
	case strings.Trim(pub.Path, "/") != "" || pub.RawQuery != "" || pub.Fragment != "":
		// Every route, including the Zitadel callback, is served from the root.
		errs = append(errs, fmt.Errorf("PUBLIC_URL must be scheme://host[:port] with no path, query or fragment, got %s with one (Keyhole cannot be served under a path)", shownURL(pub)))
	default:
		pub.Path = ""
		c.PublicURL = pub
	}

	ttl, err := time.ParseDuration(get("SESSION_TTL", "24h"))
	if err != nil || ttl < time.Minute {
		errs = append(errs, fmt.Errorf("SESSION_TTL must be a duration of at least 1m, got %q", getenv("SESSION_TTL")))
	}
	c.SessionTTL = ttl

	if c.SessionSecret != "" && len(c.SessionSecret) < 32 {
		errs = append(errs, errors.New("SESSION_SECRET must be at least 32 characters"))
	}

	if c.AuthMode.Zitadel() {
		c.OIDC = OIDC{
			Issuer:       strings.TrimSuffix(getenv("OIDC_ISSUER"), "/"),
			ClientID:     getenv("OIDC_CLIENT_ID"),
			ClientSecret: getenv("OIDC_CLIENT_SECRET"),
			Scopes:       strings.Fields(get("OIDC_SCOPES", "openid profile email")),
		}
		for key, v := range map[string]string{
			"OIDC_ISSUER":        c.OIDC.Issuer,
			"OIDC_CLIENT_ID":     c.OIDC.ClientID,
			"OIDC_CLIENT_SECRET": c.OIDC.ClientSecret,
		} {
			if v == "" {
				errs = append(errs, fmt.Errorf("%s is required when AUTH_MODE is %s", key, c.AuthMode))
			}
		}
		if c.PublicURL != nil {
			// The callback is always served here, and the cookie carrying the
			// sign-in flow is scoped to its path, so the redirect URL is
			// derived rather than configurable. OIDC_REDIRECT_URL may only
			// restate it.
			c.OIDC.RedirectURL = c.PublicURL.String() + "/auth/oidc/callback"
			if v := get("OIDC_REDIRECT_URL", c.OIDC.RedirectURL); v != c.OIDC.RedirectURL {
				// Both URLs by scheme://host only: either may carry a credential.
				got := "a URL that does not parse"
				if gu, err := url.Parse(v); err == nil {
					got = shownURL(gu) + " + its path"
				}
				errs = append(errs, fmt.Errorf("OIDC_REDIRECT_URL must be PUBLIC_URL (%s) + /auth/oidc/callback, got %s; change PUBLIC_URL or unset OIDC_REDIRECT_URL",
					shownURL(c.PublicURL), got))
			}
			c.OIDC.PostLogoutRedirectURL = get("OIDC_POST_LOGOUT_REDIRECT_URL", c.PublicURL.String()+"/")
		}
	}

	c.Bootstrap = Bootstrap{
		Email:    getenv("BOOTSTRAP_ADMIN_EMAIL"),
		Name:     getenv("BOOTSTRAP_ADMIN_NAME"),
		Password: getenv("BOOTSTRAP_ADMIN_PASSWORD"),
	}
	if (c.Bootstrap.Email == "") != (c.Bootstrap.Password == "") {
		errs = append(errs, errors.New("BOOTSTRAP_ADMIN_EMAIL and BOOTSTRAP_ADMIN_PASSWORD must be set together"))
	}

	if v := strings.TrimSpace(getenv("SEED_DEMO_DATA")); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("SEED_DEMO_DATA must be true or false, got %q", v))
		}
		c.SeedDemoData = on
	}
	// With the flag on, every sign-in without an organization is made an
	// Owner of the demo organization, so a deployment reachable beyond this
	// machine would hand Owner to any identity its issuer authenticates.
	// Loopback keeps the grant on the developer's machine.
	if c.SeedDemoData && c.PublicURL != nil && !isLoopback(c.PublicURL.Hostname()) {
		errs = append(errs, fmt.Errorf("SEED_DEMO_DATA=true requires a loopback PUBLIC_URL (localhost, 127.0.0.1 or ::1) because it makes anyone who signs in without an organization an Owner of the demo organization, got %q", c.PublicURL.Hostname()))
	}

	apps, err := parseSuiteApps(getenv("SUITE_APPS"))
	if err != nil {
		errs = append(errs, err)
	}
	c.SuiteApps = apps

	if v := strings.TrimSpace(getenv("GA_MEASUREMENT_ID")); v != "" {
		if !gaMeasurementIDRe.MatchString(v) {
			errs = append(errs, fmt.Errorf("GA_MEASUREMENT_ID must look like a GA4 measurement id (G-XXXXXXXXXX), got %q", v))
		} else {
			c.GAMeasurementID = v
		}
	}

	c.Service = Service{
		ListenAddr:        strings.TrimSpace(getenv("SERVICE_LISTEN_ADDR")),
		CertFile:          getenv("SERVICE_CERT_FILE"),
		KeyFile:           getenv("SERVICE_KEY_FILE"),
		ControlCAFile:     getenv("CONTROL_CA_FILE"),
		ControlURL:        getenv("CONTROL_URL"),
		ControlServerName: getenv("CONTROL_SERVER_NAME"),
		KEKFile:           getenv("KEK_FILE"),
	}
	if sa := c.Service.ListenAddr; sa != "" {
		host, _, err := net.SplitHostPort(sa)
		if err != nil || !isLoopback(host) {
			// vbcdx-control on the same host is the only caller; the forward
			// listener is never on a routable address.
			errs = append(errs, fmt.Errorf("SERVICE_LISTEN_ADDR must be a loopback host:port, got %q", sa))
		}
		for key, v := range map[string]string{
			"SERVICE_CERT_FILE": c.Service.CertFile,
			"SERVICE_KEY_FILE":  c.Service.KeyFile,
			"CONTROL_CA_FILE":   c.Service.ControlCAFile,
			"CONTROL_URL":       c.Service.ControlURL,
			"KEK_FILE":          c.Service.KEKFile,
		} {
			if v == "" {
				errs = append(errs, fmt.Errorf("%s is required when SERVICE_LISTEN_ADDR is set", key))
			}
		}
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return c, nil
}

// shownURL is how an error names a URL from the settings: scheme://host
// only, quoted. Its userinfo, path, query and fragment may hold a credential,
// and the error reaches logs (keyhole check runs as the unit's ExecStartPre).
func shownURL(u *url.URL) string {
	return fmt.Sprintf("%q", u.Scheme+"://"+u.Host)
}

// plainName is a SUITE_APPS name that is safe to echo in an error.
var plainName = regexp.MustCompile(`^[A-Za-z0-9 _.-]+$`)

// gaMeasurementIDRe matches a GA4 web-stream measurement id. Strict on
// purpose: a typo should fail at start-up, not silently send nothing.
var gaMeasurementIDRe = regexp.MustCompile(`^G-[A-Z0-9]{4,20}$`)

// parseSuiteApps reads a comma-separated list of Name=URL pairs. Each URL
// must be an absolute https URL (http only for localhost), so the switcher
// never renders a javascript: or relative link.
func parseSuiteApps(v string) ([]SuiteApp, error) {
	var out []SuiteApp
	for i, pair := range strings.Split(v, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		name, raw, ok := strings.Cut(pair, "=")
		name, raw = strings.TrimSpace(name), strings.TrimSpace(raw)
		if !ok || name == "" || raw == "" {
			return nil, fmt.Errorf("SUITE_APPS: entry %d must be Name=URL", i+1)
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil ||
			!(u.Scheme == "https" || (u.Scheme == "http" && isLoopback(u.Hostname()))) {
			shown := "a URL that does not parse"
			if err == nil {
				shown = shownURL(u)
			}
			// The name is shown only when it is a plain word: text before an
			// "=" inside a credentialed URL (a nameless entry) is never echoed.
			which := fmt.Sprintf("entry %d", i+1)
			if plainName.MatchString(name) {
				which = name + "'s"
			}
			return nil, fmt.Errorf("SUITE_APPS: %s URL must be an absolute https URL (http only for localhost) with no user or password, got %s", which, shown)
		}
		out = append(out, SuiteApp{Name: name, URL: u.String()})
	}
	return out, nil
}

// parseTrustedProxies reads a comma- or space-separated list of addresses
// and CIDR networks.
func parseTrustedProxies(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		p, err := netip.ParsePrefix(f)
		if err != nil {
			a, aerr := netip.ParseAddr(f)
			if aerr != nil || a.Zone() != "" {
				return nil, fmt.Errorf("TRUSTED_PROXIES: %q is not an IP address or CIDR network", f)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Bits() == 0 {
			return nil, fmt.Errorf("TRUSTED_PROXIES: %q would let every client choose its own address", f)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
