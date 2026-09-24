import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { ago, plural, uid } from '../lib/format'
import { testCall } from '../lib/simulate'
import { actions, filledSlot, isAdmin, orgEvents, toolById, toolWorkspaces, useDB, useNow } from '../lib/store'
import type { DB, HttpMethod, KeySlot, Tool, ToolAction, ToolInput, ToolSnapshot, Workspace } from '../lib/types'
import { CopyChip, KeyholeIcon } from '../components/keyhole'
import { ImpactDialog } from '../components/shared'
import { Button, Checkbox, Field, Footer, Input, Modal, Pill, Select, Textarea, cx } from '../components/ui'

const SECTIONS = [
  { id: 'basics', label: 'Basics' },
  { id: 'actions', label: 'Actions' },
  { id: 'inputs', label: 'Inputs' },
  { id: 'keys', label: 'Keys needed' },
  { id: 'limits', label: 'Limits' },
  { id: 'test', label: 'Test call' },
  { id: 'publish', label: 'Publish' },
] as const

const METHODS: HttpMethod[] = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']

/** A short, human change summary between the published version and the draft. */
export function changeSummary(t: Tool): string[] {
  const p = t.published
  if (!p) return [`First version · ${plural(t.actions.length, 'action')}, ${plural(t.slots.length, 'key slot')}`]
  const out: string[] = []
  if (p.displayName !== t.displayName) out.push(`Renamed “${p.displayName}” → “${t.displayName}”`)
  if (p.internalName !== t.internalName) out.push(`Internal name → ${t.internalName}`)
  if (p.description !== t.description) out.push('Description changed')
  if (p.baseUrl !== t.baseUrl) out.push(`Base address → ${t.baseUrl}`)
  const sig = (a: ToolAction) => `${a.method} ${a.path}`
  const before = new Map(p.actions.map((a) => [a.id, a]))
  for (const a of t.actions) {
    const b = before.get(a.id)
    if (!b) out.push(`Added action ${sig(a)}`)
    else {
      if (sig(a) !== sig(b)) out.push(`Changed action ${sig(b)} → ${sig(a)}`)
      if (a.summary !== b.summary && sig(a) === sig(b)) out.push(`Reworded ${sig(a)}`)
      if (JSON.stringify(a.inputs) !== JSON.stringify(b.inputs)) out.push(`Inputs changed on ${sig(a)}`)
    }
  }
  for (const b of p.actions) if (!t.actions.some((a) => a.id === b.id)) out.push(`Removed action ${sig(b)}`)
  const slotSig = (s: KeySlot) => `${s.name}@${s.location}:${s.field}:${s.scheme}`
  if (p.slots.map(slotSig).join() !== t.slots.map(slotSig).join()) out.push(`Key slots → ${t.slots.map((s) => s.name).join(', ') || 'none'}`)
  if (p.perMinute !== t.perMinute) out.push(`Calls per minute ${p.perMinute} → ${t.perMinute}`)
  if (p.timeoutMs !== t.timeoutMs) out.push(`Timeout ${p.timeoutMs / 1000} s → ${t.timeoutMs / 1000} s`)
  return out
}

function rawDefinition(t: ToolSnapshot) {
  return JSON.stringify(
    {
      name: t.internalName,
      title: t.displayName,
      description: t.description,
      baseUrl: t.baseUrl,
      actions: t.actions.map((a) => ({
        method: a.method,
        path: a.path,
        summary: a.summary,
        inputSchema: {
          type: 'object',
          properties: Object.fromEntries(a.inputs.map((i) => [i.name, { type: i.type, description: i.description }])),
          required: a.inputs.filter((i) => i.required).map((i) => i.name),
        },
      })),
      keySlots: t.slots.map((s) => ({ name: s.name, in: s.location, field: s.field, scheme: s.scheme })),
      limits: { callsPerMinute: t.perMinute, timeoutMs: t.timeoutMs },
    },
    null,
    2,
  )
}

function Section({ id, title, action, children, refFn }: { id: string; title: string; action?: ReactNode; children: ReactNode; refFn: (el: HTMLElement | null) => void }) {
  return (
    <section id={id} ref={refFn} data-section={id} className="scroll-mt-6 pb-8">
      <div className="flex items-center justify-between">
        <h2 className="m-0 text-[15px] font-semibold">{title}</h2>
        {action}
      </div>
      {children}
    </section>
  )
}

/** The key a draft slot gets in a workspace: its own mapping, or — for a renamed slot — the one it inherits on publish. */
function draftSlotKey(d: DB, t: Tool, w: Workspace, slot: KeySlot) {
  const wt = w.tools.find((x) => x.toolId === t.id)
  if (!wt) return null
  const old = t.published?.slots.find((o) => o.id === slot.id)?.name
  return filledSlot(d, w, wt, slot.name) ?? (old ? filledSlot(d, w, wt, old) : null)
}

const cellInput = 'w-full rounded-md border border-transparent bg-transparent px-2 py-1 text-sm2 text-zinc-200 outline-none hover:border-edge focus:border-zinc-600 focus:bg-page disabled:hover:border-transparent'

export function ToolEditor() {
  const d = useDB()
  const nav = useNavigate()
  const now = useNow(10_000)
  const { toolId } = useParams()
  const [params] = useSearchParams()
  const t = toolById(d, toolId!)
  const admin = isAdmin(d)
  const [active, setActive] = useState('basics')
  const [raw, setRaw] = useState(false)
  const [publishing, setPublishing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const scroller = useRef<HTMLDivElement>(null)
  const refs = useRef<Record<string, HTMLElement | null>>({})

  // Scroll-spy for the section list.
  useEffect(() => {
    const root = scroller.current
    if (!root) return
    const io = new IntersectionObserver(
      (entries) => {
        const vis = entries.filter((e) => e.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
        if (vis[0]) setActive((vis[0].target as HTMLElement).dataset.section!)
      },
      { root, rootMargin: '0px 0px -65% 0px' },
    )
    Object.values(refs.current).forEach((el) => el && io.observe(el))
    return () => io.disconnect()
  }, [t?.id])

  useEffect(() => {
    const s = params.get('section')
    if (s) window.setTimeout(() => refs.current[s]?.scrollIntoView({ behavior: 'smooth' }), 50)
  }, [params])

  if (!t) return <div className="p-10 text-sm text-zinc-400">This tool no longer exists. <Link to="/tools">Back to tools</Link></div>

  const edit = (patch: Partial<ToolSnapshot>) => admin && actions.editTool(t.id, patch)
  const setAction = (id: string, patch: Partial<ToolAction>) => edit({ actions: t.actions.map((a) => (a.id === id ? { ...a, ...patch } : a)) })
  const setInput = (aid: string, iid: string, patch: Partial<ToolInput>) => setAction(aid, { inputs: t.actions.find((a) => a.id === aid)!.inputs.map((i) => (i.id === iid ? { ...i, ...patch } : i)) })
  const setSlot = (id: string, patch: Partial<KeySlot>) => edit({ slots: t.slots.map((s) => (s.id === id ? { ...s, ...patch } : s)) })
  const jump = (id: string) => refs.current[id]?.scrollIntoView({ behavior: 'smooth' })
  const changes = changeSummary(t)
  const hasDraft = t.status === 'draft'
  const nameOk = /^[a-z][a-z0-9_]*$/.test(t.internalName)
  const granted = toolWorkspaces(d, t.id)
  // Slots that would have no key once this draft goes live (a renamed slot keeps its key; a new one doesn't).
  const unfilledAfterPublish = granted.flatMap((w) => t.slots.filter((s) => !draftSlotKey(d, t, w, s)).map((s) => `${w.name} · ${s.name}`))

  return (
    <div className="flex h-full flex-col bg-page text-zinc-100">
      {/* Top bar */}
      <header className="flex items-center justify-between border-b border-line bg-rail px-6 py-3.5">
        <div className="flex min-w-0 items-center gap-3.5">
          <button type="button" aria-label="Back to tools" onClick={() => nav('/tools')} className="text-base text-zinc-500 hover:text-zinc-200">
            ←
          </button>
          <span className="truncate text-sm font-semibold">{t.displayName || 'Untitled tool'}</span>
          <Pill tone={hasDraft ? 'neutral' : 'green'}>{hasDraft ? 'Draft' : 'Published'}</Pill>
          <span className="text-xs text-zinc-600">
            v{t.version}
            {hasDraft && t.published ? ` · on top of published v${t.published.version}` : ''} · Saved {ago(t.updatedAt, now).toLowerCase()}
          </span>
        </div>
        <div className="flex gap-2">
          <Button size="sm" className="!py-[7px] !text-[13px]" onClick={() => jump('test')}>
            Test call
          </Button>
          {admin && (
            <Button variant="primary" size="sm" className="!py-[7px] !text-[13px]" disabled={!hasDraft} onClick={() => setPublishing(true)}>
              Publish
            </Button>
          )}
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        {/* Section list */}
        <nav className="flex w-[220px] min-w-[220px] flex-col gap-0.5 border-r border-line bg-rail px-3 py-5">
          {SECTIONS.map((s) => (
            <button
              key={s.id}
              type="button"
              onClick={() => jump(s.id)}
              className={cx('rounded-md px-3 py-[7px] text-left text-[13px]', active === s.id ? 'bg-line font-semibold text-zinc-100' : 'text-zinc-400 hover:text-zinc-200')}
            >
              {s.label}
              {s.id === 'actions' && <span className="font-normal text-zinc-600"> · {t.actions.length}</span>}
              {s.id === 'keys' && <span className="font-normal text-zinc-600"> · {t.slots.length}</span>}
            </button>
          ))}
        </nav>

        {/* Form */}
        <div ref={scroller} className="min-w-0 flex-1 overflow-y-auto">
          <div className="max-w-[860px] px-10 py-7">
            <Section id="basics" title="Basics" refFn={(el) => (refs.current.basics = el)}>
              <div className="mt-[18px] flex gap-4">
                <div className="flex-1">
                  <Field label="Internal name" error={nameOk ? null : 'Lowercase letters, numbers and underscores; start with a letter.'}>
                    <Input mono className="bg-panel" value={t.internalName} disabled={!admin} onChange={(e) => edit({ internalName: e.target.value })} />
                  </Field>
                </div>
                <div className="flex-1">
                  <Field label="Display name">
                    <Input className="bg-panel" value={t.displayName} disabled={!admin} onChange={(e) => edit({ displayName: e.target.value })} />
                  </Field>
                </div>
              </div>
              <div className="mt-4">
                <Field label="Description" hint="AI agents read this to decide when to use the tool — write it for the AI, not for people.">
                  <Textarea rows={3} className="bg-panel leading-[1.55] text-zinc-200" value={t.description} disabled={!admin} onChange={(e) => edit({ description: e.target.value })} placeholder="What this tool does, and when an agent should reach for it." />
                </Field>
              </div>
              <div className="mt-4">
                <Field label="Base address" hint="Every action’s path is added to this.">
                  <Input mono className="bg-panel" value={t.baseUrl} disabled={!admin} onChange={(e) => edit({ baseUrl: e.target.value })} />
                </Field>
              </div>
            </Section>

            <Section
              id="actions"
              title="Actions"
              refFn={(el) => (refs.current.actions = el)}
              action={
                admin && (
                  <Button size="sm" onClick={() => edit({ actions: [...t.actions, { id: uid('a'), method: 'GET', path: '/', summary: '', inputs: [] }] })}>
                    Add action
                  </Button>
                )
              }
            >
              <div className="mt-1 text-xs text-zinc-500">Anything not listed here is not possible through this tool.</div>
              <div className="mt-3 overflow-hidden rounded-[10px] border border-edge bg-panel">
                {t.actions.length === 0 && <div className="p-6 text-center text-sm2 text-zinc-500">No actions yet. Add the first thing agents may do with this API.</div>}
                {t.actions.map((a) => (
                  <div key={a.id} className="group flex items-center gap-3 border-b border-line px-4 py-2.5 last:border-b-0">
                    <select
                      aria-label="Method"
                      value={a.method}
                      disabled={!admin}
                      onChange={(e) => setAction(a.id, { method: e.target.value as HttpMethod })}
                      className={cx(
                        'appearance-none rounded-[5px] border px-2 py-0.5 text-center font-mono text-2xs font-semibold outline-none',
                        a.method === 'GET' ? 'border-green-500/25 bg-green-500/10 text-green-400' : a.method === 'DELETE' ? 'border-red-500/25 bg-red-500/10 text-red-400' : 'border-sky-500/25 bg-sky-500/10 text-sky-400',
                      )}
                    >
                      {METHODS.map((m) => (
                        <option key={m}>{m}</option>
                      ))}
                    </select>
                    <input aria-label="Path" value={a.path} disabled={!admin} onChange={(e) => setAction(a.id, { path: e.target.value })} className={cx(cellInput, 'w-56 flex-none font-mono text-zinc-200')} />
                    <input aria-label="Summary" value={a.summary} placeholder="One-line summary" disabled={!admin} onChange={(e) => setAction(a.id, { summary: e.target.value })} className={cx(cellInput, 'min-w-0 flex-1 text-zinc-400')} />
                    {admin && (
                      <button type="button" aria-label="Remove action" onClick={() => edit({ actions: t.actions.filter((x) => x.id !== a.id) })} className="text-zinc-600 opacity-0 group-hover:opacity-100 hover:text-red-400">
                        ✕
                      </button>
                    )}
                  </div>
                ))}
              </div>
            </Section>

            <Section id="inputs" title="Inputs" refFn={(el) => (refs.current.inputs = el)}>
              <div className="mt-1 text-xs text-zinc-500">What the AI may supply for each action. Nothing else is passed through.</div>
              <div className="mt-3 flex flex-col gap-3">
                {t.actions.map((a) => (
                  <div key={a.id} className="overflow-hidden rounded-[10px] border border-edge bg-panel">
                    <div className="flex items-center justify-between border-b border-line bg-rail px-4 py-2">
                      <span className="font-mono text-xs text-zinc-400">
                        {a.method} {a.path}
                      </span>
                      {admin && (
                        <button type="button" className="text-xs text-brass hover:text-brass-light" onClick={() => setAction(a.id, { inputs: [...a.inputs, { id: uid('i'), name: '', type: 'string', required: false, description: '' }] })}>
                          Add input
                        </button>
                      )}
                    </div>
                    {a.inputs.length === 0 ? (
                      <div className="px-4 py-3 text-xs text-zinc-500">No inputs — the agent calls it as is.</div>
                    ) : (
                      <>
                        <div className="grid grid-cols-[1fr_110px_90px_2fr_24px] gap-3 px-4 pt-2.5 pb-1">
                          {['Name', 'Type', 'Required', 'Description', ''].map((h) => (
                            <div key={h} className="th">
                              {h}
                            </div>
                          ))}
                        </div>
                        {a.inputs.map((i) => (
                          <div key={i.id} className="group grid grid-cols-[1fr_110px_90px_2fr_24px] items-center gap-3 px-4 py-1.5 last:pb-3">
                            <input aria-label="Input name" value={i.name} placeholder="name" disabled={!admin} onChange={(e) => setInput(a.id, i.id, { name: e.target.value })} className={cx(cellInput, 'font-mono')} />
                            <select aria-label="Type" value={i.type} disabled={!admin} onChange={(e) => setInput(a.id, i.id, { type: e.target.value as ToolInput['type'] })} className={cx(cellInput, 'appearance-none')}>
                              {['string', 'integer', 'number', 'boolean'].map((x) => (
                                <option key={x}>{x}</option>
                              ))}
                            </select>
                            <Checkbox checked={i.required} disabled={!admin} onChange={(v) => setInput(a.id, i.id, { required: v })} label={i.required ? 'Yes' : 'No'} />
                            <input aria-label="Input description" value={i.description} placeholder="Short description" disabled={!admin} onChange={(e) => setInput(a.id, i.id, { description: e.target.value })} className={cx(cellInput, 'text-zinc-400')} />
                            {admin && (
                              <button type="button" aria-label="Remove input" onClick={() => setAction(a.id, { inputs: a.inputs.filter((x) => x.id !== i.id) })} className="text-zinc-600 opacity-0 group-hover:opacity-100 hover:text-red-400">
                                ✕
                              </button>
                            )}
                          </div>
                        ))}
                      </>
                    )}
                  </div>
                ))}
                {t.actions.length === 0 && <div className="text-xs text-zinc-500">Add an action first.</div>}
              </div>
            </Section>

            <Section
              id="keys"
              title="Keys needed"
              refFn={(el) => (refs.current.keys = el)}
              action={
                admin && (
                  <Button size="sm" onClick={() => edit({ slots: [...t.slots, { id: uid('s'), name: `${t.internalName.split('_')[0] || 'api'}_key`, location: 'header', field: 'Authorization', scheme: 'Bearer' }] })}>
                    Add key slot
                  </Button>
                )
              }
            >
              <div className="mt-3 flex flex-col gap-2">
                {t.slots.map((s) => (
                  <div key={s.id} className="group flex flex-wrap items-center gap-3 rounded-[10px] border border-edge bg-panel px-4 py-3">
                    <KeyholeIcon size={14} />
                    <input aria-label="Slot name" value={s.name} disabled={!admin} onChange={(e) => setSlot(s.id, { name: e.target.value })} className={cx(cellInput, 'w-44 flex-none font-mono text-[13px] !text-brass-light')} />
                    <span className="text-sm2 text-zinc-500">Injected in the</span>
                    <select aria-label="Where the key goes" value={s.location} disabled={!admin} onChange={(e) => setSlot(s.id, { location: e.target.value as KeySlot['location'], field: e.target.value === 'query' ? 'api_key' : 'Authorization' })} className={cx(cellInput, 'w-auto appearance-none text-zinc-300')}>
                      <option value="header">header</option>
                      <option value="query">query parameter</option>
                    </select>
                    <input aria-label="Header or parameter name" value={s.field} disabled={!admin} onChange={(e) => setSlot(s.id, { field: e.target.value })} className={cx(cellInput, 'w-32 flex-none font-mono text-zinc-300')} />
                    {s.location === 'header' && (
                      <select aria-label="Scheme" value={s.scheme} disabled={!admin} onChange={(e) => setSlot(s.id, { scheme: e.target.value as KeySlot['scheme'] })} className={cx(cellInput, 'w-auto appearance-none text-zinc-400')}>
                        <option>Bearer</option>
                        <option>Basic</option>
                        <option value="None">no scheme</option>
                      </select>
                    )}
                    {admin && (
                      <button type="button" aria-label="Remove slot" onClick={() => edit({ slots: t.slots.filter((x) => x.id !== s.id) })} className="ml-auto text-zinc-600 opacity-0 group-hover:opacity-100 hover:text-red-400">
                        ✕
                      </button>
                    )}
                  </div>
                ))}
                {t.slots.length === 0 && <div className="rounded-[10px] border border-edge bg-panel p-5 text-center text-sm2 text-zinc-500">No key slots. Add one if the API needs a key.</div>}
              </div>
              <div className="mt-2 text-xs text-zinc-500">The actual key is chosen later, when this tool is attached to a workspace.</div>
            </Section>

            <Section id="limits" title="Limits" refFn={(el) => (refs.current.limits = el)}>
              <div className="mt-4 flex gap-6">
                <Field label="Calls per minute">
                  <Input type="number" min={1} className="w-28 bg-panel" value={t.perMinute} disabled={!admin} onChange={(e) => edit({ perMinute: Math.max(1, Number(e.target.value)) })} />
                </Field>
                <Field label="Timeout">
                  <div className="flex items-center gap-2">
                    <Input type="number" min={1} className="w-20 bg-panel" value={t.timeoutMs / 1000} disabled={!admin} onChange={(e) => edit({ timeoutMs: Math.max(1, Number(e.target.value)) * 1000 })} />
                    <span className="text-sm2 text-zinc-500">seconds</span>
                  </div>
                </Field>
              </div>
              <div className="mt-2 text-xs text-zinc-500">Each workspace can set a lower limit of its own.</div>
            </Section>

            <Section id="test" title="Test call" refFn={(el) => (refs.current.test = el)}>
              <TestCallPanel t={t} />
            </Section>

            <Section id="publish" title="Publish" refFn={(el) => (refs.current.publish = el)}>
              <div className="mt-3 rounded-[10px] border border-edge bg-panel p-4">
                {hasDraft ? (
                  <>
                    <div className="text-sm2 text-zinc-300">
                      Draft v{t.version}
                      {t.published ? ` · workspaces still use v${t.published.version} until you publish` : ' · not yet usable by agents'}
                    </div>
                    <ul className="mt-2.5 mb-0 flex list-none flex-col gap-1 pl-0 text-sm2 text-zinc-400">
                      {changes.length ? changes.map((c) => <li key={c}>· {c}</li>) : <li>· No changes yet</li>}
                    </ul>
                    {admin && (
                      <div className="mt-4 flex gap-2">
                        <Button variant="primary" onClick={() => setPublishing(true)}>
                          Publish v{t.version}
                        </Button>
                        {t.published && <Button onClick={() => actions.discardDraft(t.id)}>Discard draft</Button>}
                      </div>
                    )}
                  </>
                ) : (
                  <div className="text-sm2 text-zinc-400">Published v{t.version}. Editing any field starts a new draft; agents keep using v{t.version} until you publish again.</div>
                )}
              </div>
              {admin && (
                <div className="mt-4 flex items-center justify-between rounded-[10px] border border-red-500/30 bg-red-500/[0.04] px-4 py-3">
                  <div>
                    <div className="text-sm2 font-semibold text-red-400">Delete tool</div>
                    <div className="text-xs text-zinc-400">Removes it from every workspace and cabinet it’s granted to.</div>
                  </div>
                  <Button variant="danger" size="sm" onClick={() => setDeleting(true)}>
                    Delete tool
                  </Button>
                </div>
              )}
            </Section>
          </div>
        </div>

        {/* Raw preview rail */}
        {raw ? (
          <aside className="flex w-[420px] min-w-[420px] flex-col border-l border-line bg-rail">
            <div className="flex items-center justify-between border-b border-line px-4 py-3">
              <span className="font-mono text-xs text-zinc-400">&lt;/&gt; Raw preview</span>
              <button type="button" aria-label="Collapse raw preview" onClick={() => setRaw(false)} className="text-sm text-zinc-600 hover:text-zinc-300">
                ›
              </button>
            </div>
            <pre className="m-0 flex-1 overflow-auto p-4 font-mono text-xs2 leading-relaxed text-zinc-400">{rawDefinition(t)}</pre>
          </aside>
        ) : (
          <button type="button" onClick={() => setRaw(true)} aria-label="Open raw preview" className="flex w-11 min-w-11 flex-col items-center gap-2.5 border-l border-line bg-rail pt-5 text-zinc-600 hover:text-zinc-300">
            <span className="font-mono text-2xs">&lt;/&gt;</span>
            <span className="text-2xs [writing-mode:vertical-rl]">Raw preview</span>
          </button>
        )}
      </div>

      <Modal open={publishing} onClose={() => setPublishing(false)} width={500} title={`Publish ${t.displayName} v${t.version}?`}>
        <ul className="m-0 flex list-none flex-col gap-1.5 rounded-[10px] border border-edge bg-rail p-4 text-sm2 text-zinc-300">
          {changes.map((c) => (
            <li key={c}>· {c}</li>
          ))}
        </ul>
        <div className="text-sm2 text-zinc-400">{granted.length ? `${granted.map((w) => w.name).join(', ')} switch to v${t.version} on their next request.` : 'It isn’t granted to any workspace yet.'}</div>
        {unfilledAfterPublish.length > 0 && (
          <div className="rounded-lg border border-amber-500/30 bg-amber-500/[0.06] px-3.5 py-2.5 text-xs text-amber-400">
            These slots have no key once v{t.version} is live, so calls through them fail until someone picks one: <span className="font-mono">{unfilledAfterPublish.join(', ')}</span>
          </div>
        )}
        <Footer>
          <Button size="lg" onClick={() => setPublishing(false)}>
            Cancel
          </Button>
          <Button
            size="lg"
            variant="primary"
            onClick={() => {
              actions.publishTool(t.id)
              setPublishing(false)
            }}
          >
            Publish
          </Button>
        </Footer>
      </Modal>
      <ImpactDialog
        open={deleting}
        onClose={() => setDeleting(false)}
        title={`Delete ${t.displayName}?`}
        rows={[
          ['Granted to', granted.map((w) => w.name).join(', ') || 'No workspaces', granted.length ? 'amber' : undefined],
          ['Cabinets using it', d.cabinets.filter((c) => c.tools.some((x) => x.toolId === t.id)).map((c) => c.name).join(', ') || 'None'],
          ['Last called', ago(orgEvents(d).find((e) => e.object === t.displayName && e.type === 'request')?.at ?? null, now)],
        ]}
        body="Agents lose this tool on their next request. Keys stay in their stores."
        confirmLabel="Delete tool"
        onConfirm={() => {
          actions.deleteTool(t.id)
          nav('/tools')
        }}
      />
    </div>
  )
}

function TestCallPanel({ t }: { t: Tool }) {
  const d = useDB()
  // The editor tests this draft, so it needs a workspace where every draft slot has a key
  // (a renamed slot uses the key it keeps on publish).
  const granted = toolWorkspaces(d, t.id)
  const ready = granted.filter((w) => t.slots.every((s) => draftSlotKey(d, t, w, s)))
  const [wsId, setWsId] = useState('')
  const [actionId, setActionId] = useState('')
  const [values, setValues] = useState<Record<string, string>>({})
  const [result, setResult] = useState<ReturnType<typeof testCall> | null>(null)
  const [running, setRunning] = useState(false)
  useEffect(() => {
    if (!ready.find((w) => w.id === wsId)) setWsId(ready[0]?.id ?? '')
  }, [ready.map((w) => w.id).join()]) // eslint-disable-line
  useEffect(() => {
    if (!t.actions.find((a) => a.id === actionId)) setActionId(t.actions[0]?.id ?? '')
  }, [t.actions.map((a) => a.id).join()]) // eslint-disable-line
  const action = t.actions.find((a) => a.id === actionId)
  const missing = action?.inputs.filter((i) => i.required && !values[i.name]?.trim()) ?? []
  const ws = ready.find((w) => w.id === wsId)
  const example = useMemo(() => (action ? `${action.method} ${action.path.replace(/\{(\w+)\}/g, (_, n) => values[n] || `{${n}}`)}` : ''), [action, values])

  if (!ready.length)
    return (
      <div className="mt-3 rounded-[10px] border border-edge bg-panel p-5 text-sm2 text-zinc-400">
        {granted.length ? (
          <>
            This draft has a key slot that no workspace fills yet ({t.slots.filter((s) => !granted.some((w) => draftSlotKey(d, t, w, s))).map((s) => s.name).join(', ')}). Renamed slots keep their key, but a new slot needs one: publish, then pick a key on{' '}
            <Link to={`/workspaces/${granted[0].id}/tools`}>{granted[0].name}’s Tools tab</Link>.
          </>
        ) : (
          <>
            Test calls run in a workspace where this tool’s key slots are filled. {t.published ? 'Attach it to one first —' : 'Publish it, then'} <Link to={`/workspaces`}>pick a workspace</Link> and add the tool on its Tools tab.
          </>
        )}
      </div>
    )
  return (
    <div className="mt-3 flex flex-col gap-4 rounded-[10px] border border-edge bg-panel p-4">
      <div className="flex gap-4">
        <div className="flex-1">
          <Field label="Workspace" hint="Only workspaces where every slot is filled.">
            <Select value={wsId} onChange={(e) => setWsId(e.target.value)}>
              {ready.map((w) => (
                <option key={w.id} value={w.id}>
                  {w.name}
                </option>
              ))}
            </Select>
          </Field>
        </div>
        <div className="flex-1">
          <Field label="Action">
            <Select value={actionId} onChange={(e) => setActionId(e.target.value)}>
              {t.actions.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.method} {a.path}
                </option>
              ))}
            </Select>
          </Field>
        </div>
      </div>
      {action?.inputs.map((i) => (
        <Field key={i.id} label={<span className="font-mono">{i.name}</span>} optional={!i.required} hint={i.description}>
          <Input mono value={values[i.name] ?? ''} onChange={(e) => setValues({ ...values, [i.name]: e.target.value })} placeholder={i.type} />
        </Field>
      ))}
      <div className="flex items-center gap-3">
        <Button
          variant="primary"
          disabled={!action || missing.length > 0 || running}
          onClick={() => {
            setRunning(true)
            setResult(null)
            window.setTimeout(() => {
              setResult(testCall({ toolId: t.id, wsId, actionId, draft: true }))
              setRunning(false)
            }, 700)
          }}
        >
          {running ? 'Running…' : 'Run test call'}
        </Button>
        <span className="font-mono text-xs text-zinc-500">{example}</span>
        {missing.length > 0 && <span className="text-xs text-zinc-500">Fill {missing.map((m) => m.name).join(', ')} first.</span>}
      </div>
      {result && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg border border-green-500/30 bg-green-500/[0.05] px-4 py-3 text-sm2">
          <span className="font-semibold text-green-400">200 OK</span>
          <span className="text-zinc-400">{result.ms} ms</span>
          <span className="flex items-center gap-2 text-zinc-500">
            Tracking code <CopyChip value={result.trk} />
          </span>
          <Link to={`/workspaces/${ws?.id}/audit`} className="ml-auto">
            Also in {ws?.name}’s log →
          </Link>
        </div>
      )}
    </div>
  )
}
