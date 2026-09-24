import { useState } from 'react'
import { ago, plural } from '../lib/format'
import { actions, isAdmin, useDB, useNow, visibleConnectors, wsById } from '../lib/store'
import type { Connector } from '../lib/types'
import { EnrollPanel } from '../components/EnrollPanel'
import { ImpactDialog, ListBody } from '../components/shared'
import { Button, Dot, Field, Footer, Input, Menu, Modal, PageTitle, Row, SlideOver, Table, cx } from '../components/ui'

const COLS = '1.3fr 1.1fr 0.8fr 1.4fr 1.1fr 1.4fr 36px'

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
  const admin = isAdmin(d)
  const list = visibleConnectors(d)
  const [enrolling, setEnrolling] = useState(false)
  const [renaming, setRenaming] = useState<Connector | null>(null)
  const [newName, setNewName] = useState('')
  const [revoking, setRevoking] = useState<Connector | null>(null)
  const stores = revoking ? d.stores.filter((s) => s.route === revoking.id) : []

  return (
    <div>
      <PageTitle actions={admin && <Button variant="primary" onClick={() => setEnrolling(true)}>Enroll connector</Button>}>Connectors</PageTitle>
      <div className="mt-1 text-sm2 text-zinc-500">
        Installed copies of <span className="font-mono text-xs">keyholed</span>. They make traffic leave from your network and reach vaults behind your firewall.
      </div>
      {list.length === 0 ? (
        <div className="mt-5 max-w-[1060px] rounded-[10px] border border-edge bg-panel p-10 text-center">
          <div className="text-[13px] text-zinc-400">No connectors yet. Enroll one to route traffic through your own network.</div>
          {admin && (
            <Button variant="primary" className="mt-4" onClick={() => setEnrolling(true)}>
              Enroll connector
            </Button>
          )}
        </div>
      ) : (
        <Table cols={COLS} head={['Name', 'Workspace', 'Version', 'Health', 'IP address', 'Enrolled', '']} className="mt-5 max-w-[1100px]">
          <ListBody cols={COLS} what="connectors">
            {list.map((c) => {
              const off = c.health === 'offline'
              return (
                <Row key={c.id} cols={COLS}>
                  <div className={cx('font-medium', off && 'text-zinc-500')}>{c.name}</div>
                  <div className={off ? 'text-zinc-500' : 'text-zinc-400'}>{wsById(d, c.workspaceId)?.name ?? '—'}</div>
                  <div className={cx('font-mono text-xs', off ? 'text-zinc-500' : 'text-zinc-400')}>{c.version}</div>
                  <ConnectorHealth c={c} />
                  <div className={cx('font-mono text-xs', off ? 'text-zinc-500' : 'text-zinc-400')}>{c.ip ?? '—'}</div>
                  <div className="text-zinc-500">
                    {ago(c.enrolledAt, now)} · {c.enrolledBy}
                  </div>
                  <div className="text-right">
                    {admin && (
                      <Menu
                        items={[
                          {
                            label: 'Rename',
                            onClick: () => {
                              setNewName(c.name)
                              setRenaming(c)
                            },
                          },
                          { label: 'Revoke', danger: true, onClick: () => setRevoking(c) },
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
      {list.some((c) => c.health === 'offline') && (
        <div className="mt-2.5 max-w-[1100px] text-xs text-zinc-500">Offline connectors send a notification and put an amber flag on any store that depends on them.</div>
      )}

      <SlideOver open={enrolling} onClose={() => setEnrolling(false)} width={520} title="Enroll connector">
        <EnrollPanel />
      </SlideOver>

      <Modal open={!!renaming} onClose={() => setRenaming(null)} title="Rename connector" width={420}>
        <Field label="Name">
          <Input value={newName} onChange={(e) => setNewName(e.target.value)} autoFocus />
        </Field>
        <Footer>
          <Button size="lg" onClick={() => setRenaming(null)}>
            Cancel
          </Button>
          <Button
            size="lg"
            variant="primary"
            disabled={!newName.trim()}
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
        open={!!revoking}
        onClose={() => setRevoking(null)}
        title={`Revoke ${revoking?.name}?`}
        rows={[
          ['Routes through it', `${plural(stores.length, 'store')} and 1 workspace route through this connector`, stores.length ? 'amber' : undefined],
          ['Stores', stores.map((s) => s.name).join(', ') || 'None'],
          ['Workspace', revoking ? (wsById(d, revoking.workspaceId)?.name ?? '—') : ''],
          ['Last seen', ago(revoking?.lastSeen ?? null, now)],
        ]}
        body="The connector stops being accepted immediately. Stores that route through it become unreachable until you pick another route."
        confirmLabel="Revoke connector"
        onConfirm={() => revoking && actions.revokeConnector(revoking.id)}
      />
    </div>
  )
}
