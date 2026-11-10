# Analytics — event contract, metrics and windows

Optional, minimal GA4 instrumentation. It is **off** unless you configure your
own GA4 property. This file is the Keyhole contract; the other VBCDX apps carry
the same metric definitions for their own events. The point is that any
activation/retention number an audit cites can be traced to an event
definition, a window and a source.

## Source

- One GA4 property of your own, with one web data stream per app (for example
  one for Keyhole and one for Dispatch), so cross-app funnels stay in one
  property.
- The measurement id Keyhole uses: `GA_MEASUREMENT_ID` (server env), served to
  the web app through `GET /api/auth/config` (`ga_measurement_id`). Empty by
  default, which turns analytics off.

## Events

| Event | Parameters | Fired when | Notes |
|---|---|---|---|
| `page_view` | `page_path` (normalized), `page_title` | once per app load | in-app route changes are not tracked; sessions/retention come from any event. GA4's own history-based page_views must stay off on the stream (see Turning it on) or they would double-count with raw URLs |
| `login` | `method: 'zitadel' \| 'local'` | a sign-in this browser started completes | explicit Zitadel redirect, silent SSO, or local form; a plain reload of an existing session does **not** count. GA4 recommended event |
| `store_connected` | — | a store was connected and passed its connection test | **DEFINED, NOT EMITTED YET**: connecting a store is not built yet (the Stores screens are read-only). The change that builds the action wires this event; the name/params are fixed here so no design round is needed. |

Aggregate only: an allowlist in `web/src/analytics.ts` drops every parameter
key that is not listed above. Every hit additionally carries a sanitized
`page_location` (origin + normalized path) and a sanitized `page_referrer`
(origin only) — set internally, after the allowlist, so a caller cannot supply
or omit them. Failures and sign-out send nothing.

## Metric definitions and windows

Computed in GA4 (Explorations / reports) over an explicitly stated window; an
audit citing a number must state **as-of date, window, and source property**.

| Metric | Definition | Window |
|---|---|---|
| Activated users (store) | distinct users with ≥1 `store_connected` | any stated window — **not measurable until the event ships** |
| Sign-ins completed | distinct users with ≥1 `login` | any stated window (e.g. last 28 days) |
| 7-day return | users active on day D (any event) with ≥1 event in D+1…D+7 | rolling; cite as-of |
| 28-day return | same with D+1…D+28 | rolling; cite as-of |
| Returning users | GA4 standard `returning users` (a session whose user had a prior session) | GA4 default date range; cite as-of |

"Distinct users" is GA4's client-id–based count; it counts browsers, not
verified people. Sign-up itself happens at the identity provider (Zitadel) and
is not observable from Keyhole.

## Privacy rules (invariant)

- **Never sent:** emails, names, user/org/store ids, secret names or values,
  tokens, or any page content. The module enforces an allowlist of caller
  parameter keys (`method`, `page_path`, `page_title`); everything else is
  dropped before leaving the page.
- Page paths are normalized: opaque id segments — any segment containing an
  underscore (the schema's prefixed ids, of any prefix length and with one or
  more underscores: `org_…`, `st_…`, `u_…`, `om_acme_dana`, `gr_prod_github`,
  `inv_nw_old`, `t_github@1`; ordinary route segments never contain one) and
  bare uuid/hex/number segments — become `:id`.
  GA4 auto-collects `page_location` = the raw `location.href` with every event,
  so the module overrides it on every hit **and** in the tag config with the
  origin + normalized path (no query, no hash), and overrides `page_referrer`
  with the referrer's origin only (`""` when absent). With those overrides the
  invariant holds: tenant/store ids do not reach GA4. Regression tests assert
  no `dataLayer` entry can contain a raw `org_…`/`st_…` id.
- Google signals and ad personalization are disabled at tag config.
- GA4 sets its own `_ga` cookies while enabled; no other cookie is touched.
  That is the client identifier the retention metrics are computed from.
- With `GA_MEASUREMENT_ID` unset: no script loads, no cookie is set, no network
  call happens and the CSP stays strict. Server tests assert both the empty
  config response and the strict CSP.
- CSP: `script-src`/`connect-src`/`img-src` gain only the GA4 hosts
  (`www.googletagmanager.com`, `*.google-analytics.com`,
  `*.analytics.google.com`), and only when a measurement id is configured.

## Turning it on

1. Create a GA4 property and a web data stream for your Keyhole URL, and note
   its `G-XXXXXXXXXX` id. On the stream, also disable Enhanced Measurement's
   **"Page changes based on browser history events"**: GA4 otherwise emits its
   own `page_view` on SPA history changes regardless of `send_page_view: false`,
   with the raw URL, and double-counts the one `page_view` per app load. The
   client-side overrides cannot stop that hit.
2. Set `GA_MEASUREMENT_ID=G-XXXXXXXXXX` in Keyhole's environment and restart:
   no rebuild, no code change.
3. Unset it again to switch analytics off.
