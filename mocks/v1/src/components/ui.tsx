import { createContext, useContext, useEffect, useId, useRef, useState, type ButtonHTMLAttributes, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { Link, NavLink } from 'react-router-dom'

export const cx = (...c: (string | number | false | null | undefined)[]) => c.filter((x) => typeof x === 'string' && x).join(' ')

/* ------------------------------------------------------------------ */
/* Buttons                                                            */
/* ------------------------------------------------------------------ */
type BtnVariant = 'primary' | 'secondary' | 'danger' | 'brass' | 'ghost'
const BTN: Record<BtnVariant, string> = {
  primary: 'bg-zinc-50 text-zinc-900 font-semibold hover:bg-white border border-transparent',
  secondary: 'bg-transparent text-zinc-300 border border-zinc-700 hover:bg-white/[0.03] hover:text-zinc-100',
  danger: 'bg-red-600 text-white font-semibold hover:bg-red-500 border border-transparent',
  brass: 'bg-brass text-brass-ink font-semibold hover:bg-brass-light border border-transparent',
  ghost: 'bg-transparent text-zinc-400 hover:text-zinc-100 border border-transparent',
}
const SIZE = { sm: 'rounded-[7px] px-3 py-1.5 text-sm2', md: 'rounded-lg px-3.5 py-2 text-[13px]', lg: 'rounded-lg px-4 py-[9px] text-[13px]' }

export function Button({
  variant = 'secondary',
  size = 'md',
  className,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: BtnVariant; size?: keyof typeof SIZE }) {
  return (
    <button
      type="button"
      {...rest}
      className={cx('inline-flex items-center justify-center gap-2 whitespace-nowrap transition-colors disabled:opacity-40', BTN[variant], SIZE[size], className)}
    />
  )
}

/* ------------------------------------------------------------------ */
/* Form fields                                                        */
/* ------------------------------------------------------------------ */
/** The id a Field's label points at; the input inside picks it up unless it has its own. */
const FieldIdContext = createContext<string | undefined>(undefined)
export function useFieldId(own?: string) {
  const fromField = useContext(FieldIdContext)
  return own ?? fromField
}

export function Field({ label, optional, hint, children, htmlFor, error }: { label: ReactNode; optional?: boolean | string; hint?: ReactNode; children: ReactNode; htmlFor?: string; error?: string | null }) {
  const auto = useId()
  const id = htmlFor ?? auto
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-xs font-medium text-zinc-300">
        {label}
        {optional && <span className="font-normal text-zinc-600"> · {typeof optional === 'string' ? optional : 'optional'}</span>}
      </label>
      <FieldIdContext.Provider value={id}>{children}</FieldIdContext.Provider>
      {error ? <div className="text-xs2 text-red-400">{error}</div> : hint ? <div className="text-xs2 text-zinc-500">{hint}</div> : null}
    </div>
  )
}

const INPUT = 'w-full rounded-lg border border-zinc-700 bg-page px-3 py-[9px] text-[13px] text-zinc-100 placeholder:text-zinc-600 outline-none focus:border-zinc-500 disabled:text-zinc-500 disabled:border-edge'

export function Input({ mono, className, id, ...rest }: React.InputHTMLAttributes<HTMLInputElement> & { mono?: boolean }) {
  return <input {...rest} id={useFieldId(id)} className={cx(INPUT, mono && 'font-mono text-sm2', className)} />
}
export function Textarea({ className, id, ...rest }: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...rest} id={useFieldId(id)} className={cx(INPUT, 'min-h-16 resize-y leading-relaxed', className)} />
}
export function Select({ className, children, mono, id, ...rest }: React.SelectHTMLAttributes<HTMLSelectElement> & { mono?: boolean }) {
  return (
    <div className="relative">
      <select {...rest} id={useFieldId(id)} className={cx(INPUT, 'appearance-none pr-8', mono && 'font-mono text-sm2', className)}>
        {children}
      </select>
      <span className="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-[10px] text-zinc-600">▾</span>
    </div>
  )
}

export function Segmented<T extends string>({ value, options, onChange, size = 'md', className }: { value: T; options: { value: T; label: ReactNode }[]; onChange: (v: T) => void; size?: 'sm' | 'md'; className?: string }) {
  return (
    <div role="tablist" className={cx('flex gap-0.5 self-start border border-edge bg-page', size === 'sm' ? 'rounded-[7px] p-0.5' : 'rounded-lg p-[3px]', className)}>
      {options.map((o) => {
        const on = o.value === value
        return (
          <button
            key={o.value}
            role="tab"
            aria-selected={on}
            type="button"
            onClick={() => onChange(o.value)}
            className={cx(
              size === 'sm' ? 'rounded-[5px] px-2.5 py-1 text-xs2' : 'rounded-md px-3.5 py-1.5 text-sm2',
              on ? 'bg-edge font-semibold text-zinc-100' : 'text-zinc-400 hover:text-zinc-200',
            )}
          >
            {o.label}
          </button>
        )
      })}
    </div>
  )
}

export function Checkbox({ checked, onChange, label, disabled, className }: { checked: boolean; onChange?: (v: boolean) => void; label?: ReactNode; disabled?: boolean; className?: string }) {
  return (
    <label className={cx('flex items-center gap-2.5', disabled ? 'opacity-50' : 'cursor-pointer', className)}>
      <input type="checkbox" className="peer sr-only" checked={checked} disabled={disabled} onChange={(e) => onChange?.(e.target.checked)} />
      <span
        aria-hidden
        className={cx(
          'inline-flex size-3.5 min-w-3.5 items-center justify-center rounded text-[10px] font-bold peer-focus-visible:outline-2 peer-focus-visible:outline-zinc-500',
          checked ? 'bg-zinc-50 text-zinc-900' : 'border border-zinc-700',
        )}
      >
        {checked && '✓'}
      </span>
      {label != null && <span className={cx('text-[13px]', !checked && 'text-zinc-400')}>{label}</span>}
    </label>
  )
}

export function Radio({ checked, onChange, children, name }: { checked: boolean; onChange: () => void; children: ReactNode; name: string }) {
  return (
    <label className="flex cursor-pointer items-start gap-2.5">
      <input type="radio" name={name} className="peer sr-only" checked={checked} onChange={onChange} />
      <span aria-hidden className={cx('mt-0.5 size-4 min-w-4 rounded-full peer-focus-visible:outline-2 peer-focus-visible:outline-zinc-500', checked ? 'border-[5px] border-zinc-50' : 'border border-zinc-700')} />
      <span className="flex-1">{children}</span>
    </label>
  )
}

/** A radio "card" — the bordered option tile used in wizards. */
export function OptionCard({ checked, onSelect, title, children, name }: { checked: boolean; onSelect: () => void; title: ReactNode; children?: ReactNode; name: string }) {
  return (
    <div className={cx('rounded-[10px] border bg-rail p-4', checked ? 'border-brass/35' : 'border-edge')}>
      <Radio name={name} checked={checked} onChange={onSelect}>
        <div className={cx('text-md font-semibold', !checked && 'text-zinc-300')}>{title}</div>
        {children}
      </Radio>
    </div>
  )
}

export function Toggle({ on, onChange, label, disabled }: { on: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!on)}
      className={cx('relative h-5 w-9 min-w-9 rounded-full transition-colors', on ? 'bg-green-600' : 'bg-zinc-700')}
    >
      <span className={cx('absolute top-0.5 size-4 rounded-full transition-all', on ? 'left-[18px] bg-zinc-100' : 'left-0.5 bg-zinc-400')} />
    </button>
  )
}

/* ------------------------------------------------------------------ */
/* Badges + dots                                                      */
/* ------------------------------------------------------------------ */
type Tone = 'neutral' | 'green' | 'amber' | 'red' | 'blue'
const PILL: Record<Tone, string> = {
  neutral: 'text-zinc-400 bg-line border-chip',
  green: 'text-green-400 bg-green-500/10 border-green-500/30',
  amber: 'text-amber-400 bg-amber-500/[0.08] border-amber-500/30',
  red: 'text-red-400 bg-red-500/10 border-red-500/30',
  blue: 'text-sky-400 bg-sky-500/10 border-sky-500/30',
}
export function Pill({ tone = 'neutral', children, className }: { tone?: Tone; children: ReactNode; className?: string }) {
  return <span className={cx('inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-2xs font-semibold whitespace-nowrap', PILL[tone], className)}>{children}</span>
}

export function ConnPill({ label, on }: { label: string; on: boolean }) {
  return (
    <Pill tone={on ? 'green' : 'neutral'} className={cx('px-2.5', !on && 'text-zinc-500')}>
      {label} {on ? 'on' : 'off'}
    </Pill>
  )
}

export type Health = 'healthy' | 'degraded' | 'offline' | 'error'
/** Round health dot. "Degraded" is a half-filled dot, echoing the keyhole's shape. */
export function Dot({ health, size = 8, pulse }: { health: Health; size?: number; pulse?: boolean }) {
  const style: React.CSSProperties = { width: size, height: size, minWidth: size }
  if (health === 'degraded') style.background = 'linear-gradient(90deg,#f59e0b 50%,#3f3f46 50%)'
  return (
    <span
      aria-hidden
      style={style}
      className={cx('inline-block rounded-full', health === 'healthy' && 'bg-green-500', health === 'offline' && 'bg-zinc-600', health === 'error' && 'bg-red-500', pulse && 'animate-khpulse-fast')}
    />
  )
}

export function StatusText({ health, children }: { health: Health; children: ReactNode }) {
  return (
    <span className="inline-flex items-center gap-2">
      <Dot health={health} />
      <span className={cx('text-sm2', health === 'healthy' && 'text-zinc-300', health === 'degraded' && 'text-amber-400', health === 'offline' && 'text-zinc-500', health === 'error' && 'text-red-400')}>{children}</span>
    </span>
  )
}

/** Small inline status used in tables: dot + colored word. */
export function StatusInline({ tone, children }: { tone: 'green' | 'amber' | 'gray' | 'red'; children: ReactNode }) {
  const c = { green: ['text-green-400', 'bg-green-500'], amber: ['text-amber-400', 'bg-amber-500'], gray: ['text-zinc-400', 'bg-zinc-600'], red: ['text-red-400', 'bg-red-500'] }[tone]
  return (
    <span className={cx('inline-flex items-center gap-1.5 text-xs', c[0])}>
      <span className={cx('size-1.5 rounded-full', c[1])} />
      {children}
    </span>
  )
}

export function Avatar({ initials, size = 22 }: { initials: string; size?: number }) {
  return (
    <span style={{ width: size, height: size, minWidth: size, fontSize: size > 30 ? 13 : 10 }} className="inline-flex items-center justify-center rounded-full bg-avatar font-semibold text-zinc-300">
      {initials}
    </span>
  )
}

export function MethodBadge({ method }: { method: string }) {
  const tone = method === 'GET' ? 'text-green-400 bg-green-500/10 border-green-500/25' : method === 'DELETE' ? 'text-red-400 bg-red-500/10 border-red-500/25' : 'text-sky-400 bg-sky-500/10 border-sky-500/25'
  return <span className={cx('inline-block min-w-12 rounded-[5px] border px-2 py-0.5 text-center font-mono text-2xs font-semibold', tone)}>{method}</span>
}

/* ------------------------------------------------------------------ */
/* Layout pieces                                                       */
/* ------------------------------------------------------------------ */
export function Breadcrumb({ items }: { items: { label: string; to?: string }[] }) {
  return (
    <div className="text-xs text-zinc-500">
      {items.map((it, i) => (
        <span key={i}>
          {i > 0 && ' / '}
          {it.to ? (
            <Link to={it.to} className="text-zinc-500 hover:text-zinc-300">
              {it.label}
            </Link>
          ) : (
            it.label
          )}
        </span>
      ))}
    </div>
  )
}

export function PageTitle({ children, actions, sub }: { children: ReactNode; actions?: ReactNode; sub?: ReactNode }) {
  return (
    <div className="mt-1 flex items-center justify-between gap-4">
      <div className="flex min-w-0 items-center gap-3">
        <h1 className="m-0 text-lg font-semibold tracking-[-0.01em]">{children}</h1>
        {sub}
      </div>
      {actions && <div className="flex shrink-0 gap-2">{actions}</div>}
    </div>
  )
}

export function Tabs({ tabs }: { tabs: { to: string; label: ReactNode; end?: boolean }[] }) {
  return (
    <nav className="mt-5 flex gap-1 overflow-x-auto border-b border-line">
      {tabs.map((t) => (
        <NavLink
          key={t.to}
          to={t.to}
          end={t.end}
          className={({ isActive }) =>
            cx('-mb-px px-3 py-2 text-[13px] whitespace-nowrap', isActive ? 'border-b-2 border-brass font-semibold text-zinc-100 hover:text-zinc-100' : 'text-zinc-400 hover:text-zinc-200')
          }
        >
          {t.label}
        </NavLink>
      ))}
    </nav>
  )
}

export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cx('rounded-[10px] border border-edge bg-panel', className)}>{children}</div>
}

export function SectionLabel({ children, className, action }: { children: ReactNode; className?: string; action?: ReactNode }) {
  return (
    <div className={cx('flex items-center justify-between', className)}>
      <div className="eyebrow">{children}</div>
      {action}
    </div>
  )
}

export function Callout({ tone, children, className }: { tone: 'amber' | 'red' | 'green' | 'brass' | 'neutral'; children: ReactNode; className?: string }) {
  const t = {
    amber: 'bg-amber-500/[0.06] border-amber-500/30 text-amber-400',
    red: 'bg-red-500/[0.06] border-red-500/30 text-red-400',
    green: 'bg-green-500/[0.06] border-green-500/30 text-green-400',
    brass: 'bg-brass/[0.06] border-brass/30 text-brass-light',
    neutral: 'bg-rail border-edge text-zinc-400',
  }[tone]
  return <div className={cx('rounded-lg border px-3.5 py-2.5 text-xs leading-relaxed', t, className)}>{children}</div>
}

/* ------------------------------------------------------------------ */
/* Table (CSS grid, same look everywhere)                              */
/* ------------------------------------------------------------------ */
export function Table({ cols, head, children, className }: { cols: string; head: ReactNode[]; children: ReactNode; className?: string }) {
  return (
    <div className={cx('overflow-x-auto rounded-[10px] border border-edge bg-panel', className)}>
      <div className="min-w-fit">
        <div className="grid gap-3 border-b border-line px-4 py-2.5" style={{ gridTemplateColumns: cols }}>
          {head.map((h, i) => (
            <div key={i} className="th">
              {h}
            </div>
          ))}
        </div>
        <div className="[&>*:last-child]:border-b-0">{children}</div>
      </div>
    </div>
  )
}
/** Enter/Space on the focused element itself (not a button or input inside it). */
export function activateOnKey(onActivate: () => void) {
  return (e: ReactKeyboardEvent<HTMLElement>) => {
    if (e.target !== e.currentTarget || (e.key !== 'Enter' && e.key !== ' ')) return
    e.preventDefault()
    onActivate()
  }
}
export const FOCUS_RING = 'outline-none focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-zinc-500'

export function Row({ cols, children, className, onClick }: { cols: string; children: ReactNode; className?: string; onClick?: () => void }) {
  return (
    <div
      onClick={onClick}
      role={onClick ? 'link' : undefined}
      tabIndex={onClick ? 0 : undefined}
      onKeyDown={onClick ? activateOnKey(onClick) : undefined}
      className={cx('grid items-center gap-3 border-b border-line px-4 py-[13px] text-[13px]', onClick && cx('cursor-pointer hover:bg-white/[0.015]', FOCUS_RING), className)}
      style={{ gridTemplateColumns: cols }}
    >
      {children}
    </div>
  )
}

export function SkeletonRows({ cols, n = 3 }: { cols: string; n?: number }) {
  const count = cols.split(' ').length
  return (
    <>
      {Array.from({ length: n }).map((_, r) => (
        <div key={r} className="grid gap-3 border-b border-line px-4 py-4" style={{ gridTemplateColumns: cols }} aria-hidden>
          {Array.from({ length: count }).map((_, c) => (
            <div key={c} className="skeleton h-3" style={{ width: `${50 + ((r * 7 + c * 13) % 40)}%` }} />
          ))}
        </div>
      ))}
    </>
  )
}

export function Empty({ children, action, className }: { children: ReactNode; action?: ReactNode; className?: string }) {
  return (
    <div className={cx('rounded-[10px] border border-edge bg-panel p-10 text-center', className)}>
      <div className="text-[13px] text-zinc-400">{children}</div>
      {action && <div className="mt-4 flex justify-center gap-2">{action}</div>}
    </div>
  )
}

export function ErrorBox({ what, onRetry }: { what: string; onRetry: () => void }) {
  return (
    <div className="flex items-center justify-between gap-4 rounded-[10px] border border-red-500/30 bg-red-500/[0.04] px-4 py-3.5">
      <div>
        <div className="text-[13px] font-semibold text-red-400">Couldn’t load {what}</div>
        <div className="mt-0.5 text-xs text-zinc-400">The request to Keyhole timed out after 10 s. Nothing was changed.</div>
      </div>
      <Button size="sm" onClick={onRetry}>
        Retry
      </Button>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Overlays                                                            */
/* ------------------------------------------------------------------ */
/**
 * Open overlays, oldest first. Escape only ever reaches the topmost one, so a
 * preview stacked on a modal closes itself and leaves the modal (and its focus) alone.
 */
const escapeStack: { current: () => void }[] = []
if (typeof window !== 'undefined')
  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !e.defaultPrevented) escapeStack[escapeStack.length - 1]?.current()
  })

function useEscape(open: boolean, onClose: () => void) {
  const latest = useRef(onClose)
  useEffect(() => {
    latest.current = onClose
  })
  useEffect(() => {
    if (!open) return
    const entry = { current: () => latest.current() }
    escapeStack.push(entry)
    return () => void escapeStack.splice(escapeStack.indexOf(entry), 1)
  }, [open])
}

/** The last focus move, so a dialog knows what opened it even when autoFocus got there first. */
let lastFocus: { target: Element | null; from: Element | null } = { target: null, from: null }
if (typeof document !== 'undefined')
  document.addEventListener('focusin', (e) => void (lastFocus = { target: e.target as Element, from: e.relatedTarget as Element | null }), true)

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/**
 * Keeps Tab inside an open dialog and hands focus back to whatever opened it.
 * Returns the keydown handler to put on the dialog element.
 */
function useFocusTrap(ref: RefObject<HTMLElement | null>, open: boolean) {
  useEffect(() => {
    if (!open) return
    const el = ref.current
    const opener = (el && lastFocus.target && el.contains(lastFocus.target) ? lastFocus.from : document.activeElement) as HTMLElement | null
    // autoFocus inside the dialog wins; otherwise focus the dialog itself.
    if (el && !el.contains(document.activeElement)) el.focus()
    return () => {
      if (opener?.isConnected) opener.focus()
    }
  }, [open, ref])
  return (e: ReactKeyboardEvent<HTMLElement>) => {
    if (e.key !== 'Tab' || !ref.current) return
    const items = Array.from(ref.current.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((x) => x.offsetParent !== null || x === document.activeElement)
    if (!items.length) return e.preventDefault()
    const first = items[0]
    const last = items[items.length - 1]
    if (e.shiftKey && (document.activeElement === first || document.activeElement === ref.current)) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }
}

export function CloseX({ onClick }: { onClick: () => void }) {
  return (
    <button type="button" aria-label="Close" onClick={onClick} className="text-sm text-zinc-600 hover:text-zinc-300">
      ✕
    </button>
  )
}

export function Modal({ open, onClose, width = 480, children, title, dismissable = true }: { open: boolean; onClose: () => void; width?: number; children: ReactNode; title?: ReactNode; dismissable?: boolean }) {
  const id = useId()
  const ref = useRef<HTMLDivElement>(null)
  // A non-dismissable modal still takes the top slot, so Escape doesn't close what's under it.
  useEscape(open, dismissable ? onClose : () => {})
  const trap = useFocusTrap(ref, open)
  if (!open) return null
  return createPortal(
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-canvas/65 p-4" onMouseDown={(e) => dismissable && e.target === e.currentTarget && onClose()}>
      <div ref={ref} tabIndex={-1} onKeyDown={trap} role="dialog" aria-modal aria-labelledby={title ? id : undefined} style={{ width }} className="flex outline-none max-h-[calc(100vh-2rem)] max-w-full flex-col gap-4 overflow-y-auto rounded-xl border border-edge bg-panel p-7 text-zinc-100 shadow-[0_24px_64px_rgba(0,0,0,0.5)]">
        {title && (
          <div className="flex items-center justify-between gap-4">
            <div id={id} className="text-[15px] font-semibold">
              {title}
            </div>
            {dismissable && <CloseX onClick={onClose} />}
          </div>
        )}
        {children}
      </div>
    </div>,
    document.body,
  )
}

export function SlideOver({ open, onClose, width = 480, title, children, footer }: { open: boolean; onClose: () => void; width?: number; title: ReactNode; children: ReactNode; footer?: ReactNode }) {
  const id = useId()
  const ref = useRef<HTMLElement>(null)
  useEscape(open, onClose)
  const trap = useFocusTrap(ref, open)
  if (!open) return null
  return createPortal(
    <div className="fixed inset-0 z-40">
      <div className="absolute inset-0 bg-canvas/60" onClick={onClose} />
      <aside ref={ref} tabIndex={-1} onKeyDown={trap} role="dialog" aria-modal aria-labelledby={id} style={{ width }} className="absolute outline-none top-0 right-0 bottom-0 flex max-w-full flex-col border-l border-edge bg-panel shadow-[-24px_0_48px_rgba(0,0,0,0.4)]">
        <div className="flex items-center justify-between px-8 pt-7">
          <div id={id} className="text-[15px] font-semibold">
            {title}
          </div>
          <CloseX onClick={onClose} />
        </div>
        <div className="flex flex-1 flex-col gap-[18px] overflow-y-auto px-8 pt-[18px] pb-7">{children}</div>
        {footer && <div className="flex justify-end gap-2 border-t border-line px-8 py-4">{footer}</div>}
      </aside>
    </div>,
    document.body,
  )
}

export function Footer({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cx('flex justify-end gap-2 border-t border-line pt-4', className)}>{children}</div>
}

/** Tiny popover menu for row actions. */
export function Menu({ items, label = 'Actions' }: { items: ({ label: string; onClick: () => void; danger?: boolean; disabled?: boolean } | null)[]; label?: string }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setOpen(false)
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [open])
  return (
    <div ref={ref} className="relative inline-block" onClick={(e) => e.stopPropagation()}>
      <button ref={trigger} type="button" aria-label={label} aria-expanded={open} onClick={() => setOpen(!open)} className="rounded-md px-2 py-0.5 text-zinc-500 hover:bg-line hover:text-zinc-200">
        ⋯
      </button>
      {open && (
        <div className="absolute top-full right-0 z-30 mt-1 min-w-44 rounded-lg border border-edge bg-panel p-1 shadow-xl">
          {items.filter(Boolean).map((it) => (
            <button
              key={it!.label}
              type="button"
              disabled={it!.disabled}
              onClick={() => {
                // Park focus on the trigger so a dialog opened from here can hand it back.
                trigger.current?.focus()
                setOpen(false)
                it!.onClick()
              }}
              className={cx('block w-full rounded-md px-3 py-1.5 text-left text-[13px] disabled:opacity-40', it!.danger ? 'text-red-400 hover:bg-red-500/10' : 'text-zinc-300 hover:bg-line')}
            >
              {it!.label}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

/** Delay-based loading simulation, respecting the demo list-state override. */
export function useFakeLoad(ms = 350) {
  const [ready, setReady] = useState(false)
  useEffect(() => {
    const t = setTimeout(() => setReady(true), ms)
    return () => clearTimeout(t)
  }, [ms])
  return ready
}
