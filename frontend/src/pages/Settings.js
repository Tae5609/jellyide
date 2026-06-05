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
      <div style={{ marginBottom: 32 }}>
        <h2 style={{ margin: '0 0 8px 0', fontSize: 28, letterSpacing: '-0.03em' }}>Settings</h2>
        <p style={{ margin: 0, color: 'var(--text-muted)', fontSize: 15 }}>Configure platform behavior</p>
      </div>

      <div className="card" style={{ maxWidth: 500 }}>
        <div style={{ marginBottom: 24 }}>
          <label style={{ display: 'block', marginBottom: 8, color: 'var(--text-main)', fontSize: 14, fontWeight: 500 }}>
            Concurrent Downloads
          </label>
          <input 
            type="number" 
            min="1" max="10"
            value={limit} 
            onChange={e => setLimit(e.target.value)} 
            style={{ width: '100%' }}
          />
          <p style={{ color: 'var(--text-muted)', fontSize: 12, marginTop: 8 }}>
            Number of simultaneous YouTube downloads.
          </p>
        </div>

        <div style={{ marginBottom: 32 }}>
          <label style={{ display: 'block', marginBottom: 8, color: 'var(--text-main)', fontSize: 14, fontWeight: 500 }}>
            Proxy URL <span style={{ color: 'var(--text-muted)', fontWeight: 400 }}>(Optional)</span>
          </label>
          <input 
            type="text" 
            placeholder="http://user:pass@host:port"
            value={proxy} 
            onChange={e => setProxy(e.target.value)} 
            style={{ width: '100%' }}
          />
          <p style={{ color: 'var(--text-muted)', fontSize: 12, marginTop: 8 }}>
            Useful for bypassing regional YouTube blocks.
          </p>
        </div>

        <button className="btn-primary" onClick={save} disabled={saving} style={{ width: '100%' }}>
          {saving ? "Saving..." : "Save Configuration"}
        </button>
      </div>
    </div>
  );
}
