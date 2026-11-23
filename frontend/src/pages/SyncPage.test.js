import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ModalProvider } from '../components/ModalContext';
import SyncPage from './SyncPage';

const conflictGroups = [
  {
    track_id: 't1',
    track_title: 'One',
    album_id: 'a1',
    fields: [
      {
        id: 'c1',
        field: 'title',
        db_value: 'DB Title',
        file_value: 'File Title',
        source: 'scan',
        status: 'open',
      },
      {
        id: 'c2',
        field: 'year',
        db_value: '2001',
        file_value: '2003',
        source: 'concurrent',
        status: 'open',
      },
    ],
  },
];

const missingList = [
  {
    track_id: 'm1',
    album_id: 'a1',
    title: 'Gone',
    artist: 'Artist',
    album: 'Alb',
    path: '/music/Artist/Alb/Gone.flac',
    duration: 100,
    size: 10,
    candidates: [
      {
        track_id: 'c9',
        path: '/music/Artist/Alb/Gone Copy.flac',
        title: 'Gone Copy',
        duration: 100,
      },
    ],
  },
];

const trashList = [
  {
    id: 'tr1',
    original_path: '/music/old.flac',
    trash_path: '/trash/x.flac',
    created_at: '2026-01-01 00:00:00',
  },
];

function mockSyncApi() {
  const calls = [];
  global.fetch = jest.fn(async (url, init) => {
    const method = (init && init.method) || 'GET';
    const body = init && init.body ? JSON.parse(init.body) : null;
    calls.push({ url, method, body });
    if (url.startsWith('/api/sync/conflicts?status=open')) {
      return new Response(JSON.stringify(conflictGroups), { status: 200 });
    }
    if (url.startsWith('/api/sync/missing')) {
      return new Response(JSON.stringify(missingList), { status: 200 });
    }
    if (url.startsWith('/api/sync/trash')) {
      return new Response(JSON.stringify(trashList), { status: 200 });
    }
    if (url.startsWith('/api/sync/conflicts/resolve-all')) {
      return new Response(JSON.stringify({ job_id: 'ra1' }), { status: 202 });
    }
    if (url.startsWith('/api/sync/conflicts/resolve')) {
      return new Response(JSON.stringify({ job_id: 'r1' }), { status: 202 });
    }
    if (url.endsWith('/dismiss')) {
      return new Response(JSON.stringify({ status: 'dismissed' }), { status: 200 });
    }
    if (url.endsWith('/relink')) {
      return new Response(JSON.stringify({ id: 'm1', state: 'ok' }), { status: 200 });
    }
    if (url.endsWith('/restore')) {
      // Empty body on success (the handler writes no payload).
      return new Response(null, { status: 200 });
    }
    if (url.startsWith('/api/jobs')) {
      return new Response(
        JSON.stringify([
          { id: 'r1', status: 'completed', details: '{}' },
          { id: 'ra1', status: 'completed', details: '{}' },
        ]),
        { status: 200 },
      );
    }
    return new Response(JSON.stringify({ error: 'not found' }), { status: 404 });
  });
  return { calls };
}

function renderPage() {
  return render(
    <ModalProvider>
      <SyncPage />
    </ModalProvider>,
  );
}

// Dismisses the modal alert if one is up (keeps assertions deterministic).
async function dismissAlert(user) {
  const ok = screen.queryByRole('button', { name: 'OK' });
  if (ok) await user.click(ok);
}

describe('SyncPage', () => {
  beforeEach(() => {
    mockSyncApi();
  });

  afterEach(() => {
    jest.restoreAllMocks();
  });

  test('lists conflicts, resolves one track with mixed choices', async () => {
    const user = userEvent.setup();
    const api = mockSyncApi();
    renderPage();

    await screen.findByText('DB Title');
    expect(screen.getByText('File Title')).toBeInTheDocument();

    // Pick file for the title, a custom value for the year.
    const titleRow = screen.getByText('DB Title').closest('tr');
    await user.click(titleRow.querySelectorAll('input[type=radio]')[1]); // File
    const yearRow = screen.getByText('2001').closest('tr');
    await user.click(yearRow.querySelectorAll('input[type=radio]')[2]); // Custom
    await user.type(yearRow.querySelector('.sync-custom-input'), '2002');

    // Resolve is enabled once every field has a choice.
    const resolveBtn = screen.getByRole('button', { name: 'Resolve' });
    expect(resolveBtn).toBeEnabled();
    await user.click(resolveBtn);

    await screen.findByText(/Resolutions applied/i);
    await dismissAlert(user);

    const resolveCall = api.calls.find(
      (c) => c.url.startsWith('/api/sync/conflicts/resolve') && c.method === 'POST',
    );
    expect(resolveCall).toBeTruthy();
    expect(resolveCall.body.resolutions).toEqual([
      { conflict_id: 'c1', choice: 'file' },
      { conflict_id: 'c2', choice: 'custom', value: '2002' },
    ]);
  });

  test('resolve-all sends one choice for every open conflict', async () => {
    const user = userEvent.setup();
    const api = mockSyncApi();
    renderPage();
    await screen.findByText('DB Title');

    await user.click(screen.getByRole('button', { name: /Resolve all with file values/ }));
    await screen.findByText(/All conflicts resolved with file values/i);

    const call = api.calls.find((c) => c.url.startsWith('/api/sync/conflicts/resolve-all'));
    expect(call.body).toEqual({ choice: 'file' });
  });

  test('missing tab: relink picks the candidate path', async () => {
    const user = userEvent.setup();
    const api = mockSyncApi();
    renderPage();

    // Await the load (the tabs only exist once the data is in).
    const tab = await screen.findByRole('button', { name: /Missing files/ });
    await user.click(tab);
    await screen.findByText(/Expected at:/i);
    expect(screen.getByText('/music/Artist/Alb/Gone Copy.flac')).toBeInTheDocument();

    await user.click(screen.getByRole('radio'));
    const relinkBtn = screen.getByRole('button', { name: 'Relink track' });
    expect(relinkBtn).toBeEnabled();
    await user.click(relinkBtn);
    await screen.findByText(/Track relinked/i);

    const call = api.calls.find((c) => c.url.endsWith('/relink'));
    expect(call.url).toBe('/api/sync/missing/m1/relink');
    expect(call.body).toEqual({ path: '/music/Artist/Alb/Gone Copy.flac' });
  });

  test('trash tab: restore posts and survives the empty body', async () => {
    const user = userEvent.setup();
    const api = mockSyncApi();
    renderPage();

    const tab = await screen.findByRole('button', { name: /Trash/ });
    await user.click(tab);
    await screen.findByText('/music/old.flac');
    await user.click(screen.getByRole('button', { name: 'Restore' }));
    await screen.findByText(/File restored/i);

    const call = api.calls.find((c) => c.url.endsWith('/restore'));
    expect(call.url).toBe('/api/sync/trash/tr1/restore');
  });

  test('dismiss marks one conflict without touching the others', async () => {
    const user = userEvent.setup();
    const api = mockSyncApi();
    renderPage();
    await screen.findByText('DB Title');

    const dismissButtons = screen.getAllByRole('button', { name: /Dismiss/i });
    expect(dismissButtons).toHaveLength(2);
    await user.click(dismissButtons[0]);
    await waitFor(() => {
      expect(
        api.calls.some((c) => c.url === '/api/sync/conflicts/c1/dismiss' && c.method === 'POST'),
      ).toBe(true);
    });
  });
});
