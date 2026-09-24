import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { actions, useDB } from '../lib/store'
import { cx } from './ui'

/**
 * Prototype-only controls (not part of the product design): reset the
 * scenario, switch persona, and force list loading/error states.
 */
export function DemoPanel() {
  const d = useDB()
  const nav = useNavigate()
  const [open, setOpen] = useState(false)
  const personas = [
    { id: 'u_dana', label: 'Dana Keller', sub: 'Owner' },
    { id: 'u_ravi', label: 'Ravi Mehta', sub: 'userAdmin' },
    { id: 'u_mia', label: 'Mia Chen', sub: 'user' },
    { id: 'u_sam', label: 'Sam Ortiz', sub: 'invited' },
    { id: 'u_noor', label: 'Noor Haddad', sub: 'Owner (Northwind)' },
    { id: 'support', label: 'Keyhole support', sub: 'superAdmin' },
  ].filter((p) => p.id === 'support' || d.users.some((u) => u.id === p.id))

  const btn = (on: boolean) => cx('rounded-md border px-2.5 py-1 text-xs', on ? 'border-zinc-500 bg-zinc-800 text-zinc-100' : 'border-edge text-zinc-400 hover:text-zinc-200')

  return (
    <div className="fixed bottom-3 left-3 z-50 font-sans">
      {open && (
        <div className="mb-2 w-72 rounded-xl border border-dashed border-zinc-700 bg-zinc-950/95 p-4 text-zinc-300 shadow-2xl backdrop-blur">
          <div className="flex items-center justify-between">
            <div className="text-2xs font-semibold tracking-[0.08em] text-zinc-500 uppercase">Prototype controls</div>
            <button className="text-sm text-zinc-600 hover:text-zinc-300" onClick={() => setOpen(false)} aria-label="Close">
              ✕
            </button>
          </div>

          <div className="mt-3 text-xs font-medium text-zinc-400">Scenario</div>
          <div className="mt-1.5 flex flex-wrap gap-1.5">
            <button
              className={btn(d.scenario === 'fresh')}
              onClick={() => {
                actions.reset('fresh')
                nav('/')
              }}
            >
              New org (Flow 1)
            </button>
            <button
              className={btn(d.scenario === 'populated')}
              onClick={() => {
                actions.reset('populated')
                nav('/')
              }}
            >
              Populated (Flows 2–8)
            </button>
          </div>
          <div className="mt-1 text-2xs text-zinc-600">Picking a scenario resets all data.</div>

          <div className="mt-3 text-xs font-medium text-zinc-400">View as</div>
          <div className="mt-1.5 flex flex-col gap-1">
            {personas.map((p) => (
              <button
                key={p.id}
                className={cx(btn(d.currentUserId === p.id), 'flex justify-between text-left')}
                onClick={() => {
                  actions.setPersona(p.id)
                  nav(p.id === 'support' ? '/support' : p.id === 'u_sam' ? '/invitations' : '/')
                }}
              >
                <span>{p.label}</span>
                <span className="font-mono text-2xs text-zinc-500">{p.sub}</span>
              </button>
            ))}
          </div>

          <div className="mt-3 text-xs font-medium text-zinc-400">List state</div>
          <div className="mt-1.5 flex gap-1.5">
            {(['normal', 'loading', 'error'] as const).map((s) => (
              <button key={s} className={btn(d.listState === s)} onClick={() => actions.setListState(s)}>
                {s[0].toUpperCase() + s.slice(1)}
              </button>
            ))}
          </div>
        </div>
      )}
      <button
        onClick={() => setOpen(!open)}
        className="block rounded-full border border-dashed border-zinc-700 bg-zinc-950/90 px-3 py-1.5 text-xs text-zinc-400 shadow-lg hover:text-zinc-100"
      >
        {open ? 'Hide' : 'Prototype'} controls
      </button>
    </div>
  )
}
