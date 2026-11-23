import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { AuthProvider } from '../auth/AuthContext';
import Login from './Login';
import { mockApi, callsTo, PROVIDERS_LOCAL, jsonResponse } from '../testSetup/mockApi';

function Where() {
  const loc = useLocation();
  return <div data-testid="where">{loc.pathname}</div>;
}

function setupLogin({ entry = '/login', providers = PROVIDERS_LOCAL, extra = {} } = {}) {
  const fetchMock = mockApi({
    'GET /api/auth/providers': providers,
    'GET /api/auth/me': () => jsonResponse({ error: 'unauthorized' }, 401),
    'GET /api/auth/csrf': { token: 'csrf-1' },
    ...extra,
  });
  render(
    <MemoryRouter initialEntries={[entry]}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="*" element={<Where />} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
  return fetchMock;
}

describe('Login SSO and error banner', () => {
  test('shows OIDC and SAML buttons with the configured provider names', async () => {
    setupLogin({
      providers: {
        ...PROVIDERS_LOCAL,
        oidc: { enabled: true, name: 'Keycloak' },
        saml: { enabled: true, name: 'Okta' },
      },
    });
    const oidc = await screen.findByRole('link', { name: 'Continue with Keycloak' });
    expect(oidc).toHaveAttribute('href', '/api/auth/oidc/login');
    expect(screen.getByRole('link', { name: 'Continue with Okta' })).toHaveAttribute(
      'href',
      '/api/auth/saml/login',
    );
    expect(screen.getByLabelText('Username')).toBeInTheDocument();
  });

  test('SSO-only setup hides the local form', async () => {
    setupLogin({
      providers: { ...PROVIDERS_LOCAL, local: false, oidc: { enabled: true, name: 'Corp' } },
    });
    await screen.findByRole('link', { name: 'Continue with Corp' });
    expect(screen.queryByLabelText('Username')).not.toBeInTheDocument();
  });

  test('with no sign-in method enabled it says so', async () => {
    setupLogin({ providers: { ...PROVIDERS_LOCAL, local: false } });
    expect(await screen.findByText('No sign-in methods are enabled.')).toBeInTheDocument();
  });

  test('renders the ?error= query parameter as an alert', async () => {
    setupLogin({ entry: '/login?error=SSO%20login%20failed' });
    expect(await screen.findByRole('alert')).toHaveTextContent('SSO login failed');
  });
});

describe('Login submit', () => {
  test('a failed login shows the server message and stays on the page', async () => {
    setupLogin({
      extra: { 'POST /api/auth/login': jsonResponse({ error: 'invalid credentials' }, 401) },
    });
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('Username'), 'bob');
    await user.type(screen.getByLabelText('Password'), 'wrong');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('invalid credentials');
    expect(screen.queryByTestId('where')).not.toBeInTheDocument();
  });

  test('success navigates to the remembered internal path', async () => {
    const api = setupLogin({
      entry: { pathname: '/login', state: { from: '/library/3' } },
      extra: { 'POST /api/auth/login': { id: 2, username: 'bob', is_admin: false } },
    });
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('Username'), 'bob');
    await user.type(screen.getByLabelText('Password'), 'hunter22');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/library/3'));
    const [post] = callsTo(api, 'POST', '/api/auth/login');
    expect(JSON.parse(post[1].body)).toEqual({
      username: 'bob',
      password: 'hunter22',
      csrf_token: 'csrf-1',
    });
  });

  test.each(['//evil.com', 'https://evil.com/x'])(
    'success ignores the external redirect target %s',
    async (from) => {
      setupLogin({
        entry: { pathname: '/login', state: { from } },
        extra: { 'POST /api/auth/login': { id: 2, username: 'bob', is_admin: false } },
      });
      const user = userEvent.setup();
      await user.type(await screen.findByLabelText('Username'), 'bob');
      await user.type(screen.getByLabelText('Password'), 'hunter22');
      await user.click(screen.getByRole('button', { name: 'Sign in' }));
      await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/downloads'));
    },
  );
});
