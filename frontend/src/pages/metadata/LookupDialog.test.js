import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import LookupDialog from './LookupDialog';

function mockApi() {
  global.fetch = jest.fn(async (url) => {
    if (url.startsWith('/api/metadata/lookup/search')) {
      return new Response(
        JSON.stringify([
          {
            mbid: 'm1',
            title: 'Blue Train',
            artist: 'Art Blakey',
            date: '1963',
            country: 'US',
            track_count: 7,
            score: 100,
          },
        ]),
        { status: 200 },
      );
    }
    if (url.startsWith('/api/metadata/lookup/release/m1')) {
      return new Response(
        JSON.stringify({
          release: { title: 'Blue Train', artist: 'Art Blakey' },
          cover_url: 'https://coverartarchive.org/release/m1/front',
          mapping: [
            {
              track_id: 't1',
              current: { title: 'Blue Train (Old)' },
              proposed: { title: 'Blue Train', year: 1963 },
              confidence: 0.95,
            },
            {
              track_id: 't2',
              current: { title: 'Milestone' },
              proposed: { title: 'Petra' },
              confidence: 0.3,
            },
          ],
        }),
        { status: 200 },
      );
    }
    return new Response(JSON.stringify({ error: 'not found' }), { status: 404 });
  });
}

function renderDialog(onApply) {
  return render(
    <LookupDialog
      albumId="a1"
      album={{ artist: 'Art Blakey', title: 'Blue Train' }}
      onClose={jest.fn()}
      onApply={onApply}
      onError={jest.fn()}
    />,
  );
}

async function openRelease(user) {
  const candidate = await screen.findByRole('button', { name: /7 tracks/ });
  await user.click(candidate);
  await screen.findAllByText('Blue Train (Old)');
}

const rowFor = (index) => document.querySelectorAll('.lookup-row')[index];

const fieldCheckbox = (row, fieldName) => {
  const labels = row.querySelectorAll('.lookup-field');
  const label = Array.from(labels).find(
    (l) => l.querySelector('.lookup-field-name').textContent === fieldName,
  );
  return label.querySelector('input');
};

describe('LookupDialog', () => {
  beforeEach(() => {
    mockApi();
  });

  afterEach(() => {
    jest.restoreAllMocks();
  });

  test('stages the included rows and fields as pending edits', async () => {
    const user = userEvent.setup();
    const onApply = jest.fn();
    renderDialog(onApply);

    await openRelease(user);

    // The low-confidence row is highlighted.
    expect(rowFor(1)).toHaveClass('low-confidence');
    expect(rowFor(0)).not.toHaveClass('low-confidence');

    const applyBtn = screen.getByRole('button', { name: /Stage 3 fields/ });
    await user.click(applyBtn);

    expect(onApply).toHaveBeenCalledTimes(1);
    const [entries, coverUrl] = onApply.mock.calls[0];
    expect(entries).toEqual([
      { trackId: 't1', field: 'title', value: 'Blue Train' },
      { trackId: 't1', field: 'year', value: 1963 },
      { trackId: 't2', field: 'title', value: 'Petra' },
    ]);
    expect(coverUrl).toBeNull();
  });

  test('row and field checkboxes control what gets staged', async () => {
    const user = userEvent.setup();
    const onApply = jest.fn();
    renderDialog(onApply);

    await openRelease(user);

    // Uncheck the low-confidence row entirely (its row checkbox).
    const row2 = rowFor(1);
    await user.click(row2.querySelector('.lookup-row-include input'));
    // Uncheck one field on the first row (year).
    await user.click(fieldCheckbox(rowFor(0), 'year'));

    const applyBtn = screen.getByRole('button', { name: /Stage 1 field/ });
    await user.click(applyBtn);

    expect(onApply).toHaveBeenCalledTimes(1);
    const [entries, coverUrl] = onApply.mock.calls[0];
    expect(entries).toEqual([{ trackId: 't1', field: 'title', value: 'Blue Train' }]);
    expect(coverUrl).toBeNull();
  });

  test('optionally stages the release cover URL', async () => {
    const user = userEvent.setup();
    const onApply = jest.fn();
    renderDialog(onApply);

    await openRelease(user);

    await user.click(screen.getByLabelText(/Set cover art/));
    await user.click(screen.getByRole('button', { name: /Stage 3 fields/ }));

    const [entries, coverUrl] = onApply.mock.calls[0];
    expect(entries).toHaveLength(3);
    expect(coverUrl).toBe('https://coverartarchive.org/release/m1/front');
  });

  test('shows search errors without crashing', async () => {
    global.fetch = jest.fn(
      async () => new Response(JSON.stringify({ error: 'boom' }), { status: 500 }),
    );
    const onApply = jest.fn();
    renderDialog(onApply);

    await screen.findByText('boom');
    expect(onApply).not.toHaveBeenCalled();
  });
});
