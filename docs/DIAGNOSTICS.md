# Paired network diagnostics

Generate either paused topology, then review addresses, observers, policies and
credentials before enabling individual checks:

```
topo templates cloudflare-diagnostics --output cloudflare-diagnostics.json
topo templates mihomo-diagnostics --output mihomo-diagnostics.json
topo validate --config mihomo-diagnostics.json
```

## Cloudflare

Run the local-origin and cloudflared checks on the origin host, and the public
entry check from the other observer. Replace the account ID, tunnel UUID,
public hostname and loopback service/metrics ports. Supply `CF_API_TOKEN` in the
worker environment with Cloudflare Tunnel Read. The two API checks share the
connector's link, but neither decides application availability. The status
read asserts `healthy`; the dedicated connections read asserts the deployment's
chosen redundancy (four is an example, not a platform requirement).

| Readings | Interpretation / next check |
| --- | --- |
| Public request fails, local service succeeds, connector reports zero sessions | Investigate the connector's edge session. |
| Public request fails, local service succeeds, connector is ready | Inspect public DNS, Access, ingress mapping and edge behavior. Readiness alone cannot isolate which one failed. |
| Both public and local service requests fail | Investigate the local application first; the public failure may have additional causes. |
| Public succeeds, local fails | The edge may serve cached content or another origin. Public success does not establish this origin's health. |
| API reports healthy but application fails | Keep the application failure; control-plane state does not overrule measured availability. |
| Connector ready facts remain but metrics are unavailable | Session evidence was obtained; metric collection failed. Missing counters are not zero. |

For a proxied origin outside Tunnel, use `topo templates cloudflare`: its origin
check pins the origin address while preserving the hostname for TLS. Verify
application semantics with the existing HTTP body/JSON assertions where a
status-only health endpoint would be ambiguous.

## Mihomo

Run the observer on the core's host. Replace controller and mixed-port addresses,
`MIHOMO_TOKEN`, root group, application URL, DNS listener and expected addresses.
Choose `expected_tun` for the deployment. The template compares the same
application over the explicit SOCKS5 listener and the system's default path.
The latter may itself use TUN; it is not labeled direct Internet access.

Exit checks use Cloudflare trace and independent `expected_exit` declarations.
Their documentation-only address must be replaced with an intended exit IP or
CIDR. The DNS check uses the actual DNS listener; declare a non-fake test name
or suitable expected answers for the core's fake-IP configuration.

| Readings | Interpretation / next check |
| --- | --- |
| Runtime policy fails, application works | Configuration drift is visible independently of current application availability. |
| Explicit listener fails, system path works | Investigate the listener or selected upstream. The system route is a separate observation. |
| Explicit listener works, system path fails | Inspect TUN/system-proxy capture and OS routing. |
| Both application requests fail | Compare DNS, exit and origin checks; do not infer one unique cause from this pair. |
| Exit policy fails | This request left through an unexpected exit; group selection alone cannot explain its actual route. |
| DNS assertion fails | Check listener configuration, resolver policy and fake-IP expectations. |

All runtime reads remain read-only. These templates do not switch selections,
reload a core, trigger health checks or invoke delay endpoints. They do not
capture connection tables or process logs. Separate samples remain non-atomic.
