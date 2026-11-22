import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
} from 'react';
import { apiFetch } from '../api';

const AuthContext = createContext(null);

// Used when GET /auth/providers cannot be read: treating auth as
// disabled (rather than leaving providers null) keeps the login page
// from spinning on its loader forever.
const FALLBACK_PROVIDERS = {
  enabled: false,
  local: false,
  oidc: { enabled: false, name: '' },
  saml: { enabled: false, name: '' },
};

async function readJson(res) {
  if (!res.ok) return null;
  try {
    return await res.json();
  } catch (err) {
    return null;
  }
}

// AuthProvider loads the auth providers and the current session on mount.
// State: { user, providers, loading }.
//
// - login(username, password) POSTs /auth/login and throws Error(message)
//   on failure; the session cookie is handled by the browser.
// - logout() POSTs /auth/logout and clears the local user.
// - refresh() reloads providers and the current session.
// - A global 'auth:unauthorized' event (raised by apiFetch on a 401)
//   revalidates the session via /auth/me and only drops it if that also
//   401s: some endpoints answer 401 for other reasons while the session
//   is still valid (e.g. /auth/password with a wrong current password).
export function AuthProvider({ children }) {
  const [user, setUser] = useState(null);
  const [providers, setProviders] = useState(null);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const [providersData, userData] = await Promise.all([
        apiFetch('/auth/providers').then(readJson),
        apiFetch('/auth/me').then(readJson),
      ]);
      setProviders(providersData || FALLBACK_PROVIDERS);
      setUser(userData);
    } catch (err) {
      console.error('Failed to load auth state', err);
      setProviders(FALLBACK_PROVIDERS);
      setUser(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  useEffect(() => {
    const onUnauthorized = async () => {
      // A 401 on a protected call does not prove the session is dead:
      // /auth/password returns 401 for a wrong current password while the
      // session is still valid. Revalidate before clearing; a network
      // failure keeps the session (the next call retries the check).
      try {
        const res = await apiFetch('/auth/me');
        if (res.status === 401) setUser(null);
      } catch (err) {
        // No revalidation possible; keep the session.
      }
    };
    window.addEventListener('auth:unauthorized', onUnauthorized);
    return () => window.removeEventListener('auth:unauthorized', onUnauthorized);
  }, []);

  const login = useCallback(async (username, password) => {
    const res = await apiFetch('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    });
    if (!res.ok) {
      let message = `Login failed (${res.status})`;
      try {
        const data = await res.json();
        if (data && data.error) message = data.error;
      } catch (err) {
        // Non-JSON error body; keep the generic message.
      }
      throw new Error(message);
    }
    const u = await res.json();
    setUser(u);
    return u;
  }, []);

  const logout = useCallback(async () => {
    try {
      await apiFetch('/auth/logout', { method: 'POST' });
    } catch (err) {
      // Still drop the local session even if the request fails.
    }
    setUser(null);
  }, []);

  return (
    <AuthContext.Provider
      value={{ user, providers, loading, login, logout, refresh }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export const useAuth = () => useContext(AuthContext);
