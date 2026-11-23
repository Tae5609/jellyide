import React, { useState } from 'react';
import { Link, Navigate } from 'react-router-dom';
import { useAuth } from '../auth/AuthContext';
import { apiFetch } from '../api';

const Logo = () => (
  <svg
    width="28"
    height="28"
    viewBox="0 0 24 24"
    fill="none"
    stroke="var(--accent)"
    strokeWidth="2"
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    <path d="M9 18V5l12-2v13" />
    <circle cx="6" cy="18" r="3" />
    <circle cx="18" cy="16" r="3" />
  </svg>
);

// Same client-side rules as the backend: 3-32 chars from
// [a-zA-Z0-9._-] for the username, 8+ chars for the password. The server
// re-validates everything; this only avoids a round trip.
const USERNAME_PATTERN = /^[a-zA-Z0-9._-]{3,32}$/;

export default function Register() {
  const { user, providers, refresh } = useAuth();

  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [email, setEmail] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState(null);
  const [awaitingApproval, setAwaitingApproval] = useState(false);

  // Defensive: a session appearing while this page is mounted (open-mode
  // registration) sends the user into the app.
  if (user) return <Navigate to="/downloads" replace />;

  if (!providers) {
    return (
      <div className="login-page">
        <div className="auth-loader">
          <span className="spinner" aria-hidden="true" />
          <span>Loading…</span>
        </div>
      </div>
    );
  }

  // Self sign-up is not available in admin_only mode.
  if (providers.registration === 'admin_only') {
    return <Navigate to="/login" replace />;
  }

  const submit = async (e) => {
    e.preventDefault();
    if (submitting) return;
    setError(null);
    if (!USERNAME_PATTERN.test(username)) {
      setError('Username must be 3-32 characters: letters, numbers, dot, dash or underscore.');
      return;
    }
    if (password.length < 8) {
      setError('Password must be at least 8 characters.');
      return;
    }
    setSubmitting(true);
    try {
      // Login-CSRF: the backend requires the double-submit token from
      // GET /auth/csrf for registration as well (same contract as
      // login), so a cross-site form POST cannot create an account.
      // Fetch a fresh token per attempt; the double-submit cookie rides
      // along.
      let csrfToken = '';
      try {
        const csrfRes = await apiFetch('/auth/csrf');
        if (csrfRes.ok) {
          const data = await csrfRes.json();
          csrfToken = (data && data.token) || '';
        }
      } catch (err) {
        // The registration attempt itself will surface the failure.
      }
      const res = await apiFetch('/auth/register', {
        method: 'POST',
        body: JSON.stringify({
          username,
          password,
          csrf_token: csrfToken,
          ...(email ? { email } : {}),
        }),
      });
      if (!res.ok) {
        let message = `Registration failed (${res.status})`;
        try {
          const data = await res.json();
          if (data && data.error) message = data.error;
        } catch (err) {
          // Non-JSON error body; keep the generic message.
        }
        setError(message);
        return;
      }
      const data = await res.json();
      if (data.status === 'active') {
        // Open registration: the server started a session, so load the
        // session and go straight in.
        await refresh();
      } else {
        setAwaitingApproval(true);
      }
    } catch (err) {
      setError(err.message);
    }
    setSubmitting(false);
  };

  return (
    <div className="login-page">
      <div className="login-card fade-in-up">
        <div className="login-logo">
          <Logo />
          <span>JellyIDE</span>
        </div>

        {error && (
          <div className="login-error" role="alert">
            {error}
          </div>
        )}

        {awaitingApproval ? (
          <div className="register-awaiting">
            <p className="login-hint">Your account is awaiting admin approval.</p>
            <Link to="/login" className="login-register-link">
              Back to login
            </Link>
          </div>
        ) : (
          <form className="login-form" onSubmit={submit}>
            <div className="login-field">
              <label className="settings-label" htmlFor="register-username">
                Username
              </label>
              <input
                id="register-username"
                className="settings-input login-input"
                type="text"
                name="username"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
              />
            </div>
            <div className="login-field">
              <label className="settings-label" htmlFor="register-password">
                Password
              </label>
              <input
                id="register-password"
                className="settings-input login-input"
                type="password"
                name="password"
                autoComplete="new-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
            <div className="login-field">
              <label className="settings-label" htmlFor="register-email">
                Email{' '}
                <span style={{ color: 'var(--text-muted)', fontWeight: 400 }}>(Optional)</span>
              </label>
              <input
                id="register-email"
                className="settings-input login-input"
                type="email"
                name="email"
                autoComplete="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </div>
            <button type="submit" className="btn-primary login-submit" disabled={submitting}>
              {submitting && <span className="spinner" aria-hidden="true" />}
              {submitting ? 'Creating account…' : 'Create account'}
            </button>
          </form>
        )}

        <div className="login-register">
          <Link to="/login" className="login-register-link">
            Back to login
          </Link>
        </div>
      </div>
    </div>
  );
}
