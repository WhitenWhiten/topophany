# CF-Server-Monitor external source

The `cf-server-monitor` adapter reads host resources from a CF-Server-Monitor
Worker. The upstream host Agent sends metrics to that Worker. Topophany uses
the Worker's read API. [CF-Server-Monitor architecture](https://github.com/huilang-me/CF-Server-Monitor/blob/main/README.md#项目简介)

## Configure and run

Generate a paused topology with `topo templates cf-server-monitor --output cfsm.json`
or start with [the example](../../../examples/cf-server-monitor.json).

1. Set `sources[].target` to the Worker base URL. Use HTTPS for a remote Worker.
   A simple reverse-proxy path prefix is supported. If the dashboard is hosted
   separately, use its configured `API_BASE` Worker origin.
2. Set `external.object` to the server's lowercase UUID from CF-Server-Monitor.
   Use `report: "resource"` and `primary_disk: "aggregate"` for the disk total
   reported across the monitored host.
3. For a public Worker, omit `credential_env`. For a private Worker, name an
   environment variable containing a pre-issued raw JWT, for example
   `TOPO_CFSM_JWT`. The adapter sends `Authorization: Bearer <jwt>`. The upstream
   login token has administrator authority and expires. Update the environment
   and restart the reader when replacing it. The Agent's `API_SECRET` is for
   ingestion. An API gated by Turnstile requires separate access arrangements
   for automated readers. [Upstream authentication](https://github.com/huilang-me/CF-Server-Monitor/blob/612027cbdb248bc2df7e69f9d01b5cefb4d14fa8/API.md)
4. Review the source and enable its check. Validate the topology and try a
   one-shot read from the configured observer.

```sh
topo validate --config cfsm.json
topo once --config cfsm.json --observer cfsm-reader
```

For recurring reads with a collector, run that observer with `topo worker`
or with `topo serve --observer cfsm-reader`. Use one unit of `max_pages` and `max_objects` per
distinct UUID in a source. The controlled transport applies the existing source
timeout, body, route and TLS limits to every read.

For the collector-free TUI, run `topo setup --task direct` and choose
`cf-server-monitor`. Once the profile is saved, open
`topo --source direct --profile desktop --page servers` with your profile
name. [Direct configuration](../../../docs/DIRECT.md#configure-cf-server-monitor)

## Resource evidence

The supported response is the flat JSON object from
`GET /api/server?id=<uuid>`. The mapping targets upstream commit
[`612027c`](https://github.com/huilang-me/CF-Server-Monitor/tree/612027cbdb248bc2df7e69f9d01b5cefb4d14fa8).
The Worker builds the object from its latest persisted metrics for that server.
The adapter maps the following values. [Upstream API](https://github.com/huilang-me/CF-Server-Monitor/blob/612027cbdb248bc2df7e69f9d01b5cefb4d14fa8/API.md#23-get-apiserver---获取单台服务器详情) · [Worker handler](https://github.com/huilang-me/CF-Server-Monitor/blob/612027cbdb248bc2df7e69f9d01b5cefb4d14fa8/src/handlers/dashboard.js#L181)

| Upstream fields | Topophany values |
| --- | --- |
| `cpu`, `cpu_cores` | CPU percent and optional logical core count |
| `ram_total`, `ram_used` | Memory bytes and percent when both counters are usable |
| `disk_total`, `disk_used` | Disk bytes and percent for the `aggregate` disk |
| `net_in_speed`, `net_out_speed` | Receive and transmit rates in bytes per second |
| `swap_total`, `swap_used` | Swap capacity and usage converted from MiB to bytes |
| `load_avg` | Three space-separated load averages for 1, 5 and 15 minutes |
| `last_updated` | Upstream sample time in Unix milliseconds |

The upstream Agent uses MiB for the API's MB fields. Conversion rounds to
the nearest byte. Missing or invalid counters stay unavailable. The adapter
preserves a reported CPU value of zero. Zero total capacity means no usable
RAM or disk capacity. A zero swap total and zero usage describe a host with
zero swap capacity as reported by the Worker. The Worker uses zero defaults
for some metrics, including rates and swap. A server with an unset or zero `last_updated` provides source contact
only. The sample ID is stable for a UUID and `last_updated` so repeated reads
retain the original deadline. A `401`, `403`, `404` or other read error records
a source collection failure. Host resources are reported evidence, independent
of measured application reachability. [Upstream Agent units](https://github.com/huilang-me/cfsm-agent/blob/85208b81487e919aa8585e538ae82710e90961d2/internal/cfprobe/metrics_common.go)
