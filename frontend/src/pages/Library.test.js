import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { ModalProvider } from '../components/ModalContext';
import Library from './Library';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

// jobs: list returned by GET /api/jobs (a function is called per request,
// so tests can mutate job state between polls).
function makeFetch({ scanStatus = 202, artists = [], jobs = [] } = {}) {
  return jest.fn(async (url) => {
    if (url === '/api/library/artists') {
      const list = typeof artists === 'function' ? artists() : artists;
      return new Response(JSON.stringify(list), { status: 200 });
    }
    if (url.startsWith('/api/library/artists/') && url.endsWith('/albums')) {
      return new Response(JSON.stringify([]), { status: 200 });
    }
    if (url === '/api/library/scan') {
      return new Response(null, { status: scanStatus });
    }
    if (url === '/api/jobs') {
      const list = typeof jobs === 'function' ? jobs() : jobs;
      return new Response(JSON.stringify(list), { status: 200 });
    }
    return new Response(null, { status: 500 });
  });
}

async function renderLibrary(fetchImpl) {
  global.fetch = fetchImpl;
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <MemoryRouter initialEntries={['/library']}>
        <ModalProvider>
          <Routes>
            <Route path="/library" element={<Library />} />
            <Route path="/library/:artistId" element={<Library />} />
          </Routes>
        </ModalProvider>
      </MemoryRouter>
    );
  });
  return {
    container,
    unmount: () => act(async () => root.unmount()),
  };
}

const scanButton = (container) =>
  Array.from(container.querySelectorAll('button')).find(
    (b) => b.textContent === 'SCAN'
  );

async function clickScan(container) {
  await act(async () => {
    scanButton(container).dispatchEvent(
      new MouseEvent('click', { bubbles: true })
    );
  });
}

const artistsFetches = (fetchMock) =>
  fetchMock.mock.calls.filter((c) => c[0] === '/api/library/artists');

describe('Library scan feedback', () => {
  afterEach(() => {
    jest.restoreAllMocks();
    jest.useRealTimers();
  });

  test('shows an error modal instead of alert() when the scan cannot start', async () => {
    const alertSpy = jest.spyOn(window, 'alert').mockImplementation(() => {});
    const { container, unmount } = await renderLibrary(
      makeFetch({ scanStatus: 500 })
    );

    await clickScan(container);

    expect(alertSpy).not.toHaveBeenCalled();
    expect(container.querySelector('.modal-title').textContent).toBe('Error');
    expect(container.querySelector('.modal-message').textContent).toBe(
      'Failed to start scan'
    );
    await unmount();
  });

  test('shows a modal instead of alert() when the scan starts', async () => {
    const alertSpy = jest.spyOn(window, 'alert').mockImplementation(() => {});
    const { container, unmount } = await renderLibrary(
      makeFetch({ scanStatus: 202 })
    );

    await clickScan(container);

    expect(alertSpy).not.toHaveBeenCalled();
    expect(container.querySelector('.modal-title').textContent).toBe(
      'Library scan started'
    );
    await unmount();
  });
});
