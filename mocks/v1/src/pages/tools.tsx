import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { CATALOG } from '../lib/catalog'
import { ago, uid } from '../lib/format'
import { actions, isAdmin, orgTools, toolWorkspaces, useDB, useNow } from '../lib/store'
import type { HttpMethod, KeySlot, ToolAction, ToolSnapshot } from '../lib/types'
import { ListBody } from '../components/shared'
import { Button, Checkbox, Footer, MethodBadge, Modal, PageTitle, Pill, Row, Table, cx } from '../components/ui'

const COLS = '2fr 1.2fr 1.4fr 0.8fr 1.4fr 1fr'

export function ToolsPage() {
  const d = useDB()
  const nav = useNavigate()
  const now = useNow()
  const tools = orgTools(d)
  const admin = isAdmin(d)
  const [importing, setImporting] = useState(false)

  return (
    <div>
      <PageTitle
        actions={
          admin && (
            <>
              <Button onClick={() => setImporting(true)}>Import API description</Button>
              <Button
                variant="primary"
                onClick={() =>
                  nav(
                    `/tools/${actions.createTool({ internalName: 'untitled_tool', displayName: 'Untitled tool', description: '', baseUrl: 'https://api.example.com', actions: [], slots: [], perMinute: 60, timeoutMs: 10000 })}`,
                  )
                }
              >
                Start blank
              </Button>
            </>
          )
        }
      >
        Tools
      </PageTitle>

      <div className="eyebrow mt-7">Starter catalog</div>
      <div className="mt-3 flex max-w-[1040px] gap-4">
        {CATALOG.map((c) => {
          const installed = tools.find((t) => t.catalog === c.id)
          return (
            <div key={c.id} className="flex flex-1 flex-col gap-2.5 rounded-[10px] border border-edge bg-panel p-[18px]">
              <div className="flex size-8 items-center justify-center rounded-lg border border-chip bg-line text-sm font-bold text-zinc-400">{c.mark}</div>
              <div>
                <div className="text-md font-semibold">{c.tool.displayName}</div>
                <div className="mt-0.5 text-xs text-zinc-500">
                  {c.blurb} · {c.tool.slots.length} key slot
                </div>
              </div>
              {installed ? (
                <div className="flex items-center gap-3">
                  <Button size="sm" onClick={() => nav(`/tools/${installed.id}`)}>
                    Open
                  </Button>
                  <span className="text-xs text-zinc-500">Installed</span>
                </div>
              ) : (
                <Button size="sm" className="self-start" disabled={!admin} onClick={() => actions.installCatalog(c.id)}>
                  Install
                </Button>
              )}
            </div>
          )
        })}
      </div>

      <div className="eyebrow mt-8">Your tools</div>
      {tools.length === 0 ? (
        <div className="mt-3 max-w-[1040px] rounded-[10px] border border-edge bg-panel p-10 text-center text-[13px] text-zinc-400">No tools yet. Import an API description or start from the catalog.</div>
      ) : (
        <Table cols={COLS} head={['Name', 'Version', 'Key slots', 'Actions', 'Attached to', 'Updated']} className="mt-3 max-w-[1040px]">
          <ListBody cols={COLS} what="tools">
            {tools.map((t) => {
              const ws = toolWorkspaces(d, t.id)
              return (
                <Row key={t.id} cols={COLS} onClick={() => nav(`/tools/${t.id}`)} className="py-3.5">
                  <div className="font-medium">{t.displayName}</div>
                  <div className="flex items-center gap-2">
                    <span>v{t.version}</span>
                    <Pill tone={t.status === 'published' ? 'green' : 'neutral'}>{t.status === 'published' ? 'Published' : 'Draft'}</Pill>
                  </div>
                  <div className="font-mono text-xs text-zinc-400">{t.slots.map((s) => s.name).join(', ') || '—'}</div>
                  <div className="text-zinc-400">{t.actions.length}</div>
                  <div className={ws.length ? 'text-zinc-400' : 'text-zinc-500'}>{ws.length ? ws.map((w) => w.name).join(', ') : 'Not attached'}</div>
                  <div className="text-zinc-500">{ago(t.updatedAt, now)}</div>
                </Row>
              )
            })}
          </ListBody>
        </Table>
      )}
      <ImportApiModal open={importing} onClose={() => setImporting(false)} />
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Import an API description (OpenAPI)                                 */
/* ------------------------------------------------------------------ */
const SAMPLE_OPENAPI = {
  openapi: '3.0.3',
  info: { title: 'Acme Orders API', description: 'Orders and refunds for the Acme storefront.' },
  servers: [{ url: 'https://api.acme-orders.dev' }],
  components: { securitySchemes: { orders_key: { type: 'http', scheme: 'bearer' } } },
  security: [{ orders_key: [] }],
  paths: {
    '/v1/orders': {
      get: { summary: 'List recent orders', parameters: [{ name: 'limit', in: 'query', schema: { type: 'integer' }, description: 'How many orders to return.' }] },
      post: { summary: 'Create an order' },
    },
    '/v1/orders/{id}': {
      get: { summary: 'Get one order by id', parameters: [{ name: 'id', in: 'path', required: true, schema: { type: 'string' }, description: 'The order id.' }] },
      delete: { summary: 'Cancel an order', parameters: [{ name: 'id', in: 'path', required: true, schema: { type: 'string' } }] },
    },
    '/v1/refunds': { post: { summary: 'Refund an order' } },
  },
}

type Op = ToolAction & { selected: boolean }
type Parsed = { title: string; description: string; baseUrl: string; ops: Op[]; slots: KeySlot[] }

function parseOpenApi(text: string): Parsed {
  const t = text.trim()
  if (!t.startsWith('{')) throw new Error('This looks like YAML. Upload the JSON version of the OpenAPI file — YAML isn’t supported in this prototype.')
  let spec: any // eslint-disable-line @typescript-eslint/no-explicit-any
  try {
    spec = JSON.parse(t)
  } catch {
    throw new Error('This file isn’t valid JSON. Check it and try again.')
  }
  if (!spec.paths || typeof spec.paths !== 'object') throw new Error('No “paths” found — is this an OpenAPI file?')
  const ops: Op[] = []
  for (const [path, methods] of Object.entries<any>(spec.paths)) // eslint-disable-line @typescript-eslint/no-explicit-any
    for (const [m, op] of Object.entries<any>(methods)) { // eslint-disable-line @typescript-eslint/no-explicit-any
      const method = m.toUpperCase() as HttpMethod
      if (!['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) continue
      ops.push({
        id: uid('a'),
        method,
        path,
        summary: op.summary ?? op.operationId ?? `${method} ${path}`,
        inputs: (op.parameters ?? []).map((p: any) => ({ id: uid('i'), name: p.name, type: ['integer', 'number', 'boolean'].includes(p.schema?.type) ? p.schema.type : 'string', required: !!p.required, description: p.description ?? '' })), // eslint-disable-line @typescript-eslint/no-explicit-any
        selected: method === 'GET',
      })
    }
  const schemes = spec.components?.securitySchemes ?? {}
  const slots: KeySlot[] = Object.entries<any>(schemes).map(([name, s]) => ({ // eslint-disable-line @typescript-eslint/no-explicit-any
    id: uid('s'),
    name: name.toLowerCase().replace(/[^a-z0-9]+/g, '_'),
    location: s.in === 'query' ? 'query' : 'header',
    field: s.type === 'apiKey' ? s.name : 'Authorization',
    scheme: s.scheme === 'basic' ? 'Basic' : s.type === 'apiKey' ? 'None' : 'Bearer',
  }))
  return { title: spec.info?.title ?? 'Imported API', description: spec.info?.description ?? '', baseUrl: spec.servers?.[0]?.url ?? 'https://api.example.com', ops, slots }
}

function ImportApiModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const nav = useNavigate()
  const [parsed, setParsed] = useState<Parsed | null>(null)
  const [err, setErr] = useState<string | null>(null)
  useEffect(() => {
    setParsed(null)
    setErr(null)
  }, [open])
  const take = (text: string) => {
    try {
      setParsed(parseOpenApi(text))
      setErr(null)
    } catch (e) {
      setErr((e as Error).message)
    }
  }
  const sel = parsed?.ops.filter((o) => o.selected) ?? []
  return (
    <Modal open={open} onClose={onClose} width={620} title="Import an API description">
      {!parsed ? (
        <>
          <div className="-mt-2 text-sm2 text-zinc-400">Upload an OpenAPI file. Keyhole pre-fills the tool; you just check which actions to expose.</div>
          <label className="cursor-pointer rounded-[10px] border border-dashed border-zinc-700 p-7 text-center hover:border-zinc-500">
            <input type="file" accept=".json,.yaml,.yml,application/json" className="sr-only" onChange={(e) => e.target.files?.[0]?.text().then(take)} />
            <div className="text-[13px] text-zinc-300">Drop an OpenAPI file here</div>
            <div className="mt-1 text-xs text-zinc-500">
              JSON · or <span className="text-brass">browse</span>
            </div>
          </label>
          {err && <div className="text-xs text-red-400">{err}</div>}
          <button type="button" className="self-start text-sm2 text-brass hover:text-brass-light" onClick={() => take(JSON.stringify(SAMPLE_OPENAPI))}>
            Try it with a sample file
          </button>
        </>
      ) : (
        <>
          <div className="-mt-2 text-sm2 text-zinc-400">
            <span className="font-medium text-zinc-200">{parsed.title}</span> · <span className="font-mono text-xs">{parsed.baseUrl}</span>
          </div>
          <div>
            <div className="eyebrow">Actions to expose</div>
            <div className="mt-1 text-xs text-zinc-500">Anything you leave unchecked is simply not possible through this tool.</div>
          </div>
          <div className="overflow-hidden rounded-[10px] border border-edge bg-rail">
            {parsed.ops.map((o, i) => (
              <label key={o.id} className={cx('flex cursor-pointer items-center gap-3.5 border-b border-line px-4 py-2.5 last:border-b-0', !o.selected && 'opacity-60')}>
                <Checkbox checked={o.selected} onChange={(v) => setParsed({ ...parsed, ops: parsed.ops.map((x, j) => (j === i ? { ...x, selected: v } : x)) })} />
                <MethodBadge method={o.method} />
                <span className="font-mono text-sm2 text-zinc-200">{o.path}</span>
                <span className="truncate text-sm2 text-zinc-400">{o.summary}</span>
              </label>
            ))}
          </div>
          {parsed.slots.length > 0 && (
            <div className="text-xs text-zinc-500">
              Key slot{parsed.slots.length > 1 ? 's' : ''} found: {parsed.slots.map((s) => <span key={s.id} className="font-mono text-brass-light">{s.name} </span>)}
            </div>
          )}
          <Footer>
            <Button size="lg" onClick={() => setParsed(null)}>
              Back
            </Button>
            <Button
              size="lg"
              variant="primary"
              disabled={!sel.length}
              onClick={() => {
                const snap: ToolSnapshot = {
                  internalName: parsed.title.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, ''),
                  displayName: parsed.title,
                  description: parsed.description,
                  baseUrl: parsed.baseUrl,
                  actions: sel.map(({ selected: _s, ...a }) => a), // eslint-disable-line @typescript-eslint/no-unused-vars
                  slots: parsed.slots,
                  perMinute: 60,
                  timeoutMs: 10000,
                }
                const id = actions.createTool(snap)
                onClose()
                nav(`/tools/${id}`)
              }}
            >
              Create tool with {sel.length} action{sel.length === 1 ? '' : 's'}
            </Button>
          </Footer>
        </>
      )}
    </Modal>
  )
}
