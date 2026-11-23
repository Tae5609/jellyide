import React from 'react';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { AuthProvider } from '../auth/AuthContext';
import { ModalProvider } from '../components/ModalContext';
import Users from './Users';
import { mockApi, callsTo, jsonResponse, PROVIDERS_LOCAL, ADMIN } from '../testSetup/mockApi';

const USERS = [
  {
    id: 1,
    username: 'root',
    email: 'root@x.io',
    provider: 'local',
    status: 'active',
    is_admin: true,
  },
  { id: 2, username: 'pat', provider: 'local', status: 'pending', is_admin: false },
  { id: 3, username: 'dee', provider: 'oidc', status: 'disabled', is_admin: false },
];

function setup(extra = {}) {
  const fetchMock = mockApi({
    'GET /api/auth/providers': PROVIDERS_LOCAL,
    'GET /api/auth/me': ADMIN,
    'GET /api/admin/users': USERS,
    ...extra,
  });
  render(
    <MemoryRouter>
      <AuthProvider>
        <ModalProvider>
          <Users />
        </ModalProvider>
      </AuthProvider>
    </MemoryRouter>,
  );
  return fetchMock;
}

// Each user renders twice (table row + mobile card); scope to the table.
const row = async (name) => {
  const cell = await screen.findByText(name, { selector: '.user-username' });
  return cell.closest('tr');
};

describe('Users admin page', () => {
  test('lists users, marks self, and offers actions per status', async () => {
    setup();
    const root = await row('root');
    expect(within(root).getByText('(you)')).toBeInTheDocument();
    // You cannot delete yourself.
    expect(within(root).queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
    expect(within(root).getByRole('button', { name: 'Remove admin' })).toBeInTheDocument();

    const pat = await row('pat');
    expect(within(pat).getByRole('button', { name: 'Approve' })).toBeInTheDocument();
    expect(within(pat).getByRole('button', { name: 'Make admin' })).toBeInTheDocument();

    const dee = await row('dee');
    expect(within(dee).getByRole('button', { name: 'Enable' })).toBeInTheDocument();
    // SSO accounts have no local password to reset.
    expect(within(dee).queryByRole('button', { name: 'Reset password' })).not.toBeInTheDocument();
  });

  test('approve PATCHes status active and reloads the list', async () => {
    const fetchMock = setup({ 'PATCH /api/admin/users/2': { ok: true } });
    const user = userEvent.setup();
    await user.click(within(await row('pat')).getByRole('button', { name: 'Approve' }));
    await waitFor(() => expect(callsTo(fetchMock, 'PATCH', '/api/admin/users/2')).toHaveLength(1));
    const [, opts] = callsTo(fetchMock, 'PATCH', '/api/admin/users/2')[0];
    expect(JSON.parse(opts.body)).toEqual({ status: 'active' });
    await waitFor(() => expect(callsTo(fetchMock, 'GET', '/api/admin/users')).toHaveLength(2));
  });

  test('disable asks for confirmation first', async () => {
    const fetchMock = setup({ 'PATCH /api/admin/users/1': { ok: true } });
    const user = userEvent.setup();
    await user.click(within(await row('root')).getByRole('button', { name: 'Disable' }));
    expect(await screen.findByText('Disable user')).toBeInTheDocument();
    expect(callsTo(fetchMock, 'PATCH', '/api/admin/users/1')).toHaveLength(0);
    await user.click(screen.getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(callsTo(fetchMock, 'PATCH', '/api/admin/users/1')).toHaveLength(1));
  });

  test('the last-admin error from the server is shown in an alert', async () => {
    setup({
      'PATCH /api/admin/users/1': jsonResponse({ error: 'cannot remove the last admin' }, 409),
    });
    const user = userEvent.setup();
    await user.click(within(await row('root')).getByRole('button', { name: 'Remove admin' }));
    await user.click(await screen.findByRole('button', { name: 'Confirm' }));
    expect(await screen.findByText('cannot remove the last admin')).toBeInTheDocument();
  });

  test('delete confirms, then DELETEs and reloads', async () => {
    const fetchMock = setup({ 'DELETE /api/admin/users/3': { ok: true } });
    const user = userEvent.setup();
    await user.click(within(await row('dee')).getByRole('button', { name: 'Delete' }));
    await user.click(await screen.findByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(callsTo(fetchMock, 'DELETE', '/api/admin/users/3')).toHaveLength(1));
  });

  test('create user posts the form and reloads', async () => {
    const fetchMock = setup({ 'POST /api/admin/users': { id: 9 } });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Create user' }));
    await user.type(await screen.findByLabelText('Username'), 'newbie');
    await user.type(screen.getByLabelText('Password'), 'longenough1');
    const buttons = screen.getAllByRole('button', { name: 'Create user' });
    await user.click(buttons[buttons.length - 1]);
    await waitFor(() => expect(callsTo(fetchMock, 'POST', '/api/admin/users')).toHaveLength(1));
    const body = JSON.parse(callsTo(fetchMock, 'POST', '/api/admin/users')[0][1].body);
    expect(body.username).toBe('newbie');
    expect(body.password).toBe('longenough1');
  });

  test('shows the load error when the list request fails', async () => {
    setup({ 'GET /api/admin/users': jsonResponse({ error: 'forbidden' }, 403) });
    expect(await screen.findByText('forbidden')).toBeInTheDocument();
  });

  test('shows an empty state without users', async () => {
    setup({ 'GET /api/admin/users': [] });
    expect(await screen.findByText('No users')).toBeInTheDocument();
  });
});
