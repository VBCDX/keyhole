import { useState } from 'react'
import { ago, plural } from '../lib/format'
import { actions, agentById, canAdminWorkspace, connectorKind, protocolList, orgWorkspaces, sidecarRequests24h, useDB, useNow, visibleConnectors, wsById } from '../lib/store'
import type { Connector } from '../lib/types'
import { EnrollPanel } from '../components/EnrollPanel'
import { TokenPanel } from '../components/keyhole'
import { ImpactDialog, ImpactRows, ListBody } from '../components/shared'
import { SidecarEnrollPanel } from '../components/SidecarEnrollPanel'
import { Button, Dot, Field, Footer, Input, Menu, Modal, OptionCard, PageTitle, Row, Segmented, Select, SlideOver, Table, cx } from '../components/ui'

const COLS = '1.05fr 0.85fr 0.85fr 1.1fr 0.5fr 2.6fr 1fr 36px'
type Filter = 'all' | 'vault' | 'sidecar'

export function ConnectorHealth({ c }: { c: Connector }) {
  const now = useNow()
  if (c.health === 'healthy')
    return (
      <span className="flex items-center gap-2">
        <Dot health="healthy" />
        <span className="text-sm2 text-zinc-300">Healthy</span>
      </span>
    )
  if (c.health === 'degraded')
    return (
      <span className="flex items-center gap-2">
        <Dot health="degraded" />
        <span className="text-sm2 text-amber-400">Degraded</span>
      </span>
    )
  return (
    <span className="flex items-center gap-2">
      <Dot health="offline" />
      <span className="text-sm2 text-zinc-500">Offline · last seen {ago(c.lastSeen, now).replace(' ago', '')} ago</span>
    </span>
  )
}

export function Connectors() {
  const d = useDB()
  const now = useNow()
  // Org admins, and workspace admins for connectors enrolled to their workspace, manage connectors.
  const canManage = (c: Connector) => canAdminWorkspace(d, c.workspaceId)
  const admin = orgWorkspaces(d).some((w) => canAdminWorkspace(d, w.id))
  const all = visibleConnectors(d)
  const [filter, setFilter] = useState<Filter>('all')
  const list = all.filter((c) => filter === 'all' || c.kind === filter)
  const [enrolling, setEnrolling] = useState(false)
  const [enrollKind, setEnrollKind] = useState<'vault' | 'sidecar'>('vault')
  const [renaming, setRenaming] = useState<Connector | null>(null)
  const [newName, setNewName] = useState('')
  const [revoking, setRevoking] = useState<Connector | null>(null)
  const [rotating, setRotating] = useState<Connector | null>(null)
  const [rotated, setRotated] = useState<{ c: Connector; token: string } | null>(null)
  const [rebinding, setRebinding] = useState<Connector | null>(null)
  const [rebindTo, setRebindTo] = useState('')
  const routedThrough = (c: Connector | null) => (c ? d.stores.filter((s) => s.route === c.id).map((s) => s.name) : [])
  const agentLabel = (c: Connector | null) => (c?.agentId ? (agentById(d, c.agentId)?.label ?? '—') : '—')
  const rebindOptions = rebinding ? (wsById(d, rebinding.workspaceId)?.agentIds ?? []).map((id) => agentById(d, id)).filter((a) => a && a.status === 'active' && a.id !== rebinding.agentId) : []

  return (
    <div>
      <PageTitle
        actions={
          admin && (
            <Button
              variant="primary"
              onClick={() => {
                setEnrollKind(filter === 'sidecar' ? 'sidecar' : 'vault')
                setEnrolling(true)
              }}
            >
              Enroll connector
            </Button>
          )
        }
      >
        Connectors
      </PageTitle>
      <div className="mt-1 max-w-[1100px] text-sm2 text-zinc-500">
        Installed copies of <span className="font-mono text-xs">keyholed</span>. A <span className="text-zinc-300">vault connector</span> reaches vaults behind your firewall. A{' '}
        <span className="text-zinc-300">sidecar</span> runs next to an app or agent harness and injects keys into its calls, acting as one agent.
      </div>
      <Segmented<Filter>
        className="mt-5"
        size="sm"
        value={filter}
        onChange={setFilter}
        options={[
          { value: 'all', label: `All · ${all.length}` },
          { value: 'vault', label: `Vault connectors · ${all.filter((c) => c.kind === 'vault').length}` },
          { value: 'sidecar', label: `Sidecars · ${all.filter((c) => c.kind === 'sidecar').length}` },
        ]}
      />
      {list.length === 0 ? (
        <div className="mt-3 max-w-[1100px] rounded-[10px] border border-edge bg-panel p-10 text-center">
          <div className="text-[13px] text-zinc-400">
            {filter === 'sidecar' ? 'No sidecars yet. Enroll one next to an app to keep keys out of its memory.' : filter === 'vault' ? 'No vault connectors yet. Enroll one to reach vaults behind your firewall.' : 'No connectors yet.'}
          </div>
          {admin && (
            <Button
              variant="primary"
              className="mt-4"
              onClick={() => {
                setEnrollKind(filter === 'sidecar' ? 'sidecar' : 'vault')
                setEnrolling(true)
              }}
            >
              Enroll connector
            </Button>
          )}
        </div>
      ) : (
        <Table cols={COLS} head={['Name', 'Type', 'Workspace', 'Health', 'Version', 'Details', 'Enrolled', '']} className="mt-3 max-w-[1180px]">
          <ListBody cols={COLS} what="connectors">
            {list.map((c) => {
              const off = c.health === 'offline'
              const bound = c.agentId ? agentById(d, c.agentId) : null
              return (
                <Row key={c.id} cols={COLS}>
                  <div className={cx('truncate font-medium', off && 'text-zinc-500')}>{c.name}</div>
                  <div className="text-zinc-400">{c.kind === 'sidecar' ? 'Sidecar' : 'Vault connector'}</div>
                  <div className={off ? 'text-zinc-500' : 'text-zinc-400'}>{wsById(d, c.workspaceId)?.name ?? '—'}</div>
                  <ConnectorHealth c={c} />
                  <div className={cx('font-mono text-xs', off ? 'text-zinc-500' : 'text-zinc-400')}>{c.version}</div>
                  {c.kind === 'sidecar' ? (
                    <div className="min-w-0 text-xs text-zinc-400">
                      <div className="truncate" title={`As ${bound?.label ?? '—'} · ${sidecarRequests24h(d, c).toLocaleString()} requests in the last 24 hours`}>
                        As <span className={cx('font-medium', bound?.status === 'active' ? 'text-zinc-200' : 'text-amber-400')}>{bound?.label ?? '—'}</span>
                        {bound && bound.status !== 'active' && ` (${bound.status})`} · {sidecarRequests24h(d, c).toLocaleString()} requests / 24 h
                      </div>
                      <div className="truncate text-zinc-500" title={c.host}>
                        {protocolList(c)} · <span className="font-mono">{c.host}</span>
                      </div>
                    </div>
                  ) : (
                    <div className={cx('font-mono text-xs', off ? 'text-zinc-500' : 'text-zinc-400')}>{c.ip ?? '—'}</div>
                  )}
                  <div className="text-zinc-500">
                    {ago(c.enrolledAt, now)} · {c.enrolledBy}
                  </div>
                  <div className="text-right">
                    {canManage(c) && (
                      <Menu
                        items={[
                          {
                            label: 'Rename',
                            onClick: () => {
                              setNewName(c.name)
                              setRenaming(c)
                            },
                          },
                          { label: 'Rotate token…', onClick: () => setRotating(c) },
                          c.kind === 'sidecar'
                            ? {
                                label: 'Rebind agent…',
                                onClick: () => {
                                  setRebindTo('')
                                  setRebinding(c)
                                },
                              }
                            : null,
                          { label: 'Revoke…', danger: true, onClick: () => setRevoking(c) },
                        ]}
                      />
                    )}
                  </div>
                </Row>
              )
            })}
          </ListBody>
        </Table>
      )}
      {all.some((c) => c.health === 'offline') && <div className="mt-2.5 max-w-[1100px] text-xs text-zinc-500">Offline connectors send a notification and put an amber flag on any store or workspace that depends on them.</div>}

      <SlideOver open={enrolling} onClose={() => setEnrolling(false)} width={560} title="Enroll connector">
        <div className="flex flex-col gap-2.5">
          <OptionCard name="kind" checked={enrollKind === 'vault'} onSelect={() => setEnrollKind('vault')} title="Vault connector">
            <div className="mt-0.5 text-xs text-zinc-500">Reaches an OpenBao vault behind your firewall. Stores route through it.</div>
          </OptionCard>
          <OptionCard name="kind" checked={enrollKind === 'sidecar'} onSelect={() => setEnrollKind('sidecar')} title="Sidecar">
            <div className="mt-0.5 text-xs text-zinc-500">Runs next to an app or agent harness, acts as one agent, and injects keys into its calls.</div>
          </OptionCard>
        </div>
        {enrollKind === 'vault' ? <EnrollPanel key="vault" /> : <SidecarEnrollPanel key="sidecar" />}
      </SlideOver>

      <Modal open={!!renaming} onClose={() => setRenaming(null)} title={`Rename ${renaming ? connectorKind(renaming) : ''}`} width={420}>
        <Field label="Name" hint={renaming ? `Was ${renaming.name}. Past log rows keep the old name.` : undefined}>
          <Input value={newName} onChange={(e) => setNewName(e.target.value)} autoFocus />
        </Field>
        <Footer>
          <Button size="lg" onClick={() => setRenaming(null)}>
            Cancel
          </Button>
          <Button
            size="lg"
            variant="primary"
            disabled={!newName.trim() || newName.trim() === renaming?.name}
            onClick={() => {
              actions.renameConnector(renaming!.id, newName)
              setRenaming(null)
            }}
          >
            Rename
          </Button>
        </Footer>
      </Modal>

      <ImpactDialog
        open={!!rotating}
        onClose={() => setRotating(null)}
        title={`Rotate the token for ${rotating?.name}?`}
        rows={
          rotating?.kind === 'sidecar'
            ? [
                ['Acts as', agentLabel(rotating)],
                ['Workspace', wsById(d, rotating.workspaceId)?.name ?? '—'],
                ['Current credential', 'Keeps working for 10 minutes', 'amber'],
              ]
            : [
                ['Stores that route through it', routedThrough(rotating).join(', ') || 'None'],
                ['Workspace', rotating ? (wsById(d, rotating.workspaceId)?.name ?? '—') : ''],
                ['Current credential', 'Keeps working for 10 minutes', 'amber'],
              ]
        }
        body={`A new token is shown once. The running ${rotating ? connectorKind(rotating) : 'connector'} picks it up with keyholed rotate --token <new token> (or a restart with it). After 10 minutes the old credential is refused${rotating?.kind === 'sidecar' ? ' and calls through the sidecar fail until it has the new one' : ' and those stores become unreachable through it'}.`}
        confirmLabel="Rotate token"
        onConfirm={() => {
          const token = rotating && actions.rotateConnector(rotating.id)
          if (rotating && token) setRotated({ c: rotating, token })
        }}
      />
      <Modal open={!!rotated} onClose={() => {}} width={540} dismissable={false}>
        {rotated && (
          <TokenPanel
            token={rotated.token}
            title={`${rotated.c.kind === 'sidecar' ? 'Sidecar' : 'Vault connector'} token rotated`}
            subtitle={`${rotated.c.name} · ${wsById(d, rotated.c.workspaceId)?.name ?? ''}`}
            note={
              <>
                On that machine, run <span className="font-mono">keyholed rotate --token</span> with this token. The old credential keeps working for 10 minutes.
              </>
            }
            onDone={() => setRotated(null)}
          />
        )}
      </Modal>

      <Modal open={!!rebinding} onClose={() => setRebinding(null)} title={`Rebind ${rebinding?.name} to another agent?`} width={500}>
        <Field label="New agent" hint="Only active agents of this workspace.">
          <Select value={rebindTo} onChange={(e) => setRebindTo(e.target.value)}>
            <option value="">Pick an agent…</option>
            {rebindOptions.map((a) => (
              <option key={a!.id} value={a!.id}>
                {a!.label}
              </option>
            ))}
          </Select>
        </Field>
        <ImpactRows
          rows={[
            ['Acts as', rebindTo ? `${agentLabel(rebinding)} → ${agentById(d, rebindTo)?.label}` : agentLabel(rebinding), rebindTo ? 'amber' : undefined],
            ['Requests / 24 h', rebinding ? sidecarRequests24h(d, rebinding).toLocaleString() : ''],
          ]}
        />
        <div className="text-sm2 text-zinc-400">From the next request, the new agent’s tool grants, rate limit, expiry and status apply. Past requests keep the agent they ran as.</div>
        <Footer>
          <Button size="lg" onClick={() => setRebinding(null)}>
            Cancel
          </Button>
          <Button
            size="lg"
            variant="danger"
            disabled={!rebindTo}
            onClick={() => {
              actions.rebindSidecar(rebinding!.id, rebindTo)
              setRebinding(null)
            }}
          >
            Rebind agent
          </Button>
        </Footer>
      </Modal>

      <ImpactDialog
        open={!!revoking}
        onClose={() => setRevoking(null)}
        title={`Revoke ${revoking?.name}?`}
        rows={
          revoking?.kind === 'sidecar'
            ? [
                ['Acts as', agentLabel(revoking)],
                ['Protocols', protocolList(revoking)],
                ['Listens on', revoking.listen ?? '—'],
                ['Requests / 24 h', sidecarRequests24h(d, revoking).toLocaleString(), sidecarRequests24h(d, revoking) ? 'amber' : undefined],
                ['Last seen', ago(revoking.lastSeen, now)],
              ]
            : [
                ['Routes through it', `${plural(routedThrough(revoking).length, 'store')} and 1 workspace route through this vault connector`, routedThrough(revoking).length ? 'amber' : undefined],
                ['Stores', routedThrough(revoking).join(', ') || 'None'],
                ['Workspace', revoking ? (wsById(d, revoking.workspaceId)?.name ?? '—') : ''],
                ['Last seen', ago(revoking?.lastSeen ?? null, now)],
              ]
        }
        body={
          revoking?.kind === 'sidecar'
            ? `The sidecar is refused from its next request, so apps pointing at ${revoking.listen} get errors. The agent itself keeps working everywhere else.`
            : 'The vault connector stops being accepted immediately. Stores that route through it become unreachable until you pick another route.'
        }
        confirmLabel={revoking?.kind === 'sidecar' ? 'Revoke sidecar' : 'Revoke vault connector'}
        onConfirm={() => revoking && actions.revokeConnector(revoking.id)}
      />
    </div>
  )
}
