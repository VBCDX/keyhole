// The signed-in user, their organizations and the one they're working in.
// The current organization is remembered per browser; it falls back to the
// first one they're active in.
import { createContext, useContext, useState, type ReactNode } from 'react'
import { api, type Me, type Org, type SuiteApp } from './api'

const ORG_KEY = 'keyhole.org'

type Session = {
  me: Me
  /** Memberships the switcher shows: active and suspended (not pending invitations). */
  orgs: Org[]
  current: Org | null
  setOrg: (id: string) => void
  /** The VBCDX suite's apps for the app switcher (SUITE_APPS); empty hides it. */
  apps: SuiteApp[]
  signOut: () => Promise<void>
}

const SessionContext = createContext<Session | null>(null)

export function useSession(): Session {
  const s = useContext(SessionContext)
  if (!s) throw new Error('useSession outside SessionProvider')
  return s
}

export function isAdmin(o: Org | null | undefined) {
  return o?.status === 'active' && (o.role === 'Owner' || o.role === 'userAdmin')
}

function initialOrg(orgs: Org[]): string | null {
  const saved = localStorage.getItem(ORG_KEY)
  if (saved && orgs.some((o) => o.id === saved)) return saved
  return (orgs.find((o) => o.status === 'active') ?? orgs[0])?.id ?? null
}

export function SessionProvider({ me, orgs: all, apps, children }: { me: Me; orgs: Org[]; apps: SuiteApp[]; children: ReactNode }) {
  const orgs = all.filter((o) => o.status !== 'invited')
  const [currentId, setCurrentId] = useState(() => initialOrg(orgs))
  const value: Session = {
    me,
    orgs,
    apps,
    current: orgs.find((o) => o.id === currentId) ?? null,
    setOrg: (id) => {
      localStorage.setItem(ORG_KEY, id)
      setCurrentId(id)
    },
    signOut: async () => {
      const { redirect_url } = await api.logout(me.csrf_token)
      window.location.assign(redirect_url)
    },
  }
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
}
