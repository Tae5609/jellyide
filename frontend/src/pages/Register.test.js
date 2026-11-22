import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom';
import { AuthProvider } from '../auth/AuthContext';
import Register from './Register';

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

const userBody = {
  id: '1',
  username: 'newbie',
  provider: 'local',
  is_admin: false,
  status: 'active',
};

// mockFetch routes:
//   registerStatus/status: the /api/auth/register answer
//   loggedIn: flipped by a successful register so /auth/me then reports a
//   session (open mode starts one server-side).
function mockFetch({ registration, register = { status: 201, body: { status: 'pending' } } }) {
  const state = { loggedIn: false };
  global.fetch = jest.fn(async (url) => {
    if (url === '/api/auth/providers') {
      return new Response(JSON.stringify(providersBody({ registration })), {
        status: 200,
      });
    }
    if (url === '/api/auth/me') {
      if (state.loggedIn) {
        return new Response(JSON.stringify(userBody), { status: 200 });
      }
      return new Response(JSON.stringify({ error: 'unauthorized' }), {
        status: 401,
      });
    }
    if (url === '/api/auth/register') {
      if (register.status === 201) state.loggedIn = register.body.status === 'active';
      return new Response(JSON.stringify(register.body), { status: register.status });
    }
    return new Response(null, { status: 500 });
  });
  return state;
}

async function renderRegister() {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <MemoryRouter initialEntries={['/register']}>
        <AuthProvider>
          <Routes>
            <Route path="/register" element={<Register />} />
            <Route path="/login" element={<span data-testid="loc">/login</span>} />
            <Route path="/downloads" element={<span data-testid="loc">/downloads</span>} />
          </Routes>
          <LocationProbe />
        </AuthProvider>
      </MemoryRouter>
    );
  });
  return {
    container,
    unmount: () => act(async () => root.unmount()),
  };
}

function LocationProbe() {
  return <span data-testid="location">{useLocation().pathname}</span>;
}

function setInput(container, name, value) {
  const input = container.querySelector(`input[name="${name}"]`);
  const setter = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    'value'
  ).set;
  setter.call(input, value);
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

async function submitForm(container) {
  const button = container.querySelector('button[type="submit"]');
  await act(async () => {
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  });
  await act(async () => {});
}

describe('Register page', () => {
  afterEach(() => {
    jest.restoreAllMocks();
  });

  test('redirects to /login when registration is admin_only', async () => {
    mockFetch({ registration: 'admin_only' });
    const { container, unmount } = await renderRegister();
    expect(container.querySelector('form')).toBeNull();
    expect(
      container.querySelector('[data-testid="loc"]').textContent
    ).toBe('/login');
    await unmount();
  });

  test('shows the approval message after registering in approval mode', async () => {
    mockFetch({ registration: 'approval' });
    const { container, unmount } = await renderRegister();
    expect(container.querySelector('form')).toBeTruthy();

    setInput(container, 'username', 'newbie');
    setInput(container, 'password', 'password-123');
    await submitForm(container);

    expect(container.textContent).toContain(
      'Your account is awaiting admin approval'
    );
    // No session: the app did not navigate in.
    expect(
      container.querySelector('[data-testid="location"]').textContent
    ).toBe('/register');
    await unmount();
  });

  test('logs in and navigates to /downloads in open mode', async () => {
    mockFetch({
      registration: 'open',
      register: { status: 201, body: { status: 'active' } },
    });
    const { container, unmount } = await renderRegister();

    setInput(container, 'username', 'newbie');
    setInput(container, 'password', 'password-123');
    await submitForm(container);

    expect(global.fetch).toHaveBeenCalledWith(
      '/api/auth/register',
      expect.objectContaining({ method: 'POST' })
    );
    // The open-mode session is loaded and the user lands in the app.
    expect(
      container.querySelector('[data-testid="loc"]').textContent
    ).toBe('/downloads');
    await unmount();
  });

  test('rejects a short password client-side', async () => {
    mockFetch({ registration: 'open' });
    const { container, unmount } = await renderRegister();

    setInput(container, 'username', 'newbie');
    setInput(container, 'password', 'short-1');
    await submitForm(container);

    expect(container.textContent).toContain(
      'Password must be at least 8 characters'
    );
    expect(global.fetch).not.toHaveBeenCalledWith(
      '/api/auth/register',
      expect.anything()
    );
    await unmount();
  });

  test('surfaces the server error for a taken username', async () => {
    mockFetch({
      registration: 'open',
      register: { status: 409, body: { error: 'username already taken' } },
    });
    const { container, unmount } = await renderRegister();

    setInput(container, 'username', 'newbie');
    setInput(container, 'password', 'password-123');
    await submitForm(container);

    expect(container.textContent).toContain('username already taken');
    await unmount();
  });
});
