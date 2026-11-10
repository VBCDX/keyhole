package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/VBCDX/keyhole/internal/assertion"
	"github.com/VBCDX/keyhole/internal/seal"
	"github.com/VBCDX/keyhole/internal/store"
)

// Resolver serves vbcdx-control's forwards of a sidecar's resolve.
// It is its own handler, served only on the forward
// listener (mTLS, vbcdx:control's certificate required), never on the web
// listener: a value is reachable only through the tunnel, and only sealed.
type Resolver struct {
	Store *store.Store
	// Stores has the store types values come from, the Local store among
	// them; resolve knows none of them in particular.
	Stores   *store.StoreTypes
	Verifier *assertion.Verifier
	Log      *slog.Logger
	Now      func() time.Time
}

// AssertionHeader is the header vbcdx-control's edge attaches.
const AssertionHeader = "Vbcdx-Assertion"

type resolveRequest struct {
	Tool   string `json:"tool"`
	Slot   string `json:"slot"`
	EphPub string `json:"eph_pub"`
}

type resolveReply struct {
	Sealed seal.Sealed `json:"sealed"`
	// JTI is the assertion's jti, which the seal's AAD binds. vbcdx-control
	// mints it per forward and doesn't return it to the sidecar, so the
	// reply carries it; it isn't secret.
	JTI  string    `json:"jti"`
	TTL  int       `json:"ttl"`
	Tool replyTool `json:"tool"`
}

// replyTool is what the sidecar needs to make the call from the agent's
// host (P4): where, which calls are allowed, and where the value goes. It
// comes from the tool version the grant uses. No secret is in it.
type replyTool struct {
	Version   int           `json:"version"`
	BaseURL   string        `json:"base_url"`
	Actions   []replyAction `json:"actions"`
	Slot      replySlot     `json:"slot"`
	TimeoutMs int           `json:"timeout_ms"`
	// The private addresses the sidecar may let this tool reach;
	// [] when none.
	PrivateAllow []string `json:"private_allow"`
}

type replyAction struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type replySlot struct {
	Location string `json:"location"`
	Field    string `json:"field"`
	Scheme   string `json:"scheme"`
}

// Handler routes POST /orgs/{orgID}/resolve, the tail vbcdx-control
// forwards as /control/v1/keyhole/orgs/{orgID}/resolve, scoping the org
// before it sends.
func (rv *Resolver) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /orgs/{orgID}/resolve", rv.resolve)
	return mux
}

func (rv *Resolver) resolve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := rv.Now()
	org := r.PathValue("orgID")
	jws := r.Header.Get(AssertionHeader)
	if jws == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "a Vbcdx-Assertion is required")
		return
	}
	claims, err := rv.Verifier.Verify(ctx, jws, now.Unix())
	if err != nil {
		rv.Log.Warn("resolve: assertion refused", "reason", err.Error())
		writeError(w, http.StatusUnauthorized, "unauthorized", "the assertion was refused")
		return
	}
	var req resolveRequest
	if err := readJSON(w, r, &req); err != nil || req.Tool == "" || req.Slot == "" || req.EphPub == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "the body must be {tool, slot, eph_pub}")
		return
	}
	a := store.ResolveAudit{OrgID: org, ActorKind: claims.Kind, ActorID: claims.Sub, SidecarID: claims.Sidecar,
		ToolID: req.Tool, Slot: req.Slot, RequestID: claims.JTI}
	refuse := func(status int, code, result string) {
		a.Result = result
		rv.audit(ctx, a, now)
		writeError(w, status, code, "the value can't be resolved")
	}
	// Rule 3: every request comes through a sidecar. The org is the one
	// vbcdx-control scoped and asserted, and it must be the one in the path.
	switch {
	case claims.Sidecar == "":
		refuse(http.StatusForbidden, "forbidden", "refused: no sidecar")
		return
	case claims.Org == nil || claims.Org.ID != org:
		refuse(http.StatusForbidden, "forbidden", "refused: org not asserted")
		return
	}
	slot, err := rv.Store.ResolveSlot(ctx, org, claims.Kind, claims.Sub, req.Tool, req.Slot)
	switch {
	case errors.Is(err, store.ErrNotFound):
		refuse(http.StatusNotFound, "not_found", "refused: no grant")
		return
	case errors.Is(err, store.ErrAmbiguous):
		refuse(http.StatusConflict, "conflict", "refused: grants name different keys")
		return
	case err != nil:
		rv.Log.Error("resolve: grant lookup", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "the value can't be resolved")
		return
	}
	a.WorkspaceID = slot.WorkspaceID
	tool, ok := toolReply(slot, req.Slot)
	if !ok {
		refuse(http.StatusNotFound, "not_found", "refused: slot not in the tool version")
		return
	}
	value, _, err := rv.Stores.Value(ctx, slot.Provider, org, slot.KeyID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		refuse(http.StatusNotFound, "not_found", "refused: no value")
		return
	case errors.Is(err, store.ErrNoStoreType):
		// Fail closed: the same reply as no value, the reason in the audit.
		// It comes after ErrNotFound, so an error that wraps both is audited
		// as no value; the reply is the same either way.
		refuse(http.StatusNotFound, "not_found", "refused: no store type")
		return
	case err != nil:
		// A wrong KEK or a damaged envelope: never more than that in the log.
		rv.Log.Error("resolve: the value doesn't open", "key", slot.KeyID)
		writeError(w, http.StatusInternalServerError, "internal", "the value can't be resolved")
		return
	}
	// v2: the AAD binds the exact definition this reply carries, so a
	// sidecar that is handed another destination can't open the value.
	sealed, err := seal.To(req.EphPub, seal.AAD(claims.JTI, req.Tool, req.Slot, tool.sealDef()), value)
	clear(value)
	if err != nil {
		refuse(http.StatusBadRequest, "bad_request", "refused: bad eph_pub")
		return
	}
	a.OK, a.Result = true, "resolved"
	rv.audit(ctx, a, now)
	writeJSON(w, http.StatusOK, resolveReply{Sealed: sealed, JTI: claims.JTI, TTL: slot.TTL, Tool: tool})
}

// sealDef is the definition as the seal binds it: the same fields, in the
// same order, as the reply's JSON.
func (t replyTool) sealDef() seal.ToolDef {
	d := seal.ToolDef{Version: t.Version, BaseURL: t.BaseURL, TimeoutMs: t.TimeoutMs,
		SlotLocation: t.Slot.Location, SlotField: t.Slot.Field, SlotScheme: t.Slot.Scheme,
		PrivateAllow: t.PrivateAllow}
	for _, a := range t.Actions {
		d.Actions = append(d.Actions, seal.Action{Method: a.Method, Path: a.Path})
	}
	return d
}

// toolReply is the granted tool version's definition for the reply, with
// the slot the request named; false if that version has no such slot.
func toolReply(r store.ResolvedSlot, slotName string) (replyTool, bool) {
	t := replyTool{Version: r.ToolVersion, BaseURL: r.Tool.BaseURL, TimeoutMs: r.Tool.TimeoutMs, Actions: []replyAction{}, PrivateAllow: []string{}}
	t.PrivateAllow = append(t.PrivateAllow, r.Tool.PrivateAllow...)
	for _, a := range r.Tool.Actions {
		t.Actions = append(t.Actions, replyAction{Method: a.Method, Path: a.Path})
	}
	for _, sl := range r.Tool.Slots {
		if sl.Name == slotName {
			t.Slot = replySlot{Location: sl.Location, Field: sl.Field, Scheme: sl.Scheme}
			return t, true
		}
	}
	return replyTool{}, false
}

func (rv *Resolver) audit(ctx context.Context, a store.ResolveAudit, now time.Time) {
	if err := rv.Store.AuditResolve(ctx, a, now); err != nil {
		rv.Log.Error("resolve: audit row", "err", err)
	}
}
