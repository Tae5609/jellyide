// Jest setup (picked up automatically by react-scripts). Adds the
// @testing-library/jest-dom matchers (toBeInTheDocument, ...).
import '@testing-library/jest-dom';

// Fail any test that triggers a React "not wrapped in act(...)" warning.
// Those warnings were once ignored as CI noise (BUGS.md: Register and
// Settings tests) and masked real problems; keep the suite free of them.
const realConsoleError = console.error;
beforeEach(() => {
  jest.spyOn(console, 'error').mockImplementation((...args) => {
    if (typeof args[0] === 'string' && args[0].includes('not wrapped in act(')) {
      throw new Error(`act() warning: ${args[0]}`);
    }
    realConsoleError(...args);
  });
});
