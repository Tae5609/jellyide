import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { ModalProvider } from '../../components/ModalContext';
import MetadataPage from './MetadataPage';

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

const jsonResponse = (body) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });

function mockApi() {
  global.fetch = jest.fn(async (url) => {
    if (url.startsWith('/api/library/albums/a1/tracks')) {
      return jsonResponse(tracks);
    }
    if (url.startsWith('/api/library/albums/a1')) {
      return jsonResponse({ id: 'a1', title: 'Blue Train', artist: 'Art Blakey' });
    }
    if (url.startsWith('/api/metadata/analyze/a1')) {
      return jsonResponse({ suggestions: [], common_substrings: [] });
    }
    return new Response(JSON.stringify({ error: 'not found' }), { status: 404 });
  });
}

function renderPage() {
  return render(
    <ModalProvider>
      <MemoryRouter initialEntries={['/library/artist1/a1']}>
        <Routes>
          <Route path="/library/:artistId/:albumId" element={<MetadataPage />} />
        </Routes>
      </MemoryRouter>
    </ModalProvider>
  );
}

// Dispatches a real Tab keydown through the window-level shortcut listener
// and reports whether the grid intercepted it (preventDefault).
function pressTab() {
  let intercepted = true;
  act(() => {
    const ev = new KeyboardEvent('keydown', {
      key: 'Tab',
      bubbles: true,
      cancelable: true,
    });
    document.dispatchEvent(ev);
    intercepted = ev.defaultPrevented;
  });
  return intercepted;
}

describe('MetadataPage shortcuts', () => {
  beforeEach(() => {
    mockApi();
  });

  afterEach(() => {
    jest.restoreAllMocks();
  });

  test('Ctrl+A selects all rows only while the grid has focus', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByTestId('cell-t1-title');
    const [headerBox, box1, box2] = screen.getAllByRole('checkbox');

    // No grid interaction yet: the shortcut must not fire.
    await user.keyboard('{Control>}a{/Control}');
    expect(box1).not.toBeChecked();
    expect(box2).not.toBeChecked();

    // Interact with the grid: focus a cell, edit, commit.
    await user.click(screen.getByTestId('cell-t1-title'));
    await user.keyboard('{Enter}');

    // The grid has focus: Ctrl+A selects all rows.
    await user.keyboard('{Control>}a{/Control}');
    expect(box1).toBeChecked();
    expect(box2).toBeChecked();
    expect(headerBox).toBeChecked();

    // Deselect one row, then move to a side panel.
    await user.click(box1);
    expect(box1).not.toBeChecked();
    await user.click(screen.getByText('Bulk Edit'));

    // The grid no longer has focus: Ctrl+A must not select all.
    await user.keyboard('{Control>}a{/Control}');
    expect(box1).not.toBeChecked();
    expect(box2).toBeChecked();
    expect(headerBox).not.toBeChecked();
  });

  test('Tab reaches page controls before any grid cell is focused', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByTestId('cell-t1-title');

    // No cell focus yet: the shortcut must not swallow Tab, so the native
    // tab order reaches the first page control (WCAG 2.1.2).
    await user.tab();
    expect(screen.getByRole('button', { name: /back/i })).toHaveFocus();
  });

  test('Tab moves between cells while a grid cell is focused', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByTestId('cell-t1-title');

    // Focus the title cell, exit edit mode, keep the cell focus.
    await user.click(screen.getByTestId('cell-t1-title'));
    await user.keyboard('{Escape}');

    // A cell is focused: Tab is captured and moves to the next visible
    // column (artist) instead of moving browser focus out of the grid.
    await user.keyboard('{Tab}');
    expect(screen.getByTestId('cell-t1-artist').closest('td')).toHaveClass('focused');
    // No grid cell editor is open (the side panels have textboxes too).
    expect(document.querySelector('.md-cell-input')).toBeNull();

    // Arrows share the same cell-focus navigation.
    await user.keyboard('{ArrowRight}');
    expect(screen.getByTestId('cell-t1-album_artist').closest('td')).toHaveClass(
      'focused'
    );
  });

  test('Tab is released once the pointer leaves the grid card', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByTestId('cell-t1-title');

    // Focus a cell and exit edit mode; the cell keeps the virtual focus.
    await user.click(screen.getByTestId('cell-t1-title'));
    await user.keyboard('{Escape}');

    // Click a page area outside the grid card (the side panels).
    await user.click(screen.getByText('Bulk Edit'));

    // Attention has left the card: the cell focus must be gone with it.
    // Tab stays with the browser (WCAG 2.1.2) instead of being captured,
    // and the stale cell marker is dropped (onTab not called).
    expect(pressTab()).toBe(false);
    expect(screen.getByTestId('cell-t1-title').closest('td')).not.toHaveClass(
      'focused'
    );
  });

  test('Tab is released when the grid card blurs to the outside', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByTestId('cell-t1-title');

    // Virtual cell focus, with the DOM focus on a grid control (the
    // select-all checkbox) inside the card.
    await user.click(screen.getByTestId('cell-t1-title'));
    await user.keyboard('{Escape}');
    const headerBox = screen.getAllByRole('checkbox')[0];
    await user.click(headerBox);

    // The card-blur boundary: focus moves to a node outside the card.
    const card = document.querySelector('.metadata-table').closest('.card');
    fireEvent.focusOut(card, { relatedTarget: document.body });

    expect(pressTab()).toBe(false);
    expect(screen.getByTestId('cell-t1-title').closest('td')).not.toHaveClass(
      'focused'
    );
  });

  test('Enter on a focused grid button does not open the cell editor', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByTestId('cell-t1-title');

    // Give the grid a virtual cell focus, then make sure Enter still
    // opens the editor while no interactive element holds the DOM focus.
    await user.click(screen.getByTestId('cell-t1-title'));
    await user.keyboard('{Escape}');
    await user.keyboard('{Enter}');
    expect(document.querySelector('.md-cell-input')).not.toBeNull();
    await user.keyboard('{Escape}');

    // Same cell focus, but the DOM focus is now on a button inside the
    // grid card. Enter activates that button natively; it must not also
    // open the cell editor on top of it (double-fire).
    const autoButton = screen.getByRole('button', { name: /auto-number/i });
    await user.click(autoButton);
    expect(autoButton).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(document.querySelector('.md-cell-input')).toBeNull();
  });

  test('hiding the focused column drops the cell focus', async () => {
    const user = userEvent.setup();
    const { container } = renderPage();
    await screen.findByTestId('cell-t1-title');

    // Focus the title cell and exit edit mode, keeping the cell focus.
    await user.click(screen.getByTestId('cell-t1-title'));
    await user.keyboard('{Escape}');

    // Hide the focused column via the Columns menu.
    await user.click(screen.getByRole('button', { name: /columns/i }));
    await user.click(screen.getByRole('checkbox', { name: 'Title' }));
    expect(screen.queryByTestId('cell-t1-title')).not.toBeInTheDocument();

    // The cell focus is dropped with the column. Move focus off the menu
    // checkbox (typing guard) so the shortcuts see a non-input target.
    act(() => {
      document.activeElement.blur();
    });

    // Keyboard navigation must not land on any cell: the stale focus
    // pointed at the hidden column.
    await user.keyboard('{ArrowRight}');
    expect(container.querySelector('td.focused')).toBeNull();
    await user.keyboard('{Enter}');
    expect(document.querySelector('.md-cell-input')).toBeNull();
  });

  test('text commits are trimmed before becoming pending edits', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByTestId('cell-t1-genre');

    await user.click(screen.getByTestId('cell-t1-genre'));
    // The side panels have textboxes too; the grid's editor is unique.
    const input = document.querySelector('.md-cell-input');
    await user.clear(input);
    // Literal spaces: user-event types them, {Space} tokens are no-ops.
    await user.type(input, '  Jazz  ');
    await user.keyboard('{Enter}');

    // The review diff shows the trimmed value, not the padded draft
    // (textContent: text queries normalise whitespace).
    const reviewBtn = screen.getAllByRole('button', {
      name: /Review & apply \(1\)/,
    })[0];
    await user.click(reviewBtn);
    const dialog = screen.getByRole('dialog', { name: 'Review changes' });
    expect(dialog.querySelector('.review-new').textContent).toBe('Jazz');
  });
});

// ---------------------------------------------------------------------------
// T16: optimistic locking, conflict dialog and rename collisions
// ---------------------------------------------------------------------------

const versionTracks = [
  { ...tracks[0], version: 3, state: 'ok' },
  { ...tracks[1], version: 3, state: 'ok' },
];

function jsonResponseWithStatus(body, status) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

// Mocks the full T16 flow; the PATCH and jobs responses come from options.
function mockT16Api(opts = {}) {
  let patchCalls = [];
  global.fetch = jest.fn(async (url, init) => {
    const method = (init && init.method) || 'GET';
    if (url.startsWith('/api/library/albums/a1/tracks')) {
      return jsonResponse(versionTracks);
    }
    if (url.startsWith('/api/library/albums/a1')) {
      return jsonResponse({ id: 'a1', title: 'Blue Train', artist: 'Art Blakey' });
    }
    if (url.startsWith('/api/metadata/analyze/a1')) {
      return jsonResponse({ suggestions: [], common_substrings: [] });
    }
    if (url.startsWith('/api/metadata/rename-collisions')) {
      return jsonResponse(opts.collisionReports || []);
    }
    if (url.startsWith('/api/metadata/tracks') && method === 'PATCH') {
      patchCalls.push(JSON.parse(init.body));
      const responder = opts.patchResponder || (() => jsonResponse({ job_id: 'j1', change_set_id: 'c1' }, 202));
      return responder(patchCalls.length);
    }
    if (url.startsWith('/api/jobs')) {
      return jsonResponse([
        { id: 'j1', status: 'completed', details: opts.jobDetails || '{}' },
      ]);
    }
    return new Response(JSON.stringify({ error: 'not found' }), { status: 404 });
  });
  return { getPatchCalls: () => patchCalls };
}

// Edits the t1 title cell to `value` through the grid.
async function editTitle(user, value) {
  await user.click(screen.getByTestId('cell-t1-title'));
  const input = document.querySelector('.md-cell-input');
  await user.clear(input);
  await user.type(input, value);
  await user.keyboard('{Enter}');
}

describe('MetadataPage T16 apply flow', () => {
  beforeEach(() => {
    // Column visibility persists in localStorage between tests.
    window.localStorage.clear();
  });

  afterEach(() => {
    jest.restoreAllMocks();
  });

  test('PATCH carries expected_version and the chosen on_collision mode', async () => {
    const user = userEvent.setup();
    const api = mockT16Api({
      collisionReports: [
        { track_id: 't1', new_title: 'Taken', collides: true, existing_name: 'Taken.flac' },
      ],
    });
    renderPage();
    await screen.findByTestId('cell-t1-title');

    await editTitle(user, 'Taken');
    const reviewBtn = screen.getAllByRole('button', { name: /Review & apply \(1\)/ })[0];
    await user.click(reviewBtn);
    await screen.findByText('File name clash');

    // Pick the replace mode for the collision.
    const select = screen.getByRole('combobox', {
      name: /Rename collision for One/i,
    });
    await user.selectOptions(select, 'replace');

    await user.click(screen.getByRole('button', { name: /Apply 1 change/ }));
    await screen.findByText(/Changes applied successfully/i);

    const calls = api.getPatchCalls();
    expect(calls).toHaveLength(1);
    expect(calls[0].edits[0].track_id).toBe('t1');
    expect(calls[0].edits[0].expected_version).toBe(3);
    expect(calls[0].edits[0].on_collision).toBe('replace');
  });

  test('a 409 opens the conflict dialog and the re-apply uses the new version', async () => {
    const user = userEvent.setup();
    const conflicts = [
      {
        track_id: 't1',
        reason: 'version_conflict',
        current: { title: 'Newer Title' },
        current_version: 4,
      },
    ];
    let patches = 0;
    const api = mockT16Api({
      patchResponder: () => {
        patches += 1;
        if (patches === 1) {
          return jsonResponseWithStatus(
            { error: 'one or more tracks changed', conflicts },
            409
          );
        }
        return jsonResponse({ job_id: 'j1', change_set_id: 'c1' }, 202);
      },
    });
    renderPage();
    await screen.findByTestId('cell-t1-title');

    await editTitle(user, 'My Title');
    await user.click(screen.getAllByRole('button', { name: /Review & apply \(1\)/ })[0]);
    await user.click(screen.getByRole('button', { name: /Apply 1 change/ }));

    // The dialog appears on top of the review.
    const dialog = await screen.findByRole('dialog', { name: 'Version conflict' });
    expect(dialog).toHaveTextContent('Newer Title');
    expect(dialog).toHaveTextContent('My Title');

    // Overwrite with mine: re-apply against version 4.
    await user.click(screen.getByRole('button', { name: 'Overwrite all with yours' }));
    await screen.findByText(/Changes applied successfully/i);

    const calls = api.getPatchCalls();
    expect(calls).toHaveLength(2);
    expect(calls[0].edits[0].expected_version).toBe(3);
    expect(calls[1].edits[0].expected_version).toBe(4);
  });

  test('version_conflict job results open the conflict dialog', async () => {
    const user = userEvent.setup();
    mockT16Api({
      jobDetails: JSON.stringify({
        results: [
          {
            track_id: 't1',
            reason: 'version_conflict',
            current: { title: 'Jazz Title' },
            current_version: 4,
          },
        ],
      }),
    });
    renderPage();
    await screen.findByTestId('cell-t1-title');

    await editTitle(user, 'My Title');
    await user.click(screen.getAllByRole('button', { name: /Review & apply \(1\)/ })[0]);
    await user.click(screen.getByRole('button', { name: /Apply 1 change/ }));

    const dialog = await screen.findByRole('dialog', { name: 'Version conflict' });
    expect(dialog).toHaveTextContent('Jazz Title');
  });

  test('external_change job results merge into the success alert', async () => {
    const user = userEvent.setup();
    mockT16Api({
      jobDetails: JSON.stringify({
        results: [{ track_id: 't1', reason: 'external_change', fields: ['genre'] }],
      }),
    });
    renderPage();
    await screen.findByTestId('cell-t1-title');

    await editTitle(user, 'My Title');
    await user.click(screen.getAllByRole('button', { name: /Review & apply \(1\)/ })[0]);
    await user.click(screen.getByRole('button', { name: /Apply 1 change/ }));

    await screen.findByText(/changed on disk during apply/i);
    // No conflict dialog for external changes.
    expect(
      screen.queryByRole('dialog', { name: 'Version conflict' })
    ).not.toBeInTheDocument();
  });
});
