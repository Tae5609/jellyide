import React from 'react';
import { render, screen } from '@testing-library/react';
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
