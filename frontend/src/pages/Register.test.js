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
  const state = { loggedIn: false, calls: [] };
  global.fetch = jest.fn(async (url, opts) => {
    state.calls.push({ url, body: opts && opts.body });
    if (url === '/api/auth/csrf') {
      return new Response(JSON.stringify({ token: 'csrf-123' }), {
        status: 200,
      });
    }
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

async function setInput(container, name, value) {
  const input = container.querySelector(`input[name="${name}"]`);
  const setter = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    'value'
  ).set;
  await act(async () => {
    setter.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
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

    await setInput(container, 'username', 'newbie');
    await setInput(container, 'password', 'password-123');
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

    await setInput(container, 'username', 'newbie');
    await setInput(container, 'password', 'password-123');
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

  // The login-CSRF wiring: submit must fetch the token from /auth/csrf
  // and send it in the register body, so the backend's double-submit
  // check passes (same house pattern as AuthContext's login).
  test('register sends the login-CSRF token from /auth/csrf', async () => {
    const state = mockFetch({
      registration: 'open',
      register: { status: 201, body: { status: 'active' } },
    });
    const { container, unmount } = await renderRegister();

    await setInput(container, 'username', 'newbie');
    await setInput(container, 'password', 'password-123');
    await submitForm(container);

    expect(state.calls.some((c) => c.url === '/api/auth/csrf')).toBe(true);
    const registerCall = state.calls.find((c) => c.url === '/api/auth/register');
    expect(registerCall).toBeTruthy();
    const sent = JSON.parse(registerCall.body);
    expect(sent.csrf_token).toBe('csrf-123');
    expect(sent.username).toBe('newbie');
    await unmount();
  });

  test('rejects a short password client-side', async () => {
    mockFetch({ registration: 'open' });
    const { container, unmount } = await renderRegister();

    await setInput(container, 'username', 'newbie');
    await setInput(container, 'password', 'short-1');
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

    await setInput(container, 'username', 'newbie');
    await setInput(container, 'password', 'password-123');
    await submitForm(container);

    expect(container.textContent).toContain('username already taken');
    await unmount();
  });

  // Regression (BUGS.md, Register.test.js setInput): the bare `input`
  // dispatch used to run outside act, so every state-changing setInput
  // logged an "An update to Register inside a test was not wrapped in
  // act(...)" warning through console.error. Fail if one ever returns.
  test('input updates log no act warnings', async () => {
    mockFetch({ registration: 'open' });
    const { container, unmount } = await renderRegister();
    const errors = [];
    const originalError = console.error;
    console.error = (...args) => errors.push(args.map(String).join(' '));
    try {
      await setInput(container, 'username', 'newbie');
      await setInput(container, 'password', 'password-123');
      // Let React flush any update it scheduled outside act, so an
      // escaped-update warning would land in the capture before the
      // assertion runs.
      await new Promise((resolve) => setTimeout(resolve, 0));
      await act(async () => {});
      const actWarnings = errors.filter((line) =>
        line.includes('not wrapped in act')
      );
      expect(actWarnings).toEqual([]);
    } finally {
      console.error = originalError;
      await unmount();
    }
  });
});
