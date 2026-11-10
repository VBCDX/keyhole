package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// backchannelLogoutEvent is the member the "events" claim of a logout
// token must have (OpenID Connect Back-Channel Logout 1.0, section 2.4).
const backchannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"

// logoutClockSkew is how far in the future a logout token's iat may be.
const logoutClockSkew = time.Minute

// ErrLogoutToken is wrapped by every reason a logout token is refused.
var ErrLogoutToken = errors.New("auth: invalid logout token")

// LogoutToken is a validated back-channel logout token. At least one of
// SID and Subject is set.
type LogoutToken struct {
	Subject   string
	SID       string
	JTI       string
	ExpiresAt time.Time
}

// VerifyLogoutToken validates a back-channel logout token as
// https://openid.net/specs/openid-connect-backchannel-1_0.html#Validation
// and https://zitadel.com/docs/guides/integrate/back-channel-logout ask:
// signature against the provider's keys, issuer, audience containing our
// client ID, exp and iat, sub and/or sid, the back-channel logout event,
// no nonce, and a jti. Remembering the jti to refuse a replay is the
// caller's job.
//
// A provider that cannot be reached returns an error that does not wrap
// ErrLogoutToken, so the caller can answer 503 rather than 400.
func (o *OIDC) VerifyLogoutToken(ctx context.Context, raw string) (LogoutToken, error) {
	if err := o.ensure(ctx); err != nil {
		return LogoutToken{}, err
	}
	bad := func(format string, args ...any) (LogoutToken, error) {
		return LogoutToken{}, fmt.Errorf("%w: %s", ErrLogoutToken, fmt.Sprintf(format, args...))
	}
	if raw == "" {
		return bad("no logout_token")
	}
	// The ID token verifier checks the signature, iss, aud and exp; none of
	// those differ between an ID token and a logout token.
	tok, err := o.verifier.Verify(oidc.ClientContext(ctx, o.client), raw)
	if err != nil {
		return bad("%v", err)
	}
	var c struct {
		Subject string                     `json:"sub"`
		SID     string                     `json:"sid"`
		JTI     string                     `json:"jti"`
		IAT     *json.Number               `json:"iat"`
		Events  map[string]json.RawMessage `json:"events"`
		Nonce   *json.RawMessage           `json:"nonce"`
	}
	if err := tok.Claims(&c); err != nil {
		return bad("claims: %v", err)
	}
	if c.Nonce != nil {
		return bad("has a nonce claim")
	}
	ev, ok := c.Events[backchannelLogoutEvent]
	var evObj map[string]json.RawMessage
	if !ok || json.Unmarshal(ev, &evObj) != nil || evObj == nil {
		return bad("events claim lacks the back-channel logout event")
	}
	if c.IAT == nil {
		return bad("no iat")
	}
	iat, err := c.IAT.Float64()
	if err != nil {
		return bad("iat is not a number")
	}
	if time.Unix(int64(iat), 0).After(time.Now().Add(logoutClockSkew)) {
		return bad("iat is in the future")
	}
	if c.SID == "" && tok.Subject == "" {
		return bad("neither sid nor sub")
	}
	if c.JTI == "" {
		return bad("no jti")
	}
	return LogoutToken{Subject: tok.Subject, SID: c.SID, JTI: c.JTI, ExpiresAt: tok.Expiry}, nil
}
