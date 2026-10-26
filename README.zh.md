<h1 align="center">
  <img src="docs/assets/banner.png" alt="Topophany — 场所的显现" width="100%">
</h1>

<p align="center"><b>从哪里出发，经由哪条路径，抵达了什么——依据又是什么。</b></p>

<p align="center">
  <a href="https://topophany.com/zh/demo/"><img src="https://img.shields.io/badge/%E2%96%B6%20在线演示-在浏览器中试用-5eead4?style=for-the-badge&labelColor=0a1016" alt="在线演示：在浏览器中试用"></a>
</p>

<p align="center">
  <a href="https://topophany.com/zh/"><b>topophany.com</b></a> ·
  <a href="https://github.com/WhitenWhiten/topophany/releases">下载</a> ·
  <a href="docs/GUIDE.md">用户指南（英文）</a>
</p>

<p align="center"><sub><a href="README.md">English</a> · <a href="README.ja.md">日本語</a> · <b>简体中文</b></sub></p>

<p align="center">
  <a href="https://github.com/WhitenWhiten/topophany/actions/workflows/ci.yml"><img src="https://github.com/WhitenWhiten/topophany/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/WhitenWhiten/topophany"><img src="https://pkg.go.dev/badge/github.com/WhitenWhiten/topophany.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
  <a href="https://linux.do"><img src="https://img.shields.io/badge/LINUX%20DO-社区认可-FFB003?logo=linux" alt="LINUX DO"></a>
</p>

**Topophany** 把整张网络摆在一块屏幕上：有哪些机器、各在何处，彼此之间走哪条路，上面跑着哪些服务。worker 从你真正上网的地方（家里、公司、海外的 VPS）发起检查，终端界面告诉你：哪里出了问题，是从哪里看到的，凭什么这么说。这一切都装在一个可以自托管的 Go 二进制文件 `topo` 里。

<p align="center">
  <img src="docs/assets/outage.zh.gif" alt="Overview 上故障来临：场景选择框打开，检查随样本陆续失败，直到 5 个事件打开、故障路径在地图上变成琥珀色；每选中一个问题，它的路径就会亮起，最后打开一个事件的证据；接着地理地图铺满全屏，在地点卡片和圆点之间切换，再回到面板" width="100%">
  <br><sub>在 <code>topo preview --lang zh</code> 里按 <code>x</code>，家里到洛杉矶线路机的连接就断了。选中一个问题，它的路径就会亮起；按 <code>Enter</code> 查看证据。接着地理地图铺满全屏（<code>f</code>），地点卡片换成圆点（<code>p</code>）。</sub>
</p>

## 为什么选择 Topophany

- **不只知道坏了，还知道坏在哪。** 每项检查都从你命名的观测点发起。从成都不通、从纽约正常，说明出问题的是成都那条路径；Topophany 会如实报告这一点，而不是一口咬定服务器宕机。
- **讲证据，不只看颜色。** 健康状态、证据来源和新鲜度是彼此独立的字段。没有通过的检查就不会变绿，没人检查的服务保持 `unknown`，显示的是缓存数据也会明确标出来。
- **一份网络备忘录。** 一个审阅过的 JSON 文件，写清什么跑在哪里、每台机器怎么连上、每条路径走哪条代理链，旁边还有你的笔记和处置手册。你看它，你的 Agent 看它，监控也照着它来检查。
- **好看，也耐看。** 盲文点阵的世界地图会把检查得到答复的 Cloudflare 机房（colo）画在它的位置上；保留地点先后顺序的示意地图、网络拓扑图、每台服务器一张卡片，服务按所属的应用归类。任何终端都能跑，也支持鼠标。
- **一个文件，别无依赖。** 不需要 CGO、数据库守护进程或 Node.js，也不依赖任何外部 Go 模块。

<p align="center">
  <img src="docs/assets/overview.zh.png" alt="模拟故障时的 Overview：虚拟地图上从东京到法兰克福的各个地点排成卡片，故障路径以琥珀色画出；状态卡片显示 5 个事件，旁边是需要关注的项目列表，以及按应用归类的服务，其中代理线路被标为需要关注" width="100%">
  <br><sub>模拟故障时的 Overview，来自 <code>topo preview --lang zh --scenario 1</code>。</sub>
</p>

## 一分钟上手

从[发布页面](https://github.com/WhitenWhiten/topophany/releases)（Linux、macOS、Windows · amd64、arm64）下载，或者用 Go 1.26.10 及以上版本安装：

```sh
go install github.com/WhitenWhiten/topophany/cmd/topo@latest
```

无需任何配置，就能用模拟数据浏览完整界面；按 `x` 制造一场故障试试。内置的网络和故障场景有中文、英文和日文三个版本，默认跟随系统语言，也可以用 `--lang zh` 指定：

```sh
topo preview
```

也可以用演示拓扑和新生成的凭据，在本机跑一个真正的采集器：

```sh
topo init --dir ./demo
topo serve --config ./demo/topology.json --auth ./demo/auth.json \
  --data ./demo/collector-data --observer demo

# in another terminal
topo --credentials ./demo/credentials.json
```

## 交给 Agent 来写

做监控最费工夫的一步，是把要监控的东西描述清楚，而这恰好是编程 Agent 擅长的事。让你平时用的 Agent（Claude Code、Codex、Gemini CLI、Cursor，或任何能执行命令的 Agent）去读机器上现成的信息，替你写出拓扑。它会用 `topo validate` 自查，你再用 `topo preview` 过目，确认无误后才真正运行。可以从这段提示词开始：

> 请把我的网络写成 Topophany 拓扑，保存到 `topology.json`。格式参考 `topo preview --print-topology` 的输出，以及 github.com/WhitenWhiten/topophany 里的 docs/REFERENCE.md。读取我的 SSH 配置、`tailscale status`，以及你能找到的 Compose 文件和反向代理配置：为每台机器写明所在位置、上面跑的服务、服务所属的应用和监听的地址，再从我要运行 worker 的机器发起检查，并把你了解到的情况写进各台机器的 notes。不要把任何密钥写进文件。反复运行 `topo validate --config topology.json` 直到通过，然后告诉我哪些是查到的、哪些是推测的。

写好之后，你和 Agent 都能一直用下去：

- **网络备忘录。** 这份文件写清了什么跑在哪里、每台机器怎么连上，旁边附有笔记和处置手册。Agent 动手之前先读它；它改了什么，你像审代码一样看 diff。也可以用 `topo edit` 在 TUI 里修改。
- **随手可查的接口。** `topo status`、`topo show`、`topo history` 用一行字回答，加上 `--json` 就输出 JSON：现在有没有问题、那台服务器上跑着什么、某项检查昨晚表现如何。还没有采集器的时候，`topo preview --config topology.json show servers` 也能直接根据文件作答。

## 工作原理

```text
  WORKERS                          COLLECTORS                  CLIENTS
  measure from where they are      keep the evidence           read it

  topo worker   (home laptop)  ─┬─▶  topo serve  (A)  ─┬─▶  topo          the TUI
  topo worker   (Tokyo server) ─┴─▶  topo serve  (B)  ─┴─▶  topo status   for scripts and agents
```

- **worker**（负责测量）执行审阅过的本地拓扑文件里的检查，把观测结果投递给一个或两个采集器；网络中断期间先缓存在磁盘上。
- **采集器**（负责保存证据）维护一份只追加写入的日志（先 fsync 再确认），据此判断健康状态、事件和新鲜度。它会记下每个事件从发生到关闭的全过程，以及每项检查几个月来的表现；可以把事件推送到 Webhook（ntfy、Slack、Discord 或 JSON），也可以自己执行检查。
- **客户端**（负责读取）只读不写。关掉 TUI 不会停止监控，Topophany 也从不修改路由、代理或服务。

| 命令 | 作用 |
|---|---|
| `topo` | 用已保存的凭据，打开远程采集器的 TUI |
| `topo status` / `topo show TAB [ID]` | 以文本或 `--json` 输出 TUI 显示的内容；`status` 的退出码也会反映结果 |
| `topo login` | 验证并保存到采集器的连接 |
| `topo serve` | 运行采集器，也可以让它自己执行检查 |
| `topo worker` | 从本机执行检查并上报 |
| `topo edit` / `topo validate` | 在 TUI 里编辑拓扑文件 / 检查它并输出版本和契约 |
| `topo preview` | 用模拟数据打开 TUI |
| `topo monitor` | 把状态推送给桌面小组件和 [topophany-bar](https://github.com/WhitenWhiten/topophany-bar) 这样的菜单栏应用，可以写成文件，也可以输出 NDJSON 流 |
| `topo init` | 用新生成的凭据创建本地演示 |

运行 `topo help` 查看完整列表。

## 监控你自己的网络

1. **描述它。** 拓扑是一份 JSON，列出节点、链路、端点、服务、路径、观测点和检查。可以像上面那样交给 Agent 来写，也可以从 [`examples/homelab.json`](examples/homelab.json) 改起，用 `topo edit --config topology.json` 编辑。无论哪种方式，都用 `topo validate` 检查。如果是 Cloudflare 区域，[`examples/cloudflare.json`](examples/cloudflare.json) 是一份可以直接参照的完整示例：被代理的源站和它自己的直连检查、Worker 与 Pages 站点、带 `cloudflared` 检查的 Tunnel 以及服务自身的环回检查、trace 与 DoT 检查，还有一个受 Access 保护的域名。`topo preview` 会模拟质询页、来自声明列表之外的机房的应答以及 Tunnel 连接器中断，让你在真正运行之前看清它们各自如何判定。
2. **运行采集器。** 放在你的设备都能访问到的地方，并启用 TLS：`topo serve --config topology.json --auth auth.json --data collector-data --listen 0.0.0.0:7447 --tls-cert server.pem --tls-key server-key.pem`
3. **运行 worker。** 在每台你想作为测量起点的机器上：`topo worker --config topology.json --observer <id> --credentials worker-credentials.json --data worker-data`
4. **随时随地查看。** 运行一次 `topo login --server https://your-collector:7447 --project <name>`，之后只需要 `topo`。

[运维指南](docs/OPERATIONS.md)介绍双采集器、systemd 单元和各项上限，[内存预算](docs/MEMORY.md)介绍如何在小机器上估算内存，[安全模型](docs/SECURITY.md)介绍令牌与传输（均为英文）。

### 升级注意事项

- **升级到 1.5.1 时，先升级 collector，再升级 worker。** 1.5.1 的 worker 会报告旧版 collector 可能拒绝的投递诊断信息和 collector 别名，请先升级 collector。使用多个 collector 的 worker 需要返回 `collector_id` 的 collector。使用 `availability_scope` 的拓扑需要同时升级 collector、worker 和查看端；使用 `allow_insecure` 的直连配置需要本机 1.5.1 二进制。详见 [1.5.1 发布说明](docs/releases/v1.5.1.md)。
- **启用 Komari 前先升级到 1.5.0。** Komari 资源采集和直连配置需要新适配器。升级前请排空或归档超过新版各接收端容量份额的 worker 积压队列；回滚已扩大的外部状态前，请保留 collector 备份。详见 [1.5.0 发布说明](docs/releases/v1.5.0.md)。
- **启用外部数据源前，先升级所有组件。** `sources` 和 `external` 检查需要 1.4.2 的 collector、worker 和查看端；旧版本会拒绝这些新字段。批次和快照为 schema 3，但仍可读取 schema 2 的原生通信。collector 确认首条外部报告后，检查点会变为 schema 2，旧版本会拒绝读取；回滚需要升级前的匹配备份。详见 [1.4.2 发布说明](docs/releases/v1.4.2.md)。
- **启用 1.4.0 新字段前，先升级 collector 和所有查看端。** 旧版本会拒绝新增的 Cloudflare 适配器选项、DNS 传输、私密请求头及 trace/机房断言；运行新检查前也要升级 worker。流协议仍为 1，使用 `service.card` 或 `services.list` 前请检查指标目录。详见 [1.4.0 发布说明](docs/releases/v1.4.0.md)。
- **安装 bundle 需要审阅凭证。** `topo install --bundle DIR --dry-run` 会输出一个 `bundle_sha256`。把它保存在 bundle 之外，安装时用 `--bundle-sha256` 传回；审阅之后 bundle 有任何改动，安装都会被拒绝。
- **采集器和 worker 有了内存目标。** `serve` 默认 128 MiB，`worker` 默认 64 MiB。可以用 `--memory-limit` 调整（设为 `0` 则沿用 `GOMEMLIMIT`）；在 Linux 上还可以用 `--memory-max` 加一道硬上限。详见[内存预算](docs/MEMORY.md)（英文）。
- **令牌不再出现在命令行里。** `--credential TOKEN` 已经移除。请用 `topo login` 保存凭据；临时换用其他令牌时，从标准输入或私密文件（POSIX 上权限为 `0600`，Windows 上限制 ACL）读取。两种方式只能选一种；如果配置里有多台服务器，还要加上 `--server`：

  ```sh
  topo --json --token-stdin < token.txt
  topo --credential-file token.txt
  ```

- **日志必须是私密的。** 在 POSIX 系统上，`--log-file` 指定的文件及其编号备份的权限必须是 `0600` 或更严格，否则 `serve` 和 `worker` 会拒绝启动。

更早版本的升级说明见[变更记录](CHANGELOG.md)（英文）。

## 它能检查什么

- **原生探针：** HTTP（状态码、正文、响应头、出口 IP）、TCP、带证书校验的 TLS、SSH banner、DNS A/AAAA（UDP、TCP、DoT、DoH）、UDP echo、ICMP（IPv4）。
- **主机资源（Linux、macOS、Windows）：** CPU、内存、文件系统容量与 inode、进程数以及指定进程的内存占用，可以设置资源阈值和进程数检查。参见 [`examples/host-resources.json`](examples/host-resources.json)。
- **代理链：** HTTP CONNECT、HTTPS CONNECT、SOCKS5 和 OpenSSH `-W`，可以多跳串联。
- **只读适配器：** Mihomo、Tailscale、WireGuard、cloudflared、Cloudflare API。
- **外部资源：** Beszel Hub 和 Komari Monitor 的 CPU、内存、磁盘采样，保留样本身份和新鲜度。Beszel 还支持上游报告的系统状态；Komari 仅提供资源报告，磁盘为聚合统计。参见 [Beszel 说明](internal/adapters/beszel/README.md)、[`examples/beszel.json`](examples/beszel.json)、[Komari 说明](internal/adapters/komari/README.md)及 [`examples/komari.json`](examples/komari.json)。
- **边缘与平台：** Cloudflare（包括 Workers 和 Pages）、Vercel、Supabase。边缘有应答，只能证明边缘本身没问题，证明不了它背后的源站。

## 无需 collector 的 Beszel / Komari

direct 模式下，每个 `topo` 进程直接从 Beszel Hub 或 Komari 服务器读取 CPU、内存和磁盘，无需运行 collector：`topo --source direct --profile desktop`。`--json`、`status`、`show` 和 `monitor configure`/`run` 使用同一个 profile。用 `topo sources inspect|save|remove --json-stdin` 配置 profile，用 `topo sources list --json` 列出；它们与 collector 连接分开保存，一个 profile 可以混用 Beszel 和 Komari 数据源。默认每 60 秒采集一次、每 5 秒刷新显示，数据有效期 180 秒；一次性读取最多等待 15 秒拿到首批样本（`--wait`）。

direct 模式不保存资源历史（`history --source direct` 会明确返回不支持），不运行后台服务，也不会从资源读数推断网络是否可达。支持 Beszel v0.21.0 资源格式和 Komari 1.6.0；Komari 可用 API Key 或匿名读取公开节点，磁盘为聚合统计。不承诺兼容任意版本或完整的 MFA/SSO 流程。参见[配置与限制](docs/DIRECT.md)、[Komari 配置示例](docs/DIRECT.md#configure-komari)和 [D1 规范包](docs/contracts/direct-v1/contract.md)（均为英文）。

## Cloudflare：从边缘到源站

Topophany 1.4.0 可以把 Cloudflare 部署的公网域名、源站、Tunnel 连接器和控制平面放在一起观察。HTTP 检查能识别质询页和 Access 登录重定向，读取边缘连接耗时与 `/cdn-cgi/trace`，统计缓存及机房（colo）占比；地图会标出响应报告的机房，`expected_pops` 与 trace 断言可检查是否偏离声明的策略。Access 服务令牌通过 worker 的环境变量注入请求头。本地 `cloudflared` 检查报告 Tunnel 会话，只读 Cloudflare API 检查提供 Tunnel 状态、DNS 记录及负载均衡池健康信息。DNS 检查也支持 DoT 和 DoH。

从 [Cloudflare 详细配置文档](docs/CLOUDFLARE.md) 和 [`examples/cloudflare.json`](examples/cloudflare.json) 开始。文档说明了源站直连检查、Access 凭据、连接器本地指标、API 令牌权限及策略断言的配置方法。边缘有响应、Tunnel 会话已建立，都不能证明源站应用正常；请保留应用自身的独立检查。

## 终端界面

九个标签页：**Overview**、**Topology**（虚拟地图、地理地图和网络图三种地图）、**Servers**、**Services**、**Paths**、**Checks**、**Incidents**、**Observers** 和 **About**。服务按你声明的应用归类，检查按它所验证的服务归类；按 `b` 可以改为按应用、环境或主机浏览。

<p align="center">
  <img src="docs/assets/views.zh.gif" alt="从成都的 MacBook 看 Overview，线路机那条路径已断；换到纽约的服务器看则一切正常。接着是 Topology 标签页的虚拟地图、铺满全屏并在地点卡片和圆点之间切换的地理地图，以及网络图" width="100%">
  <br><sub>从成都看已断，从纽约看正常：每个观测点只报告自己看得到的（<code>v</code>）。接着是三种地图（<code>m</code>）：虚拟地图、地理地图和网络图；地理地图铺满全屏（<code>f</code>），地点卡片换成圆点（<code>p</code>）。</sub>
</p>

**Service card 模式。** 在 **Services** 标签页按 `s`，可切换为每个服务一张卡片，按应用分组，并沿用所在主机的颜色。卡片汇总各观测点及其检查、近期采样、当天延迟与可用率、端点、依赖和边缘机房。按 `Enter` 展开完整卡片，查看逐项检查、缓存占比、备注及 runbook。选择会保存为 `service_cards`；也可以用 `topo --page services --service-cards` 直接打开。桌面小组件可通过 `topo monitor stream` 的 `service.card` 和 `services.list` 使用同一视图，详见[指标参考](docs/REFERENCE.md#template-and-metric-contract)。

| 按键 | |
|---|---|
| `1`–`9`、`Tab` | 切换标签页 |
| `j` / `k`、方向键 | 在列表中移动 |
| `Enter` | 显示所选行的证据 |
| `/` | 筛选 |
| `v` | 切换从哪个观测点查看 |
| `m` / `z` / `f` / `p` | 切换地图 / 缩放 / 只显示地图 / 地理地图在地点卡片、地名和圆点之间切换 |
| `s` | 统一服务器卡片的尺寸；在 Services 上，为每个服务显示一张卡片 |
| `b` | 按应用、环境或主机浏览服务和检查 |
| `q` | 退出 |

也可以用鼠标操作。颜色会自动适配 truecolor、256 色和 `NO_COLOR` 终端。[用户指南](docs/GUIDE.md)（英文）介绍了每个标签页和每种地图，以及如何让脚本和 Agent 通过 `topo status`、`topo show` 和 `topo history` 读取同样的信息。

## 在 macOS 菜单栏里

<p align="center">
  <a href="https://github.com/WhitenWhiten/topophany-bar"><img src="docs/assets/topophany-bar.jpg" alt="运行 topophany-bar 的 macOS 桌面：菜单栏里是白夜图标，月亮处于月偏食，旁边显示 2；其下展开的面板显示一个有两条警告的降级网络、看到它们的观测点和一张地图；桌面上摆着状态、服务器、时区、服务和地图小组件" width="100%"></a>
  <br><sub>topophany-bar 显示一个有两条警告的演示网络，由应用自己的视图用合成数据绘制。</sub>
</p>

[**topophany-bar**](https://github.com/WhitenWhiten/topophany-bar) 是 Topophany 的 macOS 伴侣应用。它通过 `topo monitor stream` 读取 collector：状态以图标、数量或几个字显示在菜单栏；面板告诉你每个问题是从哪里看到的，并可直接在 TUI 中打开；桌面小组件让服务器、服务、地图和时区一直留在视野里。它也能不经 collector 直接读取 Beszel 和 Komari。需要 macOS 26 或更高版本，目前为预发布版；想试用请[从源码构建](https://github.com/WhitenWhiten/topophany-bar/blob/main/docs/DEVELOPMENT.md#building)（英文）。

## 文档

文档目前只有英文版。

| | |
|---|---|
| [用户指南](docs/GUIDE.md) | TUI、编辑、预览模式、状态语义、探针、多地部署 |
| [配置与 API 参考](docs/REFERENCE.md) | 每种实体、探针参数和 API 端点 |
| [运维](docs/OPERATIONS.md) · [内存](docs/MEMORY.md) | 双采集器、存储、恢复、资源上限和内存预算 |
| [安全模型](docs/SECURITY.md) | 凭据、传输和权限边界（[审查](docs/SECURITY-REVIEW-2026-10-14.md)） |
| [场景](docs/SCENARIOS.md) · [验收](docs/ACCEPTANCE.md) | 测试证明了什么，哪些还需要在真实硬件上验证 |
| [外部适配器](docs/EXTERNAL-ADAPTERS.md) · [实现契约](docs/MULTI-PROBE-IMPLEMENTATION.md) | 源适配器编写、报告/时间/去重语义、预算与升级/回滚 |
| [变更记录](CHANGELOG.md) | 发布历史 |

## 仓库结构

```text
cmd/topo        the single binary
internal/       model, config, probes, scheduler, journal, engine, API, client, outbox, view, TUI, preview
examples/       sample topologies and credential templates
deploy/         hardened systemd units for the collector and the worker
docs/           guides, reference and verification records
scripts/        acceptance and verification suites
site/           the topophany.com homepage (a separate module, not part of go install)
```

## 开发

```sh
go test ./...                # unit and integration tests
go test -race ./...
python3 scripts/verify.py    # the full acceptance suite (Linux, Go, Python 3)
make site                    # re-record the homepage's TUI replays and rewrite its pages
```

构建时不需要下载任何模块。提交修改前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md) 和[行为准则](CODE_OF_CONDUCT.md)；发现漏洞请按 [SECURITY.md](SECURITY.md) 中的说明私下报告。

## 许可证

[MIT](LICENSE)
