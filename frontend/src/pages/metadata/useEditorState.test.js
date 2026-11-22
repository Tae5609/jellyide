import {
  editorReducer,
  initialEditorState,
  fillDownEntries,
  autoNumberEntries,
  getDiff,
  pendingCount,
  buildPatchEdits,
} from './useEditorState';

const tracks = [
  {
    id: 't1',
    title: 'One',
    artist: null,
    genre: 'Rock',
    track_number: 5,
    duration: 60,
  },
  {
    id: 't2',
    title: 'Two',
    artist: null,
    genre: 'Rock',
    track_number: 2,
    duration: 61,
  },
  {
    id: 't3',
    title: 'Three',
    artist: null,
    genre: 'Rock',
    track_number: 9,
    duration: 62,
  },
];

const base = () => initialEditorState(tracks);

describe('editorReducer', () => {
  test('EDIT adds a pending edit and one undo step', () => {
    const s = editorReducer(base(), {
      type: 'EDIT',
      trackId: 't1',
      field: 'genre',
      value: 'Jazz',
    });
    expect(s.edits['t1.genre']).toBe('Jazz');
    expect(pendingCount(s)).toBe(1);
    expect(s.undoStack).toHaveLength(1);
  });

  test('editing back to the original value removes the edit', () => {
    let s = editorReducer(base(), {
      type: 'EDIT',
      trackId: 't1',
      field: 'genre',
      value: 'Jazz',
    });
    s = editorReducer(s, {
      type: 'EDIT',
      trackId: 't1',
      field: 'genre',
      value: 'Rock',
    });
    expect(pendingCount(s)).toBe(0);
    expect(s.undoStack).toHaveLength(2);
  });

  test('an edit that changes nothing is a no-op', () => {
    const s = editorReducer(base(), {
      type: 'EDIT',
      trackId: 't1',
      field: 'genre',
      value: 'Rock',
    });
    expect(s.undoStack).toHaveLength(0);
    expect(pendingCount(s)).toBe(0);
  });

  test('UNDO then REDO round-trips edits in reverse order', () => {
    let s = editorReducer(base(), {
      type: 'EDIT',
      trackId: 't1',
      field: 'genre',
      value: 'Jazz',
    });
    s = editorReducer(s, { type: 'EDIT', trackId: 't2', field: 'title', value: 'Two Take' });
    expect(s.undoStack).toHaveLength(2);

    s = editorReducer(s, { type: 'UNDO' });
    expect(s.edits['t2.title']).toBeUndefined();
    expect(s.edits['t1.genre']).toBe('Jazz');
    expect(s.redoStack).toHaveLength(1);

    s = editorReducer(s, { type: 'REDO' });
    expect(s.edits['t2.title']).toBe('Two Take');
    expect(s.undoStack).toHaveLength(2);
  });

  test('UNDO clears a field that was set from empty', () => {
    let s = editorReducer(base(), {
      type: 'EDIT',
      trackId: 't1',
      field: 'artist',
      value: 'Tae',
    });
    s = editorReducer(s, { type: 'UNDO' });
    expect(s.edits['t1.artist']).toBeUndefined();
  });

  test('UNDO of a nulling edit restores the original value', () => {
    let s = editorReducer(base(), {
      type: 'EDIT',
      trackId: 't1',
      field: 'genre',
      value: null,
    });
    expect(s.edits['t1.genre']).toBeNull();
    s = editorReducer(s, { type: 'UNDO' });
    expect(s.edits['t1.genre']).toBeUndefined();
  });

  test('a new edit after UNDO clears the redo stack', () => {
    let s = editorReducer(base(), {
      type: 'EDIT',
      trackId: 't1',
      field: 'genre',
      value: 'Jazz',
    });
    s = editorReducer(s, { type: 'UNDO' });
    expect(s.redoStack).toHaveLength(1);
    s = editorReducer(s, { type: 'EDIT', trackId: 't3', field: 'title', value: '3' });
    expect(s.redoStack).toHaveLength(0);
  });

  test('FILL DOWN copies the focused cell to the selected rows', () => {
    let s = base();
    s = editorReducer(s, { type: 'SELECT', trackId: 't2' });
    s = editorReducer(s, { type: 'SELECT', trackId: 't3' });
    s = editorReducer(s, { type: 'EDIT', trackId: 't1', field: 'genre', value: 'Folk' });

    const entries = fillDownEntries(s, 't1', 'genre');
    expect(entries).toEqual([
      { trackId: 't2', field: 'genre', value: 'Folk' },
      { trackId: 't3', field: 'genre', value: 'Folk' },
    ]);

    s = editorReducer(s, { type: 'SET_EDITS', entries });
    expect(s.edits['t2.genre']).toBe('Folk');
    expect(s.edits['t3.genre']).toBe('Folk');

    // One undo step for the whole fill: the edits go back to "unset",
    // i.e. the tracks' original values show again.
    s = editorReducer(s, { type: 'UNDO' });
    expect(s.edits['t2.genre']).toBeUndefined();
    expect(s.edits['t3.genre']).toBeUndefined();
    expect(s.edits['t1.genre']).toBe('Folk');
  });

  test('FILL DOWN with an empty focused cell stages nothing', () => {
    const s = editorReducer(base(), { type: 'SELECT', trackId: 't2' });
    expect(fillDownEntries(s, 't1', 'artist')).toEqual([]);
  });

  test('FILL DOWN copies an explicit clear (null) to the selected rows', () => {
    let s = editorReducer(base(), { type: 'SELECT', trackId: 't2' });
    s = editorReducer(s, { type: 'EDIT', trackId: 't1', field: 'genre', value: null });
    expect(fillDownEntries(s, 't1', 'genre')).toEqual([
      { trackId: 't2', field: 'genre', value: null },
    ]);
  });

  test('AUTO NUMBER sets 1..n in the current order', () => {
    const entries = autoNumberEntries(base());
    expect(entries).toEqual([
      { trackId: 't1', field: 'track_number', value: 1 },
      { trackId: 't2', field: 'track_number', value: 2 },
      { trackId: 't3', field: 'track_number', value: 3 },
    ]);
    const s = editorReducer(base(), { type: 'SET_EDITS', entries });
    // t2 was already number 2, so only two cells are dirty.
    expect(pendingCount(s)).toBe(2);
    expect(s.edits['t1.track_number']).toBe(1);
    expect(s.edits['t3.track_number']).toBe(3);
  });

  test('SELECT_RANGE selects the rows between the anchor and the target', () => {
    let s = editorReducer(base(), { type: 'SELECT', trackId: 't1' });
    s = editorReducer(s, { type: 'SELECT_RANGE', fromId: 't1', toId: 't3' });
    expect(Object.keys(s.selected).sort()).toEqual(['t1', 't2', 't3']);
  });

  test('getDiff reports from/to per pending edit in stable order', () => {
    let s = base();
    s = editorReducer(s, { type: 'EDIT', trackId: 't1', field: 'genre', value: null });
    s = editorReducer(s, { type: 'EDIT', trackId: 't2', field: 'title', value: 'Two (Reprise)' });
    expect(getDiff(s)).toEqual([
      { trackId: 't1', field: 'genre', from: 'Rock', to: null },
      { trackId: 't2', field: 'title', from: 'Two', to: 'Two (Reprise)' },
    ]);
  });

  test('buildPatchEdits groups the diff per track for PATCH /metadata/tracks', () => {
    let s = base();
    s = editorReducer(s, { type: 'EDIT', trackId: 't1', field: 'genre', value: null });
    s = editorReducer(s, { type: 'EDIT', trackId: 't1', field: 'year', value: 2021 });
    s = editorReducer(s, { type: 'EDIT', trackId: 't3', field: 'title', value: '3!' });
    expect(buildPatchEdits(s)).toEqual([
      { track_id: 't1', fields: { genre: null, year: 2021 } },
      { track_id: 't3', fields: { title: '3!' } },
    ]);
  });

  test('REMOVE_TRACK drops the track edits and selection', () => {
    let s = base();
    s = editorReducer(s, { type: 'EDIT', trackId: 't1', field: 'genre', value: 'X' });
    s = editorReducer(s, { type: 'SELECT', trackId: 't1' });
    s = editorReducer(s, { type: 'REMOVE_TRACK', trackId: 't1' });
    expect(s.edits['t1.genre']).toBeUndefined();
    expect(s.selected['t1']).toBeUndefined();
  });
});
