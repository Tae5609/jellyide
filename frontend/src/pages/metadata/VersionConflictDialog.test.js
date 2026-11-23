import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import VersionConflictDialog from './VersionConflictDialog';

const conflicts = [
  {
    track_id: 't1',
    reason: 'version_conflict',
    current: { title: 'Theirs Title', genre: 'Blues' },
    current_version: 5,
  },
];
const edits = [
  { track_id: 't1', fields: { title: 'Mine Title', genre: 'Jazz' }, expected_version: 1 },
  { track_id: 't2', fields: { genre: 'Untouched' }, expected_version: 2 },
];
const tracks = [{ id: 't1', title: 'One' }, { id: 't2', title: 'Two' }];

function renderDialog(onResolve) {
  return render(
    <VersionConflictDialog
      conflicts={conflicts}
      edits={edits}
      tracks={tracks}
      onResolve={onResolve || jest.fn()}
      onClose={jest.fn()}
    />
  );
}

describe('VersionConflictDialog', () => {
  test('shows both sides per field with the track title', () => {
    renderDialog();
    expect(screen.getByRole('dialog', { name: 'Version conflict' })).toBeInTheDocument();
    expect(screen.getAllByText('One').length).toBeGreaterThan(0);
    expect(screen.getByText('Theirs Title')).toBeInTheDocument();
    expect(screen.getByText('Mine Title')).toBeInTheDocument();
    // Two fields -> two "Theirs" / "Yours" radio labels.
    expect(screen.getAllByRole('radio', { name: 'Theirs' })).toHaveLength(2);
    expect(screen.getAllByRole('radio', { name: 'Yours' })).toHaveLength(2);
  });

  test('Keep all theirs drops every field of the conflicted track', async () => {
    const user = userEvent.setup();
    const onResolve = jest.fn();
    renderDialog(onResolve);

    await user.click(screen.getByRole('button', { name: 'Keep all theirs' }));

    // t1 loses all its fields (both picked theirs) and is dropped; t2 is
    // untouched and keeps its edit.
    expect(onResolve).toHaveBeenCalledTimes(1);
    const resolved = onResolve.mock.calls[0][0];
    expect(resolved).toHaveLength(1);
    expect(resolved[0]).toEqual({ track_id: 't2', fields: { genre: 'Untouched' }, expected_version: 2 });
  });

  test('Overwrite all with yours re-applies against the new version', async () => {
    const user = userEvent.setup();
    const onResolve = jest.fn();
    renderDialog(onResolve);

    await user.click(screen.getByRole('button', { name: 'Overwrite all with yours' }));

    const resolved = onResolve.mock.calls[0][0];
    expect(resolved).toHaveLength(2);
    const t1 = resolved.find((e) => e.track_id === 't1');
    expect(t1.fields).toEqual({ title: 'Mine Title', genre: 'Jazz' });
    expect(t1.expected_version).toBe(5);
  });

  test('Apply choices mixes per-field picks', async () => {
    const user = userEvent.setup();
    const onResolve = jest.fn();
    renderDialog(onResolve);

    // Default is 'theirs' for both; flip the title field to 'mine'.
    const mineRadios = screen.getAllByRole('radio', { name: 'Yours' });
    // Two fields (title, genre) x one track: the title radio is first in
    // DOM order (field order of the edit).
    await user.click(mineRadios[0]);

    await user.click(screen.getByRole('button', { name: 'Apply choices' }));

    const resolved = onResolve.mock.calls[0][0];
    const t1 = resolved.find((e) => e.track_id === 't1');
    // genre kept theirs -> dropped; title kept mine.
    expect(t1.fields).toEqual({ title: 'Mine Title' });
    expect(t1.expected_version).toBe(5);
    expect(resolved.find((e) => e.track_id === 't2')).toBeTruthy();
  });
});
