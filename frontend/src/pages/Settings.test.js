import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { ModalProvider } from '../components/ModalContext';
import { AuthProvider } from '../auth/AuthContext';
import Settings from './Settings';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

// Auth is enabled with a non-local, non-admin user, so Settings renders
// only the base configuration card (no registration or password card).
function mockFetch() {
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
          is_admin: false,
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
    return new Response(null, { status: 500 });
  });
}

async function renderSettings() {
  const fetchImpl = mockFetch();
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
  const input = limitInput(container);
  const setter = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    'value'
  ).set;
  setter.call(input, value);
  input.dispatchEvent(new Event('input', { bubbles: true }));
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
