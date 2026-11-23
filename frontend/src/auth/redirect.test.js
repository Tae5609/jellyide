import { safeRedirectFrom } from './redirect';

describe('safeRedirectFrom', () => {
  test.each([
    ['//evil.com'],
    ['//'],
    ['https://evil.com'],
    ['http:evil.com'],
    ['https:evil'],
    // eslint-disable-next-line no-script-url -- the open-redirect guard must reject this payload
    ['javascript:alert(1)'],
    [''],
    [null],
    [undefined],
    [123],
  ])('rejects external or invalid target %p', (from) => {
    expect(safeRedirectFrom(from)).toBe('/downloads');
  });

  test.each([['/'], ['/downloads'], ['/settings'], ['/library/12/34']])(
    'accepts internal path %p',
    (from) => {
      expect(safeRedirectFrom(from)).toBe(from);
    },
  );

  test('uses the custom fallback for rejected targets', () => {
    expect(safeRedirectFrom('//evil.com', '/jobs')).toBe('/jobs');
    expect(safeRedirectFrom('https:evil', '/jobs')).toBe('/jobs');
  });

  test('keeps internal targets even with a custom fallback', () => {
    expect(safeRedirectFrom('/settings', '/jobs')).toBe('/settings');
  });
});
