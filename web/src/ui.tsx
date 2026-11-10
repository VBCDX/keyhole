// A few primitives from the mock (mocks/v1/src/components/ui.tsx), trimmed
// to what the sign-in screens use.
import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from 'react'

export const cx = (...c: (string | false | null | undefined)[]) => c.filter(Boolean).join(' ')

type BtnVariant = 'primary' | 'secondary' | 'ghost'
const BTN: Record<BtnVariant, string> = {
  primary: 'bg-zinc-50 text-zinc-900 font-semibold hover:bg-white border border-transparent',
  secondary: 'bg-transparent text-zinc-300 border border-zinc-700 hover:bg-white/[0.03] hover:text-zinc-100',
  ghost: 'bg-transparent text-zinc-400 hover:text-zinc-100 border border-transparent',
}
const BTN_BASE =
  'inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-lg px-3.5 py-2 text-[13px] transition-colors disabled:opacity-40'

export function Button({ variant = 'secondary', className, ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: BtnVariant }) {
  return <button {...rest} className={cx(BTN_BASE, BTN[variant], className)} />
}

export function ButtonLink({ variant = 'secondary', className, ...rest }: React.AnchorHTMLAttributes<HTMLAnchorElement> & { variant?: BtnVariant }) {
  return <a {...rest} className={cx(BTN_BASE, BTN[variant], 'hover:!text-zinc-100', className)} />
}

export function Field({ label, htmlFor, children }: { label: ReactNode; htmlFor: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={htmlFor} className="text-xs font-medium text-zinc-300">
        {label}
      </label>
      {children}
    </div>
  )
}

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...props}
      className="w-full rounded-lg border border-zinc-700 bg-page px-3 py-[9px] text-[13px] text-zinc-100 placeholder:text-zinc-600 outline-none focus:border-zinc-500"
    />
  )
}

export function KeyholeMark({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" aria-hidden className="shrink-0">
      <circle cx="12" cy="8.5" r="4.5" stroke="#c9a24b" strokeWidth="1.6" />
      <path d="M10.4 12.4 L9 19.5 H15 L13.6 12.4" stroke="#c9a24b" strokeWidth="1.6" strokeLinejoin="round" />
    </svg>
  )
}

export function Card({ children }: { children: ReactNode }) {
  return (
    <div className="flex h-full items-center justify-center bg-page p-6 text-zinc-100">
      <div className="w-[420px] max-w-full rounded-xl border border-edge bg-panel p-7">{children}</div>
    </div>
  )
}
