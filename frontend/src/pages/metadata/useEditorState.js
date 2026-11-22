import { useReducer } from 'react';
import { apiFetch } from '../../api';

// ---------------------------------------------------------------------------
// Field definitions shared by the grid, the panels and the diff view.
// ---------------------------------------------------------------------------

// Editable track columns, in display order. `int` fields are validated
// against the same ranges the backend enforces on PATCH /metadata/tracks.
export const FIELDS = [
  { key: 'track_number', label: '#', kind: 'int' },
  { key: 'disc_number', label: 'Disc', kind: 'int' },
  { key: 'title', label: 'Title', kind: 'text' },
  { key: 'artist', label: 'Artist', kind: 'text' },
  { key: 'album_artist', label: 'Album Artist', kind: 'text' },
  { key: 'year', label: 'Year', kind: 'int' },
  { key: 'genre', label: 'Genre', kind: 'text' },
  { key: 'composer', label: 'Composer', kind: 'text' },
  { key: 'comment', label: 'Comment', kind: 'text' },
];

export const FIELD_KEYS = FIELDS.map((f) => f.key);

export const INT_MAX = { track_number: 999, disc_number: 999, year: 9999 };

// Fields the find-and-replace preview endpoint accepts.
export const PREVIEW_FIELDS = [
  { key: 'title', label: 'Title' },
  { key: 'artist', label: 'Artist' },
  { key: 'album_artist', label: 'Album Artist' },
  { key: 'genre', label: 'Genre' },
  { key: 'composer', label: 'Composer' },
  { key: 'comment', label: 'Comment' },
];

export const fieldDef = (key) => FIELDS.find((f) => f.key === key);

export const editKey = (trackId, field) => `${trackId}.${field}`;

// Original (server) value of a track field; null when unset.
export function originalValue(track, field) {
  const v = track[field];
  return v === undefined ? null : v;
}

// Effective value: a pending edit wins over the original.
export function currentValue(edits, track, field) {
  const k = editKey(track.id, field);
  return k in edits ? edits[k] : originalValue(track, field);
}

export function isDirty(edits, track, field) {
  return editKey(track.id, field) in edits;
}

// ---------------------------------------------------------------------------
// Undo / redo
//
// An entry snapshots only the keys one action touched. `before`/`after` map
// key -> value (null = clear the tag); a key missing from a side means "back
// to the track's original value". Undo/redo are strictly LIFO, so restoring
// one side always reproduces the previous state exactly.
// ---------------------------------------------------------------------------

function touchedKeys(before, after) {
  return Array.from(new Set([...Object.keys(before), ...Object.keys(after)]));
}

function restore(edits, entry, side) {
  const values = entry[side];
  const next = { ...edits };
  entry.keys.forEach((k) => {
    if (k in values) next[k] = values[k];
    else delete next[k];
  });
  return next;
}

export function initialEditorState(tracks = []) {
  return { tracks, edits: {}, selected: {}, undoStack: [], redoStack: [] };
}

// describeEdit computes {before, after} for one (trackId, field, value)
// change against the current state; null when nothing would change.
function describeEdit(state, trackId, field, value) {
  const track = state.tracks.find((t) => t.id === trackId);
  if (!track) return null;
  const k = editKey(trackId, field);
  const before = {};
  if (k in state.edits) before[k] = state.edits[k];
  if (value === originalValue(track, field)) return { before, after: {} };
  if (k in state.edits && state.edits[k] === value) return null;
  return { before, after: { [k]: value } };
}

function commitEdits(state, entries) {
  const before = {};
  const after = {};
  entries.forEach((e) => {
    const desc = describeEdit(state, e.trackId, e.field, e.value);
    if (!desc) return;
    Object.assign(before, desc.before);
    Object.assign(after, desc.after);
  });
  const keys = touchedKeys(before, after);
  if (keys.length === 0) return state;
  const entry = { keys, before, after };
  return {
    ...state,
    edits: restore(state.edits, entry, 'after'),
    undoStack: [...state.undoStack.slice(-99), entry],
    redoStack: [],
  };
}

export function editorReducer(state, action) {
  switch (action.type) {
    case 'INITIALIZE':
      return initialEditorState(action.tracks);
    case 'EDIT':
      return commitEdits(state, [
        { trackId: action.trackId, field: action.field, value: action.value },
      ]);
    case 'SET_EDITS':
      return commitEdits(state, action.entries);
    case 'UNDO': {
      const entry = state.undoStack[state.undoStack.length - 1];
      if (!entry) return state;
      return {
        ...state,
        edits: restore(state.edits, entry, 'before'),
        undoStack: state.undoStack.slice(0, -1),
        redoStack: [...state.redoStack, entry],
      };
    }
    case 'REDO': {
      const entry = state.redoStack[state.redoStack.length - 1];
      if (!entry) return state;
      return {
        ...state,
        edits: restore(state.edits, entry, 'after'),
        redoStack: state.redoStack.slice(0, -1),
        undoStack: [...state.undoStack, entry],
      };
    }
    case 'SELECT': {
      const selected = { ...state.selected };
      if (selected[action.trackId]) delete selected[action.trackId];
      else selected[action.trackId] = true;
      return { ...state, selected };
    }
    case 'SELECT_RANGE': {
      const ids = state.tracks.map((t) => t.id);
      let a = ids.indexOf(action.fromId);
      let b = ids.indexOf(action.toId);
      if (a === -1 || b === -1) return state;
      if (a > b) [a, b] = [b, a];
      const selected = { ...state.selected };
      for (let i = a; i <= b; i++) selected[ids[i]] = true;
      return { ...state, selected };
    }
    case 'SELECT_ALL': {
      const selected = {};
      state.tracks.forEach((t) => {
        selected[t.id] = true;
      });
      return { ...state, selected };
    }
    case 'CLEAR_SELECTION':
      return { ...state, selected: {} };
    case 'REMOVE_TRACK': {
      const prefix = `${action.trackId}.`;
      const edits = {};
      Object.entries(state.edits).forEach(([k, v]) => {
        if (!k.startsWith(prefix)) edits[k] = v;
      });
      const selected = { ...state.selected };
      delete selected[action.trackId];
      return { ...state, edits, selected };
    }
    case 'APPLIED':
      return { ...state, edits: {}, selected: {}, undoStack: [], redoStack: [] };
    default:
      return state;
  }
}

// ---------------------------------------------------------------------------
// Derived helpers
// ---------------------------------------------------------------------------

export function selectedIds(state) {
  return state.tracks.filter((t) => state.selected[t.id]).map((t) => t.id);
}

// Fill down: copy the value at (trackId, field) to every selected row.
export function fillDownEntries(state, trackId, field) {
  const track = state.tracks.find((t) => t.id === trackId);
  if (!track) return [];
  const value = currentValue(state.edits, track, field);
  // An unedited empty cell has nothing to copy, but an explicit clear is a
  // real value: fill it down so the selection is cleared too.
  const hasEdit = editKey(trackId, field) in state.edits;
  if (!hasEdit && (value === null || value === undefined)) return [];
  return state.tracks
    .filter((t) => state.selected[t.id] && t.id !== trackId)
    .map((t) => ({ trackId: t.id, field, value }));
}

// Auto-number: set track numbers 1..n in the current (display) order.
export function autoNumberEntries(state) {
  return state.tracks.map((t, i) => ({
    trackId: t.id,
    field: 'track_number',
    value: i + 1,
  }));
}

// One diff row per pending edit, in track order then field order.
export function getDiff(state) {
  const rows = [];
  Object.entries(state.edits).forEach(([k, value]) => {
    const dot = k.indexOf('.');
    if (dot === -1) return;
    const trackId = k.slice(0, dot);
    const field = k.slice(dot + 1);
    const track = state.tracks.find((t) => t.id === trackId);
    if (!track) return;
    rows.push({ trackId, field, from: originalValue(track, field), to: value });
  });
  const tOrder = {};
  state.tracks.forEach((t, i) => {
    tOrder[t.id] = i;
  });
  const fOrder = {};
  FIELDS.forEach((f, i) => {
    fOrder[f.key] = i;
  });
  rows.sort(
    (a, b) =>
      (tOrder[a.trackId] - tOrder[b.trackId]) || (fOrder[a.field] - fOrder[b.field])
  );
  return rows;
}

export const pendingCount = (state) => Object.keys(state.edits).length;

// Group the diff into the PATCH /metadata/tracks body: one entry per track
// with a `fields` map (null clears the tag).
export function buildPatchEdits(state) {
  const byTrack = {};
  getDiff(state).forEach((r) => {
    if (!byTrack[r.trackId]) {
      byTrack[r.trackId] = { track_id: r.trackId, fields: {} };
    }
    byTrack[r.trackId].fields[r.field] = r.to;
  });
  return Object.values(byTrack);
}

export function useEditorState(initialTracks = []) {
  const [state, dispatch] = useReducer(
    editorReducer,
    initialTracks,
    initialEditorState
  );
  return { state, dispatch };
}

// ---------------------------------------------------------------------------
// Job polling and response helpers (same /jobs endpoint the Jobs page uses).
// ---------------------------------------------------------------------------

const JOB_POLL_INTERVAL_MS = 1500;
const JOB_POLL_TIMEOUT_MS = 5 * 60 * 1000;

export async function waitForJob(
  jobId,
  sleep = (ms) => new Promise((r) => setTimeout(r, ms))
) {
  const deadline = Date.now() + JOB_POLL_TIMEOUT_MS;
  for (;;) {
    const res = await apiFetch('/jobs');
    if (res.ok) {
      const jobs = (await res.json()) || [];
      const job = jobs.find((j) => j.id === jobId);
      if (job) {
        if (job.status === 'completed') return job;
        if (job.status === 'failed') throw new Error(job.error || 'Job failed');
      }
    }
    if (Date.now() >= deadline) {
      throw new Error('Timed out waiting for the job to finish');
    }
    await sleep(JOB_POLL_INTERVAL_MS);
  }
}

// Extract a human-readable message from an error response body.
export async function errorFromResponse(res) {
  try {
    const data = await res.json();
    if (data && typeof data.error === 'string' && data.error) return data.error;
  } catch (e) {
    // not JSON — fall through
  }
  return `Request failed with status ${res.status}`;
}

export function formatDuration(totalSeconds) {
  const s = Math.max(0, Math.round(Number(totalSeconds) || 0));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}
