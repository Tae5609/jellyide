import React, { useCallback, useEffect, useState } from 'react';
import { useModal } from '../components/ModalContext';
import { useAuth } from '../auth/AuthContext';
import { apiFetch } from '../api';

// messageFrom extracts the server's error message from a failed response.
async function messageFrom(res, fallback) {
  try {
    const data = await res.json();
    if (data && data.error) return data.error;
  } catch (err) {
    // Non-JSON error body; keep the fallback.
  }
  return fallback;
}

const formatTime = (value) => {
  if (!value) return '—';
  const date = new Date(value.replace(' ', 'T'));
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
};

export default function Users() {
  const { showConfirm, showAlert } = useModal();
  const { user: me } = useAuth();
  const [users, setUsers] = useState(null);
  const [loadError, setLoadError] = useState(null);
  const [dialog, setDialog] = useState(null); // {type:'create'} | {type:'reset', target}
  const [busyId, setBusyId] = useState(null);

  const load = useCallback(async () => {
    try {
      const res = await apiFetch('/admin/users');
      if (!res.ok) {
        throw new Error(await messageFrom(res, `Failed to load users (${res.status})`));
      }
      setUsers(await res.json());
      setLoadError(null);
    } catch (err) {
      setLoadError(err.message);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // run executes a state-changing call, reports server errors, and
  // refreshes the list on success.
  const run = async (id, fn) => {
    setBusyId(id);
    try {
      await fn();
      await load();
    } catch (err) {
      showAlert('Error', err.message);
    }
    setBusyId(null);
  };

  const patch = (target, body) =>
    run(target.id, async () => {
      const res = await apiFetch(`/admin/users/${target.id}`, {
        method: 'PATCH',
        body: JSON.stringify(body),
      });
      if (!res.ok) {
        throw new Error(await messageFrom(res, `Request failed (${res.status})`));
      }
    });

  const approve = (u) => patch(u, { status: 'active' });

  const disable = async (u) => {
    const ok = await showConfirm(
      'Disable user',
      `${u.username} will be signed out of all sessions and cannot log in until re-enabled.`
    );
    if (ok) patch(u, { status: 'disabled' });
  };

  const enable = (u) => patch(u, { status: 'active' });

  const toggleAdmin = async (u) => {
    if (u.is_admin) {
      const ok = await showConfirm(
        'Remove admin',
        `Remove admin rights from ${u.username}?`
      );
      if (!ok) return;
    }
    patch(u, { is_admin: !u.is_admin });
  };

  const resetPassword = async (u) => {
    const ok = await showConfirm(
      'Reset password',
      `Reset the password for ${u.username}? Their active sessions will be deleted.`
    );
    if (ok) setDialog({ type: 'reset', target: u });
  };

  const removeUser = async (u) => {
    const ok = await showConfirm(
      'Delete user',
      `Delete ${u.username}? This cannot be undone and deletes all of their sessions.`
    );
    if (!ok) return;
    await run(u.id, async () => {
      const res = await apiFetch(`/admin/users/${u.id}`, { method: 'DELETE' });
      if (!res.ok) {
        throw new Error(await messageFrom(res, `Request failed (${res.status})`));
      }
    });
  };

  const isSelf = (u) => Boolean(me) && me.id === u.id;

  const rowActions = (u) => (
    <div className="user-actions">
      {u.status === 'pending' && (
        <button
          type="button"
          className="btn-small primary"
          disabled={busyId === u.id}
          onClick={() => approve(u)}
        >
          Approve
        </button>
      )}
      {u.status === 'disabled' && (
        <button
          type="button"
          className="btn-small primary"
          disabled={busyId === u.id}
          onClick={() => enable(u)}
        >
          Enable
        </button>
      )}
      {u.status === 'active' && (
        <button
          type="button"
          className="btn-small"
          disabled={busyId === u.id}
          onClick={() => disable(u)}
        >
          Disable
        </button>
      )}
      {u.is_admin ? (
        <button
          type="button"
          className="btn-small"
          disabled={busyId === u.id}
          onClick={() => toggleAdmin(u)}
        >
          Remove admin
        </button>
      ) : (
        <button
          type="button"
          className="btn-small"
          disabled={busyId === u.id}
          onClick={() => toggleAdmin(u)}
        >
          Make admin
        </button>
      )}
      {u.provider === 'local' && (
        <button
          type="button"
          className="btn-small"
          disabled={busyId === u.id}
          onClick={() => resetPassword(u)}
        >
          Reset password
        </button>
      )}
      {!isSelf(u) && (
        <button
          type="button"
          className="btn-small danger"
          disabled={busyId === u.id}
          onClick={() => removeUser(u)}
        >
          Delete
        </button>
      )}
    </div>
  );

  return (
    <div className="page-container fade-in-up">
      <div className="page-header-row">
        <div>
          <h2 className="page-title">Users</h2>
          <p className="page-subtitle">Manage accounts and registration</p>
        </div>
        <button type="button" className="btn-primary" onClick={() => setDialog({ type: 'create' })}>
          Create user
        </button>
      </div>

      {loadError && <p className="settings-msg error">{loadError}</p>}
      {users === null && !loadError && (
        <div className="auth-loader">
          <span className="spinner" aria-hidden="true" />
          <span>Loading users…</span>
        </div>
      )}

      {users !== null && (
        <div className="card card-flush">
          {users.length === 0 ? (
            <div className="empty-state">
              <h3>No users</h3>
              <p>Create the first account to get started.</p>
            </div>
          ) : (
            <>
              <div className="table-scroll users-table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>Username</th>
                      <th>Email</th>
                      <th>Provider</th>
                      <th>Status</th>
                      <th>Admin</th>
                      <th>Last login</th>
                      <th>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {users.map((u) => (
                      <tr key={u.id}>
                        <td>
                          <div className="user-username">
                            {u.username}
                            {isSelf(u) && <span className="user-self-tag"> (you)</span>}
                          </div>
                          {u.display_name && (
                            <div className="user-display-name">{u.display_name}</div>
                          )}
                        </td>
                        <td>{u.email || '—'}</td>
                        <td>{u.provider}</td>
                        <td>
                          <span className={`badge user-status-${u.status}`}>{u.status}</span>
                        </td>
                        <td>{u.is_admin ? 'Yes' : 'No'}</td>
                        <td className="user-last-login">{formatTime(u.last_login)}</td>
                        <td>{rowActions(u)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              <div className="user-cards">
                {users.map((u) => (
                  <div className="user-card" key={u.id}>
                    <div className="user-card-top">
                      <span className="user-card-username">
                        {u.username}
                        {isSelf(u) && <span className="user-self-tag"> (you)</span>}
                      </span>
                      <span className={`badge user-status-${u.status}`}>{u.status}</span>
                    </div>
                    <div className="user-card-meta">
                      {u.display_name && <span>{u.display_name}</span>}
                      <span>{u.email || 'No email'}</span>
                      <span>Provider: {u.provider} · Admin: {u.is_admin ? 'yes' : 'no'}</span>
                      <span>Last login: {formatTime(u.last_login)}</span>
                    </div>
                    {rowActions(u)}
                  </div>
                ))}
              </div>
            </>
          )}
        </div>
      )}

      {dialog && dialog.type === 'create' && (
        <CreateUserDialog
          onClose={(created) => {
            setDialog(null);
            if (created) load();
          }}
        />
      )}
      {dialog && dialog.type === 'reset' && (
        <ResetPasswordDialog
          target={dialog.target}
          onClose={(done) => {
            setDialog(null);
            if (done) load();
          }}
        />
      )}
    </div>
  );
}

function DialogFrame({ title, onClose, children }) {
  // Escape closes the dialog, like the app's confirm/alert modals.
  useEffect(() => {
    const handleKeyDown = (e) => {
      if (e.key === 'Escape') onClose(false);
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [onClose]);

  return (
    <div
      className="modal-backdrop"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose(false);
      }}
    >
      <div className="modal-content fade-in-up">
        <h3 className="modal-title">{title}</h3>
        {children}
      </div>
    </div>
  );
}

function CreateUserDialog({ onClose }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [email, setEmail] = useState('');
  const [isAdmin, setIsAdmin] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);

  const submit = async (e) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await apiFetch('/admin/users', {
        method: 'POST',
        body: JSON.stringify({
          username,
          password,
          ...(email ? { email } : {}),
          is_admin: isAdmin,
        }),
      });
      if (!res.ok) {
        throw new Error(await messageFrom(res, `Failed to create user (${res.status})`));
      }
      onClose(true);
    } catch (err) {
      setError(err.message);
      setBusy(false);
    }
  };

  return (
    <DialogFrame title="Create user" onClose={(ok) => onClose(false)}>
      <form onSubmit={submit}>
        {error && <p className="settings-msg error">{error}</p>}
        <div className="settings-field">
          <label className="settings-label" htmlFor="create-username">
            Username
          </label>
          <input
            id="create-username"
            className="settings-input"
            type="text"
            name="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
          />
          <p className="settings-hint">3-32 characters: letters, numbers, dot, dash or underscore.</p>
        </div>
        <div className="settings-field">
          <label className="settings-label" htmlFor="create-password">
            Password
          </label>
          <input
            id="create-password"
            className="settings-input"
            type="password"
            name="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
          <p className="settings-hint">At least 8 characters.</p>
        </div>
        <div className="settings-field">
          <label className="settings-label" htmlFor="create-email">
            Email <span style={{ color: 'var(--text-muted)', fontWeight: 400 }}>(Optional)</span>
          </label>
          <input
            id="create-email"
            className="settings-input"
            type="email"
            name="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </div>
        <div className="settings-field">
          <label className="settings-checkbox-label">
            <input
              type="checkbox"
              name="is_admin"
              checked={isAdmin}
              onChange={(e) => setIsAdmin(e.target.checked)}
            />
            Grant admin rights
          </label>
        </div>
        <div className="modal-actions">
          <button type="button" className="btn-secondary" onClick={() => onClose(false)}>
            Cancel
          </button>
          <button type="submit" className="btn-primary" disabled={busy}>
            {busy ? 'Creating…' : 'Create user'}
          </button>
        </div>
      </form>
    </DialogFrame>
  );
}

function ResetPasswordDialog({ target, onClose }) {
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);

  const submit = async (e) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await apiFetch(`/admin/users/${target.id}/password`, {
        method: 'POST',
        body: JSON.stringify({ new_password: password }),
      });
      if (!res.ok) {
        throw new Error(await messageFrom(res, `Failed to reset password (${res.status})`));
      }
      onClose(true);
    } catch (err) {
      setError(err.message);
      setBusy(false);
    }
  };

  return (
    <DialogFrame title={`Reset password for ${target.username}`} onClose={() => onClose(false)}>
      <form onSubmit={submit}>
        {error && <p className="settings-msg error">{error}</p>}
        <div className="settings-field">
          <label className="settings-label" htmlFor="reset-password">
            New password
          </label>
          <input
            id="reset-password"
            className="settings-input"
            type="password"
            name="new_password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
          <p className="settings-hint">
            At least 8 characters. The user's active sessions will be deleted.
          </p>
        </div>
        <div className="modal-actions">
          <button type="button" className="btn-secondary" onClick={() => onClose(false)}>
            Cancel
          </button>
          <button type="submit" className="btn-primary" disabled={busy}>
            {busy ? 'Resetting…' : 'Reset password'}
          </button>
        </div>
      </form>
    </DialogFrame>
  );
}
