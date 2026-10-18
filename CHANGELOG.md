# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- README (English, Japanese, Chinese): a plain first-screen description, who
  it is for, a comparison with Uptime Kuma, Gatus and SmokePing, install from
  GitHub Releases, and a closing invitation to star the repo or open an issue.
  Operator upgrade notes moved to `UPGRADING.md`.

### Fixed

- Isolate TUI fuzz tests from the user's saved preferences. Random input
  could select Settings' Save action and overwrite real startup defaults
  while running local verification before an upgrade. Upgrade acceptance
  now also checks that preferences survive installation and rollback unchanged.

## [1.2.0] - 2026-10-18

### Added

- The geographic map cycles through Cards, Locations and Points with `p` in
  the terminal and browser viewer. Locations shows compact city labels, or
  coordinates for unnamed sites, with collision-aware placement, selection
  highlighting and clickable labels. The map's label and zoom controls are
  independent and remain inside narrow panels.
- Mihomo supports up to 4096 names across the combined proxy/provider graph,
  4096 members per group/provider and 8 MiB inventory responses. Built-in names
  and groups count toward the graph limit; over-capacity inventories return
  explicit monitor errors. Other JSON and history budgets remain bounded.
- Mihomo can compare rotating provider membership against a fresh, independent
  names-only JSON publication, with separate publisher credentials and TLS
  settings. Fixed expectations remain strict; unavailable references are
  monitor errors and publications changing during sampling are unknown.
- Optional Mihomo TUN expectations and ordered/set provider comparisons report
  bounded counts of missing, extra and reordered members.
- Mihomo `provider_reference.settle_seconds` (0..3600, default 120, 0 disables)
  allows a bounded loading window after publication. Samples retain
  `policy / provider_membership_mismatch` with
  `provider_reference_settling: true`; opening an incident requires both the
  elapsed window and its equivalent consecutive policy samples. Repeated
  publications cannot postpone confirmation forever. Separate publisher,
  controller-load and sample times remain within the 32-fact budget.
- Optional `policy_threshold` requires 1..20 consecutive ordinary policy
  violations. Omitted or zero retains the immediate 1.1.1 behavior;
  failure/recovery thresholds and provider-loading settlement are independent.
- WireGuard supports AllowedIPs and keepalive expectations through public-field
  commands, without reading private or preshared keys.
- Opt-in bounded Tailscale DISCO/TSMP diagnostics retain reported evidence and
  do not imply application reachability. Default collection stays passive;
  versioned synthetic fixtures document supported response formats.
- Collector snapshots and CLI/terminal/browser status show TLS handshake
  failures in a rolling 30-minute ingress window. Source tracking is bounded
  and identifies truncated counts. Sources are pseudonymous by default;
  `--expose-ingress-sources` opts into raw addresses. Failed delivery makes
  affected evidence stale rather than declaring the monitored target down.
- Opt-in real-core capacity and isolated native installation acceptance tools
  record program hashes, versions and scope, including upgrade, credential
  preservation, authenticated rollback and reinstallation.

### Changed

- Cloudflare, Vercel, CloudFront, Fastly and Netlify header parsing lives in
  separate platform adapter packages. Existing HTTP configuration, first-match
  precedence, private-response filtering and evidence attribution are preserved;
  intentional Vercel location corrections are listed below.
- Mihomo, Tailscale and WireGuard implementations, validators and tests live in
  dedicated adapter packages with contribution and evidence notes.
- Tailscale peer identity resolution is deterministic and reports ambiguous
  identities, backend states, missing online state and peer relay separately.
- Software details distinguish runtime reads, policy assertions and unmeasured
  application paths; stale reports do not become current evidence.

### Fixed

- Snapshot readers try healthy collector failover first, then wait one second
  and retry once if every endpoint fails and one explicitly reports
  `429 snapshot_busy`. This reduces offline/cached flicker when LAN and overlay
  addresses share a busy collector. Cancellation, persistent failures and
  authentication/response-validation errors retain their normal behavior.
- Vercel request IDs use the real colon-separated routing format. Receiving
  and ordered routing regions are bounded and displayed as regions, without
  inferring PoPs or arbitrary function execution locations. Historical labels
  remain role-unverified and private requests withhold every new location key.
  The frozen pre-split oracle is unchanged; intentional differences have
  separate regression coverage.
- Mihomo resolves subscription-only selected nodes from the existing read-only
  provider inventory when `/proxies` omits them. Ambiguous names and cycles
  remain unresolved rather than claiming a verified route.
- Mihomo controller load times use `provider_updated_at.<name>` independently
  of membership facts. Overlong keys are omitted so long provider names cannot
  refuse a batch or overwrite membership evidence.
- Reloading policy confirmation or provider settlement re-evaluates retained
  and queued evidence while preserving measurement contracts and history.
  Provider settlement applies only to membership mismatches, requires elapsed
  time as well as samples, and conservatively waits for missing load evidence
  within the current publication window.
- Requests answered through an edge no longer automatically blame the origin.
  The explanation retains the failed assertion and unlocated failure layer;
  status codes provide candidate explanations, not proof of root cause.
  HTTP 524 is described as the platform's reported read/write timeout after an
  origin connection, separately from an unreachable origin.
- The Checks tab's `UP 24H` shows measured coverage for partial windows
  (`98.9% · 3h`, or `<1m`), rather than presenting partial evidence as a full day.
- Snapshot-backed CLI and terminal/browser views pin history to their
  collector, public revision and check contract. Source/revision switches
  clear cached charts and discard superseded background history.
- `topo version` honors injected release versions and otherwise identifies
  a tagged build, `dev-<revision>[-dirty]` or `devel` from available build
  information. Release builds inject and verify their own tag instead of
  claiming the last release or exposing a toolchain pseudo-version.
- A Tailscale CLI diagnostic's exact timeout response for its fixed peer is
  `unknown / diagnostic_response: timed_out`; other nonzero exits remain
  monitor errors. Missing `LastSeen` is reported as unavailable rather than
  a fabricated year-zero timestamp.
- Ingress counts include failures across the actual rolling window, parse
  only the HTTP server's own handshake log lines, and keep log handling off
  the connection path. Collector heartbeats tolerate a backward clock step.
- Authenticated installer rollback restores executable backups larger than
  1 MiB within the existing 64 MiB per-file limit.
- The real process/PTY acceptance harness refuses non-Linux hosts before
  starting processes, opening listeners or saving credentials.

### Security

- Privileged Unix installation rejects intermediate symlinks before mutations,
  creates directories through verified handles and changes ownership through
  an open descriptor.
- Windows bundle/installation directories replace and verify their entire
  DACL, including existing children, before writing sensitive payloads.
  Unrelated grants, untrusted owners and reparse points are refused or removed.
- Manual rollback requires installer-created private backups under the same
  installation, trusted ownership and authenticated metadata/payload hashes.
  Unsigned legacy backups are refused before service or file changes.
- Source identities and ZIP/tar exports reject nonportable paths and aliases
  under case and Unicode normalization, preventing cross-platform extraction
  from replacing reviewed files.
- Alert queues enforce presentation, encoded-event and persistence budgets so
  saved queues remain bounded and readable on restart.
- Local browser-viewer bootstrap uses a short-lived, one-time launch capability
  and a per-process session token; saved collector tokens stay out of the
  browser. Ingress source pseudonyms use a per-collector secret.
- Prerelease tags are marked as prereleases and do not replace the latest
  stable download. Release publication verifies immutable build artifacts and
  that the tag still resolves to the exact source commit.

## [1.1.1] - 2026-10-16

### Fixed

- Probe durations retain Go's monotonic clock across system clock adjustments,
  while sample timestamps remain UTC. Previously, a backward clock step could
  produce negative durations and a forward step could exceed the check's
  timeout budget, causing valid observations to be rejected with
  `observation_budget_exceeded` and quarantined. Invalid samples could also
  delay delivery of valid observations in the same batch.

## [1.1.0] - 2026-10-16

### Security

- Managed logs reject existing POSIX log files and backups with group or
  other permissions before writing; newly created and rotated logs stay private.
- Temporary client tokens use `--token-stdin` or `--credential-file` instead
  of the removed `--credential TOKEN` argument. Multi-server overrides still
  require an explicit server.
- Alert webhooks limit response headers to 32 KiB, including HTTP/2 responses.
- Release documentation is staged from an explicit list of regular Git blobs
  at the built commit, without following checkout symlinks into the runner.
- Snapshots admitted before a configuration reload return a retryable 503
  if capture has not completed, keeping storage and incident metadata within
  the request's authorization generation.

### Added

- Persistent monitor panels through `topo monitor`: typed metric catalogs,
  imported templates, transactional configuration, previews and exports,
  authenticated client panels, and panels attached to collectors or workers.
  Panels publish bounded JSON snapshots under an exclusive writer lock,
  expire stale evidence and expose lifecycle state after shutdown or a crash.
  Includes desktop, worker and compact RunCat example templates.
- Managed service logs with bounded rotation and retention, repeated-error
  summaries and recovery messages. Delivery failures retain actionable
  contract-mismatch diagnostics without logging arbitrary upstream text.
- Process-local I/O counters for journal, checkpoint, queue and sync work,
  available to monitor panels without changing durable-write guarantees.
- A browser WebAssembly preview at `/demo/` and an authenticated collector
  viewer at `/app/`, in English, Chinese and Japanese. The collector's
  `--web-origin` enables an exact-origin, read-only CORS policy; viewer
  credentials stay in the tab's worker session. Requests and input queues
  have deadlines and budgets, and quitting cancels pending reads.
- `--device` on `topo` and `topo preview` lays the TUI out for the screen
  of a 6.5-inch iPhone, of iPhone Duo shut, open, part folded like a
  book, upright and part folded like a laptop, or of a 13-inch iPad, and
  simulates that screen in the middle of the terminal. A phone gets one
  column with the tabs in a sidebar rail and rows a finger tall; iPhone
  Duo opened puts what is looked at and what is touched on either side of
  the crease, the map above the hinge and the events below it when
  upright, with the tabs in the system's column or along the foot; an
  iPad keeps the terminal's layouts with tabs and a toolbar for fingers.
  A pinch magnifies the map and a drag moves around it, and `⋯` offers
  every action the footer would. `[` and `]` fold through the poses, `\`
  picks the next device, and the mouse stands in for a finger. See
  docs/DEVICES.md. Built with `-tags nodevice` (`make build TAGS=nodevice`),
  `topo` leaves all of it out and has no `--device`.
- `scripts/device_shots.py` pictures each device's screen in its frame,
  iPhone Duo's part-folded poses rendered in 3D with the iPhone Duo
  simulator kit.
- On a phone, the website's live demo and collector viewer take the phone
  layout at the page's own width instead of scrolling an 80-column
  terminal sideways: the tabs in a sidebar rail, controls a finger tall,
  and taps, swipes and two-finger pinches read as on a device. A page
  wider than a phone is the terminal again. `--device fit` lays out the
  terminal itself for touch the same way.
- topophany.com has a section on the iPhone Duo layouts: the laptop pose
  as `topo preview --device duo-laptop` draws it, every pose side by side,
  and the commands to try the simulator. There is no iPhone app; the
  section says so. Each README links the live demo in its language.

### Changed

- The header, the tab strip, the status line and the key bar draw into the
  span they are given rather than the whole terminal, the key bar's
  actions come from one list a touch screen shares, and a click target can
  be taller than a row. The terminal draws exactly as before.

## [1.0.0] - 2026-10-15

### Added

- `s` on the Servers tab lines the cards up in a grid at one size, each
  saying the same things in the same places: the server's state, a panel
  each for CPU, memory and disk, how many services, addresses and checks it
  has with a mark for each in its state, the address it is known by and the
  first two lines of its memo. In the resources' place, an edge or a
  platform, which has no machine of yours to read, says what it stands in
  front of and where it answered from; a host nothing reads says why (no
  worker runs there, or its worker runs no host check), and one whose
  reading has gone silent says when it was last read. Anything else a
  server lacks keeps its place, empty; `s` again brings back the wall of
  full cards.
- `Enter` on a server lays its card open over the others, which dim behind
  it, in either layout: the memo first, then its resources charted over the
  samples kept with their peaks and limits, everything that runs on it and
  reaches it with nothing cut short, and the checks its state rests on, in
  two columns where the terminal is wide. `↑↓` scroll it; `Enter`, `Esc` or
  a click beside it closes it, and the cards behind keep their place.
- Providers' logos where the terminal's font is a Nerd Font (v3.3 or
  later): `--nerd-font-icons`, or Nerd Font icons in Settings. Nodes run by
  Cloudflare, Vercel, Supabase, GCP, Azure, AWS, Oracle, Deno Deploy,
  Linode or DigitalOcean wear their provider's logo before their names, on
  their cards, on the maps and in the Services tab's HOST column, and so
  does an edge wherever it is named in front of a service or an address.
  Off by default, as a terminal cannot tell which font it draws with. The
  homepage and the guide's stills draw the logos from their outlines.
- Add persistent startup display preferences, CLI overrides and `preferences
  show/set/reset`, plus a comma-key TUI settings dialog; application groups
  now start expanded by default.
- Add application and environment organization, collapsible Services/Checks
  groups, persistent browsing scope, shared dependency navigation, and editor
  support without changing measurement contracts.
- Add `topo capacity`, observer storage weights, configurable bounded
  collector state/receipt limits, and authenticated quota visibility before
  migration.
- Expose worker-reported backlog, quarantine, sample ACK, contact and retry
  telemetry in Observers, with bounded heartbeat payloads and closed
  diagnostic codes. A collector that refuses the telemetry (one not yet
  upgraded, or a worker clock running ahead) no longer has the samples
  quarantined with it: the worker resends without telemetry and withholds it
  from that collector for an hour.
- Generate private server/worker/client installation bundles with role-scoped
  enrollment, TLS, native startup definitions, dry-run, read-only enrollment
  verification and rollback.
- Centralize literal addresses with reviewed references and `topo address`;
  preview affected measurements without resetting equivalent history or
  flattening editor source. Bundle creation and address editing remain local
  operations; live routes and proxy policies are untouched.
- Collect Windows host CPU, memory, caller-relative disk capacity and selected
  process working sets through native APIs; validate Windows drive paths on
  any collector OS.
- Before the first snapshot, the TUI shows the globe turning beside the
  wordmark and names the collector it is asking, rather than empty views
  counting zeros. When none answers, the scene goes still and says which
  collector did not answer and why, that it tries again every 3 seconds
  (`r` at once), and what to check.
- Where the topology declares applications, the Overview's services card
  gives each application a line, with a mark for each of its services and
  how many need attention, and lists the services by name under their
  application where it has room, those needing attention first. A click
  opens the application, or the service, in the Services tab.
- The README and the homepage say how to have a coding agent write the
  topology, with a prompt to start from, and how the topology then serves
  as the network's memo and `topo status`, `show` and `history` as its
  quick interface.

### Changed

- Where a card has no room for the edge a service or an address is reached
  through, the edge's logo or its mark (`CF`, `▲`, `DO`, …) stands in for
  its name, whole, rather than the name cut short and the address's state
  pushed off the card.
- Known providers are named in their own spelling, by any of their usual
  names: a node's `provider` of `oracle` or `oci` reads Oracle, `gcp` or
  `google` reads GCP, `digitalocean` reads DigitalOcean. Edges and
  platforms keep their brand colors; a machine you run keeps its group's.

- `Enter` on the Servers tab opens the server's card rather than the
  evidence panel; a node's evidence panel is still on the Topology tab.
- When no collector answers, the error names each one and why it gave no
  answer: connection refused, timed out, a host name not found, a TLS
  certificate from an authority not trusted here or for another name, an
  http:// collector, a busy or failing collector, or a snapshot that is not
  current. `topo status`, `show` and `history` give the same reasons.
- `topo` run without saved credentials points to `topo preview`, `topo init`
  and `topo login` rather than to the file it could not find.
- `topo help` lists the commands by task, getting started first.
- Lists leave out a column in which every row says the same word (every
  check `fresh`, every item an `incident`); it comes back as soon as a row
  differs. On terminals 120 columns or wider the list takes a little more
  than half the width, so names read whole.
- A long label in the evidence panel keeps a short value beside it rather
  than hanging it on the next line, and `true` and `false` read yes and no.
- The Check form shows only the options of the kind chosen; what the others
  hold is kept.
- The Settings dialog explains the focused setting and has buttons like the
  other dialogs: Apply now, Save as startup defaults, Restore defaults.
- Preferences that cannot be read no longer keep the TUI from opening: it
  starts with the built-in defaults and says why. `preferences show` still
  reports the error.
- CLI tables let a column grow to 48 characters before a cell runs on.
- The built-in preview declares six applications (Main site, Serverless,
  Proxy chain, Remote access, Personal cloud, Code & backups), so
  `topo preview` opens Services grouped by application, and its Chinese and
  Japanese maps name every place in their own language.
- A group is named by its application alone where no service or check
  names an environment, rather than "Shop / Unspecified" on every group,
  and the scope line says "by application".
- On a calm Overview the all-clear keeps to a few lines, and the services
  and observers take the room.
- The homepage's replay and the README's animations and still are recorded
  at 200×50 rather than 140×42, so the full maps have room for the world
  and their places' cards; on a wide screen the replay reaches past the
  text column, up to 1600 pixels. The guide's stills keep 140×42.

### Fixed

- Recognize Windows socket errors in collector diagnostics, so connection
  failures report the same actionable reasons as on Linux and macOS.
- Wait for the failed webhook attempt to be persisted before stopping the
  notifier in the restart test, avoiding a scheduling-dependent CI failure.
- The systemd units of server and worker bundles quoted `WorkingDirectory=`
  and `EnvironmentFile=`, which systemd refuses, so installing such a bundle
  on Linux failed and rolled back. A new test checks the unit with
  `systemd-analyze verify` where it is installed.
- `topo preview` said how to switch scenarios before its clock started, so
  the hint was gone before the first frame.
- Editor labels longer than the form's label column ran out of the dialog;
  the Browse menu drew an empty key column; an evidence title too long for
  its panel was cut without an ellipsis.
- A group of one said "1 members", and a group's evidence ran its checks
  and shared dependencies into the note under them.

### Security

- Withhold reflections of private HTTP `Host` overrides from observations
  visible to viewers.
- `topo install` installs the bytes whose checksums it verified, the service
  definition and the topology included, rather than reading the bundle a
  second time, so a bundle changed between the check and the copy cannot
  install anything unreviewed.
- Running as root, `topo install` refuses an installation or data directory
  that someone other than root or the administrator who ran it could
  rearrange; changes ownership only of the file itself, never through a
  symbolic link; refuses a data directory replaced by a link; and only looks
  at the service's writer lock, never creating or changing its owner, so a
  compromised service account cannot turn the next upgrade into root access.
- A bundle's private certificate authority can sign no intermediate
  authority, and the bundle's README says when the collector certificate
  expires and how to replace it.
- The preferences file is read as a bounded regular file, so a FIFO or a
  device in its place cannot hold up the TUI's start.

## [0.9.1] - 2026-10-15

### Fixed

- Retry collector connection admission when concurrent authentication or socket
  closure frees capacity after the initial quota check, even if no pending
  socket remains to evict. New connections no longer receive an intermittent
  EOF despite available capacity; source and global connection limits remain
  enforced.
- Avoid false secret-reflection test failures when a contract digest happens
  to contain a short hexadecimal fragment from the test credential.

## [0.9.0] - 2026-10-15

### Added

- Local host resource checks on Linux and macOS: CPU utilization, physical
  memory, filesystem capacity and inodes, visible process and zombie counts,
  and optional named-process count/RSS. Resource and process-count assertions
  use existing policy incidents; metrics travel through worker buffering,
  collector storage, check details and JSON history. Includes local editor
  fields and a runnable `examples/host-resources.json` topology.
- Host resource preview with deterministic changing counters and localized
  CPU, memory, disk and missing-process scenarios. Preview shares the real
  probe's accounting and threshold evaluation, including incidents and history,
  without reading host resources. The full built-in topology includes resources
  for ten hosts and its router, with per-node failures and worker liveness.
  Servers cards and CLI output show current resource summaries; stale and
  cached readings are withheld.

### Changed

- Servers cards chart CPU, memory and disk usage, recent trends and capacity.
  Host-check evidence and node details chart resource history against configured
  limits instead of probe elapsed time. The TUI reads each host check's recent
  history once and retains newer snapshot samples, up to 240 per check under
  its current contract. Readings that are not current stay faint with their
  values withheld. Preview CPU varies between samples while memory and disk
  drift slowly.
- Host checks briefly share CPU, memory and selected-process aggregates,
  retaining the original sample timestamp and independent disk/assertion results.
  Linux CPU collection reads only the aggregate line instead of the entire
  per-CPU and interrupt table.

### Fixed

- Bound outstanding host filesystem queries across timeouts and reloads so a
  stalled mount cannot block worker shutdown. Ignore Linux procfs `ESRCH` when
  a process exits during a read, and recognize macOS `ps` fallback names for
  processes whose command information is unreadable.
- Bound each late sample's rollup correction to shifting at most 1,024 retained
  boundaries during ingestion and journal recovery. Larger corrections still
  count the sample but mark its time totals incomplete with
  `unreconciled_samples`.
- Apply node, edge and viewport limits to the geographic map as well as the
  network and virtual maps. Geographic label placement shares a bounded work
  budget across all placement attempts; oversized or exhausted layouts show a
  complexity notice while lists and details remain available.

### Security

- Preserve collector authentication capacity when pending sockets fill the
  listener: new arrivals replace the oldest pending socket within the relevant
  quota, and unauthenticated sockets expire after five seconds. Authenticated
  connections leave the source quota and are protected from pending-socket
  eviction, with at most eight sockets per principal and 96 globally under the
  default 128-connection cap, leaving 32 slots for authentication. Existing
  authenticated connections remain usable during anonymous connection floods;
  continuous transport floods can still interrupt clients before authentication.

## [0.8.3] - 2026-10-15

### Security

- Anonymous and rejected HTTP/2 requests now close their connections after
  active streams finish, releasing listener slots as HTTP/1 requests do.
- Plain HTTP checks through authenticated proxies withhold peer-controlled
  text whenever a hop receives a username, password or SSH login, so a proxy
  cannot reflect its private login into observations visible to viewers.
- Custom HTTP request headers, including API keys, require HTTPS or direct
  literal loopback. Invalid header names and values are rejected before
  execution, and incomplete collector snapshots are refused by clients.
- A worker token revoked by a reload can no longer store a sample. A
  collector found a request's token among those in force when the
  request arrived, but judged its batch under the topology in force once
  the body was read, so a worker holding its body open across a reload
  that revoked its token had a sample of the new topology stored; a
  viewer admitted just before a reload could likewise read the topology
  loaded after it. Each request is now served under the tokens and the
  topology it was admitted with, and a batch whose sending straddles a
  reload is refused with `503 topology_reloaded`, for its worker to send
  again and be admitted, or not, under the new ones.
- A response through an edge or proxy no longer proves the nodes behind
  it. A passing HTTP response whose edge said it fetched it from behind,
  with a cache status such as `MISS` or `DYNAMIC`, made the origin read
  up, measured, and stood as evidence against a failure of the origin
  seen from another observer; but those headers are the response's own
  word, which the edge, or anything behind it, can send whatever
  happened, so an edge could hide a dead origin. Such an answer now
  proves the first hop alone, and a node reached only through it reads
  unknown and says why; give it a check that does not go through the
  edge to measure it.
- A query in an HTTP check's URL is kept as the credential it may be.
  The public topology hid it from viewers, yet it was sent over plain
  HTTP to any address, where a bearer token is refused, and a peer that
  was sent one could hand it back in an edge's labels, which every
  viewer reads. Such a check now needs HTTPS or a direct literal
  loopback address, as `bearer_env` does, and its observation keeps
  what the peer answered only in closed words, with
  `peer_text: withheld_secret_request`. A topology with a query in a
  plain `http://` URL to anywhere else is refused; use HTTPS, or move
  the token to `bearer_env`.
- A worker changing its boot ID without end can no longer slow every
  observer's ingestion. Each boot whose receipts expire leaves a floor,
  up to an eighth of its observer's share, 131,072 with one observer by
  default, and every write counted them all, twice, under the lock all
  ingestion waits on, sorting the observer's floors whenever one had to
  go. Each observer's floors are now kept oldest first, so a write
  counts them and sheds the oldest without visiting the rest; with a
  full share of floors, a write prunes them about as fast as with a
  thousand.
- A peer can no longer hold a probe for its whole timeout by streaming
  compressed nothing. HTTP and Mihomo probes asked for gzip and counted
  their body budgets after decoding, so a target or controller sending
  empty gzip members without end kept the check, and a scheduler slot,
  busy until the check timed out, up to five minutes. Probes, workers,
  viewers and webhooks now ask for no compression and decode none, so
  each budget counts what the peer sends and runs out at once. A target
  that compresses its body unasked is checked as sent: `contains` and
  `body_regex` see it compressed.
- `--json` output carries no C1 control. It escaped bidi controls, and
  encoding/json escapes C0 ones, but DEL and the C1 controls, which some
  terminals act on as the escape sequences they stand for, were written
  as a collector sent them, in a check's reason, a history entry or a
  rollup's outcomes. Every control is now written as a `\u` escape,
  which reads back the same, and a rollup whose outcomes are not named
  in plain text is refused.
- Viewers no longer see an SSH hop's login. The topology a collector
  gives viewers withheld a proxy's username, password, identity and
  known-hosts files, but kept `ssh_user`, so `/v1/config` and every
  snapshot named the account the worker logs into the bastion as. It is
  now withheld with the rest, and changing it changes no public
  revision or contract.
- A collector can no longer stop `topo` or the TUI with a sample no
  worker could have sent. A history holding one passing sample that took
  some time and one that took less than none drew its latency bar below
  the lowest height, and the TUI's evidence panel or `topo show` for that
  check crashed. History entries and a snapshot's last samples are now
  held to what a collector holds a worker's samples to, durations and
  timings from zero to the longest a check may take, a known outcome and
  plain-text diagnostics, and refused otherwise, which the evidence says;
  latency bars are drawn whatever durations they are given.

### Added

- Each README shows the TUI in motion, in its own language. It opens
  with an outage arriving on the Overview, the routes it breaks and the
  evidence for one incident, and its terminal UI section shows the
  network from two vantage points and then the three maps. They are
  chapters of the homepage's tour, made into GIFs by `make captures`,
  which now needs ffmpeg as well as a Chromium. The stills it makes keep
  their pixels in a quarter fewer bytes.

### Changed

- Snapshot evaluation reuses an immutable plan per topology generation,
  indexes entity/check relationships and sorts sample references instead
  of copying observations. Configuration cloning no longer serializes JSON.
- CLI commands, virtual-map rendering/layout/routing and rollup
  persistence/query/histogram code are separated into focused files.
- Late samples correct retained time intervals, including after restart.
  Correction boundaries share the rollup budget; missing or ambiguous
  boundaries produce `unreconciled_samples` and an incomplete uptime label.
  Existing checkpoints remain readable.
- The homepage's "The edge answered" panel draws the exchange it is
  about, as a sequence diagram played once as it comes into view: a
  check from Frankfurt asks Cloudflare for the site, the edge cannot
  reach the origin in New York and answers HTTP 521 itself, and below
  each, what that proves of it: the edge up, the origin unproven. "What
  it measures" lists the native probes by layer, each with the evidence
  it leaves and what a pass of it proves; draws a proxy chain as the
  route a check takes through its hops; and says what each adapter reads
  and what each edge is credited with.

### Fixed

- Active unknown or stale checks can no longer produce `ALL CLEAR` or
  `topo status` exit 0. Paused and maintenance checks remain excluded.
- Workers keep sending heartbeats during collector storage backpressure,
  while observations remain queued for retry without false acknowledgements.
- Rollup budgets include active-series metadata and sample boundaries,
  even when there are no historical buckets left to evict.
- A new version of the homepage shows without a forced refresh. Its
  styles, scripts and data were kept for an hour and then served stale
  for a day while they were checked, so a returning visitor got the new
  page with the old styles and scripts. They are now published under
  names that carry a digest of their content, and the data with its
  digest as a query, so a browser keeps them for good and a new page
  names new ones, while the pages themselves are checked on every visit.
  `scripts/build_site.py` writes them from `site/src/css` and
  `site/src/js`.

## [0.8.1] - 2026-10-14

### Changed

- The README and the homepage show the preview where 0.8.0 put it, at
  home in Seattle, Osaka or Chengdu by language. Their stills, the
  homepage's recording and the words beside them still showed the
  network the preview had in Changsha, and the recording's two vantage
  points were observers the preview no longer has, so made again it
  would have shown nothing measured from either. They are captured
  again, and the tour now fails to record, in the tests too, when it
  views an observer the preview lacks. The homepage's vantage panel is
  drawn where each language's preview puts its home, the transit host
  and New York, with its map fitted to them and its arcs and labels laid
  out so that none overlap.
- Topophany has a new mark, the globe of the README's banner in little:
  a dot-matrix Earth lit from the upper left, with an arc of light from
  Shanghai to a place it brings to light across the Pacific. It is the
  homepage's favicon, the mark beside its name and its Apple touch icon,
  and `scripts/icon.py` (`make icon`) draws it from the land mask.

### Fixed

- TLS and HTTPS checks, and https-connect hops, no longer report a
  healthy service as `tls: timeout` on a path that drops large
  ClientHellos. Built with Go 1.26, a probe offered Go's default key
  exchanges, led by the hybrid post-quantum X25519MLKEM768, whose key
  share alone is over a kilobyte; where a path never delivered that
  ClientHello, TCP connected and the handshake went unanswered until the
  check timed out, while curl and OpenSSL reached the same server. Probes
  now offer only X25519, P-256, P-384 and P-521, as those clients do.
  Workers still offer the hybrid exchange to their collectors.
- A worker with several collectors no longer probes on while it drops
  every sample. Once each collector's share of the outbox was full, as
  when all of them were down long enough, a new sample was kept for none
  but reported as a degraded delivery, which a worker takes to mean
  another collector still has it; it logged the loss and went on. With
  room at no collector a worker now stops with the outbox's capacity
  error, as one with a single collector always did, and keeps what it
  queued.
- An incident ledger that could not be written is written at the next
  judgement, two seconds on, whether or not anything changed by then. A
  failed write was tried again only once something else changed, yet the
  collector logged "incident ledger written again" at the next judgement
  that changed nothing, and a crash before the next change lost the
  incidents that write held. The log now follows a write that succeeded.
- A collector starting on a new data directory makes it durably. The
  directory, those made above it and its new journal were never synced
  into their parents, so on a POSIX filesystem a power loss could leave
  the samples it had acknowledged, each written and synced, with no name
  to find them by. Each is now synced into its parent as it is made,
  before the collector opens, and so is any directory made to write a
  file into, such as a credentials file's.
- A worker starting on a new outbox makes it durably. Its directories
  were never synced into their parents, and each record was synced into
  `pending` but `pending` into nothing, so a power loss could lose the
  samples a worker had queued before any collector took them. The
  outbox's directories are now synced as they are made, before the
  worker opens it.

### Security

- A worker can no longer crowd other observers' alerts out of a webhook
  they share. A webhook's queue held its 64 alerts whoever they were
  from, so while it was down or slow a worker could fill it with
  incidents of its own observer, which it can make at will, and every
  alert from another observer was dropped until it drained. A full queue
  now gives an alert from an observer with fewer waiting the place of the
  newest from the observer with the most, so each keeps its share; a
  drop is logged with the observer it was from.
- A collector's TLS private key is read as its other credentials are.
  `topo serve` handed `--tls-key` to the TLS server to open as it was,
  so a key other users could read, a link or anything but a file was
  served from; now the key must be a regular file, not a link, of mode
  `0600` or stricter on POSIX, or the collector does not start, and it
  says why. Copy a key out of a certificate tool's directory of links,
  such as Let's Encrypt's `live/`, rather than pointing at it.
- The editor no longer writes a terminal control or bidi rune from a
  topology to the terminal. A field's text was shown without them, but
  its cursor, in color, redrew the rune under it as it was, so moving
  through a field of a crafted topology could send its controls one at a
  time. Any such rune now shows as a space, wherever a single rune is
  drawn, and the cursor stands where the text before it is shown rather
  than a column further per control.
- A worker can no longer fill its observer's share of a collector for
  good by changing its boot. Each boot whose receipts expired left a
  floor that was kept forever and counted against the observer's share,
  and a worker picks its boot IDs, so one that sent each sample under a
  new boot made its observer's samples refused with 429 from then on,
  through a token rotation and a restart. An observer's floors now take
  at most an eighth of its share; past that, those whose receipts were
  taken longest ago go. The IDs they bound were received more than
  `--retention` and an hour ago, too long ago for their samples to be
  accepted again, and a boot still delivering keeps its floor.
- A topology's values reach no terminal raw. Errors that refused an
  endpoint, link or path ID, a path's node or dependency, a check's kind
  or a repeated header name wrote the value as it was, so a crafted
  topology could clear, retitle or reorder the terminal of whoever
  validated, served or edited it. Those values are now quoted with their
  controls escaped, and everything `topo` writes to stderr, its errors and
  logs, leaves out terminal controls and bidi runes but for newlines and
  tabs.

## [0.8.0] - 2026-10-14

Everything since 0.3.0. No release was published between them.

### Added

- On the geo map, `p` hides every site plate and draws the arcs dashed,
  leaving the markers and the lines. `p` again brings the plates back.
  The key does nothing on the virtual geo or the network graph.
- Long-term history. Collectors roll every sample they accept up into
  per-check rollups that outlive the raw journal: each sample's outcome
  stands until the next sample or until it goes stale, and the time it
  stood is summed as up, degraded, down or inconclusive (the monitor could
  not tell), with the samples by outcome and a histogram of what the
  passing ones took. Time no current sample stood for is unmeasured,
  never up or down, so availability reads as "95.7% up over 96%
  measured". Rollups are kept five minutes wide for two days, hourly for
  32 days and daily after that, written with each checkpoint and rebuilt
  from the journal, so a crash counts no sample twice. `topo serve
  --rollup-bytes` caps them (32 MiB by default); at the cap a collector
  sheds the rollups of contracts it no longer counts, then the oldest
  days of the observer whose rollups take the most room, and never stops
  ingesting for them.
- Snapshots carry each check's `availability` over the last 24 hours, 7
  days and 30 days. A check's evidence shows them, such as `Last 24h:
  99.2% up over 96% measured · down 11m`, and the Checks tab its last
  day's share up as UP 24H.
- `GET /v1/rollups?check=ID&step=1h&span=7d` lays a check's rollups out
  in slots of five minutes, an hour or a day, and `topo history CHECK
  --step 1h [--span 7d]` prints them, slot by slot with a total, or as
  JSON. The preview keeps synthetic rollups as a collector watching all
  along would, so scenarios show in them too.
- A check's evidence opens with its recent samples, read from the
  collector's history: a strip of one slot per sample colored by outcome,
  with a grey dot for each interval in which none was taken, a bar for
  what each passing sample took, and their median and 95th-percentile
  times with the median DNS, connect, TLS and first-byte times.
  `topo show checks ID` prints the same strip, and its JSON carries it
  slot by slot as `samples`.
- Collectors keep an incident ledger, `incidents.json` in their data
  directory, judged every two seconds from the evidence of all observers
  whether anyone is looking: an incident opens when it is confirmed,
  recovers while its failed checks pass again short of their recovery
  threshold (a failure then is a relapse, not a new incident), and closes
  as recovered, unconfirmed, paused or changed. Each change is logged.
  Snapshots carry the open records and those closed in the last day as
  `ledger`; the Incidents tab and `topo show incidents` list them after
  the open incidents, one row per occurrence, and an incident's evidence
  says when it opened, how often it happened before and how long it
  lasted. A recovering incident still needs attention.
- A collector logs what its store holds when it starts: each observer's
  receipts and bytes against its share, and where its receipts settle at
  the last hour's rate, since a receipt lasts the ingestion window and an
  hour. It warns once when an observer is set to fill its share, and
  when, so its sampling or `--retention` can change before its samples
  are refused.
- Webhook alerts. `topo serve --alerts alerts.json` posts incidents that
  open and close, and observers that fall silent or report again, as
  JSON or in the formats of ntfy, Slack and Discord, filtered by event
  and observer. The file names secrets, so it is private to the
  collector like `auth.json` and reloads with it on `SIGHUP`. Each
  webhook has a bounded queue delivered in order, at least once, with
  retries for failures, 408, 429 and 5xx until an alert has waited a
  day. What waits is kept in the data directory as `alert-queue.json`,
  so a collector that stops or reloads loses none of it, and each
  webhook's sent and dropped alerts are counted there and logged at
  start; logs name webhooks by ID, never by URL.
- Everything the TUI shows can be read from the command line. `topo status`
  prints the Overview (verdict, check and tab counts, what needs attention,
  services and observers) and says it in its exit status: 0 all clear,
  2 something needs attention, 3 nothing current is known, 4 no collector
  answered. `topo show TAB [ID]` prints any tab, or the evidence behind
  one entry as the evidence panel has it, a server with its card;
  `topo show map` lists the map's nodes and edges with what each may
  claim. `topo history CHECK` reads a check's recent samples. Each takes
  `--json`, `--observer` and the connection flags of `topo`.
- Collectors and workers reload on `SIGHUP` (`systemctl reload` with the
  shipped units): a collector rereads its topology and `auth.json`, a
  worker its topology. A file that fails validation, names another
  project or drops the process's own observer is refused and logged, and
  the running topology kept; a reload logs which checks measure something
  new and so start afresh. Windows has no `SIGHUP`; restart there.
- Snapshots say, for each observer, whether its executor measures what the
  collector counts. After a collector reload, an observer whose worker
  still runs the old topology reads `outdated`, needs attention and is
  counted on the Observers tab until that worker reloads too. Workers
  report this in their heartbeats.
- Snapshots carry each endpoint's state from the checks that target it,
  and the Topology tab's endpoints show the same evidence lines as other
  entities: the state's reason and the checks behind it.
- `topo preview [--scenario N] status` (or `show`, `history`) reads the
  synthetic evidence, and `topo preview scenarios` lists the scenarios, so
  scripts and agents can be rehearsed on a simulated outage.
- The preview speaks English, Japanese and Chinese. Names, notes and
  scenarios come from one catalog per language in
  `internal/preview/locales`, through references such as `@node.win`; a
  language is supported when its catalog is there. Each language also has
  its own placed topology, so the home is Seattle, Osaka or Chengdu
  rather than one private network shown in translation. The node IDs,
  the scenarios and the evidence stay the same, and a test holds every
  language to that. `topo preview --lang` picks one, and by default the
  first of `LC_ALL`, `LC_MESSAGES` and `LANG` that is set decides,
  falling back to English. `--print-topology` writes the topology in
  that language, and `topo preview scenarios --json` gives each scenario
  an `id` that is the same in every language.
- The homepage is in English at `/`, Japanese at `/ja/` and Chinese at
  `/zh/`, each linking the others, with a language menu and a hint that
  offers a visitor arriving from elsewhere the page in their browser's
  language. The pages are written by `scripts/build_site.py` from one
  template per page in `site/src` and one catalog per language, and each
  language replays a TUI recording of its own preview. It says what
  Topophany is, and can be found as that: a sitemap naming every
  language's page, `robots.txt`, and schema.org data for the site and
  the software. Headings are lit dot by dot, the docs sit beside the
  install, and the semantics are drawn as the TUI draws them. The page
  is a line or two where it was the manual, and what was there for
  effect is gone.
- The README is translated into Japanese (`README.ja.md`) and Chinese
  (`README.zh.md`); a test keeps their commands, links and sections those
  of the original, and each shows the TUI in its own language.
- `make captures` captures the TUI stills in `docs/assets` from the
  preview, painted by the homepage's terminal renderer: the README's in
  every language, the guide's in English. They now show the current TUI.
- `f` shows the map alone, on the Overview or Topology (from another tab,
  the Topology's), and `f` or `Esc` brings the panels back. The selection
  still moves with the arrow keys and is named with its state along the
  map's foot. With the room, the virtual geo says what runs on each node,
  services with a problem first and in their color, on a line under it
  when its cards have the height, and geo plates name them after each
  node.

### Changed

- The geo map labels a site with a plate, its place over a row for each
  node, set down where it covers the least land and the fewest arcs:
  beside its marker when there is room, otherwise joined to it by a thin
  leader, so a crowded region spreads its labels into the sea around it
  rather than stacking them over each other. A short map shows fewer rows
  and counts the rest, then folds a plate to one line. Labels stay put
  while the selection moves.
- Fitting every site, the geo map folds a stretch of longitude wider than
  60° with no site in it, such as the Pacific between China and
  California, to a narrow seam marked with how many degrees it holds, so
  the coasts on either side get the width instead of an empty ocean. The
  focus and world views are never folded. Its natural height is the
  latitudes its sites span with room for their labels, rather than the
  whole world, and on the Overview it keeps the rest when nothing needs
  attention.
- Labels take the room there is rather than a fixed width: virtual geo
  cards widen to their full names where the gaps can spare it, a network
  chip cut short by its column runs on into free cells to its right, a
  service on the Overview with no cell beside it runs on to the edge, and
  a server card's row keeps what it names whole before what it says on
  the right.
- Server cards name the server once, its state in the pill, and take an
  amber or red header when something on them is affected or down. Their
  sections open with a quiet label and a hairline rather than a bright
  heading, and the frame is the card's edge, with no paper outside it.

- Accepting a batch no longer visits every receipt a collector holds.
  Receipts expire from a queue in the order they were taken, and what
  each observer holds is counted as it changes rather than recounted on
  every write, under the store's lock, twice: with 150,000 receipts held
  a write takes 5.7ms rather than 26ms.
- Deduplication receipts expire an hour after the ingestion window
  (`--retention`), when no sample they could match can be accepted any
  more, and release their capacity. Each worker boot keeps the highest
  expired sequence as a floor, so an expired observation ID stays bound
  and is refused with `409 observation_id_conflict`, across restarts.
- An incident starts as early as the ledger saw it fail, rather than at
  the oldest sample a collector still holds for its checks, and in a
  cached view its evidence reads CACHED rather than OPEN.
- **Wire format (schema 2).** Each observation carries the measurement
  contract of its check, a digest of what the check measures (kind,
  observer, target, timeout, bind and resolve settings, assertions,
  adapter options, the proxies of its path), instead of the revision of
  the whole topology. Editing a note, a name, a place, a threshold, an
  interval or another check no longer resets a check's history, refuses
  in-flight samples or renames its incident; changing what it measures
  still starts it afresh. `topo validate` prints every contract, snapshots
  and history show public contracts, and incidents are ordered by scope.
  Collectors accept the schema 1 batches of 0.3 workers while the
  topology is unchanged; upgrade collectors first, then workers, then
  viewers. 0.8 viewers read only 0.8 collectors. Schema 2 clients ignore
  snapshot fields they do not know, so later additions will not break
  them, and pass over a collector of another release rather than refuse.
- What the TUI shows is derived in one place, `internal/view`, which the
  TUI draws and the CLI prints; a test holds the two to the same rows,
  evidence, verdict and counts across every preview scenario.
- `topo preview` follows the locale, so where none of its languages is
  asked for it now shows the built-in topology in English rather than
  Chinese; `--lang zh` shows it as before.
- The reference lists every setting the TUI reads from the environment:
  `NO_COLOR`, `COLORTERM`, `TOPO_BACKGROUND`, `TOPO_HYPERLINKS`,
  `TOPO_CELL_ASPECT` and the locale variables the preview follows.
- The acceptance checklist names what to confirm on live hardware for
  the changes since 0.3.0, among them an overloaded worker, a check
  flapping past its history, storage near capacity, one collector of a
  pair answering slowly, anonymous load on a public collector, and a
  webhook outage across a restart. They are procedures, not results.

### Fixed

- Anonymous requests can no longer lock workers and viewers out of a
  collector. Every request, authenticated or not, was charged to one
  admission of 16 simultaneous requests and 300 a second before its token
  was read, so anyone who could reach a collector could have its workers'
  samples and its viewers refused with 429. Tokens are now checked first;
  a request that authenticates counts only against its own principal's
  30 a second, on every route, and the shared admission bounds the rest.
- A collector slow to answer no longer delays a worker's delivery to its
  other collector. Each round went to both at once but waited for both,
  so one that took eight seconds to time out, or answered slowly without
  failing and so was never backed off, held every round to the other as
  long. Each collector now has a delivery loop of its own, and a backoff
  counts from when a failed attempt ended rather than when it began. A
  worker with a long backlog picks each round's samples without sorting
  all of it: on 45,000 queued samples a round takes 1.7ms and 170KB
  rather than 24ms and 34MB.
- A confirmed failure stays confirmed until its check recovers, however
  long ago the samples that confirmed it were taken. A check was judged
  from the samples a collector still held, so once those that confirmed
  a failure left the history and the latest 40 samples, a check that had
  flapped ever since, never passing twice in a row, read up, and the
  incident ledger closed its incident as unconfirmed and told webhooks
  so. One healthy backend of two behind round-robin does just that. The
  samples a series lets go now leave what they established, kept for
  every pair of thresholds and written with each checkpoint, so a check
  is judged as though they were all still held, across restarts and
  threshold edits.
- The origin behind an edge is credited only with a passing response the
  edge says it fetched from it (`MISS`, `DYNAMIC`, `BYPASS`, `EXPIRED`,
  `REVALIDATED`, `REFRESHHIT` or `PASS`). Any status but a cache hit used
  to count, as did none, so the origin read up on the strength of
  answers the edge made itself: a Cloudflare Worker that sends no
  subrequest reports no cache status, and CloudFront reports
  `LambdaGeneratedResponse`. Behind a hop that reports no cache status,
  such as a plain reverse proxy, the origin is now unknown until a check
  of its own measures it.
- A worker with more checks due than it has workers runs each in turn,
  the longest overdue first. Every pass gave free workers to the checks
  listed first, so under sustained load, such as an outage with many
  checks waiting out their timeouts, those listed last never ran: with
  eight checks on one worker, five never started. A worker that comes
  free also takes the next check at once rather than at the next tick.
- A `Host` in an HTTP check's `request_headers` is sent. It was accepted
  and then dropped, since Go's client sends the URL's host whatever a
  Host header says, so a check meant for a virtual host measured the
  URL's site. A Host value that is not a host or `host:port`, and a
  header named twice in different cases, are refused.
- A `bind_ip` with an IPv6 zone (`fe80::1%eth0`) binds to it. The zone
  was validated and then lost, leaving the kernel to choose the source,
  so such a check measured from whatever address it picked. A zone that
  names no interface on the observer is now a monitor error,
  `local_bind_interface_unavailable`, and an ICMP check refuses an IPv6
  `bind_ip`, since it sends IPv4 only.
- Collectors no longer stop accepting an observer's samples once it has
  delivered its share of receipts. Receipts were kept until
  administrative archival, so of two observers, one sampling once a
  second in all was refused after about a day and a half, and the
  busiest of the preview topology's five after about eight hours. A collector paused this
  way resumes after upgrading, once its old receipts expire.
- The About globe and the geographic map keep their shape on terminals
  whose character cells are not twice as tall as they are wide; on
  Windows Terminal with a tall line height the globe was an upright oval
  and the continents stood 30% too tall. The TUI reads a cell's shape
  from the pixel size the terminal gives with its rows and columns, or
  asks for it with `CSI 16 t`, and `TOPO_CELL_ASPECT` (a cell's height
  over its width, such as `2.6`) sets it where the terminal says neither.
  Windows Terminal answers the query with a fixed 10×20 cell whatever its
  font, so there it takes the variable.
- The homepage no longer scrolls sideways on phones. On narrow screens the
  panels of its Semantics section were as wide as their widest line, up to
  65 pixels wider than the screen at 320; they now fit, and the edge
  diagram's names wrap.
- A worker no longer backs off after quarantining a sample a collector
  refused, and returns to batching with the first sample accepted; it
  used to slow to one sample per tick, with growing pauses, until its
  queue was empty.
- Endpoints are judged by the same rule as services and nodes. One whose
  checks are degraded reads degraded rather than unknown on the Topology
  tab and its server's card, and one with a confirmed failure while its
  other checks have no current evidence reads unknown rather than degraded.

### Security

- A worker token can no longer make a collector write and fsync its
  journal at will. A heartbeat was written before the store compared it
  with the one it held, so one sent again, or an older one, cost a
  journal write and an fsync under the lock every observer's ingestion
  waits on, 30 times a second per token, while changing nothing. A
  heartbeat that would change nothing is now acknowledged without being
  written.
- A viewer token can no longer make a collector redo work over the whole
  topology with each request. `/v1/config` made the public topology
  twice, and `/v1/history` and `/v1/rollups` the public topology and the
  contract of every check to answer for one, as HEAD requests did too,
  and every worker batch hashed the whole topology. What requests need of
  a topology is now worked out once when it is loaded or reloaded.
- A monitored service or Mihomo controller that is sent a secret can no
  longer hand it to viewers. Labels it answered with were kept in the
  observation every viewer reads: the edge headers an HTTP check reads
  its point of presence, region and cache status from, the exit address,
  and a controller's mode, proxy names and test URL. When a request
  carries a secret (a bearer, a private request header, a Mihomo token,
  or a proxy password for a plain HTTP target), the observation now keeps
  closed words, numbers and comparisons with the topology only: no
  `edge_pop`, `edge_region` or `exit_ip`, a `mode` of `rule`, `global`,
  `direct`, `script` or `other`, proxy names the topology declares and
  `undeclared` for the rest, no `test_url`, and `peer_text:
  withheld_secret_request`. Every check's `edge_cache` is now one of the
  statuses edges report, or absent.
- One worker token can no longer shorten another observer's long-term
  history. Rollups past the cap were shed oldest bucket first across
  every check, so an observer sending all it may could push out the
  oldest rollups of observers that held far less. A collector now sheds
  the oldest buckets of the observer whose rollups take the most room;
  each series records its observer, and keeps it through checkpoints.
- Viewers no longer see the fragment of an endpoint address or check
  target (`https://host/path#secret`), nor the user information or query
  of an address that is not a URL (`user:pass@host:22`). The public
  topology removed user information and queries only from URLs with a
  scheme, and kept every fragment. Adapter test URLs and the topology
  now share one sanitizer.
- An SSH hop's `ssh_identity_file` or `ssh_known_hosts` beginning with
  `~` is refused, as the reference already said, rather than read as a
  directory named `~`. The reference now says that a relative path is
  resolved against the worker's working directory, the security notes
  that `tailscale`, `wg` and `ssh` are found on `PATH` rather than at
  fixed paths, `SECURITY.md` that 0.8.x is the supported line, and
  `--outbox-bytes` that quarantine has a budget of its own.

## [0.3.0] - 2026-10-14

### Added

- About links to the homepage, topophany.com, and to the source on GitHub,
  written as OSC 8 hyperlinks where the terminal takes them
  (`TOPO_HYPERLINKS=0` or `1` decides outright). On short screens the
  blank rows between its paragraphs close up so the links stay.
- Where there is room, About lists the keys of the mode the UI is in
  (viewer, preview or editor) as keycaps, so `?` doubles as help.
- Tabs count what in them needs a look (failing services, paths and
  checks, open incidents, silent observers), red when anything is down and
  amber otherwise; on narrow terminals the tab's number takes the color.
- The terminal window is named after the verdict, such as
  `✖ 2 incidents · homelab · Topophany` (`✎ topology.json ●` in the
  editor), and its own title is put back on exit.

### Changed

- The TUI writes only the cells that changed since the last frame, not
  every line with a change, so animation on the maps sends a few hundred
  bytes a step instead of tens of kilobytes.
- While connecting or loading a view, the header, the status row, the
  Overview's verdict and the empty map show a turning spinner rather than
  a still `◌`, so a slow collector reads as busy, not stuck.
- Menus, forms and confirmations cast a soft shadow onto the dimmed view.
- The Overview's big number is lit from above, a shade of its color for
  each of its five pixel rows, as the About wordmark is.
- On small terminals such as 80x24 the Overview keeps the verdict's big
  number, puts the observers beside it, and when nothing needs attention
  gives the room under a short all-clear to the service matrix.
- The glints on the Overview's map run as smoothly as on the Topology
  tab, and About's globe, once its introduction has played, turns at ten
  frames a second rather than fourteen.
- Times in the evidence read alike: an incident's start, a check's last
  sample and when the collector received it are local time to the second
  (`2026-10-13 10:58:07 PDT`), and an incident's start says how long ago
  it was, rather than mixing in RFC 3339.

### Fixed

- PTY acceptance reconstructs the terminal screen before checking state, so
  incremental redraws correctly verify collector failover. CI fuzz suites use
  an execution budget instead of a short wall-clock deadline.
- Large topology diagrams have bounded layout work and an explicit fallback;
  ordinary dense diagrams retain their routing and ordering quality.
- Notices fade out, and the evidence footer identifies Enter as the back action.
- On narrow terminals the header's project and view no longer run into
  the live indicator: they end in an ellipsis before it. Before the first
  snapshot the header no longer says it was updated "never".

### Security

- Enforce per-source collector connection quotas and absolute connection lifetimes;
  close unauthenticated HTTP/1 connections while preserving authenticated reuse.
- Give viewers a public topology revision that excludes private execution settings,
  including revisions embedded in snapshots and observation history. Workers keep
  the private execution revision contract.

## [0.2.0] - 2026-10-13

### Added

- A virtual geo map, now the default when any node has a location: places
  become cards in columns from west to east and north to south, keeping
  the world's order but not its distances, so crowded sites stay legible.
  Each node has its own row, links are wires between rows (and brackets
  inside a card for links within one place), and unplaced nodes such as
  edges and the internet float as pills beside what they connect to. `m`
  cycles virtual geo, geo and network; cards fold on short maps, and links
  that find no room are counted in the legend.
- A Servers tab (`3`): a card for each server with its state, kind,
  provider and place, the services it runs and the edges in front of them,
  its addresses by network, what it hosts, who observes from it, its links,
  and its notes as a memo. Edge and platform cards list what runs there,
  what they stand in front of and where they answered from. Later tabs move
  up one: Services is `4` and About is `9`.
- Edges and platforms: nodes of kind `edge` (Cloudflare, Vercel) and
  `platform` (Supabase) with a `provider`, services with a `runtime`
  (`workers`, `pages`, `functions`, …), and endpoints that name the edges in
  front of them in `via`. Evidence is attributed along `via`: an edge's
  answer, even a 521, proves the edge; the origin counts only a passing
  response not served from the edge's cache.
- HTTP probes record which edge answered, from where and whether from its
  cache (`edge_provider`, `edge_pop`, `edge_region`, `edge_cache`).
- The preview topology puts Cloudflare in front of the main site, with a
  Worker, a Pages site and a Vercel app on Supabase, and gains two
  scenarios: the origin down behind Cloudflare, and the edge unreachable
  from the mainland.

### Changed

- The About globe reads as a sphere instead of a flat disc: a shaded ocean
  and land lit from the upper left, falling off across a soft terminator and
  toward the limb, with a graticule that curves with the surface. Truecolor
  terminals shade under the dots; 256-colour terminals keep a dotted ocean.
  The README banner is redrawn from the same art.
- The homelab example models its CDN as an edge in front of the DMIT
  origin.

### Fixed

- Avoid a data race when reading outbox delivery retry state.
- A service reached over several failing paths no longer repeats "access
  path unavailable" once per path in its reason.

### Security

- Bound Exit-IP and Mihomo JSON structure before decoding; escape bidirectional controls in CLI JSON without changing parsed values.
- Reserve outbox capacity per destination, history capacity per observer, and snapshot computation per viewer; bound connections and pre-authentication work.
- Retain durable observation-ID bindings until administrative archival and scope persisted heartbeats to their project and configuration revision.
- Bind source verification to the tested commit and file manifest; resolve release tags back to the built commit immediately before publication.
- Add regression tests for all eleven scan-bundle findings, including restart recovery, invalid acknowledgements, and moved release tags.

## [0.1.0] - 2026-10-13

Initial release.

### Changed

- The terminal UI is redesigned to be read at a glance: an Overview dashboard
  (verdict, failure count, map, what needs attention, services, observers);
  aligned tables whose state column lights up only problems; a geo map that
  clusters sites by place, colors each arc by its evidence and keeps healthy
  links quiet; a network graph drawn as a left-to-right schematic; and a
  responsive About with a one-bit dithered globe. The UI paints its own
  background (`TOPO_BACKGROUND=terminal` keeps the terminal's) and writes only
  the colors that change.
- Loading has its own calm state instead of the offline warning, the editor's
  status row lists what the draft declares, and the footer keeps the essential
  keys when the terminal is narrow.
- The README opens with a banner drawn from the About art (`make banner`) and
  shows the interface in screenshots.
- Go module path is now `github.com/WhitenWhiten/topophany`.
- Contribution guidelines moved to the repository root; added security policy,
  code of conduct, issue/PR templates, Dependabot and release automation.
- Generated verification records are no longer tracked in `docs/verification/`;
  `scripts/verify.py` writes to `.verify-build/verification/` and CI uploads them
  as an artifact. `scripts/package.py` takes `--verification` to locate them.

### Fixed

- Flaky DNS TCP-fallback test fixture when the paired TCP port was already in use.

### Security

- Isolated API resource budgets, bounded JSON and snapshot decoding, and hardened
  configuration, probe, storage and worker outbox boundaries.
- Release archives are built from an immutable checkout and verified before
  publication; executable GitHub Actions are pinned to commit SHAs.

### Added

- Single `topo` binary: remote TUI client, ingestion collector (`serve`) and
  scoped observation worker (`worker`).
- Native HTTP/TLS, DNS, TCP, UDP and ICMP probes; CONNECT/SOCKS5 proxy chains;
  adapters for local proxy and tunnel tooling.
- Single-writer append journal with checkpoints, crash recovery and
  deduplication; durable worker outbox.
- Evidence- and freshness-based state engine with dependency derivation.
- Terminal UI with geographic and network topology maps, offline preview
  scenarios and a local topology editor.

[Unreleased]: https://github.com/WhitenWhiten/topophany/compare/v1.2.0...HEAD
[1.2.0]: https://github.com/WhitenWhiten/topophany/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/WhitenWhiten/topophany/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/WhitenWhiten/topophany/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/WhitenWhiten/topophany/compare/v0.9.1...v1.0.0
[0.9.1]: https://github.com/WhitenWhiten/topophany/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/WhitenWhiten/topophany/compare/v0.8.3...v0.9.0
[0.8.3]: https://github.com/WhitenWhiten/topophany/compare/v0.8.1...v0.8.3
[0.8.1]: https://github.com/WhitenWhiten/topophany/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/WhitenWhiten/topophany/compare/v0.3.0...v0.8.0
[0.3.0]: https://github.com/WhitenWhiten/topophany/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/WhitenWhiten/topophany/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/WhitenWhiten/topophany/releases/tag/v0.1.0
