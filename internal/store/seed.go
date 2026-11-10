package store

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DemoOrgID is the organization the demo fixture creates. With demo data
// on, a user who signs in and belongs to no organization joins it as an
// Owner.
const DemoOrgID = "org_acme"

//go:embed seed/demo.json
var demoFixture []byte

// Fixture is a set of rows to insert, table by table, in order (parents
// before children). A string value of the form "@now", "@now-20d" or
// "@now+365d-2m" is a time relative to when the fixture is loaded, stored
// as Unix milliseconds like every other timestamp. An object or array
// value is stored as JSON.
type Fixture struct {
	Description string         `json:"description"`
	Tables      []FixtureTable `json:"tables"`
}

// FixtureTable is one table's rows.
type FixtureTable struct {
	Table string           `json:"table"`
	Rows  []map[string]any `json:"rows"`
}

// DemoFixture returns the embedded demo data.
func DemoFixture() (Fixture, error) {
	f, err := ParseFixture(bytes.NewReader(demoFixture))
	if err != nil {
		return Fixture{}, fmt.Errorf("store: demo fixture: %w", err)
	}
	return f, nil
}

// ParseFixture decodes a fixture, refusing unknown fields.
func ParseFixture(r io.Reader) (Fixture, error) {
	var f Fixture
	dec := json.NewDecoder(r)
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Fixture{}, err
	}
	return f, nil
}

// SeedDemo loads the demo fixture. See LoadFixture.
func (s *Store) SeedDemo(ctx context.Context, now time.Time) (int64, error) {
	f, err := DemoFixture()
	if err != nil {
		return 0, err
	}
	return s.LoadFixture(ctx, f, now)
}

// LoadFixture inserts every row of f in one transaction and returns how
// many rows were new. A row whose primary or unique key already exists is
// left exactly as it is, so loading the same fixture twice changes nothing
// the second time.
func (s *Store) LoadFixture(ctx context.Context, f Fixture, now time.Time) (int64, error) {
	var inserted int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		for _, t := range f.Tables {
			if !identifier.MatchString(t.Table) {
				return fmt.Errorf("store: fixture table name %q", t.Table)
			}
			for i, row := range t.Rows {
				// A tool version's privateAllow is checked
				// here, so a bad entry fails the load, not a later call.
				if t.Table == "tool_versions" {
					if err := checkSnapshotPrivateAllow(row["snapshot"]); err != nil {
						return fmt.Errorf("store: fixture %s row %d: %w", t.Table, i, err)
					}
				}
				query, args, err := s.fixtureInsert(t.Table, row, now)
				if err != nil {
					return fmt.Errorf("store: fixture %s row %d: %w", t.Table, i, err)
				}
				res, err := tx.ExecContext(ctx, query, args...)
				if err != nil {
					return fmt.Errorf("store: fixture %s row %d: %w", t.Table, i, err)
				}
				n, err := res.RowsAffected()
				if err != nil {
					return err
				}
				inserted += n
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func (s *Store) fixtureInsert(table string, row map[string]any, now time.Time) (string, []any, error) {
	cols := make([]string, 0, len(row))
	for c := range row {
		if !identifier.MatchString(c) {
			return "", nil, fmt.Errorf("column name %q", c)
		}
		cols = append(cols, c)
	}
	if len(cols) == 0 {
		return "", nil, errors.New("empty row")
	}
	sort.Strings(cols)
	args := make([]any, len(cols))
	for i, c := range cols {
		v, err := fixtureValue(row[c], now)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", c, err)
		}
		args[i] = v
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ")
	query := "INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES (" + marks + ") ON CONFLICT DO NOTHING"
	return s.q(query), args, nil
}

// fixtureValue converts one decoded JSON value to a driver argument.
func fixtureValue(v any, now time.Time) (any, error) {
	switch v := v.(type) {
	case nil, bool:
		return v, nil
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, nil
		}
		return v.Float64()
	case string:
		if strings.HasPrefix(v, "@now") {
			t, err := relativeTime(v, now)
			if err != nil {
				return nil, err
			}
			return millis(t), nil
		}
		return v, nil
	case map[string]any, []any:
		resolved, err := resolveTimes(v, now)
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(resolved)
		if err != nil {
			return nil, err
		}
		return string(b), nil
	default:
		return nil, fmt.Errorf("unsupported value %T", v)
	}
}

// resolveTimes replaces "@now…" strings inside a JSON value with RFC 3339
// times, the format the schema's JSON documents use.
func resolveTimes(v any, now time.Time) (any, error) {
	switch v := v.(type) {
	case string:
		if !strings.HasPrefix(v, "@now") {
			return v, nil
		}
		t, err := relativeTime(v, now)
		if err != nil {
			return nil, err
		}
		return t.UTC().Format(time.RFC3339Nano), nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			r, err := resolveTimes(x, now)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			r, err := resolveTimes(x, now)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

var offsetTerm = regexp.MustCompile(`^([+-])((?:\d+[dhms])+)`)
var offsetUnit = regexp.MustCompile(`(\d+)([dhms])`)

// relativeTime parses "@now" followed by any number of signed offsets in
// days, hours, minutes and seconds: "@now", "@now-2m27s", "@now+365d-1h5m".
func relativeTime(s string, now time.Time) (time.Time, error) {
	rest := strings.TrimPrefix(s, "@now")
	t := now
	for rest != "" {
		m := offsetTerm.FindStringSubmatch(rest)
		if m == nil {
			return time.Time{}, fmt.Errorf("bad relative time %q", s)
		}
		var d time.Duration
		for _, u := range offsetUnit.FindAllStringSubmatch(m[2], -1) {
			n, err := strconv.ParseInt(u[1], 10, 64)
			if err != nil {
				return time.Time{}, fmt.Errorf("bad relative time %q: %w", s, err)
			}
			unit := map[string]time.Duration{"d": 24 * time.Hour, "h": time.Hour, "m": time.Minute, "s": time.Second}[u[2]]
			d += time.Duration(n) * unit
		}
		if m[1] == "-" {
			d = -d
		}
		t = t.Add(d)
		rest = rest[len(m[0]):]
	}
	return t, nil
}

// HasOrgs reports whether any organization exists; SEED_DEMO_DATA only
// seeds a database that has none.
func (s *Store) HasOrgs(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM orgs").Scan(&n)
	return n > 0, err
}

// JoinDemoOrg makes userID an active Owner of the demo organization, but
// only when the demo organization exists and the user belongs to no
// organization yet. It reports whether a membership was added.
func (s *Store) JoinDemoOrg(ctx context.Context, userID string) (bool, error) {
	joined := false
	now := millis(time.Now())
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, s.q("SELECT COUNT(*) FROM orgs WHERE id = ?"), DemoOrgID).Scan(&n); err != nil || n == 0 {
			return err
		}
		if err := tx.QueryRowContext(ctx, s.q("SELECT COUNT(*) FROM org_memberships WHERE user_id = ?"), userID).Scan(&n); err != nil || n > 0 {
			return err
		}
		res, err := tx.ExecContext(ctx, s.q(
			`INSERT INTO org_memberships (id, org_id, user_id, role, status, joined_at, created_at, updated_at,
			                              updated_by_kind, updated_by_id, rev)
			 VALUES (?, ?, ?, 'Owner', 'active', ?, ?, ?, 'system', 'demo_data', 0)
			 ON CONFLICT (org_id, user_id) DO NOTHING`),
			newPrefixedID("om"), DemoOrgID, userID, now, now, now)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		joined = affected > 0
		return err
	})
	return joined, err
}
