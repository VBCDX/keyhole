// Ported from mocks/v1/src/components/AppShell.tsx. The invitation banner
// waits for the invitations API.
import { Outlet } from 'react-router-dom'
import { Sidebar } from './Sidebar'

export function AppShell() {
  return (
    <div className="flex h-full bg-page text-zinc-100">
      <Sidebar />
      <main className="min-w-0 flex-1 overflow-y-auto px-10 py-8">
        <Outlet />
      </main>
    </div>
  )
}
