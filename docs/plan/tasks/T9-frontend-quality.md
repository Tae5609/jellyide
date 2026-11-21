# T9: Frontend lint, format and test suite

Branch `test/frontend-quality`. Read `docs/plan/CONVENTIONS.md`, `frontend/package.json`, all of `frontend/src`, and `.github/workflows/ci.yml`.

## Do
1. **ESLint.** Add an explicit config extending `react-app` and `react-app/jest`, with `react-hooks/exhaustive-deps: error`, `no-unused-vars: error` and `eqeqeq: error`. Script: `"lint": "eslint src --max-warnings=0"`. Fix every finding. Fix real hook-dependency bugs properly rather than silencing them.
2. **Prettier.** Add `.prettierrc` (`singleQuote: true`, `semi: true`, `printWidth: 100`, `trailingComma: "all"`), `.prettierignore` (`build`, `node_modules`) and `eslint-config-prettier`. Scripts: `"format": "prettier --write src"` and `"format:check": "prettier --check src"`. Run format once in its own commit: `style(ui): apply prettier formatting`.
3. **Tests** with `@testing-library/react`, `@testing-library/jest-dom` and `@testing-library/user-event` (pin versions compatible with React 18 and react-scripts 5). Add `src/setupTests.js`. Cover:
   - `api.js`: headers, the 401 event, and the path prefixing.
   - `AuthContext`: loading, login success and failure, and logout on `auth:unauthorized`.
   - Routes: `Login` (form, SSO buttons per providers, `?error=`, open-redirect guard) and `RequireAuth` (redirect, auth disabled).
   - Pages: `Register`, the Users admin page (list, actions, last-admin error displayed), Settings change-password validation, and the mobile drawer open/close/Escape in `App`.
   - A smoke render of every page with mocked fetch.
   - Script: `"test:ci": "react-scripts test --watchAll=false --coverage"`, with a coverage threshold in package.json `jest.coverageThreshold.global` set to lines 60.
4. **CI.** In the frontend job run `npm run lint`, `npm run format:check`, `npm run test:ci`, then the build with `CI=true` (remove the current `CI: false` override).
5. Update the Makefile's `fmt`, `lint` and `test` targets to include the frontend.
6. Fix the frontend items in `docs/plan/BUGS.md` and tick them, each with a test.

## Verify
```
cd frontend && npm ci && npm run lint && npm run format:check && npm run test:ci && CI=true npm run build
```
Commits, for example: `build(ui): add eslint and prettier config`, `style(ui): apply prettier formatting`, `test(ui): add auth and routing tests`, `ci(ui): run lint, format check and tests`.
