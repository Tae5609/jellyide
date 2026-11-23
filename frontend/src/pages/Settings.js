import React, { useState, useEffect } from 'react';
import { useModal } from '../components/ModalContext';
import { apiFetch } from '../api';
import { useAuth } from '../auth/AuthContext';

export default function Settings() {
  const { showAlert } = useModal();
  const { user, providers } = useAuth();
  const [limit, setLimit] = useState(2);
  const [proxy, setProxy] = useState('');
  const [saving, setSaving] = useState(false);
  const [limitError, setLimitError] = useState('');
  // The last known-good limit: an invalid entry is rejected with the
  // previous value kept instead of sending NaN/null (the backend would
  // decode that as 0 and silently stop all concurrent downloads).
  const [savedLimit, setSavedLimit] = useState(2);

  // With auth disabled the anonymous superuser is an admin, like the
  // backend treats it.
  const isAdmin =
    (providers && providers.enabled === false) || (user && user.is_admin);

  useEffect(() => {
    apiFetch('/config')
      .then(r => r.json())
      .then(data => {
        const loaded = data.concurrent_limit || 2;
        setLimit(loaded);
        setSavedLimit(loaded);
        setProxy(data.proxy || '');
      });
  }, []);

  const save = async () => {
    const parsed = Number(limit);
    if (limit === '' || !Number.isInteger(parsed) || parsed < 1) {
      setLimitError('Enter a positive whole number.');
      setLimit(savedLimit);
      return;
    }
    setLimitError('');
    setSaving(true);
    try {
      const res = await apiFetch('/config', {
        method: 'POST',
        body: JSON.stringify({ concurrent_limit: parsed, proxy })
      });
      if (res.ok) {
        setSavedLimit(parsed);
        showAlert("Settings Saved", "Your configuration has been updated successfully.");
      }
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
            onChange={e => {
              setLimit(e.target.value);
              setLimitError('');
            }} 
            disabled={!isAdmin}
          />
          {limitError && (
            <p className="settings-msg error">
              {limitError}
            </p>
          )}
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
            disabled={!isAdmin}
          />
          <p className="settings-hint">
            Useful for bypassing regional YouTube blocks.
          </p>
        </div>

        {isAdmin ? (
          <button className="btn-primary settings-save" onClick={save} disabled={saving}>
            {saving ? "Saving..." : "Save Configuration"}
          </button>
        ) : (
          <p className="settings-hint" style={{ marginTop: '12px' }}>
            Only administrators can modify download concurrency and proxy settings.
          </p>
        )}
      </div>

      {isAdmin && <YtDlpCard />}

      {isAdmin && <RegistrationCard />}

      {user && user.provider === 'local' && <ChangePasswordCard />}
    </div>
  );
}

const REGISTRATION_MODES = [
  {
    value: 'admin_only',
    label: 'Admin only',
    hint: 'No self sign-up. Only admins can create accounts.',
  },
  {
    value: 'open',
    label: 'Open',
    hint: 'Anyone can register and sign in immediately.',
  },
  {
    value: 'approval',
    label: 'Approval',
    hint: 'Anyone can register, but new accounts stay pending until an admin approves them.',
  },
];

function RegistrationCard() {
  const [mode, setMode] = useState(null);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState(null);

  useEffect(() => {
    apiFetch('/admin/settings')
      .then(r => r.json())
      .then(data => {
        if (data && data.registration_mode) setMode(data.registration_mode);
      })
      .catch(() => {
        // A failed load leaves the card empty rather than broken.
      });
  }, []);

  const save = async () => {
    setSaving(true);
    setMsg(null);
    try {
      const res = await apiFetch('/admin/settings', {
        method: 'PUT',
        body: JSON.stringify({ registration_mode: mode })
      });
      if (res.ok) {
        setMsg({ type: 'success', text: 'Registration mode updated.' });
      } else {
        let text = 'Failed to update the registration mode.';
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
      <h3 className="settings-card-title">Registration</h3>
      {msg && <p className={`settings-msg ${msg.type}`}>{msg.text}</p>}
      {mode !== null && (
        <>
          <div className="registration-options">
            {REGISTRATION_MODES.map(o => (
              <label
                key={o.value}
                className={`registration-option ${mode === o.value ? 'selected' : ''}`}
              >
                <input
                  type="radio"
                  name="registration_mode"
                  value={o.value}
                  checked={mode === o.value}
                  onChange={() => setMode(o.value)}
                />
                <span className="registration-option-body">
                  <span className="registration-option-label">{o.label}</span>
                  <span className="registration-option-hint">{o.hint}</span>
                </span>
              </label>
            ))}
          </div>
          <button className="btn-primary settings-save" onClick={save} disabled={saving}>
            {saving ? 'Saving...' : 'Save Registration Mode'}
          </button>
        </>
      )}
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

const YTDLP_INTERVAL_OPTIONS = [
  { value: 6, label: '6 hours' },
  { value: 12, label: '12 hours' },
  { value: 24, label: '24 hours (1 day)' },
  { value: 72, label: '3 days' },
  { value: 168, label: '7 days (1 week)' },
];

function YtDlpCard() {
  const { showAlert, showConfirm } = useModal();
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [checking, setChecking] = useState(false);
  const [updating, setUpdating] = useState(false);
  const [rollingBack, setRollingBack] = useState(false);
  const [savingSettings, setSavingSettings] = useState(false);
  const [force, setForce] = useState(false);
  const [msg, setMsg] = useState(null);

  const [autoUpdate, setAutoUpdate] = useState(true);
  const [intervalHours, setIntervalHours] = useState(24);
  const [channel, setChannel] = useState('stable');

  const loadStatus = async () => {
    try {
      const res = await apiFetch('/admin/ytdlp');
      if (res.ok) {
        const json = await res.json();
        setData(json);
        setAutoUpdate(json.auto_update ?? true);
        setIntervalHours(json.interval_hours ?? 24);
        setChannel(json.channel || 'stable');
        if (json.updating) {
          setUpdating(true);
        } else {
          setUpdating(false);
        }
      }
    } catch (err) {
      // quiet load error
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadStatus();
  }, []);

  useEffect(() => {
    if (!updating) return;
    const interval = setInterval(async () => {
      try {
        const res = await apiFetch('/admin/ytdlp');
        if (res.ok) {
          const json = await res.json();
          setData(json);
          if (!json.updating) {
            setUpdating(false);
            setMsg({ type: 'success', text: 'yt-dlp update finished.' });
          }
        }
      } catch (e) {
        // ignore polling error
      }
    }, 2000);
    return () => clearInterval(interval);
  }, [updating]);

  const handleCheck = async () => {
    setChecking(true);
    setMsg(null);
    try {
      const res = await apiFetch('/admin/ytdlp/check', { method: 'POST' });
      if (res.ok) {
        const json = await res.json();
        setData(json);
        if (json.update_available) {
          setMsg({ type: 'success', text: `Update available: ${json.latest_version}` });
        } else {
          setMsg({ type: 'success', text: 'yt-dlp is up to date.' });
        }
      } else {
        let text = 'Failed to check for updates.';
        try {
          const errData = await res.json();
          if (errData && errData.error) text = errData.error;
        } catch (_) {}
        setMsg({ type: 'error', text });
      }
    } catch (err) {
      setMsg({ type: 'error', text: err.message });
    }
    setChecking(false);
  };

  const handleUpdate = async () => {
    setUpdating(true);
    setMsg(null);
    try {
      const res = await apiFetch('/admin/ytdlp/update', {
        method: 'POST',
        body: JSON.stringify({ force })
      });
      if (res.ok || res.status === 202) {
        setMsg({ type: 'success', text: 'Update job started...' });
        loadStatus();
      } else if (res.status === 409) {
        let text = 'Already up to date or update in progress.';
        try {
          const errData = await res.json();
          if (errData && errData.error) text = errData.error;
        } catch (_) {}
        setMsg({ type: 'error', text });
        setUpdating(false);
      } else {
        let text = 'Failed to start update.';
        try {
          const errData = await res.json();
          if (errData && errData.error) text = errData.error;
        } catch (_) {}
        setMsg({ type: 'error', text });
        setUpdating(false);
      }
    } catch (err) {
      setMsg({ type: 'error', text: err.message });
      setUpdating(false);
    }
  };

  const handleRollback = async () => {
    const confirmed = await showConfirm(
      'Roll back yt-dlp?',
      'Do you want to restore the previous yt-dlp binary? This will undo the latest update.'
    );
    if (!confirmed) return;

    setRollingBack(true);
    setMsg(null);
    try {
      const res = await apiFetch('/admin/ytdlp/rollback', { method: 'POST' });
      if (res.ok) {
        const json = await res.json();
        setData(json);
        showAlert('Rollback Successful', 'yt-dlp restored to the previous binary.');
      } else {
        let text = 'Failed to roll back.';
        try {
          const errData = await res.json();
          if (errData && errData.error) text = errData.error;
        } catch (_) {}
        showAlert('Rollback Failed', text);
      }
    } catch (err) {
      showAlert('Rollback Error', err.message);
    }
    setRollingBack(false);
  };

  const handleSaveSettings = async () => {
    setSavingSettings(true);
    setMsg(null);
    try {
      const res = await apiFetch('/admin/ytdlp/settings', {
        method: 'PUT',
        body: JSON.stringify({
          auto_update: autoUpdate,
          interval_hours: Number(intervalHours),
          channel
        })
      });
      if (res.ok) {
        const json = await res.json();
        setData(json);
        setMsg({ type: 'success', text: 'yt-dlp settings saved.' });
      } else {
        let text = 'Failed to save settings.';
        try {
          const errData = await res.json();
          if (errData && errData.error) text = errData.error;
        } catch (_) {}
        setMsg({ type: 'error', text });
      }
    } catch (err) {
      setMsg({ type: 'error', text: err.message });
    }
    setSavingSettings(false);
  };

  if (loading && !data) {
    return (
      <div className="card settings-card">
        <h3 className="settings-card-title">yt-dlp</h3>
        <p className="settings-hint">Loading yt-dlp status...</p>
      </div>
    );
  }

  const isUpToDate = data && !data.update_available;
  const updateDisabled = updating || (!force && isUpToDate);

  return (
    <div className="card settings-card" data-testid="ytdlp-card">
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
        <h3 className="settings-card-title" style={{ margin: 0 }}>yt-dlp Version</h3>
        {data && data.update_available && (
          <span className="badge running" style={{ fontWeight: 600 }}>Update available</span>
        )}
      </div>

      {msg && <p className={`settings-msg ${msg.type}`}>{msg.text}</p>}

      {data && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: '12px', marginBottom: '16px', background: 'var(--bg-main)', padding: '12px', borderRadius: '8px' }}>
          <div>
            <div className="settings-hint" style={{ fontSize: '11px', textTransform: 'uppercase' }}>Installed Version</div>
            <div style={{ fontWeight: 600, fontFamily: 'monospace' }}>{data.version || 'None'}</div>
            <div className="settings-hint" style={{ fontSize: '12px' }}>{data.managed ? 'Managed' : 'System'} ({data.path})</div>
          </div>
          <div>
            <div className="settings-hint" style={{ fontSize: '11px', textTransform: 'uppercase' }}>Latest Version</div>
            <div style={{ fontWeight: 600, fontFamily: 'monospace' }}>{data.latest_version || 'Unknown'}</div>
            <div className="settings-hint" style={{ fontSize: '12px' }}>Channel: {data.channel}</div>
          </div>
          <div>
            <div className="settings-hint" style={{ fontSize: '11px', textTransform: 'uppercase' }}>Last Check</div>
            <div style={{ fontSize: '13px' }}>{data.last_check ? new Date(data.last_check).toLocaleString() : 'Never'}</div>
            {data.last_result && <div className="settings-hint" style={{ fontSize: '12px' }}>{data.last_result}</div>}
          </div>
        </div>
      )}

      {/* Buttons */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: '10px', alignItems: 'center', marginBottom: '20px' }}>
        <button className="btn-secondary" onClick={handleCheck} disabled={checking || updating}>
          {checking ? 'Checking...' : 'Check for updates'}
        </button>

        <button className="btn-primary" onClick={handleUpdate} disabled={updateDisabled}>
          {updating ? 'Updating...' : 'Update now'}
        </button>

        <button className="btn-secondary" onClick={handleRollback} disabled={!data || !data.has_rollback || rollingBack || updating}>
          {rollingBack ? 'Rolling back...' : 'Roll back'}
        </button>

        <label className="settings-checkbox-label" style={{ marginLeft: '6px' }}>
          <input
            type="checkbox"
            checked={force}
            onChange={e => setForce(e.target.checked)}
          />
          <span style={{ fontSize: '13px' }}>Force reinstall</span>
        </label>
      </div>

      {/* Auto update options */}
      <h4 style={{ fontSize: '14px', fontWeight: 600, marginBottom: '12px' }}>Auto-update</h4>
      <div className="settings-field">
        <label className="settings-checkbox-label" style={{ marginBottom: '12px' }}>
          <input
            type="checkbox"
            checked={autoUpdate}
            onChange={e => setAutoUpdate(e.target.checked)}
          />
          <span>Enable auto-update</span>
        </label>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: '14px', marginBottom: '16px' }}>
        <div className="settings-field">
          <label className="settings-label">Update Interval</label>
          <select
            className="settings-input"
            value={intervalHours}
            onChange={e => setIntervalHours(Number(e.target.value))}
            disabled={!autoUpdate}
          >
            {YTDLP_INTERVAL_OPTIONS.map(opt => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
        </div>

        <div className="settings-field">
          <label className="settings-label">Channel</label>
          <select
            className="settings-input"
            value={channel}
            onChange={e => setChannel(e.target.value)}
          >
            <option value="stable">stable</option>
            <option value="nightly">nightly</option>
          </select>
        </div>
      </div>

      {channel === 'nightly' && (
        <p className="settings-msg error" style={{ marginBottom: '16px', background: 'rgba(234, 179, 8, 0.1)', color: '#facc15', borderColor: 'rgba(234, 179, 8, 0.3)' }}>
          Warning: Nightly builds receive daily fixes but may be unstable.
        </p>
      )}

      <button className="btn-primary settings-save" onClick={handleSaveSettings} disabled={savingSettings}>
        {savingSettings ? 'Saving...' : 'Save Settings'}
      </button>
    </div>
  );
}
