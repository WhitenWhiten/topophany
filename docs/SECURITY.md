# Security Boundaries

The threat model of Topophany assumes that the network, probe targets, and proxy API responses may be untrusted, while the operating system, installed binaries, audited local configurations, and restricted credential directories are controlled by the administrator. It does not defend against local attackers who have already gained privileges of the same user or root/administrator privileges.

## Credentials and Transport

The browser viewer connects directly to a user-selected collector using a
viewer token held in its worker's memory. The token is cleared from the form
on submission, is not written to URLs, cookies or browser storage, and is
discarded with the worker on disconnect/navigation. Browser TLS trust applies;
there is no certificate bypass. Fetch omits cookies and rejects redirects
before following them. Collector reads retain the native client's project,
schema, body-budget and freshness checks. Only the collector worker's CSP
allows outbound HTTPS (and literal `127.0.0.1` HTTP for local development);
the synthetic demo retains a same-origin connection policy. A collector must
explicitly opt in to a browser origin with `--web-origin`. This permits CORS
reads, never unauthenticated evidence or ingestion, and its preflights remain
subject to the existing anonymous admission budgets. The web origin's scripts
and the user's browser/extensions are part of the trusted client environment.

The collector's `auth.json` stores only SHA-256 digests of 256-bit random bearer tokens. The viewer role is strictly read-only; the worker role can only write results within its scoped observer boundary that match the current project and configuration contract. A worker report remains evidence supplied by a trusted executor rather than hardware remote attestation; stealing a worker token allows spoofing that specific observer perspective, so tokens should be rotated immediately and the collector restarted to reload `auth.json`.

Client and executor tokens use private files or `token_env`. HTTP probe bearer tokens, environment-backed request headers and proxy passwords also reference environment variables; placing plaintext Authorization or Cookie headers directly in request header configurations is prohibited. Note that environment variables can still be read by other processes running under the same user privileges and do not constitute a full secrets management service. Do not place URLs containing tokens in notes, runbooks, screenshots, or commit histories; generic note fields cannot automatically sanitize all secrets.

`request_header_envs` names the environment variable a request header's value comes from, never the value itself; for example, a Cloudflare Access service token is sent from `CF_ACCESS_ID` and `CF_ACCESS_SECRET` without either secret entering the topology file. The executor reads the variables when it sends the request. The map is an `http` option and other check kinds refuse it. It is cleared from the public topology beside `request_headers` and `bearer_env`, so viewers never learn which variables a check uses. A `Host` entry is allowed and must resolve to a host or `host:port`; it sets the request's Host but not TLS `ServerName`, which still names the URL's host unless `server_name` says otherwise. A missing or empty variable is a monitor error and sends nothing.

The Cloudflare API adapter (`kind: "cloudflare"`) sends a bearer token named by
`adapter.token_env`. The token itself is never written in the topology: the
executor reads that environment variable when it runs the check and the
variable's name is cleared from the public topology beside every other
credential reference. Use a scoped API token holding only **Account ›
Cloudflare Tunnel › Read**, **Zone › DNS › Read** and **Account › Load
Balancing: Monitors and Pools › Read**; the adapter calls nothing else. It does
not call `/user/tokens/verify`, which would add a second request per check and
need the `User API Tokens Read` permission; a 401 or 403 on the one selected
read is reported as `authentication_failed` and is a monitor error, never an
outage. Validation refuses `interval_seconds` under 60 for this kind, so each
check asks at most once a minute, well inside Cloudflare's documented limit of
about 1,200 requests per five minutes per user (the limit is shared by that
user's tokens, so many checks on one account add up); a `429` is reported as
the monitor error `http_429`, never as an outage. Outside a literal loopback test address the target must be exactly
`https://api.cloudflare.com/client/v4` with a valid port, so a lookalike host or
another path is refused, and the client follows no redirect, so the token
cannot be forwarded to another location; an explicit path proxy may carry it only under the same rule the
Mihomo controller read follows, while no environment proxy applies.

Every Cloudflare API response is treated as credential-bearing. The adapter
keeps only closed words, numbers, shape-validated tokens and comparisons with
the declared topology, sets `peer_text: withheld_secret_request`, and never
stores a tunnel's `origin_ip`, its connection or client identifiers, a DNS
record's content, an undeclared origin name or any `errors[].message` text.
Numeric counts are the same documented numeric exception as elsewhere: a purely
numeric credential could appear verbatim in a count. The tunnel adapter withholds
free-form connector versions, since a peer can split a bearer token across
multiple version strings. The collector also removes `connector_versions` from
viewer snapshots and history of older observations. It does not rewrite stored
records. In `tunnel_colos` and `oldest_connection_at`, a value containing the
token or a run of at least eight bytes of it is dropped; shorter fragments may
be indistinguishable from three-letter colos.

POSIX credential files, the collector's TLS private key among them, require `0600` or stricter file permissions and reject static symlinks and non-regular files; a collector whose key is otherwise refuses to start. Copy a key out of a certificate tool's directory, such as Let's Encrypt's `live/` whose files are links, rather than pointing `--tls-key` at it. The parent directory must be owned and controlled by a trusted user; no defense is claimed against race-condition replacement attacks within a world-writable parent directory. Windows runtime reads rely on filesystem ACLs. Native bundle generation and installation replace and verify directory and existing child DACLs, allowing only the current user SID and necessary Administrators/SYSTEM before writing credentials. They reject unrelated owners and reparse points; they do not audit arbitrary parent-directory ACLs or defend against replacement races in untrusted writable parents. Manual deployers must confirm those same read-access limits.

Privileged Unix installation checks every existing component of installation
and data paths before mutation and rejects symbolic links, except macOS's fixed
root-owned `/etc`, `/var` and `/tmp` system aliases. Directory creation uses
verified directory handles and data ownership changes use an open descriptor.
If preparation fails, cleanup removes only empty directories created by that
attempt. Root and the administrator invoking sudo remain trusted installers.

Remote server URLs must use HTTPS with valid hostnames and CA verification. There is no `InsecureSkipVerify` bypass. Ambient environment `HTTP_PROXY` is ignored and HTTP redirects are not followed, preventing token forwarding to arbitrary servers. Literal loopback addresses may use HTTP, though API bearer tokens are still required. A malicious local user or a rogue proxy listening on loopback is not a secure substitute for TLS. The `--allow-insecure` flag is an explicit acceptance of plaintext risk, not a recommended production deployment method.

Direct profiles use the per-source `allow_insecure:true` option for the same explicit HTTP consent during authentication, discovery and resource reads. It is saved in private direct configuration, never accepted from collector topology JSON, and does not disable HTTPS certificate verification.

HTTP and Mihomo bearer credentials, an HTTP check's request headers whether literal or read from `request_header_envs`, and a query in an HTTP check's URL, which may carry a service's token, require HTTPS except for direct literal-loopback targets. A non-loopback `resolve_ip` or any proxy path removes the plaintext exception. Credentialed HTTP CONNECT and SOCKS5 are allowed only as the first, literal-loopback hop; use HTTPS CONNECT for remote credentialed hops. TLS or SSH to an earlier proxy does not encrypt a later proxy-to-proxy leg. These checks run both during configuration validation and before execution.

A peer that receives a probe or proxy credential can reflect it in a response.
Secret-bearing HTTP observations therefore retain closed provider, cache,
mitigation, protocol, Access-redirect and the four closed trace words, bounded
numbers (Cloudflare's `edge_rtt_ms`, `edge_min_rtt_ms`, `edge_retrans` and
`edge_lost`), and assertion outcomes, but omit raw HTTP status codes and other
unrestricted response labels. Every entry in `request_header_envs` marks its
request secret, `Host` included. A trace check keeps the closed `trace_warp`,
`trace_gateway`, `trace_http` and `trace_tls` words, and withholds the colo
and client country it reports in `trace_colo` and `trace_loc`, as it does
`exit_ip`; a declared colo list is still compared without the colo being
stored. Numbers cross this boundary by design: a purely numeric
credential can therefore appear verbatim in a count and rounded in an RTT
reading. That is the documented numeric exception, not a free label that
escaped withholding. An Access redirect is kept only as
`edge_access: login_redirect`; its URL, with the key ID and redirect target, is
never kept. Mihomo also withholds history delay/time, provider load times,
numeric counts and exact unexpected API statuses under the same credential
boundary. Success, failure, fixed policy comparisons and locally measured
timing remain availability signals; this policy does not eliminate covert
channels in a peer's choice of availability or timing.

Temporary overrides use `--token-stdin` or `--credential-file` (private, regular files only); raw `--credential TOKEN` arguments are rejected. An override requires `--server` when the saved profile contains multiple endpoints. It never replaces credentials across an implicit failover set. Terminal output removes every Unicode `Bidi_Control` character as well as terminal control characters, and so does everything `topo` writes to stderr, its errors and logs, but for newlines and tabs. `--json` output writes each of them, C0, DEL and the C1 controls a terminal may read as escape sequences alike, as a `\u` escape, so it reads back as the collector sent it with none of them literal. A topology value an error refuses is quoted, its controls escaped, so the error shows what is wrong with it.

Ingress diagnostics name the addresses that failed a TLS handshake before
authenticating. Viewer snapshots pseudonymize those addresses with a short
keyed digest by default, because a viewer token is read-only but not a licence
to learn every observer's or scanner's address. The key lives only in the
collector process and changes on restart, so the digest is stable across
snapshots but cannot be reversed by enumerating the IPv4 space offline.
`--expose-ingress-sources` explicitly includes the raw addresses for an
operator who needs them.

## Capability Boundaries

The optional `scripts/serve_site.py` development bridge requires the private
one-time launch link printed in its terminal. The fragment secret expires in
five minutes and is removed from browser history before it is exchanged for a
process-local viewer token. Unauthenticated pages and scripts never contain
either secret. Loopback binding and browser-origin headers alone do not
authenticate a local OS user. Restart the bridge to obtain another launch link;
keep the terminal link private like other viewer credentials.

The API provides no endpoints for modifying topology, altering proxy routing policies, reading arbitrary files, running remote commands, or restarting services. Workers load reviewed checks from local files and do not poll or receive arbitrary tasks to execute from the collector. Because probing arbitrary targets inherently grants access to networks reachable by the executor, the audited configuration file itself forms the authorization boundary; never load untrusted third-party topology files automatically.

The [remote management plan](REMOTE-MANAGEMENT-PLAN.md) proposes separate,
locally enabled configuration and software update capabilities, both disabled by
default. It is an implementation proposal, not a capability of the current
release; the boundaries described here continue to apply.

The proxy adapter reads only explicitly allowlisted endpoints (such as safe configuration and status queries), forbidding arbitrary GET endpoints from being treated as side-effect-free authorizations. Tunnel tools (`tailscale` and `wg`) and `ssh` for SSH hops are looked up on the process `PATH` and invoked directly, never through a shell, with fixed argument structures. OpenSSH is used solely for `-W` stream forwarding with strict host-key checking and never opens an interactive shell; unknown host keys are rejected and agent forwarding is disabled. Binary installation directories and system PATH must be trusted. Because tunnel interfaces like WireGuard often require elevated privileges, probe in-tunnel services where possible rather than running the entire monitoring process persistently as root.

Raw ICMP sockets require appropriate system permissions. Missing permissions are reported as monitor errors; do not grant broad administrative privileges to the entire process merely to achieve a clean dashboard. Bandwidth and probe budgets are not port-scanning authorizations. Default checks are lightweight and do not offer throughput stress testing or automated network-wide discovery.

All network responses, API requests, historical records, terminal inputs, and outbox queues are bounded. Disabling HTTP redirects and connection reuse carries explicit diagnostic semantics. Probing through a proxy cannot guarantee that every request establishes a brand-new underlying tunnel connection. Standard HTTP GET and HEAD requests may still trigger unintended side effects on poorly implemented endpoints, so health check targets must be reviewed and approved by administrators.

## Storage and Failures

Installer rollback accepts only `.backups/install-*` directories inside the
original installation. A private per-installation key authenticates the record,
its exact backup path, service state and saved-file digests. All saved bytes are
verified before stopping a service or changing targets. POSIX backup directories
and files must belong to the invoking installer; privileged installation and
rollback require root-controlled directory chains, including the installation
directory. A sudo caller's ownership alone is insufficient. Windows verifies
the installation's restricted ACLs and ownership before reading a backup.
Keep `.backups/rollback.key` private and retain it with its installation; copied,
moved, modified and unsigned legacy backups are refused. For legacy recovery,
review and generate an installation bundle rather than importing a rollback
record as a privileged service definition.

Log checksums detect accidental corruption; they are not keyed cryptographic signatures and do not prevent a local attacker from recalculating checksums. Power-loss guarantees for fsync and atomic renames depend on the operating system and the underlying local filesystem; single-process crash recovery tests do not equate to power-loss simulations across all disks, RAID controllers, or virtualization platforms. Directory fsync and ACL semantics on Windows differ from POSIX platforms, and identical Linux power-loss guarantees are not claimed on Windows.

Do not share database or log directories across NFS or SMB network filesystems. The system does not implement automated multi-master cluster management. Stale results, clock skew, and missing sources must be preserved as-is rather than having timestamps rewritten to make dashboards look clean. Log files, quarantine stores, and history buffers may contain internal addresses and service names, and must be protected with the same care as topology files.

History drawn under a snapshot uses that snapshot's collector, public revision
and check contract. A collector changing configuration between these reads
produces unavailable history until the view refreshes. Switching snapshot
collectors clears cached samples and charts; it does not merge independently
retained histories. Standalone `topo history` retains ordered read failover and
reports the source of its own response.

When reporting security issues, provide only sanitized reproduction configurations and observation logs. Do not paste production tokens or sensitive infrastructure details into public issues. Use private vulnerability reporting channels on the hosted repository.
