import { useEffect, useState } from 'react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { useAuth } from '../auth/AuthContext';
import { useUnits } from '../lib/UnitsContext';
import { Button } from './Button';

const NAV: { to: string; label: string }[] = [
  { to: '/', label: 'Dashboard' },
  { to: '/stations', label: 'Stations' },
  { to: '/radios', label: 'Radios' },
  { to: '/account', label: 'WxAlerts account' },
  { to: '/uplink', label: 'Uplink' },
  { to: '/updates', label: 'Updates' },
  { to: '/network', label: 'Network' },
  { to: '/home-assistant', label: 'Home Assistant' },
  { to: '/logs', label: 'Logs' },
  { to: '/about', label: 'About' },
];

export function Layout() {
  const { username, logout } = useAuth();
  const { system, setSystem } = useUnits();
  const [open, setOpen] = useState(false);
  const location = useLocation();

  // Close the mobile menu on navigation.
  useEffect(() => {
    setOpen(false);
  }, [location.pathname]);

  return (
    <div className="shell">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="topbar">
        <button
          type="button"
          className="btn btn-ghost nav-toggle"
          aria-expanded={open}
          aria-controls="sidenav"
          onClick={() => setOpen((v) => !v)}
        >
          Menu
        </button>
        <span className="brand">PWS Gateway</span>
        <div className="topbar-right">
          <label className="units-toggle">
            <span className="visually-hidden">Units</span>
            <select
              className="input input-small"
              value={system}
              onChange={(e) => setSystem(e.target.value === 'us' ? 'us' : 'metric')}
              aria-label="Display units"
            >
              <option value="metric">Metric</option>
              <option value="us">US units</option>
            </select>
          </label>
          <span className="muted username" title="Signed in as">
            {username}
          </span>
          <Button variant="ghost" onClick={() => void logout()}>
            Sign out
          </Button>
        </div>
      </header>
      <nav id="sidenav" className={`sidenav${open ? ' open' : ''}`} aria-label="Main">
        <ul>
          {NAV.map((item) => (
            <li key={item.to}>
              <NavLink
                to={item.to}
                end={item.to === '/'}
                className={({ isActive }) => (isActive ? 'active' : undefined)}
              >
                {item.label}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>
      <main id="main" className="content" tabIndex={-1}>
        <Outlet />
      </main>
    </div>
  );
}
