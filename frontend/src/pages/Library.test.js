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

// Renders the Library page with a fetch mock whose scan job can be
// flipped to a terminal status between polls, and whose artist list
// grows on every fetch (so refetches are observable).
async function renderScanWatch({ finalArtist = 'New Artist' } = {}) {
  const staleJob = {
    id: 'scan-old',
    type: 'scan',
    status: 'completed',
    details: '',
    progress: '100%',
    created_at: '2026-11-01T00:00:00Z',
  };
  let newJobStatus = 'running';
  let artistFetchCount = 0;
  const fetchImpl = jest.fn(async (url) => {
    if (url === '/api/library/artists') {
      artistFetchCount += 1;
      const list =
        artistFetchCount === 1 ? [] : [{ id: 'a1', name: finalArtist }];
      return new Response(JSON.stringify(list), { status: 200 });
    }
    if (url === '/api/library/scan') {
      return new Response(null, { status: 202 });
    }
    if (url === '/api/jobs') {
      const jobs = [
        staleJob,
        {
          id: 'scan-new',
          type: 'scan',
          status: newJobStatus,
          details: '',
          progress: '50%',
          created_at: '2026-11-22T00:00:00Z',
        },
      ];
      return new Response(JSON.stringify(jobs), { status: 200 });
    }
    return new Response('[]', { status: 200 });
  });
  const rendered = await renderLibrary(fetchImpl);
  return {
    ...rendered,
    finishScan: (status) => {
      newJobStatus = status;
    },
    artistFetchCount: () => artistFetchCount,
  };
}

// Two scan jobs share a created_at timestamp (same-second starts). The
// /jobs array order may differ between polls, and the settled decision
// must not depend on it. The id tiebreak makes the running job
// (id 'scan-tie-2') win the tie in both orders.
async function renderTieScanWatch({ staleFirst = true } = {}) {
  const tiedAt = '2026-11-22T00:00:00Z';
  const staleJob = {
    id: 'scan-tie-1',
    type: 'scan',
    status: 'completed',
    details: '',
    progress: '100%',
    created_at: tiedAt,
  };
  let newJobStatus = 'running';
  let artistFetchCount = 0;
  const fetchImpl = jest.fn(async (url) => {
    if (url === '/api/library/artists') {
      artistFetchCount += 1;
      const list =
        artistFetchCount === 1 ? [] : [{ id: 'a1', name: 'Tie Artist' }];
      return new Response(JSON.stringify(list), { status: 200 });
    }
    if (url === '/api/library/scan') {
      return new Response(null, { status: 202 });
    }
    if (url === '/api/jobs') {
      const newJob = {
        id: 'scan-tie-2',
        type: 'scan',
        status: newJobStatus,
        details: '',
        progress: '50%',
        created_at: tiedAt,
      };
      const jobs = staleFirst ? [staleJob, newJob] : [newJob, staleJob];
      return new Response(JSON.stringify(jobs), { status: 200 });
    }
    return new Response('[]', { status: 200 });
  });
  const rendered = await renderLibrary(fetchImpl);
  return {
    ...rendered,
    finishScan: (status) => {
      newJobStatus = status;
    },
    artistFetchCount: () => artistFetchCount,
  };
}

describe('Library artist refetch after scan', () => {
  afterEach(() => {
    jest.restoreAllMocks();
    jest.useRealTimers();
  });

  test('refetches the artist list when the scan job completes', async () => {
    jest.useFakeTimers();
    const { container, unmount, finishScan, artistFetchCount } =
      await renderScanWatch();

    expect(artistFetchCount()).toBe(1); // mount fetch

    await clickScan(container);
    // The scan job is still running, and the stale completed job from a
    // previous scan must not count as its completion.
    expect(artistFetchCount()).toBe(1);

    finishScan('completed');
    await act(async () => {
      jest.advanceTimersByTime(2000);
    });
    expect(artistFetchCount()).toBe(2);
    expect(container.textContent).toContain('New Artist');

    // The watch stops after the terminal state: no further fetches.
    await act(async () => {
      jest.advanceTimersByTime(4000);
    });
    expect(artistFetchCount()).toBe(2);
    await unmount();
  });

  test('refetches the artist list when the scan job fails', async () => {
    jest.useFakeTimers();
    const { container, unmount, finishScan, artistFetchCount } =
      await renderScanWatch({ finalArtist: 'Partial Artist' });

    await clickScan(container);
    expect(artistFetchCount()).toBe(1);

    finishScan('failed');
    await act(async () => {
      jest.advanceTimersByTime(2000);
    });
    expect(artistFetchCount()).toBe(2);
    expect(container.textContent).toContain('Partial Artist');
    await unmount();
  });
});

describe('Library scan settled on tied created_at', () => {
  afterEach(() => {
    jest.restoreAllMocks();
    jest.useRealTimers();
  });

  for (const staleFirst of [true, false]) {
    test(`settled decision is the same when the stale job is ${
      staleFirst ? 'first' : 'last'
    } in /jobs`, async () => {
      jest.useFakeTimers();
      const { container, unmount, finishScan, artistFetchCount } =
        await renderTieScanWatch({ staleFirst });

      await clickScan(container);
      // Both jobs share a created_at. The tiebreak must pick the same
      // job (the still-running one) in both array orders, so the watch
      // must not settle early on the stale completed job.
      expect(artistFetchCount()).toBe(1);

      finishScan('completed');
      await act(async () => {
        jest.advanceTimersByTime(2000);
      });
      expect(artistFetchCount()).toBe(2);
      expect(container.textContent).toContain('Tie Artist');

      // The settled decision is stable: the watch stays stopped.
      await act(async () => {
        jest.advanceTimersByTime(4000);
      });
      expect(artistFetchCount()).toBe(2);
      await unmount();
    });
  }
});

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
