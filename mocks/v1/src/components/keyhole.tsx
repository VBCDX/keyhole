import { useState, type ReactNode } from 'react'
import { Button, cx, useFieldId } from './ui'

/* ------------------------------------------------------------------ */
/* Keyhole status icon — the one signature flourish.                  */
/* open: outline (waiting), closed: filled + turns (success).         */
/* ------------------------------------------------------------------ */
export function KeyholeIcon({
  state = 'open',
  size = 22,
  pulse,
  turn,
  color,
  className,
}: {
  state?: 'open' | 'closed' | 'error'
  size?: number
  pulse?: boolean
  turn?: boolean
  color?: string
  className?: string
}) {
  const c = color ?? (state === 'error' ? '#f87171' : '#c9a24b')
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden
      className={cx('shrink-0', pulse && 'animate-khpulse', turn && 'animate-kh-turn', className)}
      style={{ transformOrigin: '50% 50%' }}
    >
      {state === 'closed' ? (
        <>
          <circle cx="12" cy="8.5" r="4.5" fill={c} />
          <path d="M10.4 11.4 L9 19.5 H15 L13.6 11.4 Z" fill={c} />
        </>
      ) : (
        <>
          <circle cx="12" cy="8.5" r="4.5" stroke={c} strokeWidth="1.6" />
          <path d="M10.4 12.4 L9 19.5 H15 L13.6 12.4" stroke={c} strokeWidth="1.6" strokeLinejoin="round" />
        </>
      )}
    </svg>
  )
}

function CopyGlyph({ size = 12, color = '#c9a24b' }: { size?: number; color?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" style={{ minWidth: size }} aria-hidden>
      <rect x="9" y="9" width="11" height="11" rx="2" />
      <path d="M5 15V5a2 2 0 0 1 2-2h10" />
    </svg>
  )
}
function CheckGlyph({ size = 12, color = '#4ade80' }: { size?: number; color?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2.6" style={{ minWidth: size }} aria-hidden>
      <path d="M5 12.5l4.5 4.5L19 7.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

export async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const ta = document.createElement('textarea')
    ta.value = text
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    ta.remove()
  }
}

/* ------------------------------------------------------------------ */
/* Copy chip — brass-tinted monospace that copies on click.           */
/* ------------------------------------------------------------------ */
export function CopyChip({
  value,
  display,
  variant = 'inline',
  neutral,
  className,
}: {
  value: string
  display?: ReactNode
  /** inline: tracking codes; block: addresses; command: long wrapping commands */
  variant?: 'inline' | 'block' | 'command'
  /** Neutral styling for non-credential values (e.g. allow-list IPs). */
  neutral?: boolean
  className?: string
}) {
  const [copied, setCopied] = useState(0)
  const onClick = (e: React.MouseEvent) => {
    e.stopPropagation()
    copyText(value)
    setCopied((n) => n + 1)
    window.setTimeout(() => setCopied(0), 1200)
  }
  const tone = neutral ? 'text-zinc-200 bg-panel border-zinc-700 hover:border-zinc-500' : 'text-brass-light bg-brass/[0.07] border-brass/30 hover:border-brass/60'
  const shape = {
    inline: 'rounded-[5px] px-2 py-0.5 text-2xs gap-1.5',
    block: 'rounded-[7px] px-3 py-[7px] text-sm2 gap-2 self-start',
    command: 'rounded-[7px] px-3 py-[9px] text-xs2 gap-2 w-full justify-between text-left',
  }[variant]
  return (
    <button type="button" onClick={onClick} title="Copy" aria-label={`Copy ${value}`} className={cx('relative inline-flex items-center border font-mono transition-colors', tone, shape, className)}>
      <span className={cx(variant === 'command' && 'break-all')}>{display ?? value}</span>
      {copied ? <CheckGlyph size={variant === 'inline' ? 10 : 12} /> : <CopyGlyph size={variant === 'inline' ? 10 : 12} color={neutral ? '#a1a1aa' : '#c9a24b'} />}
      {copied > 0 && (
        <span key={copied} className="pointer-events-none absolute -top-6 right-0 animate-copied rounded bg-zinc-100 px-1.5 py-0.5 font-sans text-[10.5px] font-semibold text-zinc-900">
          Copied
        </span>
      )}
    </button>
  )
}

/* ------------------------------------------------------------------ */
/* Paste-a-secret field + its locked-dots after-state.                */
/* The value lives only in the DOM input; the app only learns length. */
/* ------------------------------------------------------------------ */
export const SECRET_LINE = 'Values are encrypted and never shown again.'

export function SecretField({
  id,
  onLength,
  placeholder = 'Paste the value',
  compact,
  prefix,
  autoFocus,
}: {
  id?: string
  onLength: (n: number) => void
  placeholder?: string
  compact?: boolean
  prefix?: ReactNode
  autoFocus?: boolean
}) {
  const [len, setLen] = useState(0)
  const inputId = useFieldId(id)
  return (
    <div className={cx('flex items-center justify-between gap-3 rounded-lg border bg-secret-bg px-3', compact ? 'py-2' : 'py-2.5', len ? 'border-brass/35' : 'border-brass/20 focus-within:border-brass/50')}>
      {prefix}
      <input
        id={inputId}
        type="password"
        autoComplete="off"
        autoFocus={autoFocus}
        spellCheck={false}
        data-1p-ignore
        data-lpignore="true"
        placeholder={placeholder}
        onChange={(e) => {
          setLen(e.target.value.length)
          onLength(e.target.value.length)
        }}
        className="masked min-w-0 flex-1 bg-transparent text-[13px] text-brass outline-none placeholder:font-sans placeholder:tracking-normal placeholder:text-zinc-600"
      />
      <KeyholeIcon state={len ? 'closed' : 'open'} size={14} />
    </div>
  )
}

/** After save: the value is gone — only dots remain. No reveal, anywhere. */
export function LockedDots({ dim, brass }: { dim?: boolean; brass?: boolean }) {
  // Always eight dots: the length of a stored value isn't disclosed either.
  return (
    <span aria-label="Hidden value" className={cx('masked text-xs', brass ? 'text-brass' : dim ? 'text-zinc-600' : 'text-zinc-500')}>
      ••••••••
    </span>
  )
}

/* ------------------------------------------------------------------ */
/* One-time token panel.                                               */
/* ------------------------------------------------------------------ */
export function TokenPanel({ token, title, subtitle, note, onDone }: { token: string; title: string; subtitle: ReactNode; note?: ReactNode; onDone: () => void }) {
  const [copied, setCopied] = useState(false)
  return (
    <>
      <div>
        <div className="flex items-center gap-2.5">
          <KeyholeIcon state="closed" size={18} turn />
          <div className="text-[15px] font-semibold">{title}</div>
        </div>
        <div className="mt-1 text-[13px] text-zinc-400">{subtitle}</div>
      </div>
      <div className="rounded-[10px] border border-brass/35 bg-brass/[0.06] p-5">
        <div className="text-2xs font-semibold tracking-[0.08em] text-brass uppercase">Token — shown once</div>
        <div className="mt-3 font-mono text-base leading-normal tracking-[0.06em] break-all text-brass-pale select-all">{token}</div>
        <Button
          variant="brass"
          className="mt-4 w-full py-2.5 text-md"
          onClick={() => {
            copyText(token)
            setCopied(true)
            window.setTimeout(() => setCopied(false), 1400)
          }}
        >
          {copied ? (
            <span className="inline-flex animate-copied items-center gap-2 [animation-duration:1.4s]">
              <CheckGlyph color="#1a1405" /> Copied
            </span>
          ) : (
            'Copy token'
          )}
        </Button>
        <div className="mt-3 text-center text-xs text-brass">Store this now — it won't be shown again.</div>
        {note && <div className="mt-2 text-center text-xs2 text-zinc-400">{note}</div>}
      </div>
      <Button className="w-full py-2.5" onClick={onDone}>
        I've stored it
      </Button>
    </>
  )
}
