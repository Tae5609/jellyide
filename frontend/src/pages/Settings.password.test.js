import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { AuthProvider } from '../auth/AuthContext';
import { ModalProvider } from '../components/ModalContext';
import Settings from './Settings';
import { mockApi, callsTo, jsonResponse, PROVIDERS_LOCAL } from '../testSetup/mockApi';

function setup(extra = {}) {
  const api = mockApi({
    'GET /api/auth/providers': PROVIDERS_LOCAL,
    'GET /api/auth/me': { id: 4, username: 'bob', provider: 'local', is_admin: false },
    'GET /api/config': { concurrent_limit: 3 },
    ...extra,
  });
  render(
    <MemoryRouter>
      <AuthProvider>
        <ModalProvider>
          <Settings />
        </ModalProvider>
      </AuthProvider>
    </MemoryRouter>,
  );
  return api;
}

async function fill(user, current, next, confirm) {
  if (current) await user.type(await screen.findByLabelText('Current password'), current);
  else await screen.findByLabelText('Current password');
  if (next) await user.type(screen.getByLabelText('New password'), next);
  if (confirm) await user.type(screen.getByLabelText('Confirm new password'), confirm);
  await user.click(screen.getByRole('button', { name: 'Change Password' }));
}

describe('Settings change-password validation', () => {
  test.each([
    [
      'missing current password',
      ['', 'longenough1', 'longenough1'],
      'Enter your current password.',
    ],
    [
      'short new password',
      ['old', 'short', 'short'],
      'New password must be at least 8 characters.',
    ],
    [
      'mismatched confirmation',
      ['old', 'longenough1', 'longenough2'],
      'New passwords do not match.',
    ],
  ])('%s is rejected client-side without a request', async (_name, args, message) => {
    const api = setup();
    await fill(userEvent.setup(), ...args);
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(callsTo(api, 'POST', '/api/auth/password')).toHaveLength(0);
  });

  test('a valid change posts both passwords and confirms', async () => {
    const api = setup({ 'POST /api/auth/password': { ok: true } });
    await fill(userEvent.setup(), 'old-pass', 'longenough1', 'longenough1');
    expect(await screen.findByText('Password changed successfully.')).toBeInTheDocument();
    const [, opts] = callsTo(api, 'POST', '/api/auth/password')[0];
    expect(JSON.parse(opts.body)).toEqual({
      current_password: 'old-pass',
      new_password: 'longenough1',
    });
    expect(screen.getByLabelText('Current password')).toHaveValue('');
  });

  test('a wrong current password shows the server error and keeps the session', async () => {
    setup({ 'POST /api/auth/password': jsonResponse({ error: 'current password is wrong' }, 401) });
    await fill(userEvent.setup(), 'bad', 'longenough1', 'longenough1');
    expect(await screen.findByText('current password is wrong')).toBeInTheDocument();
    // The 401 triggers a /auth/me revalidation, which still succeeds.
    await screen.findByLabelText('Current password');
  });
});
