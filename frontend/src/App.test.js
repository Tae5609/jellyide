import React from 'react';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App from './App';
import { ModalProvider } from './components/ModalContext';
import { mockApi, jsonResponse, PROVIDERS_LOCAL, ADMIN } from './testSetup/mockApi';

const BASE_ROUTES = {
  'GET /api/auth/providers': PROVIDERS_LOCAL,
  'GET /api/auth/me': ADMIN,
  'GET /api/sync/summary': { open_conflicts: 2, missing_tracks: 1 },
  'GET /api/admin/users': [],
  'GET /api/library/artists': [],
  'GET /api/jobs': [],
  'GET /api/config': { concurrent_limit: 3 },
  'GET /api/admin/settings': { registration: 'admin_only' },
  'GET /api/admin/settings/sync': { enabled: false },
  'GET /api/admin/ytdlp': { version: '2025.1.1', managed: false },
  'GET /api/sync/conflicts?status=open': [],
  'GET /api/sync/missing': [],
  'GET /api/sync/trash': [],
  'GET /api/library/albums/2': { id: 2, title: 'Kind of Blue', artist: 'Miles Davis' },
  'GET /api/library/albums/2/tracks': [],
  'GET /api/metadata/analyze/2': {},
  'GET /api/library/artists/1/albums': [],
};

function openApp(path, routes = {}) {
  window.history.pushState({}, '', path);
  const fetchMock = mockApi({ ...BASE_ROUTES, ...routes });
  render(
    <ModalProvider>
      <App />
    </ModalProvider>,
  );
  return fetchMock;
}

afterEach(() => {
  window.history.pushState({}, '', '/');
});

describe('route guard', () => {
  test('anonymous users are sent to /login', async () => {
    openApp('/jobs', { 'GET /api/auth/me': () => jsonResponse({ error: 'no' }, 401) });
    expect(await screen.findByLabelText('Username')).toBeInTheDocument();
    expect(window.location.pathname).toBe('/login');
  });

  test('a pending first-run setup redirects protected routes to /setup', async () => {
    openApp('/downloads', {
      'GET /api/auth/providers': { ...PROVIDERS_LOCAL, setup_required: true },
      'GET /api/auth/me': () => jsonResponse({ error: 'no' }, 401),
    });
    await waitFor(() => expect(window.location.pathname).toBe('/setup'));
  });

  test('auth disabled skips the login check entirely', async () => {
    openApp('/downloads', {
      'GET /api/auth/providers': { enabled: false },
      'GET /api/auth/me': () => jsonResponse({}, 401),
    });
    expect(await screen.findByPlaceholderText(/Paste YouTube/)).toBeInTheDocument();
    expect(window.location.pathname).toBe('/downloads');
  });

  test('a signed-in user hitting /login is sent to Downloads', async () => {
    openApp('/login');
    expect(await screen.findByPlaceholderText(/Paste YouTube/)).toBeInTheDocument();
  });

  test('non-admins are bounced from the admin Users route', async () => {
    openApp('/admin/users', {
      'GET /api/auth/me': { id: 5, username: 'u', is_admin: false },
    });
    await waitFor(() => expect(window.location.pathname).toBe('/downloads'));
  });

  test('/ redirects to Downloads', async () => {
    openApp('/');
    await screen.findByPlaceholderText(/Paste YouTube/);
    expect(window.location.pathname).toBe('/downloads');
  });
});

describe('mobile drawer', () => {
  const sidebar = () => screen.getByRole('complementary');

  test('opens from the menu button and closes on Escape', async () => {
    openApp('/downloads');
    await screen.findByPlaceholderText(/Paste YouTube/);
    expect(sidebar()).not.toHaveClass('open');
    await userEvent.setup().click(screen.getByRole('button', { name: 'Open menu' }));
    expect(sidebar()).toHaveClass('open');
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(sidebar()).not.toHaveClass('open');
  });

  test('closes when a nav link is followed', async () => {
    openApp('/downloads');
    await screen.findByPlaceholderText(/Paste YouTube/);
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Open menu' }));
    expect(sidebar()).toHaveClass('open');
    await user.click(screen.getByRole('link', { name: /Jobs/ }));
    await waitFor(() => expect(sidebar()).not.toHaveClass('open'));
    expect(window.location.pathname).toBe('/jobs');
  });

  test('closes when the backdrop is clicked', async () => {
    openApp('/downloads');
    await screen.findByPlaceholderText(/Paste YouTube/);
    await userEvent.setup().click(screen.getByRole('button', { name: 'Open menu' }));
    const backdrop = document.querySelector('.sidebar-backdrop');
    fireEvent.click(backdrop);
    expect(sidebar()).not.toHaveClass('open');
  });
});

describe('shell', () => {
  test('shows the user, the sync badge, and the admin Users link', async () => {
    openApp('/downloads');
    expect(await screen.findByText('root')).toBeInTheDocument();
    expect(await screen.findByText('3')).toHaveClass('nav-badge');
    expect(await screen.findByRole('link', { name: /Users/ })).toBeInTheDocument();
  });

  test('"g" then a key navigates', async () => {
    openApp('/downloads');
    await screen.findByPlaceholderText(/Paste YouTube/);
    fireEvent.keyDown(window, { key: 'g' });
    fireEvent.keyDown(window, { key: 'j' });
    await waitFor(() => expect(window.location.pathname).toBe('/jobs'));
  });

  test('sign out posts /auth/logout and returns to login', async () => {
    const fetchMock = openApp('/downloads', { 'POST /api/auth/logout': { ok: true } });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /sign out|log ?out/i }));
    await waitFor(() => expect(window.location.pathname).toBe('/login'));
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/auth/logout',
      expect.objectContaining({ method: 'POST' }),
    );
  });
});

// Every page must at least mount against a well-formed backend and show its
// heading, so a broken import or a crashing first render fails CI.
describe('page smoke renders', () => {
  test.each([
    ['/downloads', () => screen.findByRole('heading', { name: 'Downloads' })],
    ['/library', () => screen.findByRole('heading', { name: 'Artists' })],
    ['/jobs', () => screen.findByRole('heading', { name: 'Background Jobs' })],
    ['/library/1/2', async () => (await screen.findAllByText('Kind of Blue'))[0]],
    ['/sync', () => screen.findByRole('heading', { name: /conflicts|sync/i })],
    ['/settings', () => screen.findByRole('heading', { name: 'Settings' })],
    ['/admin/users', () => screen.findByRole('heading', { name: 'Users' })],
  ])('%s', async (path, expectHeading) => {
    const errors = jest.spyOn(console, 'error').mockImplementation(() => {});
    openApp(path);
    expect(await expectHeading()).toBeInTheDocument();
    expect(errors).not.toHaveBeenCalled();
    errors.mockRestore();
  });
});
