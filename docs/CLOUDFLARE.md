# Cloudflare configuration

Topophany 1.4.0 combines public HTTP checks, direct application checks, local
Tunnel connector reports and read-only Cloudflare API reports. Each answers a
different question: an edge response proves that the edge answered; it does not
prove the origin is healthy. Keep a direct check of the application alongside
its public hostname and Tunnel checks.

## Start with the worked topology

Copy [`examples/cloudflare.json`](../examples/cloudflare.json) and replace the
`example.com` hostnames, documentation IP addresses, observer IDs and locations
with your deployment. It includes a proxied origin, Workers, Pages, a home
application behind a Tunnel, Access, trace and DoT checks. Only the trace target
and the public resolver are real destinations; the application hosts are
placeholders. API checks are described in the example's notes rather than
activated with invented account IDs or credentials.

```sh
cp examples/cloudflare.json topology.json
topo edit --config topology.json
topo validate --config topology.json
```

Run a worker on each declared observer. The worker that reads the connector
listener and the application's loopback endpoint must run on their host. The
[deployment guide](GUIDE.md#multi-location-and-dual-collector-deployment)
explains collector and worker startup. The JSON fragments below are check
options to merge into that topology, not standalone topology files.

## Public hostname, origin, Workers and Pages

Declare a Cloudflare node with `kind: "edge"` and `provider: "cloudflare"`.
A public endpoint on your origin names that node in `via: ["cf"]`; the origin's
direct endpoint leaves out `via`. A direct HTTP check can set
`resolve_ip: "203.0.113.10"` to dial the origin while keeping the endpoint's
hostname for Host and TLS SNI. Replace that documentation address with the
origin's real address and ensure the observer can reach it.

A Worker or Pages service names the Cloudflare node as its `node` and sets
`runtime: "workers"` or `runtime: "pages"`. See the
[entity examples](GUIDE.md#edges-and-platforms).

HTTP checks recognize `CF-Ray` colos, cache status, challenge pages and Access
login redirects. Where supplied, Cloudflare's `Server-Timing` `cfL4` entry adds
reported connection RTT, loss, retransmissions and TCP/QUIC protocol. These are
reported facts; they do not locate a failure inside the origin. `cfL4` is
optional and is absent on some zones.

Add a colo policy to an ordinary HTTP check:

```json
"http": {
  "status": [200],
  "expected_pops": ["HKG", "NRT", "SIN"]
}
```

Choose colos appropriate to your deployment. A successful response from a
reported colo outside the list is a policy violation, not an outage. No reported
colo means no colo violation. Fresh Cloudflare response colos appear as hollow
reported markers on maps. Daily rollups show colo and cache shares and separate
cached and origin-class first-byte timings; those cache classes are the edge's
labels, not independent origin measurements.

## Cloudflare Access service tokens

Configure the Access application to accept your service token, then put its
values in the environment of the worker that runs the check. Keep only the
variable names in the topology:

```json
"http": {
  "status": [200],
  "request_header_envs": {
    "CF-Access-Client-Id": "CF_ACCESS_CLIENT_ID",
    "CF-Access-Client-Secret": "CF_ACCESS_CLIENT_SECRET"
  }
}
```

Use HTTPS. A missing or empty variable is a local monitor error and sends no
request. A credential-bearing check withholds free response labels, including
HTTP status and colo, from observations; closed words such as `challenge` and
`login_redirect` can survive. As a result, it cannot supply a map colo or a
colo assertion from a withheld header. Use a separate non-secret check if you
need that visibility. An Access login redirect says Access asked for a login;
it provides no evidence that the origin application was reached.

## Local cloudflared connector

Give cloudflared an explicit loopback metrics listener, for example
`metrics: 127.0.0.1:20241` in its configuration. Point the topology's connector
check at that address and attach it to the declared Tunnel link:

```json
{
  "id": "tunnel-connector",
  "observer": "obs-home",
  "link": "tunnel",
  "kind": "cloudflared",
  "target": "http://127.0.0.1:20241",
  "interval_seconds": 60,
  "timeout_seconds": 8,
  "stale_seconds": 180,
  "failure_threshold": 3,
  "recovery_threshold": 2,
  "adapter": {
    "cloudflared": {
      "expected_connections": 4
    }
  }
}
```

`expected_connections` is your declared redundancy (1..8), not a required
Cloudflare default. Optionally add `expected_colos`, using the connector's
reported tokens. Only literal loopback targets are accepted; `localhost`,
remote listeners, proxies and credentials are refused.

The adapter reads only `/ready` and `/metrics`. Zero ready sessions is a
`tunnel_not_ready` failure; some sessions but fewer than declared is policy.
An unreachable listener, including a stopped process, is `metrics_unavailable`,
a monitor error. A fresh connector report decides the attached link after its
failure threshold; an established Tunnel still leaves the service unproven.
Keep the public hostname check and a separate loopback application check.

## Read-only Cloudflare API checks

Put an API token in the executing worker's environment, for example
`CF_API_TOKEN`, and name it with `adapter.token_env`. Grant only the Read
permission needed for each read, restricted to the relevant account or zone:

| `adapter.cloudflare.read` | Required identifiers | Read permission |
|---|---|---|
| `tunnel` | `account_id`, `tunnel_id` | Account › Cloudflare Tunnel › Read |
| `dns_record` | `zone_id`, `record_name`, `record_type` (`A`, `AAAA`, `CNAME`) | Zone › DNS › Read |
| `pool` | `account_id`, `pool_id` | Account › Load Balancing: Monitors and Pools › Read |

For `tunnel_id`, use the Tunnel's UUID, such as the placeholder
`00000000-0000-4000-8000-000000000000`. Uppercase and lowercase ASCII hex are
accepted, as are legacy 32-hex inputs; the spelling is preserved in the
topology and request path. Account, zone and pool IDs remain 32 hex characters.

For example, add this DNS check after replacing the zone ID and record name:

```json
{
  "id": "cf-dns-site",
  "observer": "obs-home",
  "kind": "cloudflare",
  "interval_seconds": 60,
  "timeout_seconds": 8,
  "stale_seconds": 180,
  "failure_threshold": 3,
  "recovery_threshold": 2,
  "adapter": {
    "token_env": "CF_API_TOKEN",
    "cloudflare": {
      "read": "dns_record",
      "zone_id": "00000000000000000000000000000000",
      "record_name": "www.example.com",
      "record_type": "A",
      "expected_proxied": true,
      "expected_content": "203.0.113.10"
    }
  }
}
```

The target defaults to `https://api.cloudflare.com/client/v4`; use an interval
of at least 60 seconds. Each check makes one allowlisted GET, follows no
redirects and sends its token only to the API. A missing token, authentication
failure, rate limit, timeout or malformed response is a monitor error, never
an application outage.

A tunnel read can declare `expected_connections` (1..64) and `expected_colos`;
a pool read can declare `expected_origins`. DNS reads can compare proxy status
and content, with the content compared in memory and never stored in samples.
Expectation mismatches are policy. An API tunnel read may attach to the same
link as the local connector report; it appears beside that report and does not
vote on the link while a fresh connector report decides it. For every field,
see the [check reference](REFERENCE.md#cloudflare-api-adapter).

## Trace, WARP, Gateway and encrypted DNS

A trace check reads the edge's account of the observer. Use a separate HTTP
check with `target: "https://www.cloudflare.com/cdn-cgi/trace"` and:

```json
"http": {
  "exit_format": "trace",
  "expected_trace": {"warp": "on", "gateway": "off"}
}
```

Declare only the expectations you need. `expected_trace.colos` can restrict
reported colos. A missing trace field is not a violation. The Observers tab
labels WARP and Gateway as reported, not verified routing.

For DoT, the example uses `target: "1.1.1.1:853"`,
`server_name: "one.one.one.one"` and
`dns: {"transport": "dot", "name": "www.example.com", "type": "A"}`.
For DoH, use an HTTPS resolver URL as the target and `dns.transport: "doh"`.
The DNS question and optional `expected_ips` assertion work the same way across
UDP, TCP, DoT and DoH. DNS checks do not support proxy chains.

## Upgrade and verification scope

Upgrade collectors and every viewer before adding the new fields, then upgrade
workers before enabling the checks. Older versions reject the new measurement
contracts. Validate your edited topology before reloading each role.

`topo preview` offers `edge-challenge`, `edge-rerouted` and `tunnel-down`
scenarios through its `x` menu. They use synthetic evidence. API fixtures in
this release are documentation-based and have not been verified against a live
account. Local connector acceptance used cloudflared 2026.10.0 with a fake,
unregistered tunnel; connected-success fixtures are synthetic. See the
[API acceptance record](acceptance/CLOUDFLARE_API_2026-10-19.md) and
[connector acceptance record](acceptance/CLOUDFLARED_2026-10-19.md) for the exact
boundaries and remaining live checks.
