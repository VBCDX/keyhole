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
| 8 · Support lock | View as Keyhole support › search `mia` › Lock account (a reason or ticket ID is required). View as Dana › Audit shows the support action and its reason; Players › Users › Mia › *Ask support to unlock*, which support then sees on Mia's record. |

## Ground rules the prototype enforces

- Secret values are never rendered. Paste fields are password inputs, and after saving only dots remain.
  The app only ever learns a value's length.
- Full agent tokens appear once, in the brass one-time panel. They're kept in memory only for this
  browser session, so a config download can include them. Everywhere else they're masked as `kh_live_••••6TpE`.
- Every delete or revoke opens an impact preview. Organization, store, and workspace deletes require typing the name.
- One activity-log component and one Users/Agents table design, reused everywhere.
- Agents only ever get a tool's published version. New and catalog tools start as drafts that can't be granted
  or called. Workspace Tools tabs, test calls and traffic follow the published version until the next publish.

## Permission model (shared across the VBCDX suite)

Keyhole and Dispatch follow the same permission rules. Where a rule plays out differently in each product, that's noted inline.

1. **Organization roles are Owner, userAdmin and user.**
   - Owners and userAdmins administer every workspace in the organization.
   - Users see only the workspaces they've been added to.
   - The last Owner can't be removed or demoted, but ownership can be transferred.
   - Keyhole support (superAdmin) sits outside every organization. It can only lock or unlock an account, sign someone out everywhere, and resend an invite. It never sees content.
2. **Every workspace has at least one human admin.** When no human workspace admin is set, the organization's Owners and userAdmins are its admins by default. In Dispatch, agents can also hold workspace admin, but they never replace the human admin.
3. **Permission is checked when an action happens; nobody owns the result afterwards.** Creating an agent, adding a member, delegating admin, sending a message or granting a tool needs permission at that moment. The creator doesn't own what they made: agents belong to the organization, and "created by" is kept only for observability and audit.
4. **Losing permission doesn't undo what was already done.** Removing, suspending or demoting a person or agent stops what they can do next. It leaves everything they already did in place:
   - agents they created keep working;
   - admin rights they delegated stand;
   - tools they granted and messages they sent stay as they are;
   - receipts they recorded (delivered, read, acknowledged) stay as they are.

   The audit log keeps their name on all of it.
5. **Refusals win, and they only act on what hasn't happened yet.** Blocks, suspensions and revocations are checked before any grant, and they take effect immediately. In Dispatch that means queued messages; in Keyhole, the next call. A refusal never rewrites history and never counts as completion. For example, blocking the one agent that hasn't acknowledged a message doesn't satisfy an "all acknowledged" condition.
6. **Every change is attributed and previewed.**
   - Each admin change logs who made it (human, agent or support) and its before and after values.
   - Anything destructive or access-reducing shows an impact preview first.
   - Organization, store and workspace deletions require typing the name to confirm.

State persists in `localStorage` (`keyhole-mocks-v1`). Resetting the scenario clears it.
