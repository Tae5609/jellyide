import React, { useState } from 'react';
import { Navigate, useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '../auth/AuthContext';

const Logo = () => (
  <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M9 18V5l12-2v13" />
    <circle cx="6" cy="18" r="3" />
    <circle cx="18" cy="16" r="3" />
  </svg>
);

export default function Login() {
  const { user, providers, login } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [params] = useSearchParams();

  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState(null);

  // Only accept internal paths (single leading slash) as the redirect
  // target after login; anything else falls back to /downloads.
  const from = location.state && location.state.from;
  const redirectTo =
    typeof from === 'string' && from.startsWith('/') && !from.startsWith('//')
      ? from
      : '/downloads';

  // Defensive: a session appearing while this page is mounted (e.g. after
  // a successful login render) sends the user to their target page.
  if (user) return <Navigate to={redirectTo} replace />;

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

  const localEnabled = providers.local;
  const ssoButtons = [
    providers.oidc && providers.oidc.enabled
      ? { key: 'oidc', name: providers.oidc.name, href: '/api/auth/oidc/login' }
      : null,
    providers.saml && providers.saml.enabled
      ? { key: 'saml', name: providers.saml.name, href: '/api/auth/saml/login' }
      : null,
  ].filter(Boolean);

  const banner = error || params.get('error');

  const submit = async (e) => {
    e.preventDefault();
    if (submitting) return;
    setError(null);
    setSubmitting(true);
    try {
      await login(username, password);
      navigate(redirectTo, { replace: true });
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

        {banner && (
          <div className="login-error" role="alert">
            {banner}
          </div>
        )}

        {localEnabled && (
          <form className="login-form" onSubmit={submit}>
            <div className="login-field">
              <label className="settings-label" htmlFor="login-username">
                Username
              </label>
              <input
                id="login-username"
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
              <label className="settings-label" htmlFor="login-password">
                Password
              </label>
              <input
                id="login-password"
                className="settings-input login-input"
                type="password"
                name="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
            <button
              type="submit"
              className="btn-primary login-submit"
              disabled={submitting}
            >
              {submitting && <span className="spinner" aria-hidden="true" />}
              {submitting ? 'Signing in…' : 'Sign in'}
            </button>
          </form>
        )}

        {ssoButtons.length > 0 && (
          <div className="login-sso">
            {localEnabled && (
              <div className="login-divider">
                <span>or</span>
              </div>
            )}
            {ssoButtons.map((sso) => (
              <a key={sso.key} className="btn-secondary sso-btn" href={sso.href}>
                Continue with {sso.name}
              </a>
            ))}
          </div>
        )}

        {!localEnabled && ssoButtons.length === 0 && (
          <p className="login-hint">No sign-in methods are enabled.</p>
        )}
      </div>
    </div>
  );
}
