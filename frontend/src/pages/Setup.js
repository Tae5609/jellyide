import React, { useState } from 'react';
import { Navigate, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/AuthContext';
import { apiFetch } from '../api';

const Logo = () => (
  <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M9 18V5l12-2v13" />
    <circle cx="6" cy="18" r="3" />
    <circle cx="18" cy="16" r="3" />
  </svg>
);

export default function Setup() {
  const { providers, refresh } = useAuth();
  const navigate = useNavigate();

  const [token, setToken] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState(null);

  // If setup is not required (providers loaded and setup_required is false),
  // redirect to downloads.
  if (providers && !providers.setup_required) {
    return <Navigate to="/downloads" replace />;
  }

  const submit = async (e) => {
    e.preventDefault();
    if (submitting) return;
    setError(null);
    if (password !== confirm) {
      setError('Passwords do not match.');
      return;
    }
    setSubmitting(true);
    try {
      const res = await apiFetch('/setup/complete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token, username, password }),
      });
      if (res.status === 201) {
        await refresh();
        navigate('/downloads', { replace: true });
        return;
      }
      let msg = 'Setup failed. Check your token and try again.';
      try {
        const body = await res.json();
        if (body && body.error) msg = body.error;
      } catch (_) {}
      setError(msg);
    } catch (err) {
      setError('Network error. Please try again.');
    }
    setSubmitting(false);
  };

  return (
    <div className="login-page">
      <div className="login-card fade-in-up">
        <div className="login-logo">
          <Logo />
          <span>JellyIDE — First-run Setup</span>
        </div>

        <p className="login-hint">
          Find the setup token in the server logs:{' '}
          <code>docker logs jelly-ide</code>
        </p>

        {error && (
          <div className="login-error" role="alert">
            {error}
          </div>
        )}

        <form className="login-form" onSubmit={submit} dir="auto">
          <div className="login-field">
            <label className="settings-label" htmlFor="setup-token">
              Setup token
            </label>
            <input
              id="setup-token"
              className="settings-input login-input"
              type="text"
              name="token"
              autoComplete="off"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              required
            />
          </div>
          <div className="login-field">
            <label className="settings-label" htmlFor="setup-username">
              Username
            </label>
            <input
              id="setup-username"
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
            <label className="settings-label" htmlFor="setup-password">
              Password
            </label>
            <input
              id="setup-password"
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
            <label className="settings-label" htmlFor="setup-confirm">
              Confirm password
            </label>
            <input
              id="setup-confirm"
              className="settings-input login-input"
              type="password"
              name="confirm"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              required
            />
          </div>
          <button
            type="submit"
            className="btn-primary login-submit"
            disabled={submitting}
          >
            {submitting && <span className="spinner" aria-hidden="true" />}
            {submitting ? 'Setting up…' : 'Complete setup'}
          </button>
        </form>
      </div>
    </div>
  );
}
