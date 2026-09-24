# Keyhole · mocks v1

A clickable front-end prototype of the Keyhole web app, built from the Claude Design handoff
(Flows 1–8 plus the system screens). It runs entirely in the browser on mock data. There is no backend.

**Live:** https://vbcdx.github.io/keyhole/mocks/v1/ (deployed by `.github/workflows/pages.yml`)

## Run it

```sh
cd mocks/v1
npm install
npm run dev      # http://localhost:5173
npm run build    # static site in dist/, works from any sub-path
```

Stack: React 19, TypeScript, Vite, Tailwind CSS v4 (zinc scale, Inter + JetBrains Mono), and
react-router with a hash router, so deep links work on GitHub Pages.

## Walking the flows

A **Prototype controls** pill sits at the bottom left. It isn't part of the product design. Use it to:

- **Scenario**: *New org (Flow 1)* starts Acme Corp empty, for the golden path. *Populated (Flows 2–8)* loads
  stores, a connector fleet, agents, cabinets, and audit history. Switching scenarios resets all data.
- **View as**: Dana (Owner), Ravi (userAdmin), Mia (user), Sam (invited, for the invitation flow), or
  Keyhole support (superAdmin, the separate support console).
- **List state**: force every list into its loading-skeleton or error state.

| Flow | Where to start |
|---|---|
| 1 · Golden path | New org → Home checklist → Add key → Tools › Install Stripe (a draft) → Publish v1 → New workspace → Add tool (slot matching) → New agent (one-time token) → Workspace › Connect › MCP → Download config → first call lands |
| 2 · OpenBao, public | Organizations › Stores › Add store › OpenBao. The first test finds the vault **Sealed**, and *Test again* passes. A path outside `secret/` gives **Not allowed**. An address containing `unreachable` gives **Can't reach**. |
| 3 · OpenBao via connector | Same wizard, step 2 › *Through a Keyhole connector* › *Enroll a new connector* › Generate token. The heartbeat arrives in about 6 s and the new connector is picked. |
| 4 · Invite + locked cabinet | Members › Invite user. View as Sam › Accept. Workspace › Cabinets › New cabinet › Only these players (people + agents). |
| 5 · Grant with missing slot | Organizations › Tools › Grant, then tick Staging: *Missing: `stripe_secret`* › Expose a key › Use this key › Grant. The exposure is staged and only applied on Grant; Production shows as already granted with its current mapping. |
| 6 · Revoke | Players › Agents › billing-agent › Revoke token. Its next attempt appears in the log as blocked about 4 s later. |
| 7 · Live debugging | Audit › Live. ops-agent streams red rows. Expand one › *Open the tool's actions* › add `GET /v1/payouts` › Publish › back to Live, and the rows turn green. |
| 8 · Support lock | View as Keyhole support › search `mia` › Lock account. View as Dana › Audit shows the support action. |

## Ground rules the prototype enforces

- Secret values are never rendered. Paste fields are password inputs, and after saving only dots remain.
  The app only ever learns a value's length.
- Full agent tokens appear once, in the brass one-time panel. They're kept in memory only for this
  browser session, so a config download can include them. Everywhere else they're masked as `kh_live_••••6TpE`.
- Every delete or revoke opens an impact preview. Organization, store, and workspace deletes require typing the name.
- One activity-log component and one Users/Agents table design, reused everywhere.
- Agents only ever get a tool's published version. New and catalog tools start as drafts that can't be granted
  or called. Workspace Tools tabs, test calls and traffic follow the published version until the next publish.

State persists in `localStorage` (`keyhole-mocks-v1`). Resetting the scenario clears it.
