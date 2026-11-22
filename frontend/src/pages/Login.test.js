import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { AuthProvider } from '../auth/AuthContext';
import Login from './Login';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

function providersBody(overrides = {}) {
  return {
    enabled: true,
    local: true,
    oidc: { enabled: false, name: '' },
    saml: { enabled: false, name: '' },
    ...overrides,
  };
}

function mockFetch(providers) {
  global.fetch = jest.fn(async (url) => {
    if (url === '/api/auth/providers') {
      return new Response(JSON.stringify(providers), { status: 200 });
    }
    if (url === '/api/auth/me') {
      return new Response(JSON.stringify({ error: 'unauthorized' }), {
        status: 401,
      });
    }
    return new Response(null, { status: 500 });
  });
}

async function renderLogin(providers) {
  mockFetch(providers);
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <MemoryRouter>
        <AuthProvider>
          <Login />
        </AuthProvider>
      </MemoryRouter>
    );
  });
  return {
    container,
    unmount: () => act(async () => root.unmount()),
  };
}

const createAccountLink = (container) =>
  Array.from(container.querySelectorAll('a')).find(
    (a) => a.textContent === 'Create account'
  );

describe('Login create-account link', () => {
  afterEach(() => {
    jest.restoreAllMocks();
  });

  test('shows the link when registration is open', async () => {
    const { container, unmount } = await renderLogin(
      providersBody({ registration: 'open' })
    );
    const link = createAccountLink(container);
    expect(link).toBeTruthy();
    expect(link.getAttribute('href')).toBe('/register');
    await unmount();
  });

  test('shows the link when registration is approval', async () => {
    const { container, unmount } = await renderLogin(
      providersBody({ registration: 'approval' })
    );
    expect(createAccountLink(container)).toBeTruthy();
    await unmount();
  });

  test('hides the link in admin_only mode', async () => {
    const { container, unmount } = await renderLogin(
      providersBody({ registration: 'admin_only' })
    );
    expect(createAccountLink(container)).toBeUndefined();
    await unmount();
  });
});
