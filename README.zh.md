<h1 align="center">
  <img src="docs/assets/banner.png" alt="Topophany — 场所的显现" width="100%">
</h1>

<p align="center"><b>自托管的网络与可用性监控，跑在你的终端里——看清哪里挂了、<em>从哪儿看</em>是挂的、为什么。</b></p>

<p align="center"><i>从哪里出发，经由哪条路径，抵达了什么——依据又是什么。</i></p>

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

<p align="center">
  <img src="docs/assets/outage.zh.gif" alt="Overview 上故障来临：场景选择框打开，检查随样本陆续失败，直到 5 个事件打开、故障路径在地图上变成琥珀色；每选中一个问题，它的路径就会亮起，最后打开一个事件的证据；接着地理地图铺满全屏，在地点卡片和圆点之间切换，再回到面板" width="100%">
  <br><sub>在 <code>topo preview --lang zh</code> 里按 <code>x</code>，家里到洛杉矶线路机的连接被阻断。选中一个问题，它的路径就会亮起；按 <code>Enter</code> 查看证据。接着地理地图铺满全屏（<code>f</code>），地点卡片换成圆点（<code>p</code>）。</sub>
</p>

**Topophany** 是自托管的网络拓扑与服务可用性监控，界面在终端里。worker 从你真正上网的地方（家里、公司、海外的 VPS）发起检查，告诉你：哪里出了问题，是从哪里看到的，证据又是什么。这一切只是一个 Go 二进制文件 `topo`：同时当 worker、采集器和客户端。不用 CGO，不用数据库，外部 Go 模块为零。

## 它适合谁

- **家里有 Homelab、外面有几台 VPS，还有服务挂在 Cloudflare 后面。** 书桌底下那台机器、另一个城市的 VPS、CDN 对外应答的站点，放在同一块屏幕上；家里那条路不通，不会被报成「服务器宕机」。
- **厌倦了单点探测的人。** 从成都看不通、从纽约看正常，说明出问题的是成都那条路径。Topophany 把「坏了什么」「从哪里看到」「有多新」分成独立的事实。没有通过的检查就不会变绿，没人检查的服务保持 `unknown`。
- **习惯待在终端、也走叠加网络的人。** 代理链（SSH `-W`、HTTP/HTTPS CONNECT、SOCKS5），再加上只读的 Tailscale、WireGuard 和 Mihomo，都在你用来看界面的那一个静态二进制里。

<p align="center">
  <img src="docs/assets/overview.zh.png" alt="模拟故障时的 Overview：虚拟地图上从东京到法兰克福的各个地点排成卡片，故障路径以琥珀色画出；状态卡片显示 5 个事件，旁边是需要关注的项目列表，以及按应用归类的服务，其中代理线路被标为需要关注" width="100%">
  <br><sub>模拟故障时的 Overview，来自 <code>topo preview --lang zh --scenario 1</code>。</sub>
</p>

## 和别的工具有什么不同

不是排名。[Uptime Kuma](https://github.com/louislam/uptime-kuma) 和 [Gatus](https://github.com/TwiN/gatus) 更擅长公开状态页和海量通知渠道。[SmokePing](https://github.com/oetiker/SmokePing) 是延迟历史学家。Topophany 做的是：从多个地点看同一项服务，带着一份声明过的拓扑，在终端里看。

| | Topophany | Uptime Kuma | Gatus | SmokePing |
|---|---|---|---|---|
| **做什么** | 拓扑 + 可用性，在终端里 | 服务存活，在浏览器里 | 端点健康仪表盘 | 延迟与丢包曲线 |
| **界面** | TUI（盲文世界地图、网络图） | Web UI + 状态页 | Web 仪表盘 + 状态页 | Web 曲线 |
| **安装** | 一个 Go 二进制；没有数据库守护进程 | Docker / Node；SQLite 或 MariaDB | Go 二进制或 Docker；内存、SQLite 或 Postgres | Perl + rrdtool |
| **从哪里检查** | 你放置的、有名字的 worker（家、公司、VPS） | 运行该实例的那台机器；也有 push 监控 | 运行该进程的那台机器；也有 push / 外部端点 | 主节点，加上可选的远程 slave |
| **路径 vs 宕机** | 一个观测点失败、另一个仍通过 → 路径故障，而不是「服务器挂了」 | 每个实例报告自己的上/下线 | 每个实例报告自己的上/下线 | 比较各 slave 的延迟；不是服务健康模型 |
| **未检查 / 过期** | 未配置 = `unknown`；缓存会标明缓存；没有通过的检查就不会变绿 | 状态是已配置监控项的最近一次结果 | 状态是已配置端点的最近一次结果 | 只给被探测的目标画图 |
| **CDN / 源站** | 识别边缘响应头（Cloudflare、Vercel、CloudFront、Fastly、Netlify）；521 只能证明边缘，不能证明源站 | HTTP 状态码就是监控对象 | HTTP 状态码就是端点 | 不适用 |
| **代理 / 叠加网** | HTTP/HTTPS CONNECT、SOCKS5、SSH `-W`（最多 12 跳）；只读 Tailscale、WireGuard、Mihomo | 部分监控项支持 HTTP(S) 代理 | HTTP 代理 | 视探针而定 |
| **告警** | ntfy、Slack、Discord、JSON | 90 多种通知渠道 | Slack、PagerDuty、Discord 等 | 邮件等经典方式 |
| **公开状态页** | 无 | 有 | 有 | 无（只有曲线） |

[trippy](https://github.com/fujiapple852/trippy) 是交互式 traceroute TUI，用来当场诊断一条路径，不是 7×24 监控。[Netdata](https://github.com/netdata/netdata) 是按秒采集的主机与基础设施指标。Topophany 都不替代。

## 快速上手

预编译包在[发布页面](https://github.com/WhitenWhiten/topophany/releases)，覆盖 Linux、macOS（`darwin`）和 Windows，以及 amd64、arm64。每次发布都带 `SHA256SUMS.txt`。解压后运行 `topo`（Windows 上是 `topo.exe`）。目前还没有 Docker 镜像，也没有 Homebrew formula。

或者，用 **Go 1.26.9** 及以上版本：

```sh
go install github.com/WhitenWhiten/topophany/cmd/topo@latest
```

什么都不用配置，就能用合成证据看到完整的界面；按 `x` 就能弄坏点什么。内置的网络和故障场景有中文、英文和日文版本，默认跟随系统语言，也可以用 `--lang ja` 指定：

```sh
topo preview
```

用演示拓扑和新生成的凭据，在本机跑一个真正的采集器：

```sh
topo init --dir ./demo
topo serve --config ./demo/topology.json --auth ./demo/auth.json \
  --data ./demo/collector-data --observer demo

# in another terminal
topo --credentials ./demo/credentials.json
```

Linux 上加固过的 systemd 单元在 [`deploy/`](deploy/)。

## 交给 Agent 来写

任何监控最费工夫的一步，都是把要监控的东西描述清楚，而这恰好是编程 Agent 擅长的事。让你手边的 Agent（Claude Code、Codex、Gemini CLI、Cursor，或任何能执行命令的 Agent）去读机器上现成的信息，替你写出拓扑。它会用 `topo validate` 自查，你再用 `topo preview` 过目，确认无误之后才真正运行。可以从这段提示词开始：

> 请把我的网络写成 Topophany 拓扑，保存到 `topology.json`。格式参考 `topo preview --print-topology` 的输出，以及 github.com/WhitenWhiten/topophany 里的 docs/REFERENCE.md。读取我的 SSH 配置、`tailscale status`，以及你能找到的 Compose 文件和反向代理配置：为每台机器写明所在位置、上面跑的服务、服务所属的应用和监听的地址，再从我要运行 worker 的机器发起检查，并把你了解到的情况写进各台机器的 notes。不要把任何密钥写进文件。反复运行 `topo validate --config topology.json` 直到通过，然后告诉我哪些是查到的、哪些是推测的。

写好之后，你和 Agent 都能一直用它：

- **网络备忘录。** 这份文件写清了什么跑在哪里、每台机器怎么连上，旁边附有笔记和处置手册。Agent 动手之前先读它；它改了什么，你像审代码一样看 diff。也可以用 `topo edit` 在 TUI 里修改。
- **随手可查的接口。** `topo status`、`topo show`、`topo history` 用一行文字回答，加上 `--json` 就是 JSON：现在有没有问题、那台服务器上跑着什么、某项检查昨晚表现如何。还没有采集器的时候，`topo preview --config topology.json show servers` 也能直接按文件作答。

## 工作原理

```text
  WORKERS                          COLLECTORS                  CLIENTS
  measure from where they are      keep the evidence           read it

  topo worker   (home laptop)  ─┬─▶  topo serve  (A)  ─┬─▶  topo          the TUI
  topo worker   (Tokyo server) ─┴─▶  topo serve  (B)  ─┴─▶  topo status   for scripts and agents
```

- **worker** 执行审阅过的本地拓扑文件里的检查，把观测结果投递给一个或两个采集器；网络中断时先缓存在磁盘上。
- **采集器**维护只追加的日志（先 fsync 再确认），由此推出健康状态、事件和新鲜度。它记得每个事件从发生到关闭的全过程，以及每项检查几个月来的表现；可以把事件推送到 Webhook（ntfy、Slack、Discord 或 JSON），也可以自己执行检查。
- **客户端**只读不写。关掉 TUI 不会停止监控，Topophany 也从不修改路由、代理或服务。

| 命令 | 作用 |
|---|---|
| `topo` | 用已保存的凭据，打开远程采集器的 TUI |
| `topo status` / `topo show TAB [ID]` | 以文本或 `--json` 输出 TUI 显示的内容；`status` 还会用退出码作答 |
| `topo login` | 保存并验证到采集器的连接 |
| `topo serve` | 运行采集器，也可以让它自己执行检查 |
| `topo worker` | 从本机执行检查并上报 |
| `topo edit` / `topo validate` | 在 TUI 里编辑拓扑文件 / 检查它并输出版本和契约 |
| `topo preview` | 用合成证据打开 TUI |
| `topo init` | 用新生成的凭据创建本地演示 |

运行 `topo help` 查看完整列表。

## 监控你自己的网络

1. **描述它。** 拓扑是一份 JSON，列出节点、链路、端点、服务、路径、观测点和检查。可以像上面那样交给 Agent 来写，也可以从 [`examples/homelab.json`](examples/homelab.json) 改起，用 `topo edit --config topology.json` 编辑。无论哪种方式，都用 `topo validate` 检查。
2. **运行采集器**，放在你的设备都能访问到的地方，并启用 TLS：`topo serve --config topology.json --auth auth.json --data collector-data --listen 0.0.0.0:7447 --tls-cert server.pem --tls-key server-key.pem`
3. **运行 worker**，在每台你想作为测量起点的机器上：`topo worker --config topology.json --observer <id> --credentials worker-credentials.json --data worker-data`
4. **随时随地查看：** `topo login --server https://your-collector:7447 --project <name>`，之后只需运行 `topo`。

[运维指南](docs/OPERATIONS.md)（英文）介绍双采集器、systemd 单元和各项上限，[安全模型](docs/SECURITY.md)（英文）介绍令牌与传输。已有安装的破坏性变更见 [UPGRADING.md](UPGRADING.md)（英文）。

## 它能检查什么

- **原生探针：** HTTP（状态码、正文、响应头、出口 IP）、TCP、带证书校验的 TLS、SSH banner、DNS A/AAAA、UDP echo、ICMP（IPv4）。
- **主机资源（Linux / macOS / Windows）：** CPU、内存、文件系统容量（Unix 上还有 inode）、进程数以及指定进程的内存占用，可设资源阈值和进程数检查。参见 [`examples/host-resources.json`](examples/host-resources.json)。
- **代理链：** HTTP CONNECT、HTTPS CONNECT、SOCKS5 和 OpenSSH `-W`，最多 12 跳串联。
- **只读适配器：** Mihomo、Tailscale、WireGuard。
- **边缘与平台：** Cloudflare（包括 Workers 和 Pages）、Vercel、Supabase 可作为拓扑节点；HTTP 探针还会读取 Cloudflare、Vercel、CloudFront、Fastly、Netlify 的响应头。边缘的应答只能证明边缘本身，证明不了它背后的源站。

## 终端界面

九个标签页：**Overview**、**Topology**（虚拟地图、地理地图和网络图三种地图）、**Servers**、**Services**、**Paths**、**Checks**、**Incidents**、**Observers** 和 **About**。服务按你声明的应用归类，检查按它所验证的服务归类；按 `b` 可以按应用、环境或主机浏览。

<p align="center">
  <img src="docs/assets/views.zh.gif" alt="从成都的 MacBook 看 Overview，线路机那条路径已断；换到纽约的服务器看则一切正常。接着是 Topology 标签页的虚拟地图、铺满全屏并在地点卡片和圆点之间切换的地理地图，以及网络图" width="100%">
  <br><sub>从成都看已断，从纽约看正常：每个观测点只报告它看得到的（<code>v</code>）。接着是三种地图（<code>m</code>）：虚拟地图、地理地图和网络图；地理地图铺满全屏（<code>f</code>），地点卡片换成圆点（<code>p</code>）。</sub>
</p>

| 按键 | |
|---|---|
| `1`–`9`、`Tab` | 切换标签页 |
| `j` / `k`、方向键 | 在列表中移动 |
| `Enter` | 显示所选行的证据 |
| `/` | 筛选 |
| `v` | 切换查看的观测点 |
| `m` / `z` / `f` / `p` | 切换地图 / 缩放 / 只显示地图 / 地理地图只留圆点和虚线 |
| `s` | 服务器卡片统一尺寸 |
| `b` | 按应用、环境或主机浏览服务和检查 |
| `q` | 退出 |

也可以用鼠标操作。颜色会自动适配 truecolor、256 色和 `NO_COLOR` 终端。[用户指南](docs/GUIDE.md)（英文）介绍了每个标签页和地图，以及如何让脚本和 Agent 用 `topo status`、`topo show` 和 `topo history` 读取全部内容。

## 文档

文档为英文。

| | |
|---|---|
| [用户指南](docs/GUIDE.md) | TUI、编辑、预览模式、语义、探针、多地部署 |
| [配置与 API 参考](docs/REFERENCE.md) | 每种实体、探针参数和 API 端点 |
| [运维](docs/OPERATIONS.md) | 双采集器、存储、恢复和资源上限 |
| [安全模型](docs/SECURITY.md) | 凭据、传输和权限边界（[审查](docs/SECURITY-REVIEW-2026-10-14.md)） |
| [场景](docs/SCENARIOS.md) · [验收](docs/ACCEPTANCE.md) | 测试证明了什么，哪些还需要在真实硬件上检查 |
| [升级](UPGRADING.md) · [更新日志](CHANGELOG.md) | 已有安装的破坏性变更，以及发布历史 |

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

构建时不需要下载任何模块。提交修改前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md) 和[行为准则](CODE_OF_CONDUCT.md)，并按 [SECURITY.md](SECURITY.md) 中的说明私下报告漏洞。

## 许可证

[MIT](LICENSE)

如果 Topophany 帮你找到过一条断掉的路，给这个仓库点个 ⭐，能让更多人看见。缺陷和建议请[开 Issue](https://github.com/WhitenWhiten/topophany/issues/new/choose)。
