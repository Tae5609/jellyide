import React, { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { FIELDS, INT_MAX, currentValue, formatDuration, originalValue } from './useEditorState';

// SyncChip marks a track whose sync state needs attention (T16): a
// conflict (changed outside JellyIDE) or a missing file. Clicking it goes
// to the Conflicts page, where both are resolved.
function SyncChip({ state, onGo }) {
  if (state === 'conflict') {
    return (
      <button
        type="button"
        className="sync-chip conflict-chip"
        onClick={onGo}
        title="Changed outside JellyIDE — resolve on the Conflicts page"
      >
        ⚠ Conflict
      </button>
    );
  }
  if (state === 'missing') {
    return (
      <button
        type="button"
        className="sync-chip missing-chip"
        onClick={onGo}
        title="File not found on disk — see the Conflicts page"
      >
        Missing
      </button>
    );
  }
  return null;
}

const TrashIcon = () => (
  <svg
    width="16"
    height="16"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <path d="M3 6h18"></path>
    <path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"></path>
    <path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"></path>
    <line x1="10" y1="11" x2="10" y2="17"></line>
    <line x1="14" y1="11" x2="14" y2="17"></line>
  </svg>
);

const UndoIcon = () => (
  <svg
    width="15"
    height="15"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <path d="M3 7v6h6"></path>
    <path d="M21 17a9 9 0 0 0-15-6.7L3 13"></path>
  </svg>
);

const RedoIcon = () => (
  <svg
    width="15"
    height="15"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <path d="M21 7v6h-6"></path>
    <path d="M3 17a9 9 0 0 1 15-6.7L21 13"></path>
  </svg>
);

function textOf(value) {
  if (value === null || value === undefined) return '';
  return String(value);
}

// TrackGrid renders the desktop table and the mobile card list.
//
// Editing protocol with the parent:
//  - a cell click focuses the cell and calls onStartEdit; the parent flips
//    `editing` on and this grid shows the draft input for the focused cell
//  - Enter commits (onCommitEdit) and calls onExitEdit; the input blurs by
//    committing as well; Esc calls onExitEdit without committing
//  - onCommitEdit returns false to keep the input open (invalid value)
export default function TrackGrid({
  tracks,
  edits,
  selected,
  visibleCols,
  onToggleCol,
  focus,
  onFocusCell,
  onGridFocusChange,
  editing,
  onStartEdit,
  onExitEdit,
  onCommitEdit,
  onSelectToggle,
  onSelectRange,
  onSelectAll,
  onClearSelection,
  onFillDown,
  onAutoNumber,
  canUndo,
  canRedo,
  onUndo,
  onRedo,
  onDeleteTrack,
  onSaveCard,
  onError,
}) {
  const navigate = useNavigate();
  const [colMenuOpen, setColMenuOpen] = useState(false);
  const [sheetTrackId, setSheetTrackId] = useState(null);
  const [sheetDrafts, setSheetDrafts] = useState({});
  const anchorRef = useRef(null);
  const colMenuRef = useRef(null);
  const gridRef = useRef(null);
  const onGridFocusChangeRef = useRef(onGridFocusChange);
  onGridFocusChangeRef.current = onGridFocusChange;

  const focusKey = focus ? `${focus.row}:${focus.col}` : '';
  const [draft, setDraft] = useState('');
  const [draftToken, setDraftToken] = useState('');
  const closeSheet = () => setSheetTrackId(null);

  // (Re)initialise the draft whenever a cell enters edit mode, including a
  // re-edit of the same cell. Adjusting state during render (React's
  // "adjust state when a prop changes" pattern) instead of in a passive
  // effect keeps the first edit paint synchronous — the update no longer
  // lands in a separate round-trip that tests see outside act().
  const editToken = editing && focus && tracks[focus.row] ? focusKey : '';
  if (editToken === '' && draftToken !== '') {
    setDraftToken('');
  } else if (editToken !== '' && draftToken !== editToken) {
    setDraftToken(editToken);
    setDraft(textOf(currentValue(edits, tracks[focus.row], focus.col)));
  }

  // Close the column menu on outside click.
  useEffect(() => {
    if (!colMenuOpen) return undefined;
    const onDocMouseDown = (e) => {
      if (colMenuRef.current && !colMenuRef.current.contains(e.target)) {
        setColMenuOpen(false);
      }
    };
    document.addEventListener('mousedown', onDocMouseDown);
    return () => document.removeEventListener('mousedown', onDocMouseDown);
  }, [colMenuOpen]);

  // The parent gates Ctrl/⌘+A on the grid actually having focus. Focus
  // events alone would not cover a pointer press on a non-focusable panel
  // area, so also listen for pointer presses outside the grid card.
  useEffect(() => {
    const onDocPointerDown = (e) => {
      const el = gridRef.current;
      if (el && !el.contains(e.target)) onGridFocusChangeRef.current?.(false);
    };
    document.addEventListener('pointerdown', onDocPointerDown);
    return () => document.removeEventListener('pointerdown', onDocPointerDown);
  }, []);

  const allSelected = tracks.length > 0 && tracks.every((t) => selected[t.id]);

  const handleHeaderSelectAll = (e) => {
    e.stopPropagation();
    if (allSelected) onClearSelection();
    else onSelectAll();
  };

  const handleCellClick = (e, row, col) => {
    const track = tracks[row];
    if (!track) return;
    onGridFocusChange?.(true);
    if (e.shiftKey) {
      const anchor = anchorRef.current || track.id;
      onSelectRange(anchor, track.id);
      return;
    }
    anchorRef.current = track.id;
    onFocusCell(row, col);
    onStartEdit();
  };

  const handleCheckbox = (e, trackId) => {
    e.stopPropagation();
    anchorRef.current = trackId;
    onGridFocusChange?.(true);
    onSelectToggle(trackId);
  };

  const commit = () =>
    focus && tracks[focus.row] ? onCommitEdit(tracks[focus.row].id, focus.col, draft) : true;

  const moveFocus = (dir) => {
    if (!focus) return null;
    const cols = visibleCols;
    let ci = cols.indexOf(focus.col) + dir;
    let row = focus.row;
    if (ci < 0) {
      ci = cols.length - 1;
      row = Math.max(0, row - 1);
    } else if (ci >= cols.length) {
      ci = 0;
      row = Math.min(tracks.length - 1, row + 1);
    }
    return row >= 0 && row < tracks.length ? { row, col: cols[ci] } : null;
  };

  const handleEditKeyDown = (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      if (commit()) onExitEdit();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      onExitEdit();
    } else if (e.key === 'Tab') {
      e.preventDefault();
      const next = moveFocus(e.shiftKey ? -1 : 1);
      if (commit() && next) onFocusCell(next.row, next.col);
    }
  };

  const handleCellBlur = () => {
    // Clicking away commits the draft and leaves edit mode (Enter/Tab/Esc
    // handle their own transitions).
    if (commit()) onExitEdit();
  };

  // ---- mobile bottom sheet -------------------------------------------------

  const openSheet = (trackId) => {
    const track = tracks.find((t) => t.id === trackId);
    if (!track) return;
    const drafts = {};
    FIELDS.forEach((f) => {
      drafts[f.key] = textOf(currentValue(edits, track, f.key));
    });
    setSheetDrafts(drafts);
    setSheetTrackId(trackId);
  };

  const sheetTrack = sheetTrackId ? tracks.find((t) => t.id === sheetTrackId) : null;

  const saveSheet = () => {
    if (!sheetTrack) return;
    const entries = [];
    let invalid = false;
    FIELDS.forEach((f) => {
      const raw = (sheetDrafts[f.key] ?? '').trim();
      const orig = originalValue(sheetTrack, f.key);
      let value;
      if (f.kind === 'int') {
        if (raw === '') {
          value = null;
        } else if (!/^\d+$/.test(raw) || Number(raw) > INT_MAX[f.key]) {
          onError(`${f.label} must be a whole number between 0 and ${INT_MAX[f.key]}`);
          invalid = true;
          return;
        } else {
          value = Number(raw);
        }
      } else if (f.key === 'title' && raw === '') {
        onError('Title must not be empty');
        invalid = true;
        return;
      } else {
        value = raw === '' ? null : raw;
      }
      if (value !== orig) entries.push({ trackId: sheetTrack.id, field: f.key, value });
    });
    if (invalid) return;
    if (entries.length === 0) {
      closeSheet();
      return;
    }
    if (onSaveCard(sheetTrack.id, entries)) closeSheet();
  };

  const sheetField = (f) => (
    <label className="md-sheet-field" key={f.key}>
      <span className="md-sheet-label">{f.label}</span>
      <input
        value={sheetDrafts[f.key] ?? ''}
        onChange={(e) => setSheetDrafts({ ...sheetDrafts, [f.key]: e.target.value })}
        inputMode={f.kind === 'int' ? 'numeric' : 'text'}
      />
    </label>
  );

  // ---- table ---------------------------------------------------------------

  const dirtyTracks = new Set(Object.keys(edits).map((k) => k.slice(0, k.indexOf('.'))));

  const colHeader = (f) => (
    <th
      key={f.key}
      className="md-th"
      title={
        visibleCols.length === 1
          ? 'At least one column must stay visible'
          : 'Toggle via the Columns menu'
      }
    >
      {f.label}
    </th>
  );

  const cols = FIELDS.filter((f) => visibleCols.includes(f.key));

  const table = (
    <div className="table-scroll metadata-table-wrap">
      <table className="metadata-table">
        <thead>
          <tr>
            <th className="th-track-idx md-th-check">
              <input
                type="checkbox"
                aria-label="Select all rows"
                checked={allSelected}
                onChange={handleHeaderSelectAll}
                onClick={(e) => e.stopPropagation()}
              />
            </th>
            {cols.map(colHeader)}
            <th>Duration</th>
            <th className="th-track-actions"></th>
          </tr>
        </thead>
        <tbody>
          {tracks.map((track, idx) => {
            const rowSelected = Boolean(selected[track.id]);
            const isFocusedRow = focus && focus.row === idx;
            return (
              <tr
                key={track.id}
                className={`md-row ${rowSelected ? 'selected' : ''} ${dirtyTracks.has(track.id) ? 'dirty' : ''}`}
              >
                <td className="td-center-muted md-td-check">
                  <input
                    type="checkbox"
                    aria-label={`Select ${track.title}`}
                    checked={rowSelected}
                    onChange={(e) => handleCheckbox(e, track.id)}
                    onClick={(e) => e.stopPropagation()}
                  />
                </td>
                {cols.map((f) => {
                  const isFocusedCell = isFocusedRow && focus.col === f.key;
                  const isEditingCell = editing && isFocusedCell;
                  const value = currentValue(edits, track, f.key);
                  const dirty = `${track.id}.${f.key}` in edits;
                  return (
                    <td
                      key={f.key}
                      className={`md-td ${isEditingCell ? 'editing' : ''} ${isFocusedCell ? 'focused' : ''}`}
                      onClick={(e) => handleCellClick(e, idx, f.key)}
                    >
                      {isEditingCell ? (
                        <input
                          className="md-cell-input"
                          value={draft}
                          autoFocus
                          onChange={(e) => setDraft(e.target.value)}
                          onKeyDown={handleEditKeyDown}
                          onBlur={handleCellBlur}
                          inputMode={f.kind === 'int' ? 'numeric' : 'text'}
                        />
                      ) : (
                        <div
                          className={`md-cell-display ${dirty ? 'modified' : ''}`}
                          data-testid={`cell-${track.id}-${f.key}`}
                        >
                          {textOf(value) || (f.key === 'title' ? '' : '·')}
                        </div>
                      )}
                    </td>
                  );
                })}
                <td className="td-center-muted">{formatDuration(track.duration)}</td>
                <td className="td-center td-track-actions">
                  <SyncChip
                    state={track.state}
                    onGo={(e) => {
                      e.stopPropagation();
                      navigate('/sync');
                    }}
                  />
                  <button
                    className="icon-btn"
                    onClick={(e) => {
                      e.stopPropagation();
                      onDeleteTrack(track.id);
                    }}
                    title="Delete track from DB"
                  >
                    <TrashIcon />
                  </button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );

  const cards = (
    <div className="track-cards">
      {tracks.map((track, idx) => {
        const dirty = dirtyTracks.has(track.id);
        return (
          <div
            className="track-card md-track-card"
            key={track.id}
            role="button"
            tabIndex={0}
            onClick={() => openSheet(track.id)}
            onKeyDown={(e) => e.key === 'Enter' && openSheet(track.id)}
          >
            <div className="track-card-head">
              <span className="track-idx">
                {track.track_number ?? idx + 1} · {formatDuration(track.duration)}
              </span>
              {dirty && <span className="md-dirty-badge">modified</span>}
              <SyncChip
                state={track.state}
                onGo={(e) => {
                  e.stopPropagation();
                  navigate('/sync');
                }}
              />
              <button
                className="icon-btn"
                onClick={(e) => {
                  e.stopPropagation();
                  onDeleteTrack(track.id);
                }}
                title="Delete track from DB"
              >
                <TrashIcon />
              </button>
            </div>
            <div className={`track-old ${dirty ? 'modified' : ''}`}>
              {currentValue(edits, track, 'title') || track.title}
            </div>
            <div className="md-card-sub">
              {textOf(currentValue(edits, track, 'artist')) || textOf(track.album_artist) || '—'}
            </div>
          </div>
        );
      })}
    </div>
  );

  return (
    <div className="metadata-tracks">
      <div
        className="card card-flush"
        ref={gridRef}
        onFocus={() => onGridFocusChange?.(true)}
        onBlur={(e) => {
          const rt = e.relatedTarget;
          if (rt && !e.currentTarget.contains(rt)) onGridFocusChange?.(false);
        }}
      >
        <div className="md-toolbar">
          <button
            type="button"
            className="btn-secondary btn-sm"
            onClick={onFillDown}
            title="Copy the focused cell to the selected rows (Ctrl/⌘+D)"
          >
            Fill down
          </button>
          <button
            type="button"
            className="btn-secondary btn-sm"
            onClick={onAutoNumber}
            title="Set track numbers 1..n in the current order"
          >
            Auto-number
          </button>
          <div className="md-toolbar-right">
            <button
              type="button"
              className="btn-secondary btn-sm"
              onClick={onUndo}
              disabled={!canUndo}
              title="Undo (Ctrl/⌘+Z)"
            >
              <UndoIcon />
            </button>
            <button
              type="button"
              className="btn-secondary btn-sm"
              onClick={onRedo}
              disabled={!canRedo}
              title="Redo (Ctrl/⌘+Shift+Z)"
            >
              <RedoIcon />
            </button>
            <div className="md-col-menu" ref={colMenuRef}>
              <button
                type="button"
                className="btn-secondary btn-sm"
                onClick={() => setColMenuOpen(!colMenuOpen)}
                aria-expanded={colMenuOpen}
              >
                Columns ▾
              </button>
              {colMenuOpen && (
                <div className="md-col-list">
                  {FIELDS.map((f) => (
                    <label key={f.key} className="md-col-item">
                      <input
                        type="checkbox"
                        checked={visibleCols.includes(f.key)}
                        onChange={() => onToggleCol(f.key)}
                      />
                      {f.label}
                    </label>
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>

        {table}
        {cards}

        {sheetTrack && (
          <>
            <div className="md-sheet-backdrop" onClick={closeSheet} aria-hidden="true" />
            <div className="md-sheet" role="dialog" aria-label={`Edit ${sheetTrack.title}`}>
              <div className="md-sheet-head">
                <h3>Edit track</h3>
                <button type="button" className="icon-btn" onClick={closeSheet} aria-label="Close">
                  ✕
                </button>
              </div>
              <div className="md-sheet-fields">
                {FIELDS.map(sheetField)}
                <div className="md-sheet-field">
                  <span className="md-sheet-label">Duration</span>
                  <div className="md-sheet-readonly">{formatDuration(sheetTrack.duration)}</div>
                </div>
              </div>
              <div className="md-sheet-actions">
                <button type="button" className="btn-secondary" onClick={closeSheet}>
                  Cancel
                </button>
                <button type="button" className="btn-primary" onClick={saveSheet}>
                  Save changes
                </button>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
