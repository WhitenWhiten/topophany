# Contributing

Keep one binary and one versioned model. Preserve the separation between source,
requested route, runtime-reported choice and actual response evidence. Never
turn a monitor-side permission/API error into a target outage.

Software integrations live in separate packages under
[`internal/adapters`](internal/adapters/README.md). Keep each integration's
parsing, policy, validation and tests in its own directory; the probe supplies
transport and execution capabilities. See that guide before adding an adapter.
Cloud platform header parsing follows the same ownership rule. Preserve
cross-provider recognition order, private-response filtering and the distinction
between reported edge labels and measured responses when changing a platform.

Run `go fmt ./...`, `go vet ./...`, `go test -race ./...` and the complete
`scripts/verify.py` suite before publishing a release. Add a named regression
case to [SCENARIOS.md](docs/SCENARIOS.md) for every semantic bug. Tests must use temporary
files, local listeners and synthetic credentials. A mocked adapter must never
be described as a production deployment test.

Use bounded contexts/reads/queues, do not log raw upstream errors or secrets,
and keep configuration changes explicit. Adding an outbound protocol must not
introduce silent direct fallback. Adding an API GET endpoint still requires a
side-effect review. Do not add remote execution permissions to viewer/worker
roles. Keep the original observation timestamp during replay.

No third-party runtime dependencies are currently required. A future storage
or TUI library change must retain the wire contract, migration/recovery tests,
cross-builds and resource-budget behavior. JSON topology is the sole writable
configuration authority in version 1.0.

Automatic GitHub CI and Coverage run for changes to `cmd/`, `internal/`,
`scripts/`, `examples/`, `deploy/`, contract fixtures in `docs/contracts/`,
the root Go module, `Makefile`, or workflow configuration. Markdown, MDX,
reStructuredText, AsciiDoc and README files are excluded even inside those
directories. Documentation-only and site-only changes do not start these test
workflows; mixed changes still run the relevant checks. CI's manual trigger
and release-tag builds remain available. Within CI, browser builds, generated
site consistency checks and browser regressions run only when the push or PR
explicitly changes a non-documentation file under `site/`. Manual CI runs can
opt into these checks with the `site` input. Source-only acceptance runs also
skip `scripts/test_site.py`; for the same local behavior, run
`TOPO_CHECK_SITE=false python3 scripts/verify.py`.

The homepage at topophany.com is the static site in `site/public/`, published
by a Cloudflare Worker (`site/wrangler.jsonc`). Workers Builds compiles the
browser binaries and regenerates the pages before deploying on pushes to `main`
that touch `site/`. In Cloudflare's Settings > Build > Build watch paths,
include `site/*` and exclude `*.md`, `*.mdx`, `*.rst`, `*.adoc`, and `*/README*`
so documentation-only edits also skip site builds. This trigger is configured
in Cloudflare, independently of GitHub Actions and `wrangler.jsonc`.
`site/go.mod` keeps it out of the Go module. Its pages are written, one per
language, from the templates in `site/src/` with `{{key}}` where words go and
the words of each language in `site/src/locales/<language>.json`; edit those,
never the pages in `site/public/`. Its styles and scripts are edited in
`site/src/css/` and `site/src/js/` and published under names that carry a
digest of their content (`site.<digest>.css`), and the data the scripts fetch
is named with its digest as a query, so browsers keep them for good and a page
never runs with older ones (`site/public/_headers`). Its terminal replays
(`site/public/data/tour.<language>.json`) are recorded from `topo preview` by
the TUI's own renderer. Run `make site` after changing the TUI, the templates,
the words, the styles or the scripts to write them again (`python3
scripts/build_site.py` alone when the TUI is unchanged);
`scripts/test_site.py` fails while they are stale.

The interactive preview at `/demo/` (and `/zh/demo/`, `/ja/demo/`) runs the
real viewer and synthetic source in browser WebAssembly. `internal/tui/demo.go`
owns its session; `cell_frame.go` encodes the same character patches used by
the recorded tour. `site/src/js/demo-worker.js` runs Go off the page's main
thread, and `demo.js` connects browser input and the existing `term.js` canvas.
`make site` also runs `scripts/build_demo.py`, which compiles `cmd/topo-demo`,
copies the matching Go `wasm_exec.js` and its license, and records the source
and binary hashes in `site/public/data/demo.build.json`. Both WASM binaries and
this build cache are ignored by Git; a fresh checkout must run `make site`
before previewing the site. CI builds the binaries before acceptance checks.
With `site/` as its root directory, Workers Builds automatically runs `npm ci`;
`site/package.json` builds the binaries and pages in its
`postinstall` script. Keep automatic dependency installation enabled. This
does not depend on the dashboard's optional build command or its handling of
Wrangler custom builds. Local Wrangler runs the same build from its configuration.
The build image's Go toolchain follows the version in the repository's `go.mod`
through Go's automatic toolchain selection. When updating the site after Go
source or toolchain changes, run `make site`; Go-only changes do not require
refreshing the committed site's WASM hashes.
`python3 scripts/build_demo.py --check` checks that the generated runtime and
binary match their inputs, and `make site-check`
rebuilds the pages and tours and refuses a stale tree. CI runs it in the
acceptance job when site checks are selected. The demo has no terminal
server or real probes; hidden tabs pause its simulated clock. Its CSP permits
WebAssembly compilation with `wasm-unsafe-eval`, without permitting JS eval.
The demo page contains only the interactive terminal and a link home. Above
600×500 px the terminal sits in a hardware terminal's casing whose chin holds
that link, the TUI's own window title, its grid size and a power light; on
smaller screens a plain bar holds them. `--frame-x` and `--frame-y` are what
the frame adds around the screen, which the page subtracts when it fits the
terminal to the window. `--demo-cell-aspect` in
`site/src/css/demo.css` sets both the browser cell height/width and Go's map
projection; the current value is 2.6. The reference 200×50 grid therefore has
a window aspect ratio of 200/(50×2.6), about 1.54:1. Recorded homepage tours
keep their original 2:1 cells. Scenario selection uses the TUI's own `x` menu;
the preview clock runs at normal speed.

Browser input has one request in flight, up to 64 queued keys, and at most
16 keys per rendered frame. Repeated wheel events and resize notifications
coalesce; Ctrl-C immediately destroys the worker. The startup overlay can
cancel loading, and a page-side 30-second watchdog restores retry controls
even if compilation or the worker stalls. Frames are JSON-encoded once in Go
and parsed once on the page. Native editor forms and synthetic preview data
are omitted from browser builds where they are unused.

Run browser regressions with Node 20 or newer: `cd site && npm ci`,
`npx playwright install chromium`, `npm test`, then `npm run test:browser`.
Set `CHROME` to use an existing Chromium executable. Playwright is a pinned
development dependency; production assets contain no npm dependencies.
The loopback fixtures use synthetic credentials and check body deadlines,
CORS, omitted cookies, rejected redirects, input bursts, and startup recovery.

The real browser client at `/app/` (and its language-prefixed routes) uses
`internal/tui/remote.go` and the existing validated collector client. Its WASM
is built separately with `-tags=collector` into `viewer.wasm`; the preview
build excludes the network session and keeps its smaller download. Browser
requests use Go's Fetch transport with cookies omitted and redirects refused.
The eight-second deadline also closes an unfinished response body, releasing
the pending read so snapshot polling can resume.
`collector-worker.js` has its own CSP connection policy, leaving the demo's
same-origin policy intact. Its token only lives in the current connection;
`q` and navigation destroy that worker. `--web-origin` enables the collector's
exact-origin, read-only CORS wrapper inside existing admission budgets.

To test `/app/` with an existing private-CA collector without changing its
service or browser trust, run `python3 scripts/serve_site.py --profile NAME`
and open the one-time launch link printed in the terminal within five minutes.
Its fragment carries a random exchange secret, is removed from browser history
before exchange, and is never sent in an HTTP URL or referrer. Reloading needs
a new launch link (restart the development server). This development server binds only
to loopback, uses the saved viewer profile and CA, and automatically connects
through a fixed read-only bridge. A successful one-time exchange gives the
browser a temporary local token, never the saved collector token. Public pages
and scripts contain neither capability. It applies the site's CSP headers,
refuses other origins, redirects and worker routes, and verifies project
identity. Nothing is injected into the published static files. Do not expose
this credentialed development server through a tunnel or reverse proxy.

The TUI stills in `docs/assets` (`<name>.<language>.png`) are captured from the
preview the same way: the README's in every language, the guide's in English.
The README's animations (`<name>.<language>.gif`) are chapters of the
homepage's tour. Run `make captures` after changing what they show, and after
`make site`. It needs a Chromium (found on `PATH`, or given as `CHROME`) and
ffmpeg (found on `PATH`, or given as `FFMPEG`), and Japanese wants a font with
Japanese glyph forms; `make captures FONTS=dir` adds a directory of fonts, such
as Noto Sans JP, for that run only.

Words a reader sees go in catalogs, never in code. The preview's built-in
topology refers to its names, notes and places as `@key`, and each language's
text for them and for the scenarios is in `internal/preview/locales/`. To add a
language, add a catalog there and in `site/src/locales/`, a `README.<language>.md`,
and run `make site` and `make captures`; the tests list what is still missing.

The optional native browser UI is built with `make build-web` and explicitly
enabled with `topo-web serve --web`. Its source is `internal/webui/assets/`,
separate from the generated public site and WASM client. No frontend build is
required: assets are embedded only with `-tags webui`. See [WEBUI.md](docs/WEBUI.md)
for architecture, the feature matrix, local browser tests and deployment.
