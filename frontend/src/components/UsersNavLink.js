import React, { useEffect, useState } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
import { useAuth } from '../auth/AuthContext';
import { apiFetch } from '../api';

const IconUsers = () => (
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
    <circle cx="9" cy="7" r="4" />
    <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
    <path d="M16 3.13a4 4 0 0 1 0 7.75" />
  </svg>
);

// Sidebar link to the admin-only Users page, with a badge showing how many
// accounts are waiting for approval. The count is refetched whenever the
// visible route changes.
export default function UsersNavLink({ onClick }) {
  const { user, providers } = useAuth();
  const location = useLocation();
  const [pending, setPending] = useState(0);

  // Auth disabled means the anonymous superuser, like the backend treats
  // it; otherwise the signed-in user must be an admin.
  const isAdmin =
    (providers && providers.enabled === false) || (user && user.is_admin);

  useEffect(() => {
    if (!isAdmin) return undefined;
    let cancelled = false;
    (async () => {
      try {
        const res = await apiFetch('/admin/users');
        if (!res.ok || cancelled) return;
        const users = await res.json();
        if (!cancelled) {
          setPending(users.filter((u) => u.status === 'pending').length);
        }
      } catch (err) {
        // Keep the last count (or 0) on a failed refresh.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [isAdmin, location.pathname]);

  if (!isAdmin) return null;

  return (
    <NavLink
      to="/admin/users"
      title="Users"
      onClick={onClick}
      className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}
    >
      <IconUsers /> <span className="nav-label">Users</span>
      {pending > 0 && <span className="nav-badge">{pending}</span>}
    </NavLink>
  );
}
