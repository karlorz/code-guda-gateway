import { NavLink, Outlet } from 'react-router-dom';
import { Activity, BarChart3, Gauge, KeyRound, LogOut, ServerCog, Settings, ShieldCheck } from 'lucide-react';
import { useSession } from '../api/session';

const navItems = [
  { to: '/', label: 'Overview', icon: Gauge },
  { to: '/gateway-keys', label: 'Gateway Keys', icon: KeyRound },
  { to: '/provider-keys', label: 'Provider Keys', icon: ShieldCheck },
  { to: '/providers', label: 'Providers', icon: ServerCog },
  { to: '/usage', label: 'Usage', icon: BarChart3 },
  { to: '/audit', label: 'Audit', icon: Activity },
  { to: '/settings', label: 'Settings', icon: Settings },
];

export function AdminLayout() {
  const { logout } = useSession();
  return (
    <div className="min-h-screen bg-zinc-50 text-zinc-950">
      <aside className="fixed inset-y-0 left-0 hidden w-60 border-r border-zinc-200 bg-white px-3 py-4 md:block">
        <div className="mb-5 px-2 text-sm font-semibold">GuDa Gateway</div>
        <nav className="grid gap-1">
          {navItems.map((item) => {
            const Icon = item.icon;
            return (
              <NavLink
                className={({ isActive }) =>
                  `flex items-center gap-2 rounded-md px-2 py-2 text-sm ${isActive ? 'bg-zinc-950 text-white' : 'text-zinc-700 hover:bg-zinc-100'}`
                }
                end={item.to === '/'}
                key={item.to}
                to={item.to}
              >
                <Icon size={16} />
                {item.label}
              </NavLink>
            );
          })}
        </nav>
        <button className="absolute bottom-4 left-3 right-3 flex items-center gap-2 rounded-md px-2 py-2 text-sm hover:bg-zinc-100" onClick={logout} type="button">
          <LogOut size={16} />
          Log out
        </button>
      </aside>
      <main className="mx-auto max-w-6xl px-4 py-4 md:ml-60 md:px-8">
        <Outlet />
      </main>
    </div>
  );
}
