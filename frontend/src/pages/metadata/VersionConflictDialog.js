import React, { useMemo, useState } from 'react';

// Field label lookup shared with the grid (avoids importing the whole
// editor state just for a label).
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

const labelOf = (field) => FIELD_LABELS[field] || field;

const textOf = (v) => (v === null || v === undefined ? '' : String(v));

// VersionConflictDialog is shown when an apply loses the optimistic lock
// (409, or a job result with reason=version_conflict). For every conflicted
// track and every field in the user's edit, the user picks per field:
// keep the current (theirs) value, or overwrite with their own edit.
//
// conflicts: [{track_id, current: {field: value}, current_version}]
// edits:     the patch edits that were sent ({track_id, fields, ...})
// onResolve(edits): the parent re-applies the adjusted edits
// onClose():  cancel (pending edits stay in the review, unchanged)
export default function VersionConflictDialog({
  conflicts,
  edits,
  tracks = [],
  busy = false,
  onResolve,
  onClose,
}) {
  // Per (trackId.field): 'theirs' | 'mine'. Default 'theirs' — never
  // overwrite a value the user has not seen.
  const [choices, setChoices] = useState({});

  const titleOf = (trackId) => {
    const t = tracks.find((tr) => tr.id === trackId);
    return (t && t.title) || trackId;
  };

  const rows = useMemo(() => {
    const out = [];
    conflicts.forEach((c) => {
      const edit = (edits || []).find((e) => e.track_id === c.track_id);
      if (!edit) return;
      Object.keys(edit.fields).forEach((field) => {
        out.push({
          trackId: c.track_id,
          trackTitle: titleOf(c.track_id),
          field,
          mine: edit.fields[field],
          theirs: c.current && c.current[field] !== undefined ? c.current[field] : null,
          currentVersion: c.current_version,
        });
      });
    });
    return out;
  }, [conflicts, edits, tracks]);

  const choiceOf = (trackId, field) => choices[`${trackId}.${field}`] || 'theirs';

  const setChoice = (trackId, field, value) => {
    setChoices((prev) => ({ ...prev, [`${trackId}.${field}`]: value }));
  };

  const buildResolvedEdits = (force) => {
    const next = (edits || []).map((e) => ({ ...e, fields: { ...e.fields } }));
    const byTrack = {};
    conflicts.forEach((c) => {
      byTrack[c.track_id] = c;
    });
    next.forEach((e) => {
      const c = byTrack[e.track_id];
      if (!c) return; // untouched track: re-apply as-is
      const kept = {};
      Object.keys(e.fields).forEach((field) => {
        const pick = force || choiceOf(e.track_id, field);
        if (pick === 'theirs') {
          // Keep the current value: drop the field from this edit.
          return;
        }
        kept[field] = e.fields[field];
      });
      if (Object.keys(kept).length === 0) {
        // Nothing left to apply for this track.
        e.__drop = true;
        return;
      }
      e.fields = kept;
      // Re-apply against the version we were told about.
      e.expected_version = c.current_version;
    });
    return next.filter((e) => !e.__drop);
  };

  return (
    <div className="overlay" role="dialog" aria-label="Version conflict">
      <div className="dialog review-dialog fade-in-up">
        <div className="dialog-head">
          <h3 className="dialog-title">Changes made elsewhere</h3>
          <button
            type="button"
            className="icon-btn"
            onClick={onClose}
            aria-label="Close conflict dialog"
          >
            ✕
          </button>
        </div>
        <div className="dialog-body">
          <p className="lookup-status">
            These tracks changed since you started editing. Pick for each field
            whether to keep the current value or overwrite it with yours.
          </p>
          <div className="table-scroll">
            <table className="review-table">
              <thead>
                <tr>
                  <th>Track</th>
                  <th>Field</th>
                  <th>Theirs (current)</th>
                  <th>Yours</th>
                  <th>Use</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => {
                  const key = `${r.trackId}.${r.field}`;
                  const pick = choiceOf(r.trackId, r.field);
                  return (
                    <tr key={key}>
                      <td>{r.trackTitle}</td>
                      <td>{labelOf(r.field)}</td>
                      <td className="review-old">{textOf(r.theirs) || '—'}</td>
                      <td className="review-new">
                        {r.mine === null ? '∅ (cleared)' : textOf(r.mine) || '—'}
                      </td>
                      <td>
                        <label className="sync-radio">
                          <input
                            type="radio"
                            name={key}
                            checked={pick === 'theirs'}
                            onChange={() => setChoice(r.trackId, r.field, 'theirs')}
                            disabled={busy}
                          />
                          <span>Theirs</span>
                        </label>
                        <label className="sync-radio">
                          <input
                            type="radio"
                            name={key}
                            checked={pick === 'mine'}
                            onChange={() => setChoice(r.trackId, r.field, 'mine')}
                            disabled={busy}
                          />
                          <span>Yours</span>
                        </label>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
        <div className="dialog-actions">
          <button type="button" className="btn-secondary" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="btn-secondary"
            onClick={() => onResolve(buildResolvedEdits('theirs'))}
            disabled={busy}
            title="For every field, keep the current value and drop your edit there"
          >
            Keep all theirs
          </button>
          <button
            type="button"
            className="btn-secondary"
            onClick={() => onResolve(buildResolvedEdits('mine'))}
            disabled={busy}
            title="Overwrite every field with your edit"
          >
            Overwrite all with yours
          </button>
          <button
            type="button"
            className="btn-primary"
            onClick={() => onResolve(buildResolvedEdits(null))}
            disabled={busy}
            title="Apply the per-field choices above"
          >
            {busy ? 'Applying…' : 'Apply choices'}
          </button>
        </div>
      </div>
    </div>
  );
}
