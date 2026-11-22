// Thin wrapper around fetch for the JellyIDE backend.
//
// - Prefixes /api to paths that do not already start with /api.
// - Always sends the session cookie (credentials) and the
//   X-Requested-With header the backend requires on protected requests.
// - Sets Content-Type: application/json when the body is a string.
// - Signals an expired/missing session with a window 'auth:unauthorized'
//   event for every 401 except the auth endpoints themselves.
//
// Returns the Response; callers keep their own .json() handling.
export async function apiFetch(path, opts = {}) {
  const url =
    path.startsWith('/') && !path.startsWith('/api') ? `/api${path}` : path;
  const headers = {
    'X-Requested-With': 'XMLHttpRequest',
    ...(opts.headers || {}),
  };
  if (typeof opts.body === 'string') {
    headers['Content-Type'] = 'application/json';
  }
  const res = await fetch(url, {
    credentials: 'same-origin',
    ...opts,
    headers,
  });
  if (
    res.status === 401 &&
    url !== '/api/auth/me' &&
    url !== '/api/auth/login'
  ) {
    window.dispatchEvent(new Event('auth:unauthorized'));
  }
  return res;
}
