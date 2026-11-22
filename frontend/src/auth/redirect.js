// Sanitises the post-login redirect target. Only internal paths are
// accepted: a string starting with a single "/" (a leading "//" would be
// a protocol-relative URL, and anything else may be an absolute URL or
// junk). Everything else falls back to the given path.
export function safeRedirectFrom(from, fallback = '/downloads') {
  if (
    typeof from === 'string' &&
    from.startsWith('/') &&
    !from.startsWith('//')
  ) {
    return from;
  }
  return fallback;
}
