package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The scanners below read the portable column types (Unix-millisecond
// BIGINTs, nullable TEXT, BOOLEAN stored as 0/1 on SQLite, JSON as TEXT on
// SQLite and JSONB on Postgres) into plain Go values.

type rowScanner interface{ Scan(dest ...any) error }

// list runs query and scans every row with scan.
func list[T any](ctx context.Context, s *Store, scan func(rowScanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := s.db.QueryContext(ctx, s.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// one runs query and scans its single row; no row is ErrNotFound.
func one[T any](ctx context.Context, s *Store, scan func(rowScanner) (T, error), query string, args ...any) (T, error) {
	v, err := scan(s.db.QueryRowContext(ctx, s.q(query), args...))
	if errors.Is(err, sql.ErrNoRows) {
		var zero T
		return zero, ErrNotFound
	}
	return v, err
}

// at scans a NOT NULL timestamp.
type at struct{ p *time.Time }

func (a at) Scan(src any) error {
	ms, ok := src.(int64)
	if !ok {
		return fmt.Errorf("store: timestamp is %T, want int64", src)
	}
	*a.p = fromMillis(ms)
	return nil
}

// atOrNil scans a nullable timestamp into a *time.Time.
type atOrNil struct{ p **time.Time }

func (a atOrNil) Scan(src any) error {
	if src == nil {
		*a.p = nil
		return nil
	}
	ms, ok := src.(int64)
	if !ok {
		return fmt.Errorf("store: timestamp is %T, want int64", src)
	}
	t := fromMillis(ms)
	*a.p = &t
	return nil
}

// text scans a nullable string; NULL becomes "".
type text struct{ p *string }

func (t text) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*t.p = ""
	case string:
		*t.p = v
	case []byte:
		*t.p = string(v)
	default:
		return fmt.Errorf("store: text is %T", src)
	}
	return nil
}

// intOrNil scans a nullable integer into an *int.
type intOrNil struct{ p **int }

func (n intOrNil) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*n.p = nil
	case int64:
		i := int(v)
		*n.p = &i
	default:
		return fmt.Errorf("store: integer is %T", src)
	}
	return nil
}

// boolean scans a BOOLEAN, which SQLite returns as an integer.
type boolean struct{ p *bool }

func (b boolean) Scan(src any) error {
	switch v := src.(type) {
	case bool:
		*b.p = v
	case int64:
		*b.p = v != 0
	default:
		return fmt.Errorf("store: boolean is %T", src)
	}
	return nil
}

// rawJSON scans a nullable JSON column as raw JSON; NULL stays nil.
type rawJSON struct{ p *json.RawMessage }

func (j rawJSON) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j.p = nil
	case string:
		*j.p = json.RawMessage(v)
	case []byte:
		*j.p = append(json.RawMessage(nil), v...)
	default:
		return fmt.Errorf("store: json is %T", src)
	}
	return nil
}

// jsonInto decodes a nullable JSON column into v; NULL leaves v unchanged.
type jsonInto struct{ v any }

func (j jsonInto) Scan(src any) error {
	var raw json.RawMessage
	if err := (rawJSON{&raw}).Scan(src); err != nil || raw == nil {
		return err
	}
	return json.Unmarshal(raw, j.v)
}

// nullBool scans a nullable BOOLEAN into a *bool.
type nullBool struct{ p **bool }

func (n nullBool) Scan(src any) error {
	if src == nil {
		*n.p = nil
		return nil
	}
	var b bool
	if err := (boolean{&b}).Scan(src); err != nil {
		return err
	}
	*n.p = &b
	return nil
}
