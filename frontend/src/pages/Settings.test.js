import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { ModalProvider } from '../components/ModalContext';
import { AuthProvider } from '../auth/AuthContext';
import Settings from './Settings';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

// Mock fetch for auth and settings.
function mockFetch({ isAdmin = true } = {}) {
  return jest.fn(async (url, opts = {}) => {
    if (url === '/api/auth/providers') {
      return new Response(
        JSON.stringify({
          enabled: true,
          local: true,
          oidc: { enabled: false, name: '' },
          saml: { enabled: false, name: '' },
        }),
        { status: 200 }
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
        { status: 200 }
      );
    }
    if (url === '/api/config') {
      if (opts.method === 'POST') {
        return new Response(JSON.stringify({}), { status: 200 });
      }
      return new Response(
        JSON.stringify({ concurrent_limit: 2, proxy: '' }),
        { status: 200 }
      );
    }
    if (url === '/api/admin/ytdlp') {
      return new Response(
        JSON.stringify({
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
        }),
        { status: 200 }
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
        { status: 200 }
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
      </ModalProvider>
    );
  });
  return {
    fetchImpl,
    container,
    unmount: () => act(async () => root.unmount()),
  };
}

const limitInput = (container) =>
  container.querySelector('input[type="number"]');

const saveButton = (container) =>
  Array.from(container.querySelectorAll('button')).find(
    (b) => b.textContent === 'Save Configuration'
  );

function setLimit(container, value) {
  act(() => {
    const input = limitInput(container);
    const setter = Object.getOwnPropertyDescriptor(
      window.HTMLInputElement.prototype,
      'value'
    ).set;
    setter.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

async function clickSave(container) {
  await act(async () => {
    saveButton(container).dispatchEvent(
      new MouseEvent('click', { bubbles: true })
    );
  });
}

const postConfigCalls = (fetchMock) =>
  fetchMock.mock.calls.filter(
    ([url, opts]) => url === '/api/config' && opts && opts.method === 'POST'
  );

describe('Settings concurrent limit validation', () => {
  afterEach(() => {
    jest.restoreAllMocks();
  });

  test('empty limit: inline error, previous value kept, no POST', async () => {
    const { fetchImpl, container, unmount } = await renderSettings();

    setLimit(container, '');
    await clickSave(container);

    expect(container.textContent).toContain(
      'Enter a positive whole number'
    );
    expect(postConfigCalls(fetchImpl)).toHaveLength(0);
    // The previous value (loaded from /config) is restored, not NaN/null.
    expect(limitInput(container).value).toBe('2');
    await unmount();
  });

  test('non-integer limit: inline error and no POST', async () => {
    const { fetchImpl, container, unmount } = await renderSettings();

    setLimit(container, '1.5');
    await clickSave(container);
    expect(container.textContent).toContain(
      'Enter a positive whole number'
    );
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
    expect(container.querySelector('.modal-title').textContent).toBe(
      'Settings Saved'
    );
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
      b => b.textContent === 'Check for updates'
    );
    expect(checkBtn).toBeDefined();

    await act(async () => {
      checkBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });

    const checkCalls = fetchImpl.mock.calls.filter(
      ([url, opts]) => url === '/api/admin/ytdlp/check' && opts && opts.method === 'POST'
    );
    expect(checkCalls).toHaveLength(1);

    await unmount();
  });
});
