<h1 align="center">
  <img src="docs/assets/banner.png" alt="Topophany — the appearance of a place" width="100%">
</h1>

<p align="center"><b>Self-hosted network &amp; uptime monitoring in your terminal — see what's down, <em>from where</em>, and why.</b></p>

<p align="center"><i>From where, via which path, reaching what — and on what evidence.</i></p>

<p align="center">
  <a href="https://topophany.com/demo/"><img src="https://img.shields.io/badge/%E2%96%B6%20Live%20demo-Try%20it%20in%20your%20browser-5eead4?style=for-the-badge&labelColor=0a1016" alt="Live demo: try it in your browser"></a>
</p>

<p align="center">
  <a href="https://topophany.com"><b>topophany.com</b></a> ·
  <a href="https://github.com/WhitenWhiten/topophany/releases">Download</a> ·
  <a href="docs/GUIDE.md">User guide</a>
</p>

<p align="center"><sub><b>English</b> · <a href="README.ja.md">日本語</a> · <a href="README.zh.md">简体中文</a></sub></p>

<p align="center">
  <a href="https://github.com/WhitenWhiten/topophany/actions/workflows/ci.yml"><img src="https://github.com/WhitenWhiten/topophany/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/WhitenWhiten/topophany"><img src="https://pkg.go.dev/badge/github.com/WhitenWhiten/topophany.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
  <a href="https://linux.do"><img src="https://img.shields.io/badge/LINUX%20DO-Community-FFB003?logo=linux" alt="LINUX DO"></a>
</p>

<p align="center">
  <img src="docs/assets/outage.en.gif" alt="An outage arriving on the Overview: the scenario picker opens, checks fail sample by sample until five incidents are open and the failing routes turn amber on the map, each problem's route lights up as it is selected, and the evidence for one incident opens; then the geo map fills the screen, switches between place cards and points, and returns to the panels" width="100%">
  <br><sub>Press <code>x</code> in <code>topo preview --lang en</code> and home loses the path to the VPS in Los Angeles. Select a problem and its route lights up; <code>Enter</code> shows the evidence. Then the geo map takes the whole screen (<code>f</code>) and trades its place cards for points (<code>p</code>).</sub>
</p>

**Topophany** is a self-hosted monitor for your machines, the paths between them and the services they run, viewed in a terminal. Workers check from the places you actually connect from (home, the office, a VPS abroad). The UI shows what is broken, seen from where, and the evidence for it. All of it is one Go binary, `topo`: worker, collector and client. No CGO, no database, zero external Go modules.

## Who it's for

- **A homelab, a VPS or two, and something behind Cloudflare.** One screen for the box under the desk, the machine in another city, and the site the CDN answers for — so a failure from home is not reported as “the server is down”.
- **Anyone tired of a single probe.** Down from Seattle but fine from New York is a fault on Seattle's path. Topophany keeps “what failed”, “seen from where” and “how fresh” as separate facts. Nothing turns green without a passing check; a service nobody checks stays `unknown`.
- **People who live in a terminal and on overlays.** Proxy chains (SSH `-W`, HTTP/HTTPS CONNECT, SOCKS5), plus read-only Tailscale, WireGuard and Mihomo, in the same static binary you already run as the TUI.

<p align="center">
  <img src="docs/assets/overview.en.png" alt="The Overview during a simulated outage: the virtual geo map, its places as cards from Tokyo to Frankfurt with the failing route drawn in amber, a status card reading 5 incidents, the list of what needs attention, and the services grouped by application with the proxy chain marked for attention" width="100%">
  <br><sub>The Overview during a simulated outage, from <code>topo preview --lang en --scenario 1</code>.</sub>
</p>

## How is this different?

Not a ranking. [Uptime Kuma](https://github.com/louislam/uptime-kuma) and [Gatus](https://github.com/TwiN/gatus) are stronger at public status pages and huge notification lists. [SmokePing](https://github.com/oetiker/SmokePing) is the latency historian. Topophany is for seeing the same service from several places, with a declared topology, in a terminal.

| | Topophany | Uptime Kuma | Gatus | SmokePing |
|---|---|---|---|---|
| **Job** | Topology + availability, in a terminal | Service uptime, in a browser | Endpoint health dashboard | Latency and loss graphs |
| **UI** | TUI (braille world map, network graph) | Web UI + status pages | Web dashboard + status pages | Web graphs |
| **Install** | One Go binary; no database daemon | Docker/Node; SQLite or MariaDB | Go binary or Docker; memory, SQLite or Postgres | Perl + rrdtool |
| **Where checks run** | Named workers you place (home, office, a VPS) | The instance's host; also push monitors | The process's host; also push/external endpoints | Master, plus optional remote slaves |
| **Path vs outage** | Down from one observer, up from another → path fault, not “server down” | Each instance reports its own up/down | Each instance reports its own up/down | Compares latency from each slave; not a service-health model |
| **Unchecked / stale** | Unconfigured = `unknown`; cached views say cached; never green without a passing check | Status is the last result of a configured monitor | Status is the last result of a configured endpoint | Graphs probed targets only |
| **CDN / origin** | Edge headers (Cloudflare, Vercel, CloudFront, Fastly, Netlify); a 521 proves the edge, not the origin | HTTP status is the monitor target | HTTP status is the endpoint | Not applicable |
| **Proxy / overlay** | HTTP/HTTPS CONNECT, SOCKS5, SSH `-W` (up to 12 hops); read-only Tailscale, WireGuard, Mihomo | HTTP(S) proxy for some monitors | HTTP proxy | Probe-specific |
| **Alerts** | ntfy, Slack, Discord, JSON | 90+ notification services | Slack, PagerDuty, Discord, and more | Email / classic |
| **Public status page** | No | Yes | Yes | No (graphs) |

[trippy](https://github.com/fujiapple852/trippy) is an interactive traceroute TUI for diagnosing a path right now, not a 24/7 monitor. [Netdata](https://github.com/netdata/netdata) is per-second host and infrastructure metrics. Topophany replaces neither.

## Quick start

Pre-built archives are on the [releases page](https://github.com/WhitenWhiten/topophany/releases) for Linux, macOS (`darwin`) and Windows, amd64 and arm64. Each release includes `SHA256SUMS.txt`. Unpack and run `topo` (`topo.exe` on Windows). There is no Docker image or Homebrew formula yet.

Or, with **Go 1.26.9** or newer:

```sh
go install github.com/WhitenWhiten/topophany/cmd/topo@latest
```

See the whole UI on synthetic evidence, with nothing to set up, and press `x` to break something. The built-in network and its scenarios are written in English, Japanese and Chinese and follow your locale; `--lang ja` picks one:

```sh
topo preview
```

Run a real collector on this machine, with a demo topology and fresh credentials:

```sh
topo init --dir ./demo
topo serve --config ./demo/topology.json --auth ./demo/auth.json \
  --data ./demo/collector-data --observer demo

# in another terminal
topo --credentials ./demo/credentials.json
```

Hardened systemd units for Linux are in [`deploy/`](deploy/).

## Let your agent write it

The slow part of any monitor is describing what to monitor, and that is work a coding agent does well. Ask the one you already use (Claude Code, Codex, Gemini CLI, Cursor or any other that can run commands) to read what your machines already say about themselves and write the topology. It checks its own work with `topo validate`, and you look the result over in `topo preview` before anything runs. A prompt to start from:

> Write a Topophany topology of my network to `topology.json`. Learn the format from `topo preview --print-topology` and docs/REFERENCE.md in github.com/WhitenWhiten/topophany. Read my SSH config, `tailscale status`, and the Compose files and reverse-proxy configs you can find. Declare each machine with where it is, the services on it, the applications they belong to and the addresses they answer on, add checks from the machines I will run workers on, and write what you learn about each machine into its notes. Never put a secret in the file. Run `topo validate --config topology.json` until it passes, then tell me what you found and what you guessed.

Once it is written, it keeps working for both of you:

- **A memo of your network.** The file says what runs where and how each machine is reached, with notes and runbooks beside them. Your agent reads it before touching anything, and you review its edits as a diff, like code; `topo edit` changes it in a TUI.
- **A quick interface.** `topo status`, `topo show` and `topo history` answer in a line, or in JSON with `--json`: is anything wrong, what runs on that server, how a check fared overnight. Before any collector runs, `topo preview --config topology.json show servers` answers from the file alone.

## How it works

```text
  WORKERS                          COLLECTORS                  CLIENTS
  measure from where they are      keep the evidence           read it

  topo worker   (home laptop)  ─┬─▶  topo serve  (A)  ─┬─▶  topo          the TUI
  topo worker   (Tokyo server) ─┴─▶  topo serve  (B)  ─┴─▶  topo status   for scripts and agents
```

- **Workers** run the checks in a reviewed local topology file and deliver what they observe to one or two collectors, buffering on disk while the network is down.
- **Collectors** keep an append-only journal (fsync before acknowledging) and work out health, incidents and freshness. They remember each incident from opening to close and how each check fared for months, can post incidents to webhooks (ntfy, Slack, Discord or JSON), and can run checks of their own.
- **Clients** only read. Closing the TUI does not stop monitoring, and nothing in Topophany changes routes, proxies or services.

| Command | What it does |
|---|---|
| `topo` | Open the TUI on a remote collector, using saved credentials |
| `topo status` / `topo show TAB [ID]` | Print what the TUI shows, as text or `--json`; `status` answers in its exit status too |
| `topo login` | Save and verify a connection to a collector |
| `topo serve` | Run a collector, optionally with checks of its own |
| `topo worker` | Run checks from this machine and report them |
| `topo edit` / `topo validate` | Edit a topology file in a TUI / check it and print its revision and contracts |
| `topo preview` | Open the TUI on synthetic evidence |
| `topo init` | Create a local demo with fresh credentials |

Run `topo help` for the full list.

## Monitor your own network

1. **Describe it.** A topology in JSON lists nodes, links, endpoints, services, paths, observers and checks. Have your agent write it as above, or start from [`examples/homelab.json`](examples/homelab.json) and edit it with `topo edit --config topology.json`. Either way, `topo validate` checks it.
2. **Run a collector** where your devices can reach it, with TLS: `topo serve --config topology.json --auth auth.json --data collector-data --listen 0.0.0.0:7447 --tls-cert server.pem --tls-key server-key.pem`
3. **Run a worker** on every machine you want to measure from: `topo worker --config topology.json --observer <id> --credentials worker-credentials.json --data worker-data`
4. **Look at it** from anywhere: `topo login --server https://your-collector:7447 --project <name>`, then just `topo`.

The [operations guide](docs/OPERATIONS.md) covers dual collectors, systemd units and limits, and the [security model](docs/SECURITY.md) covers tokens and transport. Breaking changes for existing installs are in [UPGRADING.md](UPGRADING.md).

## What it checks

- **Native probes:** HTTP (status, body, headers, egress IP), TCP, TLS with certificate validation, SSH banner, DNS A/AAAA, UDP echo, ICMP (IPv4).
- **Host resources (Linux / macOS / Windows):** CPU, memory, filesystem capacity (and inodes on Unix), process counts and selected-process RSS, with optional resource and process-count assertions. See [`examples/host-resources.json`](examples/host-resources.json).
- **Proxy chains:** HTTP CONNECT, HTTPS CONNECT, SOCKS5 and OpenSSH `-W`, chained over up to 12 hops.
- **Read-only adapters:** Mihomo, Tailscale, WireGuard.
- **Edges and platforms:** Cloudflare (including Workers and Pages), Vercel and Supabase as topology nodes; HTTP probes also read Cloudflare, Vercel, CloudFront, Fastly and Netlify response headers. An answer from the edge proves the edge, not the origin behind it.

## The terminal UI

Nine tabs: **Overview**, **Topology** (with three maps: virtual geo, geo and network), **Servers**, **Services**, **Paths**, **Checks**, **Incidents**, **Observers** and **About**. Services fold into the applications you declare and checks into the services they verify; `b` browses either by application, environment or host.

<p align="center">
  <img src="docs/assets/views.en.gif" alt="The Overview viewed from the MacBook in Seattle, where the transit path is down, then from the server in New York, where it is fine; then the Topology tab's virtual geo map, the geo map filling the screen and switching between place cards and points, and the network graph" width="100%">
  <br><sub>Down from Seattle, fine from New York: each observer reports only what it can see (<code>v</code>). Then the three maps (<code>m</code>): virtual geo, geo and network, the geo map taking the whole screen (<code>f</code>) and trading its place cards for points (<code>p</code>).</sub>
</p>

| Keys | |
|---|---|
| `1`–`9`, `Tab` | switch tabs |
| `j` / `k`, arrows | move through the list |
| `Enter` | show the evidence for the selected row |
| `/` | filter |
| `v` | switch the observer you view from |
| `m` / `z` / `f` / `p` | cycle the map / zoom it / show it alone / geo markers and dashed arcs |
| `s` | line the server cards up at one size |
| `b` | browse services and checks by application, environment or host |
| `q` | quit |

The mouse works too. Colors adapt to truecolor, 256-color and `NO_COLOR` terminals. The [user guide](docs/GUIDE.md) describes every tab and map, and how to read all of it from scripts and agents with `topo status`, `topo show` and `topo history`.

## Documentation

| | |
|---|---|
| [User guide](docs/GUIDE.md) | The TUI, editing, preview mode, semantics, probes, multi-location deployment |
| [Configuration and API reference](docs/REFERENCE.md) | Every entity, probe parameter and API endpoint |
| [Operations](docs/OPERATIONS.md) | Dual collectors, storage, recovery and resource limits |
| [Security model](docs/SECURITY.md) | Credentials, transport and capability boundaries ([review](docs/SECURITY-REVIEW-2026-10-14.md)) |
| [Scenarios](docs/SCENARIOS.md) · [Acceptance](docs/ACCEPTANCE.md) | What the tests prove, and what needs checking on real hardware |
| [Upgrading](UPGRADING.md) · [Changelog](CHANGELOG.md) | Breaking changes for existing installs, and release history |

## Repository layout

```text
cmd/topo        the single binary
internal/       model, config, probes, scheduler, journal, engine, API, client, outbox, view, TUI, preview
examples/       sample topologies and credential templates
deploy/         hardened systemd units for the collector and the worker
docs/           guides, reference and verification records
scripts/        acceptance and verification suites
site/           the topophany.com homepage (a separate module, not part of go install)
```

## Development

```sh
go test ./...                # unit and integration tests
go test -race ./...
python3 scripts/verify.py    # the full acceptance suite (Linux, Go, Python 3)
make site                    # re-record the homepage's TUI replays and rewrite its pages
```

Building needs no module downloads. Read [CONTRIBUTING.md](CONTRIBUTING.md) and the [Code of Conduct](CODE_OF_CONDUCT.md) before sending changes, and report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)

If Topophany helped you find a broken path, a ⭐ on this repository helps others find it. Bugs and ideas: [open an issue](https://github.com/WhitenWhiten/topophany/issues/new/choose).
