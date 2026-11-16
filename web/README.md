# PWS Gateway web UI

The local web UI of the gateway. It is a single-page React app that the Go
daemon serves over HTTPS from the Pi itself, so it has to keep working when
WxAlerts (the cloud) is unreachable. Everything it shows comes from the daemon's
JSON API under `/api/`.

## Toolchain

- Node 20 LTS (20.19 or newer) and npm 9 or newer.
- `npm ci` installs the exact versions in `package-lock.json`.

## Commands

| Command              | What it does                                                                     |
| -------------------- | -------------------------------------------------------------------------------- |
| `npm run dev`        | Vite dev server on http://localhost:5173 with the mock API (see below)           |
| `npm run build`      | Type-check, then build the production bundle into `web/dist`                     |
| `npm run preview`    | Serve the built bundle locally (no mock API; needs a real daemon behind a proxy) |
| `npm run lint`       | ESLint (typescript-eslint, react-hooks, jsx-a11y) and Prettier check             |
| `npm run format`     | Rewrite files with Prettier                                                      |
| `npm test`           | Vitest unit and component tests, once                                            |
| `npm run test:watch` | Vitest in watch mode                                                             |
| `npm run typecheck`  | `tsc -b` only                                                                    |

`make web` at the repository root runs `npm run build`, and `make test` runs the
web tests together with the Go tests.

## Mock mode

`npm run dev` needs no Pi. A small Vite dev-server middleware in `mock/`
answers every `/api/` route with realistic data:

- A gateway on a Pi 4 whose CPU temperature, memory and uptime move over time.
- Three stations (one selected), two radio dongles, an uplink that drops out for
  five minutes every so often, and an update state machine that walks through
  idle, downloading, staged, deferred and rolled back.
- A deterministic readings time series with a daily temperature cycle, wind,
  cumulative rain and a UV curve, so charts look like weather and `since`
  polling appends cleanly.
- Log lines that keep growing.

The mock enforces the same rules as the real server where the UI can get them
wrong: a session cookie, `401 {"error":"unauthenticated"}` when signed out, and
a `403 {"error":"csrf"}` on any mutating request that lacks the
`X-Requested-With: pwsgw` header.

Sign in with username `admin` and password `admin`. To exercise the first-boot
flow start the dev server with `MOCK_NEEDS_SETUP=1 npm run dev`; the account you
create then works for that dev-server session.

The mock is only loaded by `vite dev` (`apply: 'serve'` in `mock/plugin.ts`).
Nothing from `mock/` ends up in the production bundle.

## Project layout

```
web/
  index.html            Entry HTML. No inline scripts or styles.
  public/.keep          Copied into dist so the Go embed always has a file.
  mock/                 Dev-only fake API (Vite middleware).
  src/
    api/                Typed client: fetch wrapper, endpoint functions, types.
    auth/               Session state, RequireAuth route guard.
    components/         Card, Stat, Badge, Alert, Button, Table, form fields,
                        Chart (uPlot wrapper), Layout (nav shell).
    lib/                Units (display-only conversion), formatting, polling hook.
    pages/              One file per route.
    styles.css          The single stylesheet. Custom properties for theming,
                        dark mode via prefers-color-scheme.
    test/setup.ts       Vitest setup (jest-dom, jsdom polyfills).
```

## API client

`src/api/client.ts` wraps `fetch`:

- Every request carries `X-Requested-With: pwsgw`. The server rejects mutating
  requests without it as cross-site requests.
- Non-2xx responses become `ApiError` with the server's `{error, detail}`.
  `errorMessage(err)` turns any error into a sentence for the UI.
- A `401` with `error: "unauthenticated"` calls the handler registered with
  `onUnauthenticated`. `AuthProvider` uses it to drop the session, and
  `RequireAuth` then redirects to `/login`. Requests where a 401 is expected
  (the login form, the session probe at boot) pass `allowUnauthenticated`.

Pages call `api.<group>.<method>()` from `src/api/index.ts` and never `fetch`
directly.

## Units

The API is SI throughout (`temp_c`, `wind_speed_ms`, `pressure_station_hpa`,
`rain_mm`, ...). The person picks metric or US units in the top bar; the
choice is stored in `localStorage` under `pwsgw.units` and applied at render
time in `src/lib/units.ts`. Inputs that the gateway stores (altitude,
anemometer height) stay in metres, with a feet hint when US units are on.

## Content Security Policy

The daemon serves the UI with a strict CSP: no inline scripts, no inline styles
from JS, no external origins. The build satisfies it as long as:

- `index.html` has no inline `<script>` or `style=` attributes. Vite emits
  `<script type="module" src=...>` and `<link rel="stylesheet">` only. The
  module preload polyfill is turned off in `vite.config.ts` so nothing is
  injected inline.
- Components use classes, never `style` attributes built from strings. uPlot
  sizes its canvas through the CSSOM (`element.style.width = ...`), which CSP
  allows; it does not set `style` attributes.
- Fonts are system fonts and there are no third-party origins.

After `npm run build`, check `dist/index.html`:

```
grep -c '<script' dist/index.html      # expect 1
grep -c 'src=' dist/index.html         # the one script has a src
grep -c 'style=' dist/index.html       # expect 0
```

## Routing

The app is served from `/` with history-API routing; the daemon falls back to
`index.html` for unknown paths. Do not set a relative `base` in
`vite.config.ts`.

## Writing rules

Same as the rest of the repository (see `AGENTS.md`): no em dashes or en
dashes, no emojis, plain sentences, and UI copy that a station owner with no
Linux background can follow.
