import React, { useEffect, useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { apiFetch } from '../../api';
import { useModal } from '../../components/ModalContext';
import {
  FIELDS,
  INT_MAX,
  fieldDef,
  useEditorState,
  getDiff,
  pendingCount,
  buildPatchEdits,
  fillDownEntries,
  autoNumberEntries,
  selectedIds,
  waitForJob,
  errorFromResponse,
} from './useEditorState';
import TrackGrid from './TrackGrid';
import BulkEditPanel from './BulkEditPanel';
import FindReplacePanel from './FindReplacePanel';
import CoverArtPanel from './CoverArtPanel';
import LookupDialog from './LookupDialog';
import HistoryDrawer from './HistoryDrawer';
import VersionConflictDialog from './VersionConflictDialog';
import useShortcuts from './useShortcuts';

// collectFlagged pulls the per-track conflict entries out of a finished
// apply job's details (version_conflict = the optimistic lock fired at run
// time; external_change = the file moved under us during the apply).
function collectFlagged(job) {
  const version = [];
  const external = [];
  let details = job && job.details;
  if (typeof details === 'string') {
    try {
      details = JSON.parse(details);
    } catch (e) {
      details = null;
    }
  }
  const results = (details && details.results) || [];
  results.forEach((r) => {
    if (!r || !r.track_id) return;
    if (r.reason === 'version_conflict') version.push(r);
    else if (r.reason === 'external_change') external.push(r);
  });
  return { version, external };
}

const COLS_STORAGE_KEY = 'jellyide.metadata.columns.v1';
const ALL_COLS = FIELDS.map((f) => f.key);

function loadColumns() {
  try {
    const raw = localStorage.getItem(COLS_STORAGE_KEY);
    if (!raw) return ALL_COLS;
    const parsed = JSON.parse(raw);
    const visible = ALL_COLS.filter((k) => Array.isArray(parsed) && parsed.includes(k));
    return visible.length > 0 ? visible : ALL_COLS;
  } catch (e) {
    return ALL_COLS;
  }
}

const SHORTCUTS = [
  ['Ctrl/⌘+S', 'Review & apply pending changes'],
  ['Ctrl/⌘+Z', 'Undo pending edit'],
  ['Ctrl/⌘+Shift+Z or Ctrl/⌘+Y', 'Redo pending edit'],
  ['Arrow keys', 'Move the cell focus'],
  ['Enter', 'Edit the focused cell'],
  ['Tab / Shift+Tab', 'Next / previous cell'],
  ['Esc', 'Cancel edit or close the top overlay'],
  ['Ctrl/⌘+A', 'Select all rows (grid focused)'],
  ['Ctrl/⌘+D', 'Fill the focused cell down to the selected rows'],
  ['/', 'Focus find & replace'],
  ['L', 'Open MusicBrainz lookup'],
  ['H', 'Open change history'],
  ['?', 'Show this help'],
  ['g then d / l / j / s', 'Go to downloads / library / jobs / settings'],
];

export default function MetadataPage() {
  const { artistId, albumId } = useParams();
  const navigate = useNavigate();
  const { showAlert, showConfirm, isOpen: modalOpen } = useModal();

  const [album, setAlbum] = useState(null);
  const [analysis, setAnalysis] = useState(null);
  const { state, dispatch } = useEditorState([]);

  // Album title inline edit
  const [isEditingAlbum, setIsEditingAlbum] = useState(false);
  const [newAlbumTitle, setNewAlbumTitle] = useState('');

  // Grid focus / editing
  const [focus, setFocus] = useState(null); // {row, col}
  const [editing, setEditing] = useState(false);
  const [visibleCols, setVisibleCols] = useState(loadColumns);
  // True while the pointer/keyboard is inside the grid card. Gates the
  // Ctrl/⌘+A shortcut ("select all rows when the grid has focus") instead
  // of the cell-focus state, which would persist after clicking a panel.
  const [gridFocused, setGridFocused] = useState(false);

  // Overlays
  const [reviewOpen, setReviewOpen] = useState(false);
  const [lookupOpen, setLookupOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const [mobilePanel, setMobilePanel] = useState(null); // 'bulk' | 'find' | 'cover'
  const [toolsOpen, setToolsOpen] = useState(false);

  // Jobs / cover
  const [applying, setApplying] = useState(false);
  const [coverKey, setCoverKey] = useState(0);
  const [lookupCoverUrl, setLookupCoverUrl] = useState('');
  const [coverBusy, setCoverBusy] = useState(false);

  // T16: version-lock conflicts (409 or job results) and rename collisions
  // (per-track suffix / skip / replace chosen in the review step).
  const [conflictInfo, setConflictInfo] = useState(null); // {conflicts, edits}
  const [collisionReports, setCollisionReports] = useState([]);
  const [collisionChoices, setCollisionChoices] = useState({});

  const cols = FIELDS.filter((f) => visibleCols.includes(f.key));
  const pc = pendingCount(state);
  const diffRows = getDiff(state);

  const refreshData = async () => {
    try {
      const aRes = await apiFetch(`/library/albums/${albumId}`);
      if (!aRes.ok) throw new Error(await errorFromResponse(aRes));
      const aData = await aRes.json();
      setAlbum(aData);
      setNewAlbumTitle(aData.title);

      const tRes = await apiFetch(`/library/albums/${albumId}/tracks`);
      if (!tRes.ok) throw new Error(await errorFromResponse(tRes));
      const tData = await tRes.json();
      dispatch({ type: 'INITIALIZE', tracks: tData || [] });
      setFocus(null);
      setEditing(false);

      const anRes = await apiFetch(`/metadata/analyze/${albumId}`);
      if (anRes.ok) setAnalysis(await anRes.json());
    } catch (e) {
      console.error('Error refreshing data:', e);
    }
  };

  const refreshCover = async () => {
    setCoverKey((k) => k + 1);
    try {
      const aRes = await apiFetch(`/library/albums/${albumId}`);
      if (aRes.ok) setAlbum(await aRes.json());
    } catch (e) {
      // cover bump is best effort
    }
  };

  useEffect(() => {
    if (!albumId) return;
    refreshData();
  }, [albumId]);

  useEffect(() => {
    try {
      localStorage.setItem(COLS_STORAGE_KEY, JSON.stringify(visibleCols));
    } catch (e) {
      // private mode — column memory is best effort
    }
  }, [visibleCols]);

  // If the focused cell sits in a column the user just hid, drop the cell
  // focus instead of pointing the shortcuts at an invisible cell.
  useEffect(() => {
    setFocus((prev) => (prev && !visibleCols.includes(prev.col) ? null : prev));
  }, [visibleCols]);

  // The cell focus is only meaningful while attention is in the grid card.
  // The card's boundary (pointer press outside it / blur to a node outside,
  // see TrackGrid) drives `gridFocused`; when it reports that attention has
  // left, drop the cell focus too, so the grid shortcuts (Tab, Enter,
  // arrows, fill down) stop acting on a cell the user moved away from —
  // otherwise Tab stays captured after the first cell click (WCAG 2.1.2).
  // The drop waits for `editing` to end: the outside press that clears
  // `gridFocused` arrives before the draft input's blur, and the blur must
  // still find the focus it commits the draft from.
  useEffect(() => {
    if (!editing && !gridFocused) setFocus(null);
  }, [editing, gridFocused]);

  // Esc closes the topmost custom overlay (context modals handle their own).
  useEffect(() => {
    const onKey = (e) => {
      if (e.key !== 'Escape' || modalOpen) return;
      if (conflictInfo) setConflictInfo(null);
      else if (reviewOpen) setReviewOpen(false);
      else if (helpOpen) setHelpOpen(false);
      else if (lookupOpen) setLookupOpen(false);
      else if (historyOpen) setHistoryOpen(false);
      else if (mobilePanel) setMobilePanel(null);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [reviewOpen, helpOpen, lookupOpen, historyOpen, mobilePanel, modalOpen, conflictInfo]);

  // ---- album actions -------------------------------------------------------

  const handleRenameAlbum = async () => {
    try {
      const res = await apiFetch(`/library/albums/${albumId}/rename`, {
        method: 'POST',
        body: JSON.stringify({ title: newAlbumTitle }),
      });
      if (!res.ok) throw new Error(await errorFromResponse(res));
      setAlbum({ ...album, title: newAlbumTitle });
      setIsEditingAlbum(false);
    } catch (e) {
      showAlert('Error', e.message);
    }
  };

  const handleDeleteAlbum = async () => {
    const ok = await showConfirm(
      'Delete album?',
      'Are you sure you want to delete this album and all its tracks from the database? This will NOT delete files from disk.'
    );
    if (!ok) return;
    try {
      const res = await apiFetch(`/library/albums/${albumId}`, { method: 'DELETE' });
      if (!res.ok) throw new Error(await errorFromResponse(res));
      navigate(`/library/${artistId}`);
    } catch (e) {
      showAlert('Error', e.message);
    }
  };

  const handleDeleteTrack = async (trackId) => {
    const ok = await showConfirm(
      'Delete track?',
      'Delete this track from the database? (File will stay on disk)'
    );
    if (!ok) return;
    try {
      const res = await apiFetch(`/library/tracks/${trackId}`, { method: 'DELETE' });
      if (!res.ok) throw new Error(await errorFromResponse(res));
      dispatch({ type: 'REMOVE_TRACK', trackId });
    } catch (e) {
      showAlert('Error', e.message);
    }
  };

  // ---- editing -------------------------------------------------------------

  const handleCommitEdit = (trackId, field, raw) => {
    const def = fieldDef(field);
    let value;
    if (def.kind === 'int') {
      const t = raw.trim();
      if (t === '') {
        value = null;
      } else if (!/^\d+$/.test(t) || Number(t) > INT_MAX[field]) {
        showAlert('Invalid value', `${def.label} must be a whole number between 0 and ${INT_MAX[field]}.`);
        return false;
      } else {
        value = Number(t);
      }
    } else {
      const trimmed = raw.trim();
      if (field === 'title' && trimmed === '') {
        showAlert('Invalid value', 'Title must not be empty.');
        return false;
      }
      value = trimmed === '' ? null : trimmed;
    }
    dispatch({ type: 'EDIT', trackId, field, value });
    return true;
  };

  const handleSaveCard = (trackId, entries) => {
    dispatch({ type: 'SET_EDITS', entries });
    return true;
  };

  const toggleCol = (key) => {
    setVisibleCols((prev) => {
      if (prev.includes(key)) {
        if (prev.length === 1) return prev; // keep at least one column
        return prev.filter((k) => k !== key);
      }
      // Reinsert in canonical order.
      const merged = FIELDS.filter((f) => prev.includes(f.key) || f.key === key);
      return merged.map((f) => f.key);
    });
  };

  // Focus is stored as column keys, but `cols` holds field objects —
  // navigate by key.
  const colKeys = cols.map((f) => f.key);

  const moveFocus = (dr, dc) => {
    setFocus((prev) => {
      if (!prev || state.tracks.length === 0) return prev;
      const row = Math.max(0, Math.min(state.tracks.length - 1, prev.row + dr));
      let ci = colKeys.indexOf(prev.col) + dc;
      ci = Math.max(0, Math.min(colKeys.length - 1, ci));
      return { row, col: colKeys[ci] };
    });
  };

  const moveFocusWrap = (dir) => {
    setFocus((prev) => {
      if (!prev || state.tracks.length === 0) return prev;
      let row = prev.row;
      let ci = colKeys.indexOf(prev.col) + dir;
      if (ci < 0) {
        ci = colKeys.length - 1;
        row -= 1;
      } else if (ci >= colKeys.length) {
        ci = 0;
        row += 1;
      }
      if (row < 0 || row >= state.tracks.length) return prev;
      return { row, col: colKeys[ci] };
    });
  };

  const doFillDown = () => {
    if (!focus || !state.tracks[focus.row]) return;
    const entries = fillDownEntries(
      state,
      state.tracks[focus.row].id,
      focus.col
    );
    if (entries.length > 0) dispatch({ type: 'SET_EDITS', entries });
  };

  const doAutoNumber = () => {
    const entries = autoNumberEntries(state);
    if (entries.length > 0) dispatch({ type: 'SET_EDITS', entries });
  };

  const handleBulkApply = (field, value) => {
    const ids = selectedIds(state);
    if (ids.length === 0) return;
    dispatch({
      type: 'SET_EDITS',
      entries: ids.map((id) => ({ trackId: id, field, value })),
    });
  };

  // Server-side find & replace preview, staged as pending edits.
  const runPreview = async (req) => {
    const res = await apiFetch('/metadata/preview', {
      method: 'POST',
      body: JSON.stringify({
        ...req,
        track_ids: state.tracks.map((t) => t.id),
      }),
    });
    if (!res.ok) throw new Error(await errorFromResponse(res));
    const rows = await res.json();
    const entries = [];
    (rows || []).forEach((r) => {
      if (r.new_title === r.old_title) return;
      if (r.new_title === '') {
        if (req.field === 'title') return; // a title can never be cleared
        entries.push({ trackId: r.track_id, field: req.field, value: null });
        return;
      }
      entries.push({ trackId: r.track_id, field: req.field, value: r.new_title });
    });
    if (entries.length > 0) dispatch({ type: 'SET_EDITS', entries });
    return entries.length;
  };

  // ---- review & apply ------------------------------------------------------

  const openReview = () => {
    if (pc === 0) return;
    setReviewOpen(true);
    setCollisionReports([]);
    setCollisionChoices({});
    // When a pending edit renames a file onto an existing name, ask the
    // backend which renames collide so the review can offer suffix / skip /
    // replace per track. A failed preview must not block applying.
    const patchEdits = buildPatchEdits(state);
    if (!patchEdits.some((e) => e.fields.title !== undefined)) return;
    apiFetch('/metadata/rename-collisions', {
      method: 'POST',
      body: JSON.stringify({ edits: patchEdits }),
    })
      .then((r) => (r.ok ? r.json() : []))
      .then((reports) => {
        const colliding = (reports || []).filter((r) => r.collides);
        if (colliding.length === 0) return;
        setCollisionReports(colliding);
        const choices = {};
        colliding.forEach((r) => {
          choices[r.track_id] = 'suffix';
        });
        setCollisionChoices(choices);
      })
      .catch(() => {});
  };

  const titleOf = (trackId) => {
    const t = state.tracks.find((tr) => tr.id === trackId);
    return (t && t.title) || trackId;
  };

  // applyEdits sends one PATCH batch and handles the T16 outcomes: 409
  // (every track stale -> the conflict dialog), per-track version_conflict
  // job results (the dialog) and external_change job results (an alert;
  // the divergences live on the Conflicts page).
  const applyEdits = async (patchEdits) => {
    if (patchEdits.length === 0) return;
    setApplying(true);
    try {
      const res = await apiFetch('/metadata/tracks', {
        method: 'PATCH',
        body: JSON.stringify({ edits: patchEdits }),
      });
      if (res.status === 409) {
        let data = null;
        try {
          data = await res.json();
        } catch (e) {
          // non-JSON 409 body; fall through to the generic error
        }
        if (data && Array.isArray(data.conflicts) && data.conflicts.length > 0) {
          setConflictInfo({ conflicts: data.conflicts, edits: patchEdits });
          return;
        }
        throw new Error(
          data && data.error ? data.error : await errorFromResponse(res)
        );
      }
      if (!res.ok) throw new Error(await errorFromResponse(res));
      const { job_id } = await res.json();
      const job = await waitForJob(job_id);
      const flagged = collectFlagged(job);
      if (flagged.version.length > 0) {
        setConflictInfo({ conflicts: flagged.version, edits: patchEdits });
        return;
      }
      // One alert: a second showAlert would replace the first (the modal
      // state is singular), so the external-change note is merged in.
      let note = 'Changes applied successfully.';
      if (flagged.external.length > 0) {
        const names = flagged.external.map((e) => titleOf(e.track_id)).join(', ');
        note += ` Skipped (changed on disk during apply): ${names} — see the Conflicts page.`;
      }
      dispatch({ type: 'APPLIED' });
      setReviewOpen(false);
      await refreshData();
      showAlert('Success', note);
    } catch (e) {
      showAlert('Error', e.message);
    } finally {
      setApplying(false);
    }
  };

  const applyChanges = () => {
    const patchEdits = buildPatchEdits(state, collisionChoices);
    applyEdits(patchEdits);
  };

  // The conflict dialog's onResolve: re-apply the adjusted edits (or finish
  // when everything was kept as "theirs").
  const handleConflictResolve = (edits) => {
    setConflictInfo(null);
    if (edits.length === 0) {
      dispatch({ type: 'APPLIED' });
      setReviewOpen(false);
      refreshData();
      showAlert('Done', 'Kept the current values; the conflicting edits were dropped.');
      return;
    }
    applyEdits(edits);
  };

  // ---- lookup & cover ------------------------------------------------------

  const handleLookupApply = (entries, coverUrl) => {
    if (entries.length > 0) dispatch({ type: 'SET_EDITS', entries });
    setLookupOpen(false);
    let note = `Staged ${entries.length} field${entries.length === 1 ? '' : 's'} as pending edits.`;
    if (coverUrl) {
      setLookupCoverUrl(coverUrl);
      setCoverBusy(true);
      apiFetch(`/library/albums/${albumId}/cover/from-url`, {
        method: 'POST',
        body: JSON.stringify({ url: coverUrl }),
      })
        .then(async (res) => {
          if (!res.ok) throw new Error(await errorFromResponse(res));
          const { job_id } = await res.json();
          await waitForJob(job_id);
          await refreshCover();
        })
        .catch((e) => showAlert('Error', e.message))
        .finally(() => setCoverBusy(false));
      note += ' Cover art is downloading.';
    }
    showAlert('MusicBrainz lookup', note);
  };

  const focusFindInput = () => {
    const el = document.getElementById('find-replace-target');
    if (el) {
      el.focus();
      el.select();
    }
  };

  // ---- keyboard shortcuts ---------------------------------------------------

  useShortcuts(
    {
      onSave: openReview,
      onUndo: () => dispatch({ type: 'UNDO' }),
      onRedo: () => dispatch({ type: 'REDO' }),
      onSelectAll: () => {
        if (gridFocused) dispatch({ type: 'SELECT_ALL' });
      },
      onFillDown: doFillDown,
      onArrowLeft: () => moveFocus(0, -1),
      onArrowRight: () => moveFocus(0, 1),
      onArrowUp: () => moveFocus(-1, 0),
      onArrowDown: () => moveFocus(1, 0),
      onEnter: () => {
        if (focus && !editing) setEditing(true);
      },
      onTab: (shift) => {
        if (focus && !editing) moveFocusWrap(shift ? -1 : 1);
      },
      onEscape: () => {
        if (editing) setEditing(false);
      },
      onFind: focusFindInput,
      onLookup: () => setLookupOpen(true),
      onHistory: () => setHistoryOpen(true),
      onHelp: () => setHelpOpen(true),
    },
    {
      disabled:
        reviewOpen ||
        lookupOpen ||
        historyOpen ||
        helpOpen ||
        Boolean(mobilePanel) ||
        Boolean(conflictInfo) ||
        modalOpen,
      // Capture Tab (cell navigation) only while a cell is focused and not
      // being edited; otherwise native tab order reaches the page controls.
      cellFocused: Boolean(focus && !editing),
    }
  );

  if (!albumId) return null;

  const side = (
    <>
      <BulkEditPanel
        selectedCount={Object.keys(state.selected).length}
        onApply={handleBulkApply}
        onError={(msg) => showAlert('Invalid value', msg)}
      />
      <FindReplacePanel
        trackIds={state.tracks.map((t) => t.id)}
        analysis={analysis}
        onPreview={runPreview}
        onNotice={(msg) => showAlert('Find & replace', msg)}
        onError={(msg) => showAlert('Error', msg)}
      />
      <CoverArtPanel
        albumId={albumId}
        coverKey={coverKey}
        lookupCoverUrl={lookupCoverUrl}
        onDone={refreshCover}
        onError={(msg) => showAlert('Error', msg)}
      />
    </>
  );

  const panelTitles = { bulk: 'Bulk Edit', find: 'Find & Replace', cover: 'Cover Art' };

  return (
    <div className="metadata-layout">
      {/* Header */}
      <div className="metadata-header">
        <button className="btn-secondary" onClick={() => navigate(`/library/${artistId}`)}>
          ← Back
        </button>
        <div className="metadata-header-main">
          {isEditingAlbum ? (
            <div className="album-edit-row">
              <input
                className="album-title-input"
                autoFocus
                value={newAlbumTitle}
                onChange={(e) => setNewAlbumTitle(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && handleRenameAlbum()}
              />
              <button className="btn-primary btn-sm" onClick={handleRenameAlbum}>
                Save
              </button>
              <button className="btn-secondary btn-sm" onClick={() => setIsEditingAlbum(false)}>
                Cancel
              </button>
            </div>
          ) : (
            <h2
              className="album-title-edit"
              onClick={() => setIsEditingAlbum(true)}
              title="Click to rename album"
            >
              {album?.title}
              <span style={{ fontSize: 12, opacity: 0.5 }}>✎</span>
            </h2>
          )}
          <p className="metadata-sub">
            {album?.artist} · {state.tracks.length} tracks
          </p>
        </div>
        <div className="metadata-actions">
          {coverBusy && <span className="md-busy">Setting cover art…</span>}
          <button
            type="button"
            className="btn-secondary desktop-only"
            onClick={() => setHelpOpen(true)}
          >
            Keyboard shortcuts
          </button>
          <button
            type="button"
            className="btn-secondary desktop-only"
            onClick={() => setHistoryOpen(true)}
          >
            History
          </button>
          <button
            type="button"
            className="btn-secondary desktop-only"
            onClick={() => setLookupOpen(true)}
          >
            Lookup
          </button>
          <button
            type="button"
            className="btn-secondary"
            style={{ color: 'var(--danger)', borderColor: 'rgba(239,68,68,0.2)' }}
            onClick={handleDeleteAlbum}
          >
            Delete Album
          </button>
          <button
            type="button"
            className="btn-primary"
            onClick={openReview}
            disabled={pc === 0 || applying}
          >
            {applying ? 'Applying…' : `Review & apply (${pc})`}
          </button>
        </div>
      </div>

      <div className="metadata-body">
        <TrackGrid
          tracks={state.tracks}
          edits={state.edits}
          selected={state.selected}
          visibleCols={visibleCols}
          onToggleCol={toggleCol}
          focus={focus}
          onFocusCell={(row, col) => setFocus({ row, col })}
          onGridFocusChange={setGridFocused}
          editing={editing}
          onStartEdit={() => setEditing(true)}
          onExitEdit={() => setEditing(false)}
          onCommitEdit={handleCommitEdit}
          onSelectToggle={(id) => dispatch({ type: 'SELECT', trackId: id })}
          onSelectRange={(fromId, toId) =>
            dispatch({ type: 'SELECT_RANGE', fromId, toId })
          }
          onSelectAll={() => dispatch({ type: 'SELECT_ALL' })}
          onClearSelection={() => dispatch({ type: 'CLEAR_SELECTION' })}
          onFillDown={doFillDown}
          onAutoNumber={doAutoNumber}
          canUndo={state.undoStack.length > 0}
          canRedo={state.redoStack.length > 0}
          onUndo={() => dispatch({ type: 'UNDO' })}
          onRedo={() => dispatch({ type: 'REDO' })}
          onDeleteTrack={handleDeleteTrack}
          onSaveCard={handleSaveCard}
          onError={(msg) => showAlert('Invalid value', msg)}
        />
        <div className="metadata-side bulk-panel">{side}</div>
      </div>

      {/* Sticky action bar (phones only) keeps apply and the tools reachable */}
      <div className="metadata-actionbar">
        <button
          type="button"
          className="btn-primary"
          onClick={openReview}
          disabled={pc === 0 || applying}
        >
          {applying ? 'Applying…' : `Review & apply (${pc})`}
        </button>
        <div className="md-tools">
          <button
            type="button"
            className="btn-secondary"
            onClick={() => setToolsOpen(!toolsOpen)}
            aria-expanded={toolsOpen}
          >
            Tools ▴
          </button>
          {toolsOpen && (
            <div className="md-tools-menu">
              {[
                ['bulk', 'Bulk edit'],
                ['find', 'Find & replace'],
                ['cover', 'Cover art'],
              ].map(([id, label]) => (
                <button
                  type="button"
                  key={id}
                  onClick={() => {
                    setToolsOpen(false);
                    setMobilePanel(id);
                  }}
                >
                  {label}
                </button>
              ))}
              <button
                type="button"
                onClick={() => {
                  setToolsOpen(false);
                  setLookupOpen(true);
                }}
              >
                Lookup
              </button>
              <button
                type="button"
                onClick={() => {
                  setToolsOpen(false);
                  setHistoryOpen(true);
                }}
              >
                History
              </button>
            </div>
          )}
        </div>
      </div>

      {/* Mobile: bulk / find / cover behind the action bar */}
      {mobilePanel && (
        <div className="overlay md-mobile-panel" onClick={() => setMobilePanel(null)}>
          <div className="dialog fade-in-up" onClick={(e) => e.stopPropagation()}>
            <div className="dialog-head">
              <h3 className="dialog-title">{panelTitles[mobilePanel]}</h3>
              <button
                type="button"
                className="icon-btn"
                onClick={() => setMobilePanel(null)}
                aria-label="Close panel"
              >
                ✕
              </button>
            </div>
            <div className="dialog-body md-mobile-body">
              {mobilePanel === 'bulk' && (
                <BulkEditPanel
                  selectedCount={Object.keys(state.selected).length}
                  onApply={handleBulkApply}
                  onError={(msg) => showAlert('Invalid value', msg)}
                />
              )}
              {mobilePanel === 'find' && (
                <FindReplacePanel
                  trackIds={state.tracks.map((t) => t.id)}
                  analysis={analysis}
                  onPreview={runPreview}
                  onNotice={(msg) => showAlert('Find & replace', msg)}
                  onError={(msg) => showAlert('Error', msg)}
                />
              )}
              {mobilePanel === 'cover' && (
                <CoverArtPanel
                  albumId={albumId}
                  coverKey={coverKey}
                  lookupCoverUrl={lookupCoverUrl}
                  onDone={refreshCover}
                  onError={(msg) => showAlert('Error', msg)}
                />
              )}
            </div>
          </div>
        </div>
      )}

      {/* Review & apply diff */}
      {reviewOpen && (
        <div className="overlay" role="dialog" aria-label="Review changes">
          <div className="dialog review-dialog fade-in-up">
            <div className="dialog-head">
              <h3 className="dialog-title">Review &amp; Apply</h3>
              <button
                type="button"
                className="icon-btn"
                onClick={() => setReviewOpen(false)}
                aria-label="Close review"
              >
                ✕
              </button>
            </div>
            <div className="dialog-body">
              {diffRows.length === 0 ? (
                <p className="lookup-status">Nothing to apply.</p>
              ) : (
                <div className="table-scroll">
                  <table className="review-table">
                    <thead>
                      <tr>
                        <th>Track</th>
                        <th>Field</th>
                        <th>Current</th>
                        <th>New</th>
                      </tr>
                    </thead>
                    <tbody>
                      {diffRows.map((r) => {
                        const track = state.tracks.find((t) => t.id === r.trackId);
                        return (
                          <tr key={`${r.trackId}-${r.field}`}>
                            <td>{(track && track.title) || r.trackId}</td>
                            <td>{r.field}</td>
                            <td className="review-old">
                              {r.from === null || r.from === undefined ? '—' : r.from}
                            </td>
                            <td className="review-new">
                              {r.to === null ? '∅ (cleared)' : r.to}
                            </td>
                          </tr>
                        );
                      })}
                      {collisionReports.map((r) => (
                        <tr key={`collision-${r.track_id}`} className="review-collision-row">
                          <td>{titleOf(r.track_id)}</td>
                          <td>File name clash</td>
                          <td className="review-old" colSpan={2}>
                            {r.existing_name || r.target_path}
                          </td>
                          <td>
                            <select
                              className="settings-input collision-select"
                              value={collisionChoices[r.track_id] || 'suffix'}
                              onChange={(e) =>
                                setCollisionChoices((prev) => ({
                                  ...prev,
                                  [r.track_id]: e.target.value,
                                }))
                              }
                              disabled={applying}
                              aria-label={`Rename collision for ${titleOf(r.track_id)}`}
                            >
                              <option value="suffix">Add a number (file keeps a copy name)</option>
                              <option value="skip">Skip the rename (keep the file name)</option>
                              <option value="replace">Replace the file (old one goes to trash)</option>
                            </select>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
            <div className="dialog-actions">
              <button
                type="button"
                className="btn-secondary"
                onClick={() => setReviewOpen(false)}
                disabled={applying}
              >
                Cancel
              </button>
              <button
                type="button"
                className="btn-primary"
                onClick={applyChanges}
                disabled={applying || diffRows.length === 0}
              >
                {applying
                  ? 'Applying…'
                  : `Apply ${diffRows.length} change${diffRows.length === 1 ? '' : 's'}`}
              </button>
            </div>
          </div>
        </div>
      )}

      {conflictInfo && (
        <VersionConflictDialog
          conflicts={conflictInfo.conflicts}
          edits={conflictInfo.edits}
          tracks={state.tracks}
          busy={applying}
          onResolve={handleConflictResolve}
          onClose={() => setConflictInfo(null)}
        />
      )}

      {lookupOpen && (
        <LookupDialog
          albumId={albumId}
          album={album || { artist: '', title: '' }}
          onClose={() => setLookupOpen(false)}
          onApply={handleLookupApply}
          onError={(msg) => showAlert('Error', msg)}
        />
      )}

      {historyOpen && (
        <HistoryDrawer
          albumId={albumId}
          onClose={() => setHistoryOpen(false)}
          onReverted={refreshData}
          onError={(msg) => showAlert('Error', msg)}
        />
      )}

      {/* Keyboard shortcuts help (desktop only) */}
      {helpOpen && (
        <div className="overlay" role="dialog" aria-label="Keyboard shortcuts">
          <div className="dialog help-dialog fade-in-up">
            <div className="dialog-head">
              <h3 className="dialog-title">Keyboard Shortcuts</h3>
              <button
                type="button"
                className="icon-btn"
                onClick={() => setHelpOpen(false)}
                aria-label="Close help"
              >
                ✕
              </button>
            </div>
            <div className="dialog-body">
              <table className="help-table">
                <tbody>
                  {SHORTCUTS.map(([keys, what]) => (
                    <tr key={keys}>
                      <td>
                        <span className="kbd">{keys}</span>
                      </td>
                      <td>{what}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="dialog-actions">
              <button type="button" className="btn-primary" onClick={() => setHelpOpen(false)}>
                Close
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
