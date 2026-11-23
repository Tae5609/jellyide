import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { ModalProvider } from '../components/ModalContext';
import { AuthProvider } from '../auth/AuthContext';
import Settings from './Settings';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

// The status payload served by GET /api/admin/ytdlp (mutable per call via
// the ytdlpUpdating sequence).
const YTDLP_STATUS = {
  version: '2025.1.15',
  path: '/media/.jellyide/bin/yt-dlp',
  managed: true,
  channel: 'stable',
  latest_version: '2025.2.2',
  update_available: true,
  last_check: '2026-11-22T10:00:00Z',
  last_result: 'updated to 2025.1.15',
  auto_update: true,
  interval_hours: 24,
  updating: false,
  has_rollback: true,
};

// Mock fetch for auth and settings. ytdlpUpdating, when given, is the
// sequence of `updating` values returned by successive GET /api/admin/ytdlp
// calls (used to drive the update polling loop).
function mockFetch({ isAdmin = true, ytdlpUpdating = null } = {}) {
  let ytdlpGets = 0;
  return jest.fn(async (url, opts = {}) => {
    if (url === '/api/auth/providers') {
      return new Response(
        JSON.stringify({
          enabled: true,
          local: true,
          oidc: { enabled: false, name: '' },
          saml: { enabled: false, name: '' },
        }),
        { status: 200 },
      );
    }
    if (url === '/api/auth/me') {
      return new Response(
        JSON.stringify({
          id: '1',
          username: 'tester',
          provider: 'oidc',
          is_admin: isAdmin,
        }),
        { status: 200 },
      );
    }
    if (url === '/api/config') {
      if (opts.method === 'POST') {
        return new Response(JSON.stringify({}), { status: 200 });
      }
      return new Response(JSON.stringify({ concurrent_limit: 2, proxy: '' }), { status: 200 });
    }
    if (url === '/api/admin/ytdlp') {
      ytdlpGets += 1;
      const updating = ytdlpUpdating ? (ytdlpUpdating[ytdlpGets - 1] ?? false) : false;
      return new Response(JSON.stringify({ ...YTDLP_STATUS, updating }), { status: 200 });
    }
    if (url === '/api/admin/ytdlp/update' && opts.method === 'POST') {
      return new Response(JSON.stringify({ job_id: 'ytdlp-job-1' }), { status: 202 });
    }
    if (url === '/api/admin/ytdlp/rollback' && opts.method === 'POST') {
      return new Response(
        JSON.stringify({ ...YTDLP_STATUS, version: '2025.1.10', has_rollback: false }),
        { status: 200 },
      );
    }
    if (url === '/api/admin/ytdlp/check') {
      return new Response(
        JSON.stringify({
          version: '2025.1.15',
          path: '/media/.jellyide/bin/yt-dlp',
          managed: true,
          channel: 'stable',
          latest_version: '2025.2.2',
          update_available: true,
          last_check: '2026-11-22T12:00:00Z',
          last_result: 'check ok',
          auto_update: true,
          interval_hours: 24,
          updating: false,
          has_rollback: true,
        }),
        { status: 200 },
      );
    }
    if (url === '/api/admin/settings') {
      return new Response(JSON.stringify({ registration_mode: 'open' }), { status: 200 });
    }
    return new Response(null, { status: 500 });
  });
}

async function renderSettings(opts = {}) {
  const fetchImpl = mockFetch(opts);
  global.fetch = fetchImpl;
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <ModalProvider>
        <AuthProvider>
          <Settings />
        </AuthProvider>
      </ModalProvider>,
    );
  });
  return {
    fetchImpl,
    container,
    unmount: () => act(async () => root.unmount()),
  };
}

const limitInput = (container) => container.querySelector('input[type="number"]');

const saveButton = (container) =>
  Array.from(container.querySelectorAll('button')).find(
    (b) => b.textContent === 'Save Configuration',
  );

function setLimit(container, value) {
  act(() => {
    const input = limitInput(container);
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
    setter.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

async function clickSave(container) {
  await act(async () => {
    saveButton(container).dispatchEvent(new MouseEvent('click', { bubbles: true }));
  });
}

const postConfigCalls = (fetchMock) =>
  fetchMock.mock.calls.filter(
    ([url, opts]) => url === '/api/config' && opts && opts.method === 'POST',
  );

describe('Settings concurrent limit validation', () => {
  afterEach(() => {
    jest.restoreAllMocks();
  });

  test('empty limit: inline error, previous value kept, no POST', async () => {
    const { fetchImpl, container, unmount } = await renderSettings();

    setLimit(container, '');
    await clickSave(container);

    expect(container.textContent).toContain('Enter a positive whole number');
    expect(postConfigCalls(fetchImpl)).toHaveLength(0);
    // The previous value (loaded from /config) is restored, not NaN/null.
    expect(limitInput(container).value).toBe('2');
    await unmount();
  });

  test('non-integer limit: inline error and no POST', async () => {
    const { fetchImpl, container, unmount } = await renderSettings();

    setLimit(container, '1.5');
    await clickSave(container);
    expect(container.textContent).toContain('Enter a positive whole number');
    expect(postConfigCalls(fetchImpl)).toHaveLength(0);

    setLimit(container, '0');
    await clickSave(container);
    expect(postConfigCalls(fetchImpl)).toHaveLength(0);
    await unmount();
  });

  test('a valid limit is saved as a number', async () => {
    const { fetchImpl, container, unmount } = await renderSettings();

    setLimit(container, '5');
    await clickSave(container);

    const calls = postConfigCalls(fetchImpl);
    expect(calls).toHaveLength(1);
    const [url, opts] = calls[0];
    expect(url).toBe('/api/config');
    expect(JSON.parse(opts.body)).toEqual({
      concurrent_limit: 5,
      proxy: '',
    });
    expect(container.querySelector('.modal-title').textContent).toBe('Settings Saved');
    await unmount();
  });
});

describe('Settings admin controls and yt-dlp card', () => {
  afterEach(() => {
    jest.restoreAllMocks();
  });

  test('non-admin cannot see save button or yt-dlp card', async () => {
    const { container, unmount } = await renderSettings({ isAdmin: false });

    // Non-admin sees read-only inputs without Save button
    expect(saveButton(container)).toBeUndefined();
    expect(limitInput(container).disabled).toBe(true);

    // yt-dlp card is hidden from non-admin
    expect(container.querySelector('[data-testid="ytdlp-card"]')).toBeNull();
    await unmount();
  });

  test('admin sees yt-dlp card with version info and update controls', async () => {
    const { fetchImpl, container, unmount } = await renderSettings({ isAdmin: true });

    // Admin sees Save button and enabled inputs
    expect(saveButton(container)).toBeDefined();
    expect(limitInput(container).disabled).toBe(false);

    // yt-dlp card is visible
    const card = container.querySelector('[data-testid="ytdlp-card"]');
    expect(card).not.toBeNull();
    expect(card.textContent).toContain('2025.1.15');
    expect(card.textContent).toContain('Update available');

    // Clicking Check for updates calls /api/admin/ytdlp/check
    const checkBtn = Array.from(card.querySelectorAll('button')).find(
      (b) => b.textContent === 'Check for updates',
    );
    expect(checkBtn).toBeDefined();

    await act(async () => {
      checkBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });

    const checkCalls = fetchImpl.mock.calls.filter(
      ([url, opts]) => url === '/api/admin/ytdlp/check' && opts && opts.method === 'POST',
    );
    expect(checkCalls).toHaveLength(1);

    await unmount();
  });

  test('update now: posts the update job and polls until updating=false', async () => {
    jest.useFakeTimers();
    try {
      // Status sequence for GET /api/admin/ytdlp: the mount load is idle,
      // the post-update reload and the first poll still report an update
      // in flight, and the second poll finishes it.
      const { fetchImpl, container, unmount } = await renderSettings({
        isAdmin: true,
        ytdlpUpdating: [false, true, true, false],
      });

      const card = container.querySelector('[data-testid="ytdlp-card"]');
      const updateBtn = Array.from(card.querySelectorAll('button')).find(
        (b) => b.textContent === 'Update now',
      );
      expect(updateBtn).toBeDefined();
      expect(updateBtn.disabled).toBe(false);

      await act(async () => {
        updateBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      });

      // The update job was started.
      const updateCalls = fetchImpl.mock.calls.filter(
        ([url, opts]) => url === '/api/admin/ytdlp/update' && opts && opts.method === 'POST',
      );
      expect(updateCalls).toHaveLength(1);
      expect(JSON.parse(updateCalls[0][1].body)).toEqual({ force: false });

      // Mount load plus the post-update reload; polling has not fired yet.
      const ytdlpGets = () =>
        fetchImpl.mock.calls.filter(([url]) => url === '/api/admin/ytdlp').length;
      expect(ytdlpGets()).toBe(2);

      // Poll (2s interval) until the status reports updating=false.
      for (let i = 0; i < 4 && !container.textContent.includes('yt-dlp update finished.'); i += 1) {
        await act(async () => {
          jest.advanceTimersByTime(2000);
        });
      }
      expect(container.textContent).toContain('yt-dlp update finished.');

      // The button is back and enabled once the update is done.
      const doneBtn = Array.from(card.querySelectorAll('button')).find(
        (b) => b.textContent === 'Update now',
      );
      expect(doneBtn).toBeDefined();
      expect(doneBtn.disabled).toBe(false);

      // Polling stopped: no further status requests after updating=false.
      const polls = ytdlpGets();
      expect(polls).toBeGreaterThanOrEqual(3);
      await act(async () => {
        jest.advanceTimersByTime(10000);
      });
      expect(ytdlpGets()).toBe(polls);

      await unmount();
    } finally {
      jest.useRealTimers();
    }
  });

  test('roll back: the confirm modal gates the rollback POST', async () => {
    const { fetchImpl, container, unmount } = await renderSettings();

    const card = container.querySelector('[data-testid="ytdlp-card"]');
    const rollbackBtn = Array.from(card.querySelectorAll('button')).find(
      (b) => b.textContent === 'Roll back',
    );
    expect(rollbackBtn).toBeDefined();
    expect(rollbackBtn.disabled).toBe(false);

    const rollbackCalls = () =>
      fetchImpl.mock.calls.filter(
        ([url, opts]) => url === '/api/admin/ytdlp/rollback' && opts && opts.method === 'POST',
      );

    // Clicking Roll back opens the confirm modal, not the request.
    await act(async () => {
      rollbackBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    const modalTitle = container.querySelector('.modal-title');
    expect(modalTitle).not.toBeNull();
    expect(modalTitle.textContent).toBe('Roll back yt-dlp?');
    const modalButtons = () =>
      Array.from(container.querySelectorAll('.modal-actions button')).map((b) => b.textContent);
    expect(modalButtons()).toEqual(expect.arrayContaining(['Cancel', 'Confirm']));
    expect(rollbackCalls()).toHaveLength(0);

    // Declining closes the modal and keeps the binary: no rollback POST.
    const cancelBtn = Array.from(container.querySelectorAll('.modal-actions button')).find(
      (b) => b.textContent === 'Cancel',
    );
    await act(async () => {
      cancelBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    expect(container.querySelector('.modal-title')).toBeNull();
    expect(rollbackCalls()).toHaveLength(0);

    // Confirming POSTs the rollback and reports success.
    await act(async () => {
      rollbackBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    const confirmBtn = Array.from(container.querySelectorAll('.modal-actions button')).find(
      (b) => b.textContent === 'Confirm',
    );
    expect(confirmBtn).toBeDefined();
    await act(async () => {
      confirmBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    expect(rollbackCalls()).toHaveLength(1);
    expect(container.querySelector('.modal-title').textContent).toBe('Rollback Successful');

    await unmount();
  });
});

// ---------------------------------------------------------------------------
// T16: sync conflict policy card (admin only, self-contained)
// ---------------------------------------------------------------------------

function mockFetchAdmin(isAdmin, syncPolicy) {
  return jest.fn(async (url, opts = {}) => {
    if (url === '/api/auth/providers') {
      return new Response(
        JSON.stringify({
          enabled: true,
          local: true,
          oidc: { enabled: false, name: '' },
          saml: { enabled: false, name: '' },
        }),
        { status: 200 },
      );
    }
    if (url === '/api/auth/me') {
      return new Response(
        JSON.stringify({
          id: '1',
          username: isAdmin ? 'root' : 'tester',
          provider: 'local',
          is_admin: isAdmin,
        }),
        { status: 200 },
      );
    }
    if (url === '/api/config') {
      if (opts.method === 'POST') {
        return new Response(JSON.stringify({}), { status: 200 });
      }
      return new Response(JSON.stringify({ concurrent_limit: 2, proxy: '' }), { status: 200 });
    }
    if (url === '/api/admin/settings/sync') {
      if (opts.method === 'PUT') {
        return new Response(JSON.stringify({ policy: JSON.parse(opts.body).policy }), {
          status: 200,
        });
      }
      return new Response(JSON.stringify({ policy: syncPolicy || 'ask' }), { status: 200 });
    }
    return new Response(null, { status: 500 });
  });
}

async function renderSettingsWith(fetchImpl) {
  global.fetch = fetchImpl;
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <ModalProvider>
        <AuthProvider>
          <Settings />
        </AuthProvider>
      </ModalProvider>,
    );
  });
  return {
    container,
    unmount: () => act(async () => root.unmount()),
  };
}

const syncPolicyCalls = (fetchMock) =>
  fetchMock.mock.calls.filter(
    ([url, opts]) => url === '/api/admin/settings/sync' && opts && opts.method === 'PUT',
  );

describe('Settings sync policy card', () => {
  afterEach(() => {
    jest.restoreAllMocks();
  });

  test('an admin can change the policy via PUT /admin/settings/sync', async () => {
    const fetchImpl = mockFetchAdmin(true, 'ask');
    const { container, unmount } = await renderSettingsWith(fetchImpl);

    // The card is present for admins, with the loaded policy selected.
    expect(container.textContent).toContain('Sync conflicts');
    const fileWins = Array.from(container.querySelectorAll('input[name="sync_policy"]')).find(
      (i) => i.value === 'file_wins',
    );
    expect(fileWins).toBeTruthy();

    await act(async () => {
      fileWins.click();
    });
    await act(async () => {
      const save = Array.from(container.querySelectorAll('button')).find(
        (b) => b.textContent === 'Save Sync Policy',
      );
      save.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });

    const calls = syncPolicyCalls(fetchImpl);
    expect(calls).toHaveLength(1);
    expect(JSON.parse(calls[0][1].body)).toEqual({ policy: 'file_wins' });
    expect(container.textContent).toContain('Sync policy updated.');
    await unmount();
  });

  test('the card is hidden for non-admins', async () => {
    const fetchImpl = mockFetchAdmin(false, 'ask');
    const { container, unmount } = await renderSettingsWith(fetchImpl);

    // Give the (absent) card a tick to appear, then assert it is not there.
    await act(async () => {});
    expect(container.textContent).not.toContain('Sync conflicts');
    expect(syncPolicyCalls(fetchImpl)).toHaveLength(0);
    await unmount();
  });
});
