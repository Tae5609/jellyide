import React, { useState, useEffect } from 'react';
import { useModal } from '../components/ModalContext';
import { apiFetch } from '../api';
import { useAuth } from '../auth/AuthContext';

export default function Settings() {
  const { showAlert } = useModal();
  const { user } = useAuth();
  const [limit, setLimit] = useState(2);
  const [proxy, setProxy] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    apiFetch('/config')
      .then(r => r.json())
      .then(data => {
        setLimit(data.concurrent_limit || 2);
        setProxy(data.proxy || '');
      });
  }, []);

  const save = async () => {
    setSaving(true);
    try {
      const res = await apiFetch('/config', {
        method: 'POST',
        body: JSON.stringify({ concurrent_limit: parseInt(limit), proxy })
      });
      if (res.ok) showAlert("Settings Saved", "Your configuration has been updated successfully.");
    } catch (e) {
      showAlert("Error", e.message);
    }
    setSaving(false);
  };

  return (
    <div className="page-container fade-in-up">
      <div className="page-header">
        <h2 className="page-title">Settings</h2>
        <p className="page-subtitle">Configure platform behavior</p>
      </div>

      <div className="card settings-card">
        <div className="settings-field">
          <label className="settings-label">
            Concurrent Downloads
          </label>
          <input 
            className="settings-input"
            type="number" 
            min="1" max="10"
            value={limit} 
            onChange={e => setLimit(e.target.value)} 
          />
          <p className="settings-hint">
            Number of simultaneous YouTube downloads.
          </p>
        </div>

        <div className="settings-field-spaced">
          <label className="settings-label">
            Proxy URL <span style={{ color: 'var(--text-muted)', fontWeight: 400 }}>(Optional)</span>
          </label>
          <input 
            className="settings-input"
            type="text" 
            placeholder="http://user:pass@host:port"
            value={proxy} 
            onChange={e => setProxy(e.target.value)} 
          />
          <p className="settings-hint">
            Useful for bypassing regional YouTube blocks.
          </p>
        </div>

        <button className="btn-primary settings-save" onClick={save} disabled={saving}>
          {saving ? "Saving..." : "Save Configuration"}
        </button>
      </div>

      {user && user.provider === 'local' && <ChangePasswordCard />}
    </div>
  );
}

function ChangePasswordCard() {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState(null);

  const submit = async (e) => {
    e.preventDefault();
    setMsg(null);
    if (!current) {
      setMsg({ type: 'error', text: 'Enter your current password.' });
      return;
    }
    if (next.length < 8) {
      setMsg({ type: 'error', text: 'New password must be at least 8 characters.' });
      return;
    }
    if (next !== confirm) {
      setMsg({ type: 'error', text: 'New passwords do not match.' });
      return;
    }
    setSaving(true);
    try {
      const res = await apiFetch('/auth/password', {
        method: 'POST',
        body: JSON.stringify({
          current_password: current,
          new_password: next
        })
      });
      if (res.ok) {
        setMsg({ type: 'success', text: 'Password changed successfully.' });
        setCurrent('');
        setNext('');
        setConfirm('');
      } else {
        let text = 'Failed to change password.';
        try {
          const data = await res.json();
          if (data && data.error) text = data.error;
        } catch (err) {
          // Non-JSON error body; keep the generic message.
        }
        setMsg({ type: 'error', text });
      }
    } catch (err) {
      setMsg({ type: 'error', text: err.message });
    }
    setSaving(false);
  };

  return (
    <div className="card settings-card">
      <h3 className="settings-card-title">Change password</h3>
      <form onSubmit={submit}>
        {msg && <p className={`settings-msg ${msg.type}`}>{msg.text}</p>}
        <div className="settings-field">
          <label className="settings-label" htmlFor="pw-current">
            Current password
          </label>
          <input
            id="pw-current"
            className="settings-input"
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={e => setCurrent(e.target.value)}
          />
        </div>
        <div className="settings-field">
          <label className="settings-label" htmlFor="pw-new">
            New password
          </label>
          <input
            id="pw-new"
            className="settings-input"
            type="password"
            autoComplete="new-password"
            value={next}
            onChange={e => setNext(e.target.value)}
          />
          <p className="settings-hint">
            At least 8 characters.
          </p>
        </div>
        <div className="settings-field">
          <label className="settings-label" htmlFor="pw-confirm">
            Confirm new password
          </label>
          <input
            id="pw-confirm"
            className="settings-input"
            type="password"
            autoComplete="new-password"
            value={confirm}
            onChange={e => setConfirm(e.target.value)}
          />
        </div>
        <button className="btn-primary settings-save" type="submit" disabled={saving}>
          {saving ? "Saving..." : "Change Password"}
        </button>
      </form>
    </div>
  );
}
