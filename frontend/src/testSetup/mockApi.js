// Test helper: installs a global fetch mock that answers by "METHOD /path".
//
// routes maps "GET /api/auth/me" (the method is optional and defaults to
// GET) to either a plain value (served as a 200 JSON body), a Response, or
// a function (url, opts) returning either. Unmatched requests answer 404 so
// a page never hangs on an unexpected call.
export function jsonResponse(body, status = 200) {
  return new Response(JSON.stringify(body), { status });
}

export function mockApi(routes) {
  const fetchMock = jest.fn(async (url, opts = {}) => {
    const method = (opts.method || 'GET').toUpperCase();
    const key = `${method} ${url}`;
    const handler = key in routes ? routes[key] : routes[url];
    if (handler === undefined) return jsonResponse({ error: `unmocked ${key}` }, 404);
    const out = typeof handler === 'function' ? await handler(url, opts) : handler;
    return out instanceof Response ? out : jsonResponse(out);
  });
  global.fetch = fetchMock;
  return fetchMock;
}

export const callsTo = (fetchMock, method, url) =>
  fetchMock.mock.calls.filter(([u, o]) => u === url && ((o && o.method) || 'GET') === method);

export const PROVIDERS_LOCAL = {
  enabled: true,
  local: true,
  registration: 'admin_only',
  oidc: { enabled: false, name: '' },
  saml: { enabled: false, name: '' },
};

export const ADMIN = { id: 1, username: 'root', is_admin: true, status: 'active' };
