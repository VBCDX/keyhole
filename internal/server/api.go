package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/VBCDX/keyhole/internal/store"
)

// The read API under /api/v1/. Every route needs a session, and every
// organization-scoped route first checks that the caller is an active
// member of the organization in the path. An organization they are not in,
// one that does not exist and one they are suspended from all answer 404
// the same way, so an ID from another organization reveals nothing. Records
// are then read with that organization's ID, so a store ID from another
// organization is a 404 too.
//
// Responses carry what the screens show and nothing secret: no store
// credentials or secret references, no key values, addresses or lengths.

func (s *Server) apiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/orgs", s.requireSession(s.handleListOrgs))
	mux.HandleFunc("GET /api/v1/orgs/{orgID}", s.requireSession(s.inOrg(s.handleGetOrg)))
	mux.HandleFunc("GET /api/v1/orgs/{orgID}/stores", s.requireSession(s.inOrg(s.handleListStores)))
	mux.HandleFunc("GET /api/v1/orgs/{orgID}/stores/{storeID}", s.requireSession(s.inOrg(s.handleGetStore)))
}

type orgKey struct{}

// inOrg resolves the {orgID} path value to the caller's active membership,
// or answers 404.
func (s *Server) inOrg(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := fromContext(r.Context())
		m, err := s.store.GetUserOrg(r.Context(), a.user.ID, r.PathValue("orgID"))
		if errors.Is(err, store.ErrNotFound) || (err == nil && m.Status != "active") {
			writeError(w, http.StatusNotFound, "not_found", "no such organization")
			return
		}
		if err != nil {
			s.internalError(w, "load membership", err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), orgKey{}, m)))
	}
}

func orgFrom(r *http.Request) store.UserOrg {
	m, _ := r.Context().Value(orgKey{}).(store.UserOrg)
	return m
}

func (s *Server) internalError(w http.ResponseWriter, what string, err error) {
	s.log.Error(what+" failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", what+" failed")
}

// ---------------------------------------------------------------------------
// Organizations.

type orgJSON struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Role     string     `json:"role"`   // Owner, userAdmin or user
	Status   string     `json:"status"` // the caller's membership: active, invited or suspended
	JoinedAt *time.Time `json:"joined_at"`
}

func newOrgJSON(m store.UserOrg) orgJSON {
	return orgJSON{ID: m.Org.ID, Name: m.Org.Name, Role: m.Role, Status: m.Status, JoinedAt: utcOrNil(m.JoinedAt)}
}

// handleListOrgs lists every organization the caller has a membership in,
// suspended ones included, so the switcher can say so.
func (s *Server) handleListOrgs(w http.ResponseWriter, r *http.Request) {
	ms, err := s.store.ListUserOrgs(r.Context(), fromContext(r.Context()).user.ID)
	if err != nil {
		s.internalError(w, "list organizations", err)
		return
	}
	out := make([]orgJSON, len(ms))
	for i, m := range ms {
		out[i] = newOrgJSON(m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"orgs": out})
}

func (s *Server) handleGetOrg(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, newOrgJSON(orgFrom(r)))
}

// ---------------------------------------------------------------------------
// Stores.

type healthJSON struct {
	Status    string    `json:"status"` // healthy, degraded, sealed, denied, unreachable or unknown
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	CheckedAt time.Time `json:"checked_at"`
}

type connectorRefJSON struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`        // active or revoked
	HealthStatus string `json:"health_status"` // healthy, degraded or offline
}

type storeJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"` // local or openbao
	Status   string `json:"status"`
	// RouteMode is direct, connector or internal (the Local store).
	RouteMode string `json:"route_mode"`
	// RouteConnectorID is set when RouteMode is connector. RouteConnector
	// is null when that connector no longer exists.
	RouteConnectorID string            `json:"route_connector_id,omitempty"`
	RouteConnector   *connectorRefJSON `json:"route_connector"`
	NetworkExposure  string            `json:"network_exposure"`
	Health           healthJSON        `json:"health"`
	SkipTLSVerify    bool              `json:"skip_tls_verify"`
	KeyCount         int               `json:"key_count"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	RemovedAt        *time.Time        `json:"removed_at"`
}

// storeDetailJSON adds the connection settings the detail page shows.
// Credentials (the AppRole role and secret IDs, tokens) stay out.
type storeDetailJSON struct {
	storeJSON
	Address                string               `json:"address"`
	Namespace              string               `json:"namespace"`
	TLSServerName          string               `json:"tls_server_name"`
	CABundleName           string               `json:"ca_bundle_name"`
	KVMount                string               `json:"kv_mount"`
	KVPathPrefix           string               `json:"kv_path_prefix"`
	AuthMethod             string               `json:"auth_method"`
	CacheDefaultTTLSeconds int                  `json:"cache_default_ttl_seconds"`
	Keys                   []keyJSON            `json:"keys"`
	ConnectionTests        []connectionTestJSON `json:"connection_tests"`
}

type cabinetRefJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type keyJSON struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Status    string          `json:"status"` // active or source_removed
	Cabinet   *cabinetRefJSON `json:"cabinet"`
	Pinned    bool            `json:"pinned"`
	UsedBy    int             `json:"used_by"` // tool slots the key fills
	CreatedAt time.Time       `json:"created_at"`
	RotatedAt *time.Time      `json:"rotated_at"`
	ExpiresAt *time.Time      `json:"expires_at"`
}

type connectionTestJSON struct {
	ID         string     `json:"id"`
	Trigger    string     `json:"trigger"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	OK         bool       `json:"ok"`
	HealthCode string     `json:"health_code"`
}

// connectionTestLimit is how many recent connection tests the detail page
// gets.
const connectionTestLimit = 5

func newStoreJSON(st store.SecretStore, connectors map[string]store.Connector) storeJSON {
	out := storeJSON{
		ID: st.ID, Name: st.Name, Provider: st.Provider, Status: st.Status,
		RouteMode: st.RouteMode, RouteConnectorID: st.RouteConnectorID, NetworkExposure: st.NetworkExposure,
		Health: healthJSON{
			Status: st.Health.Status, Code: st.Health.Code, Message: st.Health.Message,
			CheckedAt: st.Health.CheckedAt.UTC(),
		},
		SkipTLSVerify: st.SkipTLSVerify, KeyCount: st.KeyCount,
		CreatedAt: st.CreatedAt.UTC(), UpdatedAt: st.UpdatedAt.UTC(), RemovedAt: utcOrNil(st.RemovedAt),
	}
	if c, ok := connectors[st.RouteConnectorID]; ok && st.RouteConnectorID != "" {
		out.RouteConnector = &connectorRefJSON{ID: c.ID, Name: c.Name, Status: c.Status, HealthStatus: c.Health.Status}
	}
	return out
}

func (s *Server) connectorsByID(r *http.Request, orgID string) (map[string]store.Connector, error) {
	cs, err := s.store.ListConnectors(r.Context(), orgID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]store.Connector, len(cs))
	for _, c := range cs {
		out[c.ID] = c
	}
	return out, nil
}

// handleListStores lists the organization's stores, plus the keys whose
// store was removed (the "Keys needing attention" list).
func (s *Server) handleListStores(w http.ResponseWriter, r *http.Request) {
	ctx, orgID := r.Context(), orgFrom(r).Org.ID
	sts, err := s.store.ListStores(ctx, orgID)
	if err != nil {
		s.internalError(w, "list stores", err)
		return
	}
	connectors, err := s.connectorsByID(r, orgID)
	if err != nil {
		s.internalError(w, "list connectors", err)
		return
	}
	stores := make([]storeJSON, len(sts))
	for i, st := range sts {
		stores[i] = newStoreJSON(st, connectors)
	}
	keys, err := s.store.ListKeys(ctx, orgID)
	if err != nil {
		s.internalError(w, "list keys", err)
		return
	}
	var removed []store.Key
	for _, k := range keys {
		if k.Status == "source_removed" {
			removed = append(removed, k)
		}
	}
	orphans, err := s.keysJSON(r, orgID, removed)
	if err != nil {
		s.internalError(w, "list keys", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stores": stores, "source_removed_keys": orphans})
}

func (s *Server) handleGetStore(w http.ResponseWriter, r *http.Request) {
	ctx, orgID := r.Context(), orgFrom(r).Org.ID
	st, err := s.store.GetStore(ctx, orgID, r.PathValue("storeID"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no such store")
		return
	}
	if err != nil {
		s.internalError(w, "load store", err)
		return
	}
	connectors, err := s.connectorsByID(r, orgID)
	if err != nil {
		s.internalError(w, "list connectors", err)
		return
	}
	keys, err := s.store.ListStoreKeys(ctx, orgID, st.ID)
	if err != nil {
		s.internalError(w, "list keys", err)
		return
	}
	kj, err := s.keysJSON(r, orgID, keys)
	if err != nil {
		s.internalError(w, "list keys", err)
		return
	}
	tests, err := s.store.ListConnectionTests(ctx, orgID, st.ID, connectionTestLimit)
	if err != nil {
		s.internalError(w, "list connection tests", err)
		return
	}
	tj := make([]connectionTestJSON, len(tests))
	for i, t := range tests {
		tj[i] = connectionTestJSON{
			ID: t.ID, Trigger: t.Trigger, StartedAt: t.StartedAt.UTC(), FinishedAt: utcOrNil(t.FinishedAt),
			OK: t.OK, HealthCode: t.HealthCode,
		}
	}
	writeJSON(w, http.StatusOK, storeDetailJSON{
		storeJSON: newStoreJSON(st, connectors),
		Address:   st.Address, Namespace: st.Namespace, TLSServerName: st.TLSServerName,
		CABundleName: st.CABundleName, KVMount: st.KVMount, KVPathPrefix: st.KVPathPrefix,
		AuthMethod: st.AuthMethod, CacheDefaultTTLSeconds: st.CacheDefaultTTLSeconds,
		Keys: kj, ConnectionTests: tj,
	})
}

// keysJSON shapes keys for the API, with their cabinet's name and how many
// tool slots each fills.
func (s *Server) keysJSON(r *http.Request, orgID string, keys []store.Key) ([]keyJSON, error) {
	out := make([]keyJSON, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	usage, err := s.store.KeyUsageCounts(r.Context(), orgID)
	if err != nil {
		return nil, err
	}
	cabinets := map[string]*cabinetRefJSON{}
	for i, k := range keys {
		out[i] = keyJSON{
			ID: k.ID, Name: k.Name, Status: k.Status, Pinned: k.Pinned, UsedBy: usage[k.ID],
			CreatedAt: k.CreatedAt.UTC(), RotatedAt: utcOrNil(k.RotatedAt), ExpiresAt: utcOrNil(k.ExpiresAt),
		}
		if k.CabinetID == "" {
			continue
		}
		cab, seen := cabinets[k.CabinetID]
		if !seen {
			c, err := s.store.GetCabinet(r.Context(), orgID, k.CabinetID)
			if err != nil {
				return nil, err
			}
			cab = &cabinetRefJSON{ID: c.ID, Name: c.Name}
			cabinets[k.CabinetID] = cab
		}
		out[i].Cabinet = cab
	}
	return out, nil
}

func utcOrNil(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
