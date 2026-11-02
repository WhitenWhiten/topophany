# Real-core compatibility matrix

`mihomo.yml` runs pinned v1.19.32 and v1.19.33 on native Linux, macOS and
Windows. `fetch_mihomo.py` requires an explicit release and verifies the release
API's SHA-256 digest before extracting the executable. Downloads stay outside
the repository; each run records executable hashes and exact core version.

`accept_mihomo.py` retains the existing bounded capacity cases.
`accept_mihomo_network.py` starts a private core and synthetic loopback HTTP
origin, exercises SOCKS5 and HTTP CONNECT success, origin failure and recovery,
DNS host resolution, runtime policies and failure after stopping that core.
It never reads or modifies a production core or subscription. No delay or
healthcheck controller endpoint is called. Ephemeral logs and credentials are
removed with the private directory; only structured outcomes are exported.

The Linux CI job additionally creates a fresh network namespace, enables only
its loopback interface, and runs `--tun`. A synthetic destination is routed
through the real TUN interface into a loopback HTTP proxy and origin. Running
`--tun` in the host network namespace is refused. This tests actual packet
capture without changing the host's routes; it does not establish arbitrary
production protocol or operating-system routing compatibility.

Local macOS ARM64 receipts under `evidence/mihomo-matrix/` passed for both
versions: four inventory cases and nine network cases per version. They name
the exact binaries tested. Linux/Windows and TUN jobs are configured here but
are not claimed as executed by these local receipts. Cloudflare account
acceptance is outside this matrix.
