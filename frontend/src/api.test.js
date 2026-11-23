import { apiFetch } from './api';

describe('apiFetch', () => {
  let fetchMock;
  let listener;

  beforeEach(() => {
    fetchMock = jest.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    global.fetch = fetchMock;
    listener = jest.fn();
    window.addEventListener('auth:unauthorized', listener);
  });

  afterEach(() => {
    window.removeEventListener('auth:unauthorized', listener);
  });

  test('prefixes /api to paths that do not start with /api', async () => {
    await apiFetch('/jobs');
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/jobs',
      expect.objectContaining({ credentials: 'same-origin' }),
    );
  });

  test('leaves paths that already start with /api untouched', async () => {
    await apiFetch('/api/jobs');
    expect(fetchMock).toHaveBeenCalledWith('/api/jobs', expect.anything());
  });

  test('sends X-Requested-With and sets JSON content type for string bodies', async () => {
    await apiFetch('/queue', { method: 'POST', body: JSON.stringify([]) });
    const [, init] = fetchMock.mock.calls[0];
    expect(init.headers['X-Requested-With']).toBe('XMLHttpRequest');
    expect(init.headers['Content-Type']).toBe('application/json');
    expect(init.body).toBe(JSON.stringify([]));
  });

  test('does not set a JSON content type when there is no body', async () => {
    await apiFetch('/jobs');
    const [, init] = fetchMock.mock.calls[0];
    expect(init.headers['Content-Type']).toBeUndefined();
  });

  test('dispatches auth:unauthorized on 401 for protected paths', async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 401 }));
    await apiFetch('/jobs');
    expect(listener).toHaveBeenCalledTimes(1);
  });

  test('does not dispatch auth:unauthorized for /api/auth/me or /api/auth/login', async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 401 }));
    await apiFetch('/auth/me');
    await apiFetch('/auth/login', { method: 'POST', body: '{}' });
    expect(listener).not.toHaveBeenCalled();
  });

  test('returns the response to the caller', async () => {
    const res = new Response('{"ok":true}', { status: 200 });
    fetchMock.mockResolvedValueOnce(res);
    const out = await apiFetch('/config');
    expect(out).toBe(res);
  });
});
