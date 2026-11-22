import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { apiFetch } from '../api';
import { AuthProvider, useAuth } from './AuthContext';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

const providersBody = {
  enabled: true,
  local: true,
  oidc: { enabled: false, name: '' },
  saml: { enabled: false, name: '' },
};

const userBody = {
  id: '1',
  username: 'admin',
  provider: 'local',
  is_admin: true,
};

// Fetch mock keyed by URL; statuses can be flipped between calls to
// simulate an expiring session.
function mockFetch(routes) {
  return jest.fn(async (url) => {
    const status = routes[url] || 500;
    if (status === 200) {
      const body =
        url === '/api/auth/providers'
          ? providersBody
          : url === '/api/auth/me'
            ? userBody
            : {};
      return new Response(JSON.stringify(body), { status: 200 });
    }
    if (status === 401) {
      return new Response(JSON.stringify({ error: 'unauthorized' }), {
        status: 401,
      });
    }
    return new Response(null, { status });
  });
}

function Probe() {
  const { user, providers, loading } = useAuth();
  return (
    <div>
      <span data-testid="state">
        {loading ? 'loading' : user ? user.username : 'no-user'}
      </span>
      <span data-testid="providers">
        {providers ? (providers.enabled ? 'on' : 'off') : 'none'}
      </span>
    </div>
  );
}

async function renderProvider() {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <AuthProvider>
        <Probe />
      </AuthProvider>
    );
  });
  return {
    container,
    unmount: () => act(async () => root.unmount()),
  };
}

const stateText = (container) =>
  container.querySelector('[data-testid="state"]').textContent;

describe('AuthProvider session handling', () => {
  afterEach(() => {
    jest.restoreAllMocks();
  });

  test('a 401 with a still-valid session (wrong current password) keeps the session', async () => {
    global.fetch = mockFetch({
      '/api/auth/providers': 200,
      '/api/auth/me': 200,
      '/api/auth/password': 401,
    });
    const { container, unmount } = await renderProvider();
    expect(stateText(container)).toBe('admin');

    // Simulate what Settings does on a failed password change: a 401 from
    // a protected POST while the session cookie is still valid.
    await act(async () => {
      await apiFetch('/auth/password', {
        method: 'POST',
        body: JSON.stringify({
          current_password: 'wrong',
          new_password: 'whatever123',
        }),
      });
    });
    await act(async () => {});

    // The /auth/me revalidation returns 200, so the session must survive.
    expect(stateText(container)).toBe('admin');
    await unmount();
  });

  test('a 401 confirmed by /auth/me clears the session', async () => {
    const routes = {
      '/api/auth/providers': 200,
      '/api/auth/me': 200,
      '/api/jobs': 401,
    };
    global.fetch = mockFetch(routes);
    const { container, unmount } = await renderProvider();
    expect(stateText(container)).toBe('admin');

    // The session expires server-side.
    routes['/api/auth/me'] = 401;
    await act(async () => {
      await apiFetch('/jobs');
    });
    await act(async () => {});

    expect(stateText(container)).toBe('no-user');
    await unmount();
  });

  test('a failed /auth/providers request falls back to disabled providers, not an endless loader', async () => {
    global.fetch = mockFetch({
      '/api/auth/providers': 500,
      '/api/auth/me': 401,
    });
    const { container, unmount } = await renderProvider();

    // Loading finished, providers is the fallback (enabled: false) and
    // the (absent) session is reported, so the login page renders
    // instead of spinning forever.
    expect(stateText(container)).toBe('no-user');
    expect(
      container.querySelector('[data-testid="providers"]').textContent
    ).toBe('off');
    await unmount();
  });
});
