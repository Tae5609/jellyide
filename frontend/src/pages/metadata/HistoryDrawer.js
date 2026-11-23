import React, { useCallback, useEffect, useState } from 'react';
import { apiFetch } from '../../api';
import { useModal } from '../../components/ModalContext';
import { errorFromResponse, waitForJob } from './useEditorState';

function fmtValue(v) {
  return v === null || v === undefined ? '∅' : String(v);
}

// HistoryDrawer lists the album's change sets (GET /metadata/history) with
// expandable per-change details. Revert confirms, runs the revert job and
// reports back so the parent can refresh.
export default function HistoryDrawer({ albumId, onClose, onReverted, onError }) {
  const [entries, setEntries] = useState(null);
  const [error, setError] = useState('');
  const [expanded, setExpanded] = useState({});
  const [busyId, setBusyId] = useState(null);
  const { showConfirm } = useModal();

  const load = useCallback(async () => {
    setError('');
    try {
      const res = await apiFetch(`/metadata/history?album_id=${encodeURIComponent(albumId)}`);
      if (!res.ok) throw new Error(await errorFromResponse(res));
      setEntries(await res.json());
    } catch (e) {
      setError(e.message);
    }
  }, [albumId]);

  useEffect(() => {
    load();
  }, [load]);

  const revert = async (entry) => {
    const ok = await showConfirm(
      'Revert change set?',
      `The previous values of ${entry.track_count} track${entry.track_count === 1 ? '' : 's'} will be restored as a new change set.`,
    );
    if (!ok) return;
    setBusyId(entry.change_set_id);
    try {
      const res = await apiFetch(`/metadata/history/${entry.change_set_id}/revert`, {
        method: 'POST',
      });
      if (!res.ok) throw new Error(await errorFromResponse(res));
      const { job_id } = await res.json();
      await waitForJob(job_id);
      onReverted();
      load();
    } catch (e) {
      onError(e.message);
    } finally {
      setBusyId(null);
    }
  };

  return (
    <div className="drawer-wrap" role="dialog" aria-label="Change history">
      <div className="drawer-backdrop" onClick={onClose} aria-hidden="true" />
      <aside className="drawer">
        <div className="drawer-head">
          <h3 className="drawer-title">Change History</h3>
          <button type="button" className="icon-btn" onClick={onClose} aria-label="Close history">
            ✕
          </button>
        </div>
        <div className="drawer-body">
          {error && <p className="lookup-error">{error}</p>}
          {!error && entries === null && <p className="lookup-status">Loading history…</p>}
          {entries && entries.length === 0 && (
            <p className="lookup-status">No changes recorded for this album yet.</p>
          )}
          {entries &&
            entries.map((entry) => (
              <div className="history-entry" key={entry.change_set_id}>
                <div
                  className="history-entry-head"
                  onClick={() =>
                    setExpanded((p) => ({ ...p, [entry.change_set_id]: !p[entry.change_set_id] }))
                  }
                >
                  <span className="history-date">
                    {new Date(entry.created_at).toLocaleString()}
                  </span>
                  <span className="history-count">
                    {entry.track_count} track{entry.track_count === 1 ? '' : 's'} ·{' '}
                    {entry.changes.length} change{entry.changes.length === 1 ? '' : 's'}
                  </span>
                  <button
                    type="button"
                    className="btn-secondary btn-sm"
                    disabled={busyId !== null}
                    onClick={(e) => {
                      e.stopPropagation();
                      revert(entry);
                    }}
                  >
                    {busyId === entry.change_set_id ? 'Reverting…' : 'Revert'}
                  </button>
                </div>
                {expanded[entry.change_set_id] && (
                  <table className="history-table">
                    <thead>
                      <tr>
                        <th>Track</th>
                        <th>Field</th>
                        <th>Old</th>
                        <th>New</th>
                      </tr>
                    </thead>
                    <tbody>
                      {entry.changes.map((c, i) => (
                        <tr key={i}>
                          <td className="history-track">{c.track_title || c.track_id}</td>
                          <td>{c.field}</td>
                          <td>{fmtValue(c.old_value)}</td>
                          <td>{fmtValue(c.new_value)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </div>
            ))}
        </div>
      </aside>
    </div>
  );
}
