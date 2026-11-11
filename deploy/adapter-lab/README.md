# OrbStack real-service adapter acceptance

Run from the repository root on macOS with OrbStack, Docker Compose,
Go (the version in `go.mod`) and Python 3.12 or newer:

```sh
python3 scripts/accept_orbstack.py
```

To keep sanitized evidence at a predictable location:

```sh
python3 scripts/accept_orbstack.py --output .verify-build/adapter-evidence
```

An optional subset saves time when investigating a particular integration:

```sh
python3 scripts/accept_orbstack.py --adapters cfsm tailscale
```

The default runs all seven adapters. A Tailscale subset also runs the
WireGuard peer prerequisite. A nonzero exit code means some acceptance or
cleanup condition failed. Passing scenarios can deliberately contain `error`,
`unknown`, `policy` or `fail` observations: the assertions check the expected
classification, rather than accepting any error as evidence of success.

The script explicitly selects the `orbstack` Docker context without changing
the user's selected context. Each invocation creates a private directory
under `.verify-build/` and a unique Compose project. It builds the current
production CLI for the VM's Linux architecture. The Komari and CFSM Agent
release downloads are checked against GitHub's SHA-256 asset digests. The
CFSM Worker is built from the exact supported upstream commit and its lockfile;
its application handlers are not replaced or mocked. Docker image IDs,
upstream artifacts, CLI and harness hashes are recorded in `summary.json`.

## Host network isolation

Runtime tests must not change the user's computer's network configuration.
The lab enforces the following conditions before testing:

- Docker networking is internal, with no published host ports or external
  egress. No service uses the host network or host PID namespace.
- No container is privileged. Only the runner, peer and TUN test receive
  container-scoped `NET_ADMIN`; the runner and peer also have `NET_RAW`.
- No Docker socket or host `/proc`, `/sys`, `/dev` or root filesystem is
  mounted. Only `/dev/net/tun` from the OrbStack Linux VM is passed as a device.
- WireGuard interfaces and Tailscale routes exist only in the two container
  network namespaces. Tailscale uses the lab's Headscale control plane,
  refuses DNS and advertised route changes, disables netfilter management
  and does not use a public DERP map.
- Mihomo's TUN scenario uses a third container with `network_mode: none`.
  It shares the runner's *container* PID namespace so the existing acceptance
  guard can verify that its network namespace differs from `/proc/1/ns/net`.
  It has no access to the macOS network stack.

The three resource services share the runner's container network namespace,
allowing the production adapters to use literal loopback HTTP targets without
relaxing the project's credential transport policy. Nothing is bound on macOS.

Downloads and Docker builds use normal internet access before the runtime
tests. The script reads macOS's default route, DNS, system proxy and interface
list before and after the run, and requires each to remain unchanged. Private
configuration snapshots and process logs stay in the private work directory;
only comparison results enter the sanitized evidence.

## Coverage and boundaries

| Adapter | Actual upstream software | Acceptance |
| --- | --- | --- |
| Beszel | Hub and Agent 0.21.0 | Agent-generated raw `1m` record, metric units/values, record identity/time, paused status, invalid-token empty result, stop/restart |
| Komari | Server 1.6.0 and Agent 1.5.11 | Real Agent reports, exact wire bytes/receive time, optional metrics, public/private/hidden node reads, stop/restart and recent-cache loss |
| CF-Server-Monitor | Worker `612027c` and Go Agent 1.1.3 | Original Worker on workerd with local D1 and Durable Objects; real Agent ingestion, MiB conversions, optional metrics, private JWT reads, stop/restart |
| Mihomo | 1.19.33 | Controller inventory/budgets, real HTTP CONNECT and SOCKS5 traffic, DNS, origin failure/recovery, stopped core, isolated TUN capture |
| WireGuard | Linux kernel and `wg` | Two peers, real handshakes and encrypted transfer, AllowedIPs/keepalive assertions, absent peer/interface |
| Tailscale | 1.90.9 with Headscale 0.29.7 | Two real daemons, online status, direct DISCO and TSMP replies, absent peer, stopped backend, recovery, daemon unavailable |
| cloudflared | 2026.10.5 | Original connector's `/ready` and metrics with zero edge sessions; stopped connector classified as a monitor error |

Every resource adapter output is compared with the same immutable sample
returned by the upstream service after its real Agent is stopped. This checks
mapping without a race against the next sample. Omitted upstream fields remain
unavailable. Repeated reads must retain sample identity/time. Service restarts
must retain persisted sample time; Komari's in-memory recent window instead
becomes empty, and a newly started Agent must supply new evidence.

The lab does not certify cloudflared's connected-edge branch, Cloudflare
account APIs, CDN-specific production headers, public DERP or peer relays.
Those require separate authorized external environments. The monitored host
metrics describe the disposable OrbStack Linux environment. The tests make no
claim about production deployments or all future upstream versions.

Containers, networks and volumes belonging to this invocation are removed
on exit, including failure. Images and build/download caches remain available
for subsequent runs. If the script is killed without an opportunity to clean
up, use the unique project name from its private `runner.log` with
`docker --context orbstack compose -p NAME -f deploy/adapter-lab/compose.yaml down --volumes`,
setting `LAB_ROOT` and `LAB_DEPLOY` to that run's work directory and this
directory. Never use an unrelated project's name or a global prune command.
