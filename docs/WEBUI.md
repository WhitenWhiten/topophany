# Native Web UI

## Architecture and acceptance matrix

The terminal remains the source of truth. `internal/view` owns verdicts, rows,
evidence details, grouping, resource selection and service/server cards. The
optional API projects those types; JavaScript only renders them. Map placement
uses a build-tagged adapter around the terminal's virtual layout, geographic
projection, land mask, network layers and selection focus. No terminal frames
or ANSI output are parsed. Existing `/app/` WASM and CLI clients are unchanged.

The browser uses native ES modules and SVG, with no production npm dependencies.
This avoids a second component/build pipeline for a compact terminal-style UI.
CSS uses the exact ink-and-phosphor palette in `internal/tui/theme.go`. HTTP
streams use fetch with Authorization (no token in a URL), one active tab per
connection, five-second updates, bounded sessions and reconnect backoff. Hidden
pages stop streaming. All business conclusions remain in Go.

| TUI capability | Native browser implementation | Status |
| --- | --- | --- |
| Overview verdict/counts, attention, services, observers | Shared view projections and navigable panels | Implemented; validation below |
| Topology hierarchy, endpoints, links and evidence | Searchable list with native detail dialog | Implemented; validation below |
| Virtual / geographic / network maps | SVG geometry from Go layout adapter, Braille land, selection | Implemented; validation below |
| Map zoom, full map, cards/locations/points | Toolbar and m/z/f/p keys, pan and node drag | Implemented; validation below |
| Servers wall/uniform layout, resources and memos | Shared Fleet cards, list/cards and resource details | Implemented; validation below |
| Services cards, app/environment/host grouping | Shared GroupedList and ServiceCard | Implemented; validation below |
| Group drilldown, expand/collapse, attention filter | Group rows, scope toolbar and Space/i/c/b keys | Implemented; validation below |
| Paths, proxies, candidates and dependency evidence | Shared path rows, route diagram and full evidence | Implemented; validation below |
| Checks and samples/phase latency/availability | Shared check rows, NewStrip, raw history and rollups | Implemented; validation below |
| Incidents: open/recovering/closed and endings | Shared ledger rows, complete evidence and check links | Implemented; validation below |
| Observers, contracts, delivery, freshness | Shared observer rows and global vantage selector | Implemented; validation below |
| About, version, project statistics and key reference | Native About panel and declared-location globe/map | Implemented; validation below |
| Keyboard navigation, search, mouse and touch | Native accessible controls and shortcuts | Implemented; validation below |
| Display preferences and session overrides | Local display preferences only; never tokens | Implemented; validation below |
| Local topology editor: all entities, undo, validation, save | Local JSON entity forms, upload, Go validation, download | Implemented; validation below |
| Synthetic scenarios/debug | Remain `topo preview` tools; browser tests use explicit synthetic fixtures | Separate mode |
| Direct profiles, login and install tooling | Remain process-local CLI tools; no remote execution/config writes | Separate mode |

`Tab` keeps browser focus traversal; `1–9`, arrows/j/k, Enter, /, v, m, z, f,
p, s, b, i, Space, c, C, !, r, comma, ? and q provide the terminal actions. Editing
keys apply only inside the draft dialog. Browser-reserved Ctrl shortcuts are
not intercepted outside that dialog. Draft downloads are never automatically
applied to a collector or worker; redacted collector config is not a lossless
backup of private execution configuration.

## Implementation checklist

- [x] Phase 1: architecture, feature matrix, trust and dependency boundaries.
- [x] Phase 2: optional authenticated projection/history/stream/validation API.
- [x] Phase 3: embedded shell, theme, navigation, client and preferences.
- [x] Phase 4: nine pages, evidence, cards, grouping and draft editing.
- [x] Phase 5: native maps, interactions, paths and history charts.
- [x] Phase 6: local regressions, browser acceptance, cross-builds and size evidence; baseline/race exceptions are recorded below.

## Build and deployment

Requires the Go version in `go.mod`. No Node installation is needed to build:

```sh
go build -o topo ./cmd/topo
go build -tags webui -o topo-web ./cmd/topo
./topo-web serve --config topology.json --auth auth.json --data collector-data \
  --web --web-listen 127.0.0.1:8080
```

Open `http://127.0.0.1:8080` and enter an existing **viewer** token. Tokens live
only in page memory, are cleared on disconnect/reload, and never enter browser
storage, cookies or URLs. Worker tokens cannot read the Web API. Static shell
files contain no monitoring data. The listener is absent unless `--web` is
explicitly passed, and the ordinary binary rejects that flag with a rebuild hint.

For a remote server, forward the loopback listener over SSH. A non-loopback
`--web-listen` requires the collector's `--tls-cert` and `--tls-key`; the Web
listener uses the same certificate. Listen hosts must be concrete literal IPs (no wildcard binds) or
localhost. Host/Origin checks restrict requests to the configured listener;
reverse proxies must preserve its Host, scheme and origin (SSH forwarding is
simpler). There is no Web configuration write or command execution endpoint.

Frontend source is the embedded `internal/webui/assets/` directory; edit these
files directly, never the unrelated generated `site/public/` files. Runtime
security headers prohibit third-party scripts, frames and connections.

## Validation

```sh
go test ./...
go test -tags webui ./...
go test -race -tags webui ./internal/api ./internal/app ./internal/webui ./internal/tui
node --test internal/webui/tests/*.test.mjs
# Optional browser acceptance (requires Node >=20):
make build-web
(cd internal/webui && npm ci --ignore-scripts && npx playwright install chromium)
node internal/webui/tests/browser.mjs
GOOS=linux GOARCH=amd64 go build -tags webui ./cmd/topo
GOOS=linux GOARCH=arm64 go build -tags webui ./cmd/topo
```

See the final validation record below for commands actually executed. A
cross-build is not a native platform runtime test.

## Optional API contract and budgets

Both `-tags webui` and `serve --web` are required to register the new routes.
The Web listener also forwards only the existing viewer GET routes; worker
information and observation ingestion are excluded. Existing collector routes,
authentication and admission budgets continue to apply. A stream holds one of
the viewer's two concurrent admissions and rechecks authorization every frame.

| Route | Contract |
| --- | --- |
| `GET /v1/web/view` | Schema 1: selected tab's rows/details, shared verdict/counts, cards, map geometry and scope choices |
| `GET /v1/web/events` | Same projection as fetch SSE `event: view`; first frame immediately, then every 5 seconds, reconnect after 45 seconds |
| `GET /v1/web/history?check=ID` | Shared `view.NewStrip`, phase latency and full bounded observations; no invented samples for gaps |
| `POST /v1/web/validate` | Viewer-authenticated, pure `config.Decode` validation of a local draft; returns public/redacted configuration, never applies it |

View/stream queries accept `tab` (0–8), `observer`, `search`, `attention`,
`by` (application/environment/host/service/observer/path/flat), application,
environment and service scope, `collapsed`, repeated `expanded`/`folded` group
IDs, `offset`, `limit` (1–200; default 100), `map` (virtual/geo/network), and
selected entity `kind`/`id`. Queries are bounded to 4096 encoded bytes; projected
responses to 16 MiB; drafts to 4 MiB. Maps retain the terminal's 256-node,
512-edge complexity guard and layout work budget. Large or crowded maps report
an explicit notice; lists, evidence and pagination remain available.

The browser treats a missed/failed connection as historical evidence, clears
current resource values and map signals, and reconnects with backoff. A
15-second update age also marks the view cached. Revoked or wrong-role tokens
clear the connection and displayed monitoring data. Display settings are the
only browser-persisted state. Drafts remain local and preserve imported private
fields; a validation response never overwrites a draft with a redacted copy.

## Scope and known limits

This is a collector-backed native viewer. Process-local direct profiles,
synthetic preview scenarios/debugging, credential installation and system
service installation remain CLI/TUI modes, as recorded in the matrix. No remote
configuration or execution authority is added for a viewer. The editor supports
all topology entity fields through JSON entity forms rather than reproducing
the terminal's individual field widgets. Browser focus traversal keeps Tab;
the other documented navigation keys remain available. Browser font rendering
and the SVG graph's spacing differ from terminal cells; no pixel-identical
rendering claim is made.

The native UI uses server-driven updates at five-second cadence, including the
original About globe illustration. It has no third-party tile, font or CDN
requests. It connects to the collector serving the page; saved multi-collector
client profiles and direct source sessions are not imported into browser storage.

## Local verification record (2026-10-23)

Environment: Linux ARM64, Go 1.26.10, Node 22.23.3, pinned Playwright 1.63.0
with the local Chromium executable. Local artifacts and the toolchain are kept
under the Git-ignored `.verify-build/webui/` directory; `plan.md` is untracked
and is not part of the implementation or its commits. The system Go installation
was retained; in this workspace, `source .verify-build/webui/env.sh` selects
the installed Go 1.26.10 toolchain.

| Check | Observed result |
| --- | --- |
| Native frontend protocol/time-axis tests | 7 passed (`frontend-tests.log`) |
| Web API, role isolation, revocation, draft purity, optional listener lifecycle, map/focus/land/globe and environment grouping | Passed (`web-integration.log`) |
| Tagged compilation of every Go package/test | Passed (`tagged-compile.log`) |
| `go vet -tags webui` on changed Go packages and their shared view/geometry packages | Passed (`vet.log`) |
| Chromium end-to-end acceptance | Passed: nine pages; cards/list; SVG maps; check/incident evidence; observer projection; local validation/undo/download; mobile grouping; collector restart/reconnect; token revocation (`browser/result.json`) |
| Default build contains no native frontend assets | Confirmed by dependency inspection and embedded-asset checks |
| Tagged build without `--web` | No additional listener; `/v1/web/view` returns 404 |
| Tagged build with `--web` | Additional loopback listener; authorized projection returns 200 |
| `go test -p 2 -skip '^TestMonitorStreamProtocol$' -timeout=15m ./...` | Passed for every remaining package, including the complete app, TUI and view suites (`default-regression.log`) |

Both Linux architectures built successfully with `CGO_ENABLED=0`, `-trimpath`
and the same source. Embedded assets in both optional binaries were checked
against the final frontend source, byte for byte (`build-results.json`).

| Architecture | Default bytes | With Web UI bytes | Increase bytes |
| --- | ---: | ---: | ---: |
| ARM64 | 16,819,188 | 17,075,922 | 256,734 |
| AMD64 | 18,482,965 | 18,813,537 | 330,572 |

In one idle loopback collector run without probes, after one viewer request,
RSS was 12,148 KiB for the default build, 12,740 KiB for a tagged build with Web
disabled, and 13,068 KiB with Web enabled (`resource-results.json`). These are
single-run observations, not a capacity or performance guarantee. AMD64 was
cross-compiled; no AMD64 runtime execution is claimed.

Two limitations prevent describing the complete local gate set as green:

- `TestMonitorStreamProtocol/first_replacement_rejected_request_and_EOF` timed
  out waiting for a frame at line 165; `EPIPE` was killed at line 265. The same
  failures were reproduced from an unmodified `8f3fb0d` HEAD archive, at the same
  source locations (`baseline-monitor.log` versus `monitor-protocol.log`). No
  monitor code or existing test deadline was changed. The remaining regression
  run explicitly excludes this one test group rather than hiding its failures.
- The ARM64 host cannot execute Go's race runtime: ThreadSanitizer reports
  `unsupported VMA range`, `Found 39 - Supported 48` (`race.log`). Race coverage
  remains a required check in the added AMD64 CI job, not a local pass claim.
