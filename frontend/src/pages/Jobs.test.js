import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { ModalProvider } from '../components/ModalContext';
import Jobs from './Jobs';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

const LONG_ERROR =
  'ERROR: [youtube] dQw4w9WgXcQ: HTTP Error 503: Service Unavailable ' +
  'repeatedly for several minutes while the network kept dropping';

function makeJob(overrides = {}) {
  return {
    id: 'job-1',
    type: 'download',
    status: 'queued',
    details: JSON.stringify({ video_id: 'abc', title: 'Cool Song' }),
    progress: '0%',
    error: '',
    attempts: 0,
    next_retry_at: null,
    created_at: '2026-11-22T12:00:00Z',
    ...overrides,
  };
}

// jobsFetch builds a fetch mock for the jobs endpoints. jobs may be a list
// or a function of the current call count, so tests can observe refetches
// and mutate state between them.
function jobsFetch(jobs, { retryStatus = 200, retryBody, retryAllStatus = 200 } = {}) {
  const impl = jest.fn(async (url, opts = {}) => {
    const method = opts.method || 'GET';
    if (url === '/api/jobs') {
      const list = typeof jobs === 'function' ? jobs() : jobs;
      return new Response(JSON.stringify(list), { status: 200 });
    }
    if (url.startsWith('/api/jobs/') && url.endsWith('/retry')) {
      const body = retryBody || { id: url.split('/')[3], status: 'queued' };
      return new Response(JSON.stringify(body), { status: retryStatus });
    }
    if (url === '/api/jobs/retry-failed') {
      return new Response(JSON.stringify({ retried: 2 }), {
        status: retryAllStatus,
      });
    }
    return new Response(null, { status: 404 });
  });
  const jobListCalls = () =>
    impl.mock.calls.filter((c) => c[0] === '/api/jobs').length;
  const callsWith = (url, method = 'GET') =>
    impl.mock.calls.filter(
      (c) => c[0] === url && (c[1] || {}).method === method
    );
  return { impl, jobListCalls, callsWith };
}

async function renderJobs(fetchImpl) {
  global.fetch = fetchImpl;
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <ModalProvider>
        <Jobs />
      </ModalProvider>
    );
  });
  return {
    container,
    unmount: () => act(async () => root.unmount()),
  };
}

// The page renders each job twice (desktop table and mobile cards), so
// every lookup returns all instances.
const retryButtons = (container) =>
  Array.from(container.querySelectorAll('.job-retry-btn'));
const retryAllButton = (container) =>
  Array.from(container.querySelectorAll('button')).find((b) =>
    b.textContent.includes('Retry all failed')
  );
const click = async (element) => {
  await act(async () => {
    element.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  });
};

describe('Jobs retry', () => {
  afterEach(() => {
    jest.restoreAllMocks();
    jest.useRealTimers();
  });

  test('Retry button calls the endpoint, spins, then refreshes', async () => {
    const failed = makeJob({
      id: 'dl-1',
      status: 'failed',
      error: 'HTTP Error 503: Service Unavailable',
    });
    // The retry response is gated behind a promise so the in-flight
    // spinner state is observable.
    let jobListCalls = 0;
    let retryCalls = 0;
    let resolveRetry;
    const retryGate = new Promise((r) => {
      resolveRetry = r;
    });
    const impl = jest.fn(async (url, opts = {}) => {
      if (url === '/api/jobs') {
        jobListCalls += 1;
        return new Response(JSON.stringify([failed]), { status: 200 });
      }
      if (url === '/api/jobs/dl-1/retry' && (opts || {}).method === 'POST') {
        retryCalls += 1;
        await retryGate;
        return new Response(
          JSON.stringify({ ...failed, status: 'queued' }),
          { status: 200 }
        );
      }
      return new Response(null, { status: 404 });
    });

    const { container, unmount } = await renderJobs(impl);
    expect(jobListCalls).toBe(1);

    const buttons = retryButtons(container);
    expect(buttons.length).toBeGreaterThan(0);
    for (const b of buttons) {
      expect(b.textContent).toBe('Retry');
      expect(b.disabled).toBe(false);
    }

    await click(buttons[0]);

    // In flight: the endpoint was hit, every Retry button spins.
    expect(retryCalls).toBe(1);
    expect(container.querySelector('.job-retry-btn .spinner')).not.toBeNull();
    expect(retryButtons(container)[0].textContent).toBe('Retrying…');
    expect(retryButtons(container)[0].disabled).toBe(true);
    // No refresh until the request settles.
    expect(jobListCalls).toBe(1);

    await act(async () => {
      resolveRetry();
    });

    // Settled: spinner gone, list refetched, button back to Retry.
    expect(container.querySelector('.job-retry-btn .spinner')).toBeNull();
    expect(retryButtons(container)[0].textContent).toBe('Retry');
    expect(retryButtons(container)[0].disabled).toBe(false);
    expect(jobListCalls).toBe(2);
    await unmount();
  });

  test('retry-all is hidden when there are no failed downloads', async () => {
    const mocked = jobsFetch([
      makeJob({ id: 'dl-done', status: 'completed', progress: '100%' }),
      makeJob({
        id: 'scan-1',
        type: 'scan',
        status: 'failed',
        error: 'scan exploded',
      }),
    ]);
    const { container, unmount } = await renderJobs(mocked.impl);

    expect(retryAllButton(container)).toBeUndefined();
    // A failed non-download job gets no Retry button either.
    expect(retryButtons(container)).toHaveLength(0);
    await unmount();
  });

  test('Retry all failed (n) is shown, calls the endpoint and refreshes', async () => {
    const mocked = jobsFetch([
      makeJob({ id: 'dl-1', status: 'failed', error: 'err a' }),
      makeJob({ id: 'dl-2', status: 'failed', error: 'err b' }),
      makeJob({ id: 'scan-1', type: 'scan', status: 'failed', error: 'c' }),
    ]);
    const { container, unmount } = await renderJobs(mocked.impl);

    const button = retryAllButton(container);
    expect(button).toBeDefined();
    expect(button.textContent).toBe('Retry all failed (2)');

    await click(button);

    expect(mocked.callsWith('/api/jobs/retry-failed', 'POST')).toHaveLength(1);
    expect(mocked.jobListCalls()).toBe(2);
    await unmount();
  });

  test('shows a modal (not alert) when the retry endpoint rejects', async () => {
    const alertSpy = jest.spyOn(window, 'alert').mockImplementation(() => {});
    const failed = makeJob({
      id: 'dl-1',
      status: 'failed',
      error: 'boom',
    });
    const mocked = jobsFetch([failed], {
      retryStatus: 409,
      retryBody: { error: 'job is not a failed download' },
    });
    const { container, unmount } = await renderJobs(mocked.impl);

    await click(retryButtons(container)[0]);

    expect(alertSpy).not.toHaveBeenCalled();
    expect(container.querySelector('.modal-title').textContent).toBe(
      'Retry failed'
    );
    expect(container.querySelector('.modal-message').textContent).toBe(
      'job is not a failed download'
    );
    await unmount();
  });

  test('truncates long errors with a working details expander', async () => {
    const mocked = jobsFetch([
      makeJob({ id: 'dl-1', status: 'failed', error: LONG_ERROR }),
    ]);
    const { container, unmount } = await renderJobs(mocked.impl);

    const truncated = `${LONG_ERROR.slice(0, 80)}…`;
    const cells = Array.from(container.querySelectorAll('.job-error'));
    expect(cells.length).toBeGreaterThan(0);
    for (const cell of cells) {
      expect(cell.textContent).toBe(`${truncated}details`);
    }
    expect(container.textContent).not.toContain(LONG_ERROR);

    // Expander state is shared per job, so one click expands the error
    // in both views (desktop table and mobile cards).
    await click(container.querySelector('.job-error-toggle'));

    const expanded = Array.from(container.querySelectorAll('.job-error'));
    for (const cell of expanded) {
      expect(cell.textContent).toContain(LONG_ERROR);
    }
    for (const t of Array.from(container.querySelectorAll('.job-error-toggle'))) {
      expect(t.textContent).toBe('less');
    }

    // Collapse again.
    await click(container.querySelector('.job-error-toggle'));
    for (const cell of Array.from(container.querySelectorAll('.job-error'))) {
      expect(cell.textContent).toBe(`${truncated}details`);
    }
    await unmount();
  });

  test('shows attempt count and the retrying-in badge', async () => {
    // attempt n for a job that already used retry budget.
    let { container, unmount } = await renderJobs(
      jobsFetch([
        makeJob({
          id: 'dl-1',
          status: 'failed',
          error: 'HTTP Error 503',
          attempts: 2,
        }),
      ]).impl
    );
    expect(container.textContent).toContain('attempt 2');
    expect(container.textContent).not.toContain('retrying in');
    await unmount();

    // "retrying in …" badge for a queued job with a future next_retry_at.
    const future = new Date(Date.now() + 30 * 1000).toISOString();
    ({ container, unmount } = await renderJobs(
      jobsFetch([
        makeJob({ id: 'dl-2', status: 'queued', next_retry_at: future }),
      ]).impl
    ));
    expect(container.textContent).toContain('retrying in');
    await unmount();

    // A plain queued job without a scheduled retry shows no badge.
    ({ container, unmount } = await renderJobs(
      jobsFetch([makeJob({ id: 'dl-3', status: 'queued' })]).impl
    ));
    expect(container.textContent).not.toContain('retrying in');
    await unmount();
  });
});
