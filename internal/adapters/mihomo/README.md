# Mihomo

`adapter.go` orchestrates collection, `client.go` owns response structures and
the read allowlist, `chain.go` expands selected groups and dialer proxies, and
`policy.go` implements software-specific interpretation. `config.go` validates
the typed view of the existing flat JSON adapter options.

Collection reads only `/configs`, `/proxies` and `/providers/proxies`.
`inspect.go` (used by `topo local inspect|save`) reads `/configs`, the optional
`/version` and `/proxies` once, and returns only the version, mode, TUN state
and the names and types of groups usable as `root_proxy`, never members or
selections. Nothing else may be read. Delay endpoints are excluded because some
can clear a group's fixed selection. The three reads
are not an atomic snapshot. A reported selection, TUN setting or delay does not
prove that an application request followed that route.

Provider expectations compare names **and order**. They cannot verify proxy
parameters. Unknown group shapes, cycles and ambiguous load balancing remain
unresolved. Credential-bearing controller responses may display only names
already declared by the topology; arbitrary returned names are withheld.
Raw history delay/timestamps, provider load timestamps, numeric provider counts
and exact unexpected HTTP statuses are also withheld when the controller receives
a token or an unauthenticated HTTP response can be rewritten by a credentialed
proxy. Policy matches, settlement decisions and closed status words remain;
public controllers retain their detailed diagnostic telemetry.

Default automated compatibility tests use synthetic API responses. Opt-in live
acceptance is recorded separately with exact core versions and platforms; it
does not establish compatibility with other releases. Add sanitized response fixtures and named regressions
when supporting another response shape. Never commit full proxy configurations,
tokens or real subscriber credentials.

See [recorded v1.19.32 real-core acceptance](../../../docs/acceptance/MIHOMO_2026-10-17.md)
for the exact platforms, bounded capacity cases and remaining gaps.

Run `go test ./internal/adapters/mihomo ./internal/probe`.

### Inventory capacity

Collection reads the full controller inventory, then resolves the configured
root and compares only the configured providers. `/proxies` accepts up to 4096
entries, including built-in proxies and groups. A group's `all` list and a
provider's `proxies` list each allow up to 4096 entries. Each inventory response
is limited to 8 MiB; `/configs` remains limited to 2 MiB. Other objects retain
128 keys, other arrays (including each history) retain 256 entries, and JSON
depth, duplicate-key, string, aggregate entry and container limits still apply.
Names used as object keys remain limited to 128 bytes. This is bounded full
inventory collection, not a targeted API read or an unlimited subscription size.
For cores that omit subscription nodes from `/proxies`, a missing chain also
reads `/providers/proxies` and completes the graph from unique provider node
names. The combined graph is limited to 4096 names; ambiguous names remain
unresolved. Group lists nested in provider responses use the same member limit.

Excess inventory reports `proxy_inventory_capacity_exceeded`; excess group or
provider membership reports `proxy_members_capacity_exceeded`. Bytes and other
complexity use `response_limit_exceeded` and `response_complexity_exceeded`.
These are monitor-side errors, never evidence that a proxy or business is down.
Expected membership configuration and the independent reference document retain
their existing smaller budgets. Larger actual providers can still be read and
compared, including reporting unexpected extra members.

`scripts/accept_mihomo.py --core /path/to/mihomo --topo /path/to/topo --output result.json`
starts isolated cores with synthetic file providers, disabled TUN/listeners and
health checks. It checks 1000 nodes, the 4096-entry boundary and explicit
overflow. It does not read or alter the production configuration.


Dynamic providers can opt into `adapter.provider_reference`, an independent
names-only publication. Keep fixed groups in `expected_providers` and list only
the rotating provider in the reference's `providers` array:

```json
{
  "token_env": "MIHOMO_TOKEN",
  "root_proxy": "daily",
  "expected_mode": "rule",
  "expected_providers": {"daily": ["dmit", "azure", "CDN"]},
  "provider_reference": {
    "url": "https://publisher.example/mihomo-members.json",
    "providers": ["edge-local"],
    "max_age_seconds": 10800,
    "token_env": "PUBLISHER_TOKEN"
  }
}
```

`token_env` and `ca_file` inside the reference are optional and independent of
the controller's credential and CA. The publisher must serve this JSON shape:

```json
{
  "schema": 1,
  "published_at": "2026-10-17T07:15:00Z",
  "providers": {"edge-local": ["edge-a", "edge-b", "edge-c"]}
}
```

Generate that document from the publisher's authoritative provider artifact,
not from the controller being checked. Publish it atomically alongside each
successful provider publication, preserving node order and updating the UTC
publication timestamp. It contains no proxy passwords or connection parameters.
The timestamp above is illustrative and must be replaced by the actual
publication time. Existing YAML provider files are not parsed as reference JSON;
the publisher needs to expose this names-only companion document.

References require HTTPS or direct literal loopback HTTP without proxy hops.
The check's explicit proxy path and bind address still apply; its controller
`resolve_ip` and `server_name` overrides do not apply to the publisher. Redirects
are refused, and controller credentials are never forwarded to the publisher.
`max_age_seconds` is required (1..604800). Documents are limited to 64 KiB,
16 providers, 256 members per provider and bounded JSON complexity. Names must
be nonempty, unique, free of control characters and at most 2048 bytes. Static
and dynamic providers must be distinct, with at most 16 combined per check.

`provider_reference.settle_seconds` (0..3600, default 120, and shorter than
`max_age_seconds` when set) is the window after `published_at` in which a
difference is not yet confirmed: a controller loads a new provider over a
minute or two, and every publication would otherwise report a violation while
it catches up. A difference inside the window is still a policy sample, but
`provider_reference_settling: true` and `provider_comparison` saying
`=settling` mark it as not yet confirmed. The engine opens the incident only
after the window has elapsed across as many consecutive policy samples as it
represents at the check's interval (and at least `failure_threshold`), so a publisher that keeps
republishing cannot defer it forever. Matching membership is never deferred;
`0` disables the extra wait.

`provider_reference_load_pending` records whether every known difference
could still be the controller loading the publication, independently of the
worker's configured wait. The collector applies its current `settle_seconds`
to that evidence and the publication age at `sampled_at`, so changing the
window takes effect for retained and queued samples before workers reload.
The worker's `provider_reference_settling` and comparison labels describe its
own window. Older samples without independent load evidence conservatively
wait inside the collector's current publication window; explicitly known mode
and TUN violations still use the ordinary confirmation threshold.
At the full fact budget, a true `provider_reference_settling` already supplies
the pending-load evidence, so the redundant `provider_reference_load_pending`
fact may be omitted.

Facts put the three times side by side: `provider_reference_published_at` from
the publisher, `provider_updated_at.<name>` from the controller's own
`updatedAt` for each provider that reports one, and the sample's own
`sampled_at`. Load times are optional evidence: they are added last,
reference providers first, and only while the observation stays within the
collector's 32 facts and each complete key fits 128 bytes. Longer metadata
keys are omitted; membership remains in `provider.<name>`, so a provider named
`pool.updated_at` cannot be overwritten by `pool`'s load time. The independent
load evidence remains available even when optional times are omitted.
Credential-bearing controller responses omit these raw load times entirely;
the publisher's independent time and fixed load-pending decision are retained.
A difference that outlives the window is the drift this check
exists to find; one that does not is the publication loading. A provider whose
controller reports an `updatedAt` after the publication is not still loading
it, so its difference is not marked settling. `settle_seconds` is absent from the
measurement contract, so changing it does not discard history. It is still a
topology field: upgrade every collector and worker before adding it, because
older binaries reject unknown fields.

The adapter reads the publication immediately before and after the runtime
provider read. A stable, fresh publication with different loaded members/order
still produces `policy / provider_membership_mismatch`. A publication that
changes during sampling produces `unknown / provider_reference_changed`, unless
an independent fixed policy violation is already known. Unavailable, malformed
or stale publications produce monitor errors, with no fallback to an old list
or to the controller's own membership. Publication timestamps more than 30
seconds in the future are rejected. These reads are still not an atomic
cross-system snapshot; freshness is bounded by the configured publication age.

Observations include `provider_reference_published_at` and the closed-word
`provider_reference_consistency` (`stable` or `changed`). A content fingerprint
is used only internally: an authenticated publisher could reflect its credential
in returned names, so no digest of its arbitrary text is exported to viewers.
Dynamic contents do not change the configured measurement contract, but changing
the source or freshness policy does. Dynamic names do not expand the controller
response's display allowlist. Names still cannot verify proxy parameters or an
end-to-end route. This option requires workers that support `provider_reference`;
existing checks without it keep their previous behavior.

`adapter.expected_tun` optionally asserts the reported TUN boolean. Missing
TUN state stays unknown, including when false was expected. It does not prove
that OS routes or application traffic use TUN.

`adapter.provider_match_modes` maps expected provider names to `ordered`
(default) or `set`. Use ordered mode for priority-sensitive fallback groups.
Set mode tolerates reordering but rejects duplicate members and still detects
missing/extra names. It applies to static or reference-backed expectations.
`provider_comparison` reports bounded counts and order changes for public
controllers without exporting undeclared names. Credential-bearing controllers
retain only closed comparison labels. A publication changing during sampling is labeled unverified.
The summary is truncated at the usual fact budget for very large provider names.
