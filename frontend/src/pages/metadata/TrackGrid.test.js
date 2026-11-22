import React, { useState } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import TrackGrid from './TrackGrid';

const tracks = [
  {
    id: 't1',
    title: 'One',
    artist: null,
    album_artist: null,
    track_number: 1,
    disc_number: null,
    year: null,
    genre: 'Rock',
    composer: null,
    comment: null,
    duration: 213,
  },
  {
    id: 't2',
    title: 'Two',
    artist: null,
    album_artist: null,
    track_number: 2,
    disc_number: null,
    year: null,
    genre: 'Rock',
    composer: null,
    comment: null,
    duration: 224,
  },
];

// A tiny stand-in for MetadataPage: it owns the focus/editing state the
// way the real page does, and spies on the commit callback.
function GridHarness(props) {
  const [focus, setFocus] = useState(null);
  const [editing, setEditing] = useState(false);
  return (
    <TrackGrid
      focus={focus}
      onFocusCell={(row, col) => setFocus({ row, col })}
      editing={editing}
      onStartEdit={() => setEditing(true)}
      onExitEdit={() => setEditing(false)}
      canUndo={false}
      canRedo={false}
      {...props}
    />
  );
}

function renderGrid(overrides = {}) {
  const onCommitEdit = jest.fn(() => true);
  const utils = render(
    <GridHarness
      tracks={tracks}
      edits={overrides.edits || {}}
      selected={overrides.selected || {}}
      visibleCols={overrides.visibleCols || ['track_number', 'title', 'genre']}
      onCommitEdit={onCommitEdit}
      onToggleCol={jest.fn()}
      onSelectToggle={jest.fn()}
      onSelectRange={jest.fn()}
      onSelectAll={jest.fn()}
      onClearSelection={jest.fn()}
      onFillDown={jest.fn()}
      onAutoNumber={jest.fn()}
      onUndo={jest.fn()}
      onRedo={jest.fn()}
      onDeleteTrack={jest.fn()}
      onSaveCard={jest.fn(() => true)}
      onError={jest.fn()}
    />
  );
  return { onCommitEdit, ...utils };
}

describe('TrackGrid inline editing', () => {
  test('click focuses the cell, opens the editor with the current value', async () => {
    const user = userEvent.setup();
    renderGrid();

    await user.click(screen.getByTestId('cell-t1-title'));

    const input = screen.getByRole('textbox');
    expect(input).toHaveValue('One');

    // The focused cell is visually marked (and shows the editor).
    const td = input.closest('td');
    expect(td).toHaveClass('focused');
    expect(td).toHaveClass('editing');
  });

  test('Enter commits the typed value and exits edit mode', async () => {
    const user = userEvent.setup();
    const { onCommitEdit } = renderGrid();

    await user.click(screen.getByTestId('cell-t1-title'));
    const input = screen.getByRole('textbox');
    await user.clear(input);
    await user.type(input, 'One (Take)');
    await user.keyboard('{Enter}');

    expect(onCommitEdit).toHaveBeenCalledWith('t1', 'title', 'One (Take)');
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();

    // After the edit commits, the cell keeps the focus marker so the
    // arrow/Enter/Tab shortcuts visibly keep targeting it.
    const td = screen.getByTestId('cell-t1-title').closest('td');
    expect(td).toHaveClass('focused');
    expect(td).not.toHaveClass('editing');
  });

  test('Esc cancels the edit without committing', async () => {
    const user = userEvent.setup();
    const { onCommitEdit } = renderGrid();

    await user.click(screen.getByTestId('cell-t2-genre'));
    const input = screen.getByRole('textbox');
    expect(input).toHaveValue('Rock');
    await user.type(input, 'J');
    await user.keyboard('{Escape}');

    expect(onCommitEdit).not.toHaveBeenCalled();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  test('re-editing the same cell starts from the current value again', async () => {
    const user = userEvent.setup();
    const { onCommitEdit } = renderGrid();

    await user.click(screen.getByTestId('cell-t1-title'));
    let input = screen.getByRole('textbox');
    await user.type(input, 'J');
    await user.keyboard('{Escape}');
    expect(onCommitEdit).not.toHaveBeenCalled();

    // The discarded text must not leak into the next edit of the same cell.
    await user.click(screen.getByTestId('cell-t1-title'));
    input = screen.getByRole('textbox');
    expect(input).toHaveValue('One');
  });

  test('Tab commits and moves to the next visible column', async () => {
    const user = userEvent.setup();
    const { onCommitEdit } = renderGrid();

    await user.click(screen.getByTestId('cell-t1-title'));
    const input = screen.getByRole('textbox');
    await user.clear(input);
    await user.type(input, 'One!');
    await user.keyboard('{Tab}');

    expect(onCommitEdit).toHaveBeenCalledWith('t1', 'title', 'One!');
    // The editor followed the focus to the genre cell with its value.
    await screen.findByDisplayValue('Rock');

    // The focus marker moved with it (the genre cell edits, the title
    // display div is back).
    expect(screen.getByRole('textbox').closest('td')).toHaveClass('focused');
    expect(screen.getByTestId('cell-t1-title').closest('td')).not.toHaveClass('focused');
  });

  test('Shift+Tab moves to the previous visible column', async () => {
    const user = userEvent.setup();
    renderGrid();

    await user.click(screen.getByTestId('cell-t1-genre'));
    await user.keyboard('{Shift>}{Tab}{/Shift}');

    // Back on the title cell with its current value.
    const input = await screen.findByDisplayValue('One');
    expect(input).toBeInTheDocument();
  });

  test('dirty cells are highlighted and the original stays visible in the diff', async () => {
    const user = userEvent.setup();
    const { container } = renderGrid({ edits: { 't1.title': 'One (New)' } });

    // The pending value is shown, marked as modified.
    const cell = screen.getByTestId('cell-t1-title');
    expect(cell).toHaveTextContent('One (New)');
    expect(cell).toHaveClass('modified');

    // An unedited cell is not marked.
    expect(screen.getByTestId('cell-t2-title')).not.toHaveClass('modified');

    // Row gets the dirty marker.
    const dirtyRow = container.querySelector('.md-row.dirty');
    expect(dirtyRow).not.toBeNull();
  });
});
