import React, { useCallback, useEffect, useState } from 'react';
import { useModal } from '../components/ModalContext';
import { apiFetch } from '../api';
import { waitForJob, errorFromResponse } from './metadata/useEditorState';

const FIELD_LABELS = {
  track_number: 'Track #',
  disc_number: 'Disc',
  title: 'Title',
  artist: 'Artist',
  album_artist: 'Album Artist',
  album: 'Album',
  year: 'Year',
  genre: 'Genre',
  composer: 'Composer',
  comment: 'Comment',
};

const SOURCE_LABELS = {
  scan: 'changed on disk by another program',
  apply: 'changed on disk during an apply',
  concurrent: 'edited by another user',
};

const labelOf = (f) => FIELD_LABELS[f] || f;
const textOf = (v) => (v === null || v === undefined ? '' : String(v));

// SyncPage is the T16 "Conflicts" page: open sync conflicts (pick a value
// per field and resolve), missing tracks (relink to a candidate file or
// delete the row) and the trash (restore displaced files).
export default function SyncPage() {
  const { showAlert, showConfirm } = useModal();
  const [tab, setTab] = useState('conflicts');
  const [groups, setGroups] = useState([]);
  const [missing, setMissing] = useState([]);
  const [trash, setTrash] = useState([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  // Per conflict row: 'db' | 'file' | 'custom' (+ value for custom).
  const [choices, setChoices] = useState({});
  // Per missing track: the candidate path the user picked.
  const [relinkPick, setRelinkPick] = useState({});

  const loadAll = useCallback(async () => {
    try {
      const [cRes, mRes, tRes] = await Promise.all([
        apiFetch('/sync/conflicts?status=open'),
        apiFetch('/sync/missing'),
        apiFetch('/sync/trash'),
      ]);
      if (cRes.ok) setGroups((await cRes.json()) || []);
      if (mRes.ok) setMissing((await mRes.json()) || []);
      if (tRes.ok) setTrash((await tRes.json()) || []);
    } catch (e) {
      console.error('Failed to load sync data:', e);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    loadAll();
  }, [loadAll]);

  // withJob runs a mutation that may kick off a background job (resolve /
  // resolve-all / relink) and waits for it when one was returned; 200s
  // with an empty body (restore, delete) are handled too.
  const withJob = async (label, fn) => {
    setBusy(true);
    try {
      const res = await fn();
      if (!res.ok) throw new Error(await errorFromResponse(res));
      let data = null;
      const text = await res.text();
      if (text) {
        try {
          data = JSON.parse(text);
        } catch (e) {
          data = null;
        }
      }
      if (data && data.job_id) await waitForJob(data.job_id);
      showAlert('Success', label);
      await loadAll();
    } catch (e) {
      showAlert('Error', e.message);
    }
    setBusy(false);
  };

  // ---- conflicts -----------------------------------------------------------

  const choiceOf = (id) => choices[id] || null;

  const setChoice = (id, choice, value) => {
    setChoices((prev) => ({
      ...prev,
      [id]: { choice, value: value === undefined ? '' : value },
    }));
  };

  const fillTrack = (group, choice) => {
    setChoices((prev) => {
      const next = { ...prev };
      group.fields.forEach((f) => {
        next[f.conflict_id || f.id] = { choice };
      });
      return next;
    });
  };

  const allChosen = (group) =>
    group.fields.every(
      (f) => choiceOf(f.conflict_id || f.id) && choiceOf(f.conflict_id || f.id).choice,
    );

  const resolveTrack = (group) => {
    const resolutions = group.fields.map((f) => ({
      conflict_id: f.conflict_id || f.id,
      choice: choiceOf(f.conflict_id || f.id).choice,
      ...(choiceOf(f.conflict_id || f.id).choice === 'custom'
        ? { value: choiceOf(f.conflict_id || f.id).value }
        : {}),
    }));
    withJob('Resolutions applied.', () =>
      apiFetch('/sync/conflicts/resolve', {
        method: 'POST',
        body: JSON.stringify({ resolutions }),
      }),
    );
  };

  const resolveAll = (choice) =>
    withJob(`All conflicts resolved with ${choice === 'file' ? 'file' : 'JellyIDE'} values.`, () =>
      apiFetch('/sync/conflicts/resolve-all', {
        method: 'POST',
        body: JSON.stringify({ choice }),
      }),
    );

  const dismiss = async (conflict) => {
    setBusy(true);
    try {
      const res = await apiFetch(`/sync/conflicts/${conflict.conflict_id || conflict.id}/dismiss`, {
        method: 'POST',
      });
      if (!res.ok) throw new Error(await errorFromResponse(res));
      await loadAll();
    } catch (e) {
      showAlert('Error', e.message);
    }
    setBusy(false);
  };

  // ---- missing -------------------------------------------------------------

  const relink = (m) => {
    const path = relinkPick[m.track_id];
    if (!path) return;
    withJob('Track relinked.', () =>
      apiFetch(`/sync/missing/${m.track_id}/relink`, {
        method: 'POST',
        body: JSON.stringify({ path }),
      }),
    );
  };

  const deleteMissing = async (m) => {
    const ok = await showConfirm(
      'Delete track row?',
      `Delete "${m.title}" from the database? The file is already gone; this only removes the row and its history.`,
    );
    if (!ok) return;
    setBusy(true);
    try {
      const res = await apiFetch(`/sync/missing/${m.track_id}`, {
        method: 'DELETE',
      });
      if (!res.ok) throw new Error(await errorFromResponse(res));
      showAlert('Success', 'Track row deleted.');
      await loadAll();
    } catch (e) {
      showAlert('Error', e.message);
    }
    setBusy(false);
  };

  // ---- trash -----------------------------------------------------------------

  const restore = (entry) => {
    withJob('File restored.', () =>
      apiFetch(`/sync/trash/${entry.id}/restore`, { method: 'POST' }),
    );
  };

  if (loading) {
    return (
      <div className="page-container fade-in-up">
        <div className="page-header">
          <h2 className="page-title">Conflicts</h2>
        </div>
        <p className="lookup-status">Loading…</p>
      </div>
    );
  }

  return (
    <div className="page-container fade-in-up">
      <div className="page-header">
        <h2 className="page-title">Conflicts</h2>
        <p className="page-subtitle">
          Changes made outside JellyIDE, missing files and trashed files.
        </p>
      </div>

      <div className="sync-tabs">
        <button
          type="button"
          className={`sync-tab ${tab === 'conflicts' ? 'active' : ''}`}
          onClick={() => setTab('conflicts')}
        >
          Conflicts{groups.length > 0 ? ` (${groups.length})` : ''}
        </button>
        <button
          type="button"
          className={`sync-tab ${tab === 'missing' ? 'active' : ''}`}
          onClick={() => setTab('missing')}
        >
          Missing files{missing.length > 0 ? ` (${missing.length})` : ''}
        </button>
        <button
          type="button"
          className={`sync-tab ${tab === 'trash' ? 'active' : ''}`}
          onClick={() => setTab('trash')}
        >
          Trash{trash.length > 0 ? ` (${trash.length})` : ''}
        </button>
      </div>

      {tab === 'conflicts' && (
        <section className="sync-section">
          {groups.length === 0 ? (
            <p className="lookup-status">
              No open conflicts. When a file changes outside JellyIDE it is listed here for a
              decision.
            </p>
          ) : (
            <>
              <div className="sync-bulk-actions">
                <button
                  type="button"
                  className="btn-secondary"
                  onClick={() => resolveAll('db')}
                  disabled={busy}
                >
                  Resolve all with JellyIDE values
                </button>
                <button
                  type="button"
                  className="btn-secondary"
                  onClick={() => resolveAll('file')}
                  disabled={busy}
                >
                  Resolve all with file values
                </button>
              </div>
              {groups.map((g) => (
                <div className="card sync-group" key={g.track_id}>
                  <div className="sync-group-head">
                    <h3 className="sync-group-title">{g.track_title}</h3>
                    <div className="sync-group-actions">
                      <button
                        type="button"
                        className="btn-secondary btn-sm"
                        onClick={() => fillTrack(g, 'db')}
                        disabled={busy}
                      >
                        Use all JellyIDE
                      </button>
                      <button
                        type="button"
                        className="btn-secondary btn-sm"
                        onClick={() => fillTrack(g, 'file')}
                        disabled={busy}
                      >
                        Use all file
                      </button>
                      <button
                        type="button"
                        className="btn-primary btn-sm"
                        onClick={() => resolveTrack(g)}
                        disabled={busy || !allChosen(g)}
                      >
                        Resolve
                      </button>
                    </div>
                  </div>
                  <div className="table-scroll">
                    <table className="review-table sync-conflict-table">
                      <thead>
                        <tr>
                          <th>Field</th>
                          <th>JellyIDE (DB)</th>
                          <th>File</th>
                          <th>Use</th>
                          <th></th>
                        </tr>
                      </thead>
                      <tbody>
                        {g.fields.map((f) => {
                          const c = choiceOf(f.conflict_id || f.id);
                          return (
                            <tr key={f.conflict_id || f.id}>
                              <td title={SOURCE_LABELS[f.source] || ''}>
                                {labelOf(f.field)}
                                {f.source && (
                                  <span className="sync-source-hint">
                                    {' '}
                                    ({SOURCE_LABELS[f.source] || f.source})
                                  </span>
                                )}
                              </td>
                              <td className="review-old">{textOf(f.db_value) || '—'}</td>
                              <td className="review-new">{textOf(f.file_value) || '—'}</td>
                              <td className="sync-choice-cell">
                                <label className="sync-radio">
                                  <input
                                    type="radio"
                                    name={`choice-${f.conflict_id || f.id}`}
                                    checked={Boolean(c && c.choice === 'db')}
                                    onChange={() => setChoice(f.conflict_id || f.id, 'db')}
                                    disabled={busy}
                                  />
                                  <span>JellyIDE</span>
                                </label>
                                <label className="sync-radio">
                                  <input
                                    type="radio"
                                    name={`choice-${f.conflict_id || f.id}`}
                                    checked={Boolean(c && c.choice === 'file')}
                                    onChange={() => setChoice(f.conflict_id || f.id, 'file')}
                                    disabled={busy}
                                  />
                                  <span>File</span>
                                </label>
                                <label className="sync-radio">
                                  <input
                                    type="radio"
                                    name={`choice-${f.conflict_id || f.id}`}
                                    checked={Boolean(c && c.choice === 'custom')}
                                    onChange={() => setChoice(f.conflict_id || f.id, 'custom')}
                                    disabled={busy}
                                  />
                                  <span>Custom</span>
                                </label>
                                {c && c.choice === 'custom' && (
                                  <input
                                    className="settings-input sync-custom-input"
                                    value={c.value}
                                    onChange={(e) =>
                                      setChoice(f.conflict_id || f.id, 'custom', e.target.value)
                                    }
                                    placeholder="Value to write to both"
                                    disabled={busy}
                                  />
                                )}
                              </td>
                              <td>
                                <button
                                  type="button"
                                  className="icon-btn"
                                  aria-label={`Dismiss ${labelOf(f.field)} conflict`}
                                  title="Dismiss (keep both values as-is)"
                                  onClick={() => dismiss(f)}
                                  disabled={busy}
                                >
                                  ✕
                                </button>
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                </div>
              ))}
            </>
          )}
        </section>
      )}

      {tab === 'missing' && (
        <section className="sync-section">
          {missing.length === 0 ? (
            <p className="lookup-status">No missing files.</p>
          ) : (
            missing.map((m) => (
              <div className="card sync-group" key={m.track_id}>
                <div className="sync-group-head">
                  <h3 className="sync-group-title">
                    {m.title} <span className="sync-muted">· {m.album}</span>
                  </h3>
                </div>
                <p className="sync-muted sync-path">
                  Expected at: <code>{m.path}</code>
                </p>
                {m.candidates.length === 0 ? (
                  <p className="lookup-status">
                    No matching file found (same size, duration and tags).
                  </p>
                ) : (
                  <div className="sync-candidates">
                    {m.candidates.map((c) => (
                      <label className="sync-radio" key={c.track_id}>
                        <input
                          type="radio"
                          name={`relink-${m.track_id}`}
                          checked={relinkPick[m.track_id] === c.path}
                          onChange={() =>
                            setRelinkPick((prev) => ({ ...prev, [m.track_id]: c.path }))
                          }
                          disabled={busy}
                        />
                        <span>
                          <code className="sync-path-inline">{c.path}</code>
                        </span>
                      </label>
                    ))}
                  </div>
                )}
                <div className="sync-group-actions">
                  <button
                    type="button"
                    className="btn-primary btn-sm"
                    onClick={() => relink(m)}
                    disabled={busy || !relinkPick[m.track_id]}
                  >
                    Relink track
                  </button>
                  <button
                    type="button"
                    className="btn-secondary btn-sm"
                    onClick={() => deleteMissing(m)}
                    disabled={busy}
                  >
                    Delete row
                  </button>
                </div>
              </div>
            ))
          )}
        </section>
      )}

      {tab === 'trash' && (
        <section className="sync-section">
          <p className="lookup-status">
            Files displaced by a “replace” rename are kept here for 30 days.
          </p>
          {trash.length === 0 ? (
            <p className="lookup-status">Trash is empty.</p>
          ) : (
            <div className="table-scroll">
              <table className="review-table">
                <thead>
                  <tr>
                    <th>Original path</th>
                    <th>Moved</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {trash.map((e) => (
                    <tr key={e.id}>
                      <td className="sync-path">
                        <code>{e.original_path}</code>
                      </td>
                      <td>{e.created_at}</td>
                      <td>
                        <button
                          type="button"
                          className="btn-secondary btn-sm"
                          onClick={() => restore(e)}
                          disabled={busy}
                        >
                          Restore
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      )}
    </div>
  );
}
