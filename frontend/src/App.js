import React, { useState, useEffect } from 'react';
import { BrowserRouter as Router, Routes, Route, NavLink, Navigate, useLocation } from 'react-router-dom';
import { AuthProvider, useAuth } from './auth/AuthContext';
import Downloads from './pages/Downloads';
import Library from './pages/Library';
import Metadata from './pages/Metadata';
import Jobs from './pages/Jobs';
import Settings from './pages/Settings';
import Login from './pages/Login';
import Register from './pages/Register';

const IconDownload = () => <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><polyline points="7 10 12 15 17 10"></polyline><line x1="12" y1="15" x2="12" y2="3"></line></svg>;
const IconLibrary = () => <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"></path><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"></path></svg>;
const IconJobs = () => <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="10"></circle><polyline points="12 6 12 12 16 14"></polyline></svg>;
const IconSettings = () => <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="3"></circle><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"></path></svg>;
const IconMenu = () => <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><line x1="3" y1="6" x2="21" y2="6"></line><line x1="3" y1="12" x2="21" y2="12"></line><line x1="3" y1="18" x2="21" y2="18"></line></svg>;
const Logo = () => <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M9 18V5l12-2v13"></path><circle cx="6" cy="18" r="3"></circle><circle cx="18" cy="16" r="3"></circle></svg>;

const getPageTitle = (pathname) => {
  if (pathname === '/downloads') return 'Downloads';
  if (pathname === '/jobs') return 'Jobs';
  if (pathname === '/settings') return 'Settings';
  if (pathname.startsWith('/library')) return 'Library';
  return 'JellyIDE';
};

// Guard for protected pages: shows a loader while the session is being
// loaded, skips the check entirely when auth is disabled, and redirects
// to /login (remembering where the user was going) when there is no
// session.
function Guarded({ children }) {
  const { user, providers, loading } = useAuth();
  const location = useLocation();

  if (loading) {
    return (
      <div className="auth-loader">
        <span className="spinner" aria-hidden="true" />
        <span>Loading…</span>
      </div>
    );
  }
  if (!providers || providers.enabled === false) return children;
  if (!user) {
    return <Navigate to="/login" state={{ from: location.pathname }} replace />;
  }
  return children;
}

// Public login route: already-logged-in users go straight to Downloads.
function LoginRoute() {
  const { user, loading } = useAuth();
  if (!loading && user) return <Navigate to="/downloads" replace />;
  return <Login />;
}

// Public registration route: already-logged-in users go straight to
// Downloads; Register itself bounces to /login when self sign-up is off.
function RegisterRoute() {
  const { user, loading } = useAuth();
  if (!loading && user) return <Navigate to="/downloads" replace />;
  return <Register />;
}

function Shell() {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const location = useLocation();
  const { user, providers, logout } = useAuth();

  const showUser = Boolean(user) && Boolean(providers) && providers.enabled !== false;

  // Close the drawer on navigation
  useEffect(() => {
    setDrawerOpen(false);
  }, [location.pathname]);

  // Close the drawer on Escape
  useEffect(() => {
    const handleKeyDown = (e) => {
      if (e.key === 'Escape') setDrawerOpen(false);
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, []);

  return (
    <div className="app-container">
      <header className="mobile-topbar">
        <button
          type="button"
          className="menu-btn"
          aria-label="Open menu"
          onClick={() => setDrawerOpen(true)}
        >
          <IconMenu />
        </button>
        <div className="topbar-logo">
          <Logo />
          JellyIDE
        </div>
        <div className="topbar-title">{getPageTitle(location.pathname)}</div>
      </header>

      <aside className={`sidebar ${drawerOpen ? 'open' : ''}`}>
        <div className="sidebar-logo">
          <Logo />
          <span className="logo-text">JellyIDE</span>
        </div>
        <nav className="sidebar-nav">
          <NavLink to="/downloads" title="Downloads" onClick={() => setDrawerOpen(false)} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
            <IconDownload /> <span className="nav-label">Downloads</span>
          </NavLink>
          <NavLink to="/library" title="Library" onClick={() => setDrawerOpen(false)} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
            <IconLibrary /> <span className="nav-label">Library</span>
          </NavLink>
          <NavLink to="/jobs" title="Jobs" onClick={() => setDrawerOpen(false)} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
            <IconJobs /> <span className="nav-label">Jobs</span>
          </NavLink>
        </nav>

        <div className="sidebar-footer">
          <hr className="sidebar-divider" />
          {showUser && (
            <div className="sidebar-user">
              <div className="sidebar-user-name" title={user.username}>
                {user.display_name || user.username}
              </div>
              <button
                type="button"
                className="btn-secondary sidebar-logout"
                title="Logout"
                onClick={() => logout()}
              >
                Logout
              </button>
            </div>
          )}
          <NavLink to="/settings" title="Settings" onClick={() => setDrawerOpen(false)} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
            <IconSettings /> <span className="nav-label">Settings</span>
          </NavLink>
        </div>
      </aside>

      {drawerOpen && (
        <div className="sidebar-backdrop" onClick={() => setDrawerOpen(false)} aria-hidden="true" />
      )}

      <main className="main-content">
        <Routes>
          <Route path="/" element={<Navigate to="/downloads" replace />} />
          <Route path="/downloads" element={<Downloads />} />
          <Route path="/library" element={<Library />} />
          <Route path="/library/:artistId" element={<Library />} />
          <Route path="/library/:artistId/:albumId" element={<Metadata />} />
          <Route path="/jobs" element={<Jobs />} />
          <Route path="/settings" element={<Settings />} />
        </Routes>
      </main>
    </div>
  );
}

function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<LoginRoute />} />
      <Route path="/register" element={<RegisterRoute />} />
      <Route
        path="/*"
        element={
          <Guarded>
            <Shell />
          </Guarded>
        }
      />
    </Routes>
  );
}

function App() {
  return (
    <Router>
      <AuthProvider>
        <AppRoutes />
      </AuthProvider>
    </Router>
  );
}

export default App;
