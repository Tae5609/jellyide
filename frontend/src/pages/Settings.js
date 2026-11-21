import React, { useState, useEffect } from 'react';
import { useModal } from '../components/ModalContext';

const API = window.location.origin + '/api';

export default function Settings() {
  const { showAlert } = useModal();
  const [limit, setLimit] = useState(2);
  const [proxy, setProxy] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    fetch(`${API}/config`)
      .then(r => r.json())
      .then(data => {
        setLimit(data.concurrent_limit || 2);
        setProxy(data.proxy || '');
      });
  }, []);

  const save = async () => {
    setSaving(true);
    try {
      const res = await fetch(`${API}/config`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
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
    </div>
  );
}
