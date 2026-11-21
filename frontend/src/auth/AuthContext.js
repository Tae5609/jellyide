import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
} from 'react';
import { apiFetch } from '../api';

const AuthContext = createContext(null);

// AuthProvider loads the auth providers and the current session on mount.
// State: { user, providers, loading }.
//
// - login(username, password) POSTs /auth/login and throws Error(message)
//   on failure; the session cookie is handled by the browser.
// - logout() POSTs /auth/logout and clears the local user.
// - refresh() reloads providers and the current session.
// - A global 'auth:unauthorized' event (raised by apiFetch on a 401)
//   drops the session so the guards redirect to /login.
export function AuthProvider({ children }) {
  const [user, setUser] = useState(null);
  const [providers, setProviders] = useState(null);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const [pRes, mRes] = await Promise.all([
        apiFetch('/auth/providers'),
        apiFetch('/auth/me'),
      ]);
      if (pRes.ok) setProviders(await pRes.json());
      setUser(mRes.ok ? await mRes.json() : null);
    } catch (err) {
      console.error('Failed to load auth state', err);
      setUser(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  useEffect(() => {
    const onUnauthorized = () => setUser(null);
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
