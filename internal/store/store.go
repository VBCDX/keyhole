// Package store is Keyhole's system of record for users, identities and
// sessions. One set of queries and one migration set serve both Postgres and
// SQLite; the database is picked by the DATABASE_URL scheme.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	_ "modernc.org/sqlite"             // registers the "sqlite" driver
)

// Dialect is the SQL database behind a Store.
type Dialect string

const (
	Postgres Dialect = "postgres"
	SQLite   Dialect = "sqlite"
)

var (
	// ErrNotFound is returned when a row does not exist (or a session is not
	// live: expired, revoked or idle-expired).
	ErrNotFound = errors.New("store: not found")
	// ErrExists is returned when creating something whose unique key is taken.
	ErrExists = errors.New("store: already exists")
)

// Store wraps a database handle with Keyhole's queries.
type Store struct {
	db      *sql.DB
	dialect Dialect
}

// Open connects to the database named by rawURL and applies pending migrations.
//
//	postgres://user:pass@host:5432/db?sslmode=disable  (or postgresql://)
//	sqlite://data/keyhole.db                          (relative path)
//	sqlite:///var/lib/keyhole/keyhole.db             (absolute path)
//
// For SQLite, before the database is opened, its files (and, with
// WithDataDir, keyhole's own data directory) are checked and any group or
// other permission bits taken off, each with one warning on WithLogger.
// A symlink or another
// user's file refuses to open.
func Open(ctx context.Context, rawURL string, opts ...Option) (*Store, error) {
	var o openOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.log == nil {
		o.log = slog.Default()
	}
	s, err := open(rawURL, o)
	if err != nil {
		return nil, err
	}
	if err := s.db.PingContext(ctx); err != nil {
		s.db.Close()
		return nil, fmt.Errorf("store: connect %s: %w", s.dialect, err)
	}
	if err := s.migrate(ctx); err != nil {
		s.db.Close()
		return nil, err
	}
	return s, nil
}

func open(rawURL string, o openOptions) (*Store, error) {
	switch {
	case strings.HasPrefix(rawURL, "postgres://"), strings.HasPrefix(rawURL, "postgresql://"):
		db, err := sql.Open("pgx", rawURL)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
		return &Store{db: db, dialect: Postgres}, nil
	case strings.HasPrefix(rawURL, "sqlite://"):
		path := strings.TrimPrefix(rawURL, "sqlite://")
		if i := strings.IndexByte(path, '?'); i >= 0 {
			path = path[:i]
		}
		if path == "" {
			return nil, errors.New("store: sqlite URL has no file path")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("store: create sqlite directory: %w", err)
		}
		// A database made by an older release
		// may be readable by others: fix ours, refuse what isn't ours. SQLite
		// then opens the file again by path; a symlink swapped in between
		// isn't covered here, which takes write access to DATA_DIR, the
		// owner's own 0700 directory.
		findings, err := SQLitePermissions(path, o.dataDir, true)
		if err != nil {
			return nil, err
		}
		for _, f := range findings {
			o.log.Warn(f.Message())
		}
		q := url.Values{}
		for _, p := range []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(NORMAL)", "foreign_keys(1)"} {
			q.Add("_pragma", p)
		}
		// BEGIN IMMEDIATE takes the write lock up front, so a transaction that
		// reads then writes waits on busy_timeout instead of failing with
		// SQLITE_BUSY when another writer got there first.
		q.Set("_txlock", "immediate")
		db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
		return &Store{db: db, dialect: SQLite}, nil
	default:
		return nil, errors.New("store: DATABASE_URL must start with postgres://, postgresql:// or sqlite://")
	}
}

// Dialect reports which database is in use.
func (s *Store) Dialect() Dialect { return s.dialect }

// Close closes the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks the database is reachable; used by /readyz.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// q rewrites ? placeholders to $1, $2, … for Postgres. Queries in this
// package never contain a literal question mark.
func (s *Store) q(query string) string {
	if s.dialect != Postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// newPrefixedID returns a schema-style ID: the record's prefix, an
// underscore and 16 random base62 characters ("u_3fK9…").
func newPrefixedID(prefix string) string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(err) // crypto/rand.Read never fails on supported platforms
	}
	b := make([]byte, 0, len(prefix)+1+len(buf))
	b = append(b, prefix...)
	b = append(b, '_')
	for _, c := range buf {
		b = append(b, base62[int(c)%len(base62)])
	}
	return string(b)
}

func millis(t time.Time) int64 { return t.UnixMilli() }

func fromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// User is a Keyhole account.
type User struct {
	ID        string
	Email     string
	Name      string
	CreatedAt time.Time
}

// Identity providers, as the shared auth schema names them. A password
// identity has connection_id ” and subject = the lower-cased email; an OIDC
// identity has connection_id = the issuer URL and subject = the token's "sub".
const (
	ProviderPassword = "password"
	ProviderOIDC     = "oidc"
)

// CountUsers returns how many users exist.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n)
	return n, err
}

// CreateLocalUser creates a user with an email-and-password identity. The
// email must already be normalised (trimmed and lower-cased). It returns
// ErrExists if a local account with that email already exists.
func (s *Store) CreateLocalUser(ctx context.Context, email, name, passwordHash string) (User, error) {
	now := time.Now()
	u := User{ID: newPrefixedID("u"), Email: email, Name: name, CreatedAt: now.UTC()}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, s.q(
			"INSERT INTO users (id, email, email_lower, name, status, created_at, updated_at) VALUES (?, ?, ?, ?, 'active', ?, ?)"),
			u.ID, u.Email, strings.ToLower(u.Email), u.Name, millis(now), millis(now)); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, s.q(
			`INSERT INTO identities (id, user_id, provider, connection_id, subject, email_at_link, password_hash, linked_at)
			 VALUES (?, ?, ?, '', ?, ?, ?, ?)
			 ON CONFLICT (provider, connection_id, subject) DO NOTHING`),
			newPrefixedID("idn"), u.ID, ProviderPassword, email, email, passwordHash, millis(now))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrExists
		}
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// LocalCredentials returns the user ID and password hash for a local account.
func (s *Store) LocalCredentials(ctx context.Context, email string) (userID, passwordHash string, err error) {
	var hash sql.NullString
	err = s.db.QueryRowContext(ctx, s.q(
		"SELECT user_id, password_hash FROM identities WHERE provider = ? AND connection_id = '' AND subject = ?"),
		ProviderPassword, email).Scan(&userID, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	return userID, hash.String, nil
}

// TouchLocalIdentity records a successful local sign-in on the identity and
// the user.
func (s *Store) TouchLocalIdentity(ctx context.Context, email string) error {
	now := millis(time.Now())
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var userID string
		err := tx.QueryRowContext(ctx, s.q(
			"SELECT user_id FROM identities WHERE provider = ? AND connection_id = '' AND subject = ?"),
			ProviderPassword, email).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.q(
			"UPDATE identities SET last_used_at = ? WHERE provider = ? AND connection_id = '' AND subject = ?"),
			now, ProviderPassword, email); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, s.q("UPDATE users SET last_sign_in_at = ? WHERE id = ?"), now, userID)
		return err
	})
}

// UpsertOIDCUser finds the user linked to issuer+subject, creating the user
// and identity on first sign-in. Email and name are refreshed from the
// provider on every sign-in. Users are linked by issuer and subject only,
// never by email: an email match is not proof the same person owns both.
func (s *Store) UpsertOIDCUser(ctx context.Context, issuer, subject, email, name string) (User, error) {
	for attempt := 0; attempt < 2; attempt++ {
		u, err := s.upsertOIDCUser(ctx, issuer, subject, email, name)
		if !errors.Is(err, ErrExists) {
			return u, err
		}
		// A concurrent first sign-in created the identity; the retry finds it.
	}
	return User{}, fmt.Errorf("store: could not link identity %s/%s", issuer, subject)
}

func (s *Store) upsertOIDCUser(ctx context.Context, issuer, subject, email, name string) (User, error) {
	now := time.Now()
	var u User
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var userID string
		err := tx.QueryRowContext(ctx, s.q(
			"SELECT user_id FROM identities WHERE provider = ? AND connection_id = ? AND subject = ?"),
			ProviderOIDC, issuer, subject).Scan(&userID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			userID = newPrefixedID("u")
			if _, err := tx.ExecContext(ctx, s.q(
				`INSERT INTO users (id, email, email_lower, name, status, created_at, updated_at, last_sign_in_at)
				 VALUES (?, ?, ?, ?, 'active', ?, ?, ?)`),
				userID, email, strings.ToLower(email), name, millis(now), millis(now), millis(now)); err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, s.q(
				`INSERT INTO identities (id, user_id, provider, connection_id, subject, email_at_link, linked_at, last_used_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
				 ON CONFLICT (provider, connection_id, subject) DO NOTHING`),
				newPrefixedID("idn"), userID, ProviderOIDC, issuer, subject, nullable(email), millis(now), millis(now))
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n == 0 {
				return ErrExists
			}
		case err != nil:
			return err
		default:
			if _, err := tx.ExecContext(ctx, s.q(
				"UPDATE users SET email = ?, email_lower = ?, name = ?, updated_at = ?, last_sign_in_at = ? WHERE id = ?"),
				email, strings.ToLower(email), name, millis(now), millis(now), userID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, s.q(
				"UPDATE identities SET last_used_at = ? WHERE provider = ? AND connection_id = ? AND subject = ?"),
				millis(now), ProviderOIDC, issuer, subject); err != nil {
				return err
			}
		}
		u, err = getUser(ctx, tx, s, userID)
		return err
	})
	return u, err
}

// GetUser loads a user by ID.
func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	return getUser(ctx, s.db, s, id)
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getUser(ctx context.Context, db queryer, s *Store, id string) (User, error) {
	var u User
	var created int64
	err := db.QueryRowContext(ctx, s.q("SELECT id, email, name, created_at FROM users WHERE id = ?"), id).
		Scan(&u.ID, &u.Email, &u.Name, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	u.CreatedAt = fromMillis(created)
	return u, err
}

// Session is a signed-in browser session.
type Session struct {
	IDHash      string // SHA-256 of the cookie value, hex encoded
	UserID      string
	Method      string // "local" or "zitadel"
	CSRFToken   string
	IDTokenHint string // the OIDC ID token, sent back to the provider on sign-out
	OIDCSID     string // the provider's session ID ("sid"), for back-channel logout
	OIDCSub     string // the provider's subject, for a logout token without a sid
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// CreateSession stores a new session.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.db.ExecContext(ctx, s.q(
		`INSERT INTO sessions (id_hash, user_id, method, amr, csrf_token, id_token_hint, oidc_sid, oidc_sub, created_at, last_seen_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		sess.IDHash, sess.UserID, sess.Method, sessionAMR(sess.Method), sess.CSRFToken, nullable(sess.IDTokenHint),
		nullable(sess.OIDCSID), nullable(sess.OIDCSub),
		millis(sess.CreatedAt), millis(sess.CreatedAt), millis(sess.ExpiresAt))
	return err
}

// sessionAMR is the schema's amr list for a sign-in method: a password for
// local accounts, single sign-on for Zitadel.
func sessionAMR(method string) string {
	if method == "zitadel" {
		return `["sso"]`
	}
	return `["pwd"]`
}

// GetSession loads a live session and its user. Unknown, expired, revoked or
// idle-expired sessions return ErrNotFound. Revocation is enforced here on the
// authentication path itself. ListUserSessions also filters revoked and
// expired rows, but not idle_expires_at: until an idle-expiry writer lands it
// can still list an idle-expired-but-unrevoked session that this query rejects.
func (s *Store) GetSession(ctx context.Context, idHash string, now time.Time) (Session, User, error) {
	var sess Session
	var u User
	var hint, sid, sub sql.NullString
	var created, expires, userCreated int64
	err := s.db.QueryRowContext(ctx, s.q(
		`SELECT s.id_hash, s.user_id, s.method, s.csrf_token, s.id_token_hint, s.oidc_sid, s.oidc_sub, s.created_at, s.expires_at,
		        u.id, u.email, u.name, u.created_at
		   FROM sessions s JOIN users u ON u.id = s.user_id
		  WHERE s.id_hash = ? AND s.expires_at > ?
		    AND s.revoked_at IS NULL
		    AND (s.idle_expires_at IS NULL OR s.idle_expires_at > ?)`), idHash, millis(now), millis(now)).
		Scan(&sess.IDHash, &sess.UserID, &sess.Method, &sess.CSRFToken, &hint, &sid, &sub, &created, &expires,
			&u.ID, &u.Email, &u.Name, &userCreated)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, User{}, ErrNotFound
	}
	if err != nil {
		return Session{}, User{}, err
	}
	sess.IDTokenHint, sess.OIDCSID, sess.OIDCSub = hint.String, sid.String, sub.String
	sess.CreatedAt, sess.ExpiresAt, u.CreatedAt = fromMillis(created), fromMillis(expires), fromMillis(userCreated)
	return sess, u, nil
}

// DeleteSession removes a session; deleting a missing session is not an error.
func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.db.ExecContext(ctx, s.q("DELETE FROM sessions WHERE id_hash = ?"), idHash)
	return err
}

// ErrReplayedLogout is returned for a logout token whose jti was used before.
var ErrReplayedLogout = errors.New("store: logout token already used")

// EndOIDCSessions applies a validated back-channel logout token in one
// transaction: it records the token's jti (kept until expires) and removes
// the Zitadel sessions the token ends, those with its sid or, when it has
// no sid, every one for its subject. A jti seen before returns
// ErrReplayedLogout and removes nothing. It returns how many sessions went.
func (s *Store) EndOIDCSessions(ctx context.Context, jti string, expires time.Time, sid, sub string) (int64, error) {
	if jti == "" || (sid == "" && sub == "") {
		return 0, errors.New("store: EndOIDCSessions needs a jti and a sid or subject")
	}
	var n int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, s.q(
			"INSERT INTO oidc_logout_jtis (jti, expires_at) VALUES (?, ?) ON CONFLICT (jti) DO NOTHING"),
			jti, millis(expires))
		if err != nil {
			return err
		}
		if added, err := res.RowsAffected(); err != nil {
			return err
		} else if added != 1 {
			return ErrReplayedLogout
		}
		if sid != "" {
			res, err = tx.ExecContext(ctx, s.q("DELETE FROM sessions WHERE method = 'zitadel' AND oidc_sid = ?"), sid)
		} else {
			res, err = tx.ExecContext(ctx, s.q("DELETE FROM sessions WHERE method = 'zitadel' AND oidc_sub = ?"), sub)
		}
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// DeleteExpiredLogoutJTIs forgets logout token IDs whose tokens expired
// before now; such a token would be refused as expired anyway.
func (s *Store) DeleteExpiredLogoutJTIs(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, s.q("DELETE FROM oidc_logout_jtis WHERE expires_at <= ?"), millis(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteExpiredSessions removes sessions that expired before now.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, s.q("DELETE FROM sessions WHERE expires_at <= ?"), millis(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
