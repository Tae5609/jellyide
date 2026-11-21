# T2: Make the React UI work on mobile

Repo: JellyIDE. Frontend in `frontend/` (CRA, React 18, react-router v7, plain CSS in `frontend/src/index.css`). Read `frontend/src/App.js`, `frontend/src/index.css`, every file in `frontend/src/pages/`, and `frontend/src/components/ModalContext.js` first.

Problem: there are no `@media` rules. Layout is a fixed 260px sidebar with `.app-container{height:100vh;overflow:hidden}`. Pages use many inline `style={{...}}` for layout (flex rows, fixed widths, grids), which media queries cannot override.

Follow `docs/plan/CONVENTIONS.md` (commit, PR and quality-gate rules).

## Do
1. **Shell (App.js + CSS)**
   - Add a `.mobile-topbar` (hidden on desktop) with a hamburger button (aria-label "Open menu"), the logo, and the current page title.
   - At `max-width:768px` the `.sidebar` becomes an off-canvas drawer (`position:fixed; transform:translateX(-100%)`, plus an `.open` class that slides it in) with a `.sidebar-backdrop`. The drawer closes on NavLink click, on backdrop click, and on the Escape key. Use React state in App.js, and close it on route change via `useLocation`. Because `useLocation` needs to be inside the Router, split it into `App` (Router) and `Shell`.
   - At `769px–1024px` the sidebar collapses to a 72px icon rail (hide text labels; keep icons, using `title` attrs for tooltips).
   - Use `height:100dvh` with a `100vh` fallback. `.main-content` padding shrinks to 16px on phones.
2. **Pages**: move every layout-affecting inline style (display, flex, grid, width, gap, padding on containers) into named classes in `index.css`. Pure colors and one-off cosmetic styles can stay inline. Then add responsive rules:
   - Header rows (title + action buttons) wrap: `flex-wrap:wrap; gap`.
   - Forms and inputs are full-width on phones. Inputs use `font-size:16px` on phones to stop iOS zoom.
   - Tables and list rows (Jobs list, Downloads playlist items, Metadata track list, Library album grid) are either reflowed to stacked cards at ≤768px or wrapped in `.table-scroll{overflow-x:auto}`. Prefer cards for Jobs and the Metadata track list.
   - Library: artist/album grid uses `grid-template-columns: repeat(auto-fill, minmax(140px,1fr))` on phones.
   - Metadata IDE: any side-by-side panels stack vertically. The bulk-operation toolbar wraps. The Apply/Preview buttons are reachable (sticky bottom bar on phones is fine).
   - Buttons and nav items have a minimum 44px touch height on phones.
3. **Modals** (`ModalContext.js` + CSS): `width: min(520px, calc(100vw - 32px))`, `max-height: calc(100dvh - 32px)`, `overflow-y:auto`.
4. No horizontal page scroll at 360px width on any page.

## Do NOT
- Add dependencies (no Tailwind, no UI libs).
- Change any `fetch` calls, API paths, or app logic.
- Change the desktop look beyond what the class extraction requires. Desktop must look the same.

## Verify (all must pass before you stop)
```
cd frontend && npm ci && CI=true npm run build
```
The build must succeed with no new ESLint warnings (CI=true turns warnings into errors). Then `git add -A && git commit -m "feat(mobile): add responsive layout with drawer nav and tablet rail"`.
