package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

// With SEED_DEMO_DATA on, someone signing in for the first time, with no
// organization, joins the demo organization as an Owner; with it off,
// nothing changes.
func TestDemoDataFirstSignInJoinsDemoOrgAsOwner(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		for _, on := range []bool{false, true} {
			h := newHarness(t, b, config.AuthBoth, nil)
			h.cfg.SeedDemoData = on
			if _, err := h.st.SeedDemo(ctx, time.Now()); err != nil {
				t.Fatal(err)
			}

			email := fmt.Sprintf("demo-local-%v@example.com", on)
			local := h.createUser(email, testPassword)
			if res := h.login(h.client(), email, testPassword); res.StatusCode != http.StatusOK {
				t.Fatalf("login = %d", res.StatusCode)
			}
			c := h.client()
			h.do(c, http.MethodGet, h.oidcFlow(c), nil, nil)
			me, status := h.me(c)
			if status != http.StatusOK {
				t.Fatalf("zitadel sign-in: me = %d", status)
			}

			for _, id := range []string{local.ID, me.User.ID} {
				orgs, err := h.st.ListUserOrgs(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if !on {
					if len(orgs) != 0 {
						t.Fatalf("demo data off: user %s joined %+v", id, orgs)
					}
					continue
				}
				if len(orgs) != 1 || orgs[0].Org.ID != store.DemoOrgID || orgs[0].Role != "Owner" || orgs[0].Status != "active" {
					t.Fatalf("demo data on: user %s orgs = %+v; want Owner of %s", id, orgs, store.DemoOrgID)
				}
			}
		}
	})
}
