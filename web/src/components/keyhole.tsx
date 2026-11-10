// From the mock (mocks/v1/src/components/keyhole.tsx): the icon and the
// hidden-value dots.
import { cx } from './ui'

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

/** After save: the value is gone — only dots remain. No reveal, anywhere. */
export function LockedDots({ dim, brass }: { dim?: boolean; brass?: boolean }) {
  // Always eight dots: the length of a stored value isn't disclosed either.
  return (
    <span aria-label="Hidden value" className={cx('masked text-xs', brass ? 'text-brass' : dim ? 'text-zinc-600' : 'text-zinc-500')}>
      ••••••••
    </span>
  )
}
