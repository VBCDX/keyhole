package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// ErrBadPrivateAllow is returned for a privateAllow entry that isn't an IP
// address or CIDR, allows everything (/0) or has host bits set.
var ErrBadPrivateAllow = errors.New("store: a privateAllow entry is an IP address or a CIDR (not /0, no host bits set)")

// The ranges an entry may never name or overlap. The sidecar never reaches
// them whatever the list says, so naming
// one is a mistake to refuse at the source.
var neverPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),      // unspecified, "this network"
	netip.MustParsePrefix("169.254.0.0/16"), // link-local, cloud metadata
	netip.MustParsePrefix("224.0.0.0/4"),    // multicast
	netip.MustParsePrefix("240.0.0.0/4"),    // reserved, broadcast
	netip.MustParsePrefix("::/128"),         // unspecified
	netip.MustParsePrefix("fe80::/10"),      // link-local
	netip.MustParsePrefix("ff00::/8"),       // multicast
}

// The IPv6 ranges that carry an IPv4 address. The sidecar judges an
// address in them by that IPv4 address, so an entry written in them would
// mean something other than it says: the admin writes
// the IPv4 form instead.
var embeddingPrefixes = []netip.Prefix{
	netip.MustParsePrefix("::ffff:0:0/96"),  // IPv4-mapped
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("2002::/16"),      // 6to4
}

// The widest entry allowed: two halves of /1 would otherwise rebuild the /0
// the ruling forbids.
const minBitsV4, minBitsV6 = 8, 16

// NormalizePrivateAllow checks a tool's privateAllow: the
// private addresses the sidecar may let this tool reach. Each entry is an
// IP address (a /32 or /128) or a CIDR, in the form the sidecar reads
// exactly as written:
//   - a host name is refused: a name doesn't pin the address it resolves to;
//   - an address or CIDR in 6to4, NAT64 or IPv4-mapped space is refused
//     (write the IPv4 form), except that a bare IPv4-mapped address is
//     taken as its IPv4 address;
//   - a zone id, host bits, a prefix wider than /8 (IPv4) or /16 (IPv6),
//     and a range overlapping link-local, unspecified, multicast or
//     reserved space are refused.
//
// The result is canonical (an address is its /32 or /128), sorted and
// without duplicates.
func NormalizePrivateAllow(entries []string) ([]string, error) {
	out := []string{}
	for _, e := range entries {
		bad := func(why string) error { return fmt.Errorf("%w: %q %s", ErrBadPrivateAllow, e, why) }
		var p netip.Prefix
		if a, err := netip.ParseAddr(e); err == nil {
			if a.Zone() != "" {
				return nil, bad("has a zone id")
			}
			a = a.Unmap()
			p = netip.PrefixFrom(a, a.BitLen())
		} else if q, err := netip.ParsePrefix(e); err == nil {
			p = q
		} else {
			return nil, bad("is not an IP address or CIDR")
		}
		// First: a /0 overlaps everything, so it gets its own message rather
		// than whichever check below it happens to trip.
		if p.Bits() == 0 {
			return nil, bad("allows every address")
		}
		for _, em := range embeddingPrefixes {
			if em.Overlaps(p) {
				return nil, bad("is in IPv6 space that carries an IPv4 address (6to4, NAT64 or IPv4-mapped): write the IPv4 address or CIDR")
			}
		}
		if (p.Addr().Is4() && p.Bits() < minBitsV4) || (p.Addr().Is6() && p.Bits() < minBitsV6) {
			return nil, bad(fmt.Sprintf("is wider than /%d (IPv4) or /%d (IPv6)", minBitsV4, minBitsV6))
		}
		if p.Masked() != p {
			return nil, bad(fmt.Sprintf("has host bits set (did you mean %s?)", p.Masked()))
		}
		for _, n := range neverPrefixes {
			if n.Overlaps(p) {
				return nil, bad(fmt.Sprintf("overlaps %s, which a sidecar never reaches", n))
			}
		}
		out = append(out, p.String())
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// checkSnapshotPrivateAllow checks a tool_versions fixture row's snapshot:
// its privateAllow, when present, must be a list that normalizes unchanged.
func checkSnapshotPrivateAllow(snapshot any) error {
	var raw []byte
	switch s := snapshot.(type) {
	case string:
		raw = []byte(s)
	default:
		b, err := json.Marshal(s)
		if err != nil {
			return err
		}
		raw = b
	}
	var snap struct {
		PrivateAllow *[]string `json:"privateAllow"`
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	if snap.PrivateAllow == nil {
		return nil
	}
	norm, err := NormalizePrivateAllow(*snap.PrivateAllow)
	if err != nil {
		return err
	}
	if !slices.Equal(norm, *snap.PrivateAllow) {
		return fmt.Errorf("%w: write it as %q (canonical, sorted, no duplicates)", ErrBadPrivateAllow, norm)
	}
	return nil
}

// PublishedPrivateAllow is the result of SetToolPrivateAllow.
type PublishedPrivateAllow struct {
	Version      int      // the tool's published version after the call
	PrivateAllow []string // its privateAllow, canonical
	Changed      bool     // whether a new version was published
	Pinned       int      // grants pinned to an older version, which keep it
}

// SetToolPrivateAllow sets the private addresses a published tool may reach.
// A tool version is immutable, so a change publishes a new
// version: the current published snapshot with privateAllow replaced. The
// tool's published_version moves to it, and an admin audit row records it.
// Grants that follow the published version get it at their next resolve;
// grants pinned to a version keep theirs (Pinned counts them). Setting the
// list it already has changes nothing. An unknown, deleted or unpublished
// tool, or one in another org, is ErrNotFound.
func (s *Store) SetToolPrivateAllow(ctx context.Context, orgID, toolID string, entries []string, actorID string, now time.Time) (PublishedPrivateAllow, error) {
	allow, err := NormalizePrivateAllow(entries)
	if err != nil {
		return PublishedPrivateAllow{}, err
	}
	var res PublishedPrivateAllow
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var version, retention int
		var snapshot []byte
		err := tx.QueryRowContext(ctx, s.q(`SELECT t.published_version, tv.snapshot, o.audit_retention_days
			FROM tools t
			JOIN tool_versions tv ON tv.org_id = t.org_id AND tv.tool_id = t.id AND tv.version = t.published_version
			JOIN orgs o ON o.id = t.org_id
			WHERE t.org_id = ? AND t.id = ? AND t.status = 'published' AND t.deleted_at IS NULL AND o.status = 'active'`),
			orgID, toolID).Scan(&version, &snapshot, &retention)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var snap map[string]any
		if err := json.Unmarshal(snapshot, &snap); err != nil {
			return fmt.Errorf("store: tool %s version %d snapshot: %w", toolID, version, err)
		}
		// encoding/json matches keys case-insensitively when resolve decodes
		// the snapshot, so any spelling of the key is the field: read it, and
		// drop every spelling before writing the new one.
		var current []string
		for k, v := range snap {
			if !strings.EqualFold(k, "privateAllow") {
				continue
			}
			if raw, ok := v.([]any); ok {
				for _, e := range raw {
					if str, ok := e.(string); ok {
						current = append(current, str)
					}
				}
			}
			delete(snap, k)
		}
		if current == nil {
			current = []string{}
		}
		if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM grants
			WHERE org_id = ? AND tool_id = ? AND pinned_version IS NOT NULL`),
			orgID, toolID).Scan(&res.Pinned); err != nil {
			return err
		}
		if slices.Equal(current, allow) {
			res.Version, res.PrivateAllow = version, allow
			return nil
		}
		snap["privateAllow"] = allow
		next, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		changes, err := json.Marshal(map[string]any{"privateAllow": map[string]any{"from": current, "to": allow}})
		if err != nil {
			return err
		}
		at := millis(now)
		// The next version after every one that exists, published or not.
		var newVersion int
		if err := tx.QueryRowContext(ctx, s.q(`SELECT COALESCE(MAX(version), 0) + 1 FROM tool_versions WHERE org_id = ? AND tool_id = ?`),
			orgID, toolID).Scan(&newVersion); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO tool_versions
			(id, org_id, tool_id, version, snapshot, changes, published_at, published_by_kind, published_by_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'system', ?)`),
			fmt.Sprintf("%s@%d", toolID, newVersion), orgID, toolID, newVersion, string(next), string(changes), at, actorID); err != nil {
			return err
		}
		// The UPDATE is the guard: two concurrent changes can't both move
		// the same version (the second finds published_version moved, and
		// the version's UNIQUE refuses its insert anyway).
		r, err := tx.ExecContext(ctx, s.q(`UPDATE tools SET published_version = ?, updated_at = ?, updated_by_kind = 'system', updated_by_id = ?, rev = rev + 1
			WHERE org_id = ? AND id = ? AND published_version = ?`), newVersion, at, actorID, orgID, toolID, version)
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return fmt.Errorf("store: tool %s changed while publishing; try again", toolID)
		}
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO audit_events
			(id, org_id, workspace_id, app, shared, type, severity, at, actor_kind, actor_id, actor_label,
			 subject_kind, subject_id, subject_label, object, result, trk, expire_at)
			VALUES (?, ?, NULL, 'keyhole', FALSE, 'admin', 'ok', ?, 'system', ?, ?, 'tool', ?, ?, ?, 'tool version published', ?, ?)`),
			newPrefixedID("ev"), orgID, at, actorID, actorID, toolID, toolID,
			fmt.Sprintf("version %d: privateAllow %v", newVersion, allow), newPrefixedID("trk"), millis(now.AddDate(0, 0, retention))); err != nil {
			return err
		}
		res = PublishedPrivateAllow{Version: newVersion, PrivateAllow: allow, Changed: true, Pinned: res.Pinned}
		return nil
	})
	return res, err
}
