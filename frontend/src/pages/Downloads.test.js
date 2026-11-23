import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ModalProvider } from '../components/ModalContext';
import Downloads from './Downloads';
import { mockApi, callsTo, jsonResponse } from '../testSetup/mockApi';

const PLAYLIST = {
  title: 'Great Album',
  uploader: 'The Band',
  entries: [
    { id: 'a1', title: 'Song A', duration: 125, thumbnail: 't1' },
    null,
    { id: 'b2', title: 'Song B', duration: 61, uploader: 'Guest' },
  ],
};

function setup(extra = {}) {
  const api = mockApi({ 'GET /api/info?url=https%3A%2F%2Fy.t%2Fp': PLAYLIST, ...extra });
  render(
    <ModalProvider>
      <Downloads />
    </ModalProvider>,
  );
  return api;
}

async function load(user) {
  await user.type(screen.getByPlaceholderText(/Paste YouTube/), 'https://y.t/p');
  await user.click(screen.getByRole('button', { name: 'Fetch Metadata' }));
  await screen.findByText('Great Album');
}

describe('Downloads', () => {
  test('fetches playlist info, skipping null entries, with sensible defaults', async () => {
    setup();
    await load(userEvent.setup());
    expect(screen.getByText('2 tracks detected')).toBeInTheDocument();
    expect(screen.getByText('Song A')).toBeInTheDocument();
    expect(screen.getByText('2:05')).toBeInTheDocument();
    expect(screen.getByDisplayValue('Guest')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Download 2 Tracks' })).toBeEnabled();
  });

  test('bulk edit applies the artist to every selected track', async () => {
    setup();
    const user = userEvent.setup();
    await load(user);
    await user.type(screen.getByPlaceholderText('Common artist name...'), 'Various');
    await user.click(screen.getByRole('button', { name: 'Apply to Selected (2)' }));
    expect(screen.getAllByDisplayValue('Various')).toHaveLength(2);
  });

  test('deselecting everything disables the download button', async () => {
    setup();
    const user = userEvent.setup();
    await load(user);
    await user.click(screen.getAllByRole('checkbox')[0]);
    expect(screen.getByRole('button', { name: 'Download 0 Tracks' })).toBeDisabled();
  });

  test('queueing posts the selected items and reports the count', async () => {
    const api = setup({ 'POST /api/queue': { queued: 2 } });
    const user = userEvent.setup();
    await load(user);
    await user.click(screen.getByRole('button', { name: 'Download 2 Tracks' }));
    expect(await screen.findByText(/Successfully queued 2 downloads/)).toBeInTheDocument();
    const body = JSON.parse(callsTo(api, 'POST', '/api/queue')[0][1].body);
    expect(body).toEqual([
      {
        video_id: 'a1',
        title: 'Song A',
        artist: 'The Band',
        album: 'Great Album',
        thumbnail: 't1',
      },
      { video_id: 'b2', title: 'Song B', artist: 'Guest', album: 'Great Album' },
    ]);
  });

  test('an info error from the backend is shown in an alert', async () => {
    setup({ 'GET /api/info?url=https%3A%2F%2Fy.t%2Fp': jsonResponse({ error: 'bad url' }) });
    const user = userEvent.setup();
    await user.type(screen.getByPlaceholderText(/Paste YouTube/), 'https://y.t/p');
    await user.click(screen.getByRole('button', { name: 'Fetch Metadata' }));
    await screen.findByText('bad url');
  });
});
