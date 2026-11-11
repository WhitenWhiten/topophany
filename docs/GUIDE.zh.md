# Topophany 使用指南

先选择要完成的任务，再逐步增加观测点。检查结果只代表实际测量到的范围；
没有检查、读数过期和监控端出错，都不能当作目标宕机或正常。

## 首次接入

运行 `topo setup`，选择网站、资源直连，或多地部署。资源直连支持 Beszel、
Komari 和 CF-Server-Monitor。
网站也可以直接创建：

```sh
topo init --url https://example.com/health --dir ./website
```

它会生成私密配置和配套凭据，并打印启动采集器和查看端的命令。生成时不会
访问网站。先审阅 `topology.json`；默认每 60 秒检查一次，期望 HTTP 200。
启动采集器后，等待第一条真实证据：

```sh
topo doctor --credentials ./website/credentials.json --wait 90s
topo --credentials ./website/credentials.json --lang zh
```

收到样本只证明监控链路在工作；样本本身仍可能报告目标失败。
`topo doctor --json` 可导出不含地址、凭据、拓扑正文的诊断报告。

已有网络配置时，`topo templates list` 可选择模板：

```sh
topo templates cloudflare --output cloudflare.json
topo validate --config cloudflare.json
```

模板中的检查全部暂停。先修改地址、观测点、断言和凭据环境变量，再启用
需要的检查。模板的兼容说明针对已验证的上游格式，不代表所有未来版本。

已有 CF-Server-Monitor 时，可用 `topo setup --task direct` 选择
`cf-server-monitor`，填入 Worker 地址，选择要查看的服务器。保存后运行
`topo --source direct --profile desktop --page servers`，将 `desktop` 换成刚保存的
配置名称。需要通过采集器查看时，可用 `topo templates cf-server-monitor` 生成拓扑。
[直连配置](DIRECT.md#configure-cf-server-monitor) · [适配器说明](../internal/adapters/cfservermonitor/README.md)

使用 mihomo 时可从 `topo templates mihomo --output mihomo.json` 开始：工作端
运行在 mihomo 核心所在的机器上，通过 `http://127.0.0.1:9090` 读取外部控制器，
密钥由 `adapter.token_env` 指定的环境变量提供。控制器报告的模式、TUN 和节点
选择只是运行状态，不能证明应用流量走了这条路由，因此模板另配了一条经本地监听
端口的 HTTP 应用检查。

需要成组对照诊断时，可生成 `cloudflare-diagnostics` 或 `mihomo-diagnostics`
模板。前者对照公网入口、本地服务、连接器及两个 Cloudflare API 读取；后者
对照控制器、显式代理、系统默认路径、出口和 DNS。两者全部默认暂停。
系统默认路径也可能被 TUN 接管，不能直接称为“直连”。配置步骤和读数解释见
[组合诊断指南](DIAGNOSTICS.md)。

## 读懂状态

- **当前检查全部通过**：此观测视角的活跃检查通过，不代表所有登记的服务都有应用级证据。
- **服务可用性已知**：当前证据能判断可用性的服务数量，包括正常、降级和失败。
- **未配置检查**：服务只有登记信息。
- **此观测点没有检查**：其他观测点可能有证据，当前视角不能代替它们作答。
- **只有连接检查**：TCP/TLS 成功不能单独证明应用可用。
- **样本过期、缓存数据**：只能参考过去的结果，不能据此确认当前状态。
- **监控端错误**：权限、配置、凭据或采集过程的问题，与目标故障分开显示。

`--lang zh` 适用于查看端、预览和 `status` / `show`。状态代码、检查 ID 和
原始证据不翻译，便于脚本、日志和不同语言界面相互对应。

## 日常查看与排障

```sh
topo status --lang zh
topo show services SERVICE_ID --lang zh
topo problems
topo problems node:TARGET_ID
```

`problems` 将指向同一声明目标的事件放在一起，保留各观测点的事件、检查和
反证。归组说明相关性，不自动认定共同根因。在终端或浏览器的事件页按 `b`
切换关联视图，按 Enter 查看完整证据。

常用键：Tab 或 1–9 切换页面，方向键选择，Enter 打开详情，`/` 筛选，`v`
切换观测点，`m` 切换地图，`f` 展开地图，`,` 打开设置，`q` 退出。

```sh
topo views save home-services --page services --observer home --filter web --lang zh
topo views open home-services -- --profile production
topo views list
```

命名视图保存展示选择，不保存登录凭据。

## 对比历史

```sh
topo compare --checks home-http,cloud-http --step 5m --span 1h
topo compare --incident INCIDENT_ID --span 30m --json
```

对比固定在同一个采集器、配置契约和时间窗口。事件前后窗口按步长对齐；
事件后的窗口尚未结束时，请缩短范围。缺失历史、未测量时间和无法修正的
历史不会填成正常；修改测量契约会开始新的序列。

## 修改和部署配置

```sh
topo edit --config topology.json
topo edit --config topology.json --apply proposed.json --dry-run
```

批量应用时，使用预览返回的两个完整版本：

```sh
topo edit --config topology.json --apply proposed.json \
  --expect-revision BEFORE --expect-candidate-revision AFTER
```

审阅后任何一方文件发生变化都会拒绝应用。编辑器遇到外部修改时保留草稿，
请重新打开文件并合并改动。分发并重载之后检查：

```sh
topo rollout --config topology.json --json
topo runtime --data WORKER_DATA --config topology.json --json
```

前者逐个读取采集器地址，只验证公开配置和 worker 报告的测量契约；后者在
本机检查进程实际加载的私有配置摘要。后者的报告不属于可公开分享的诊断导出。

## 告警与恢复

```sh
topo alerts status --data COLLECTOR_DATA --json
topo alerts test --config topology.json --alerts alerts.json --webhook ops
topo alerts quiet --config topology.json --alerts alerts.json --webhook ops --for 30m
```

通知测试会明确发送一条测试消息。静默配置需要重载采集器才生效；它暂缓
通知投递，不停止测量、不修改健康状态。排队、过期和丢弃仍受原有上限约束。
双采集器可用 `primary_instance` 指定通知端，这不是自动选主。

用新版本二进制验证停止写入后的备份：

```sh
topo recovery --data BACKUP_DATA --config topology.json --auth auth.json
```

验证在临时私密副本中执行，不改写原始数据，也不启动探针、投递或告警。
根据实际部署传入存储预算。备份仍需保留匹配的配置、凭据与证书材料。

## 直连与菜单栏

`topo sources export --profile desktop --output collector-topology.json` 可生成
采集器迁移拓扑，保留服务器 ID，所有检查先暂停。命令只列出需要的凭据环境
变量，不复制凭据。直连的非环回 HTTP 授权不能迁移到拓扑，请先配置 HTTPS。

菜单栏支持在应用内连接采集器、体验明确标注的模拟演示，并返回原连接。
迁移完成后，在数据源设置中选择“沿用直连配置的固定服务器”，显式复制原来
的固定项。直连没有历史可转移；确认采集器数据正常后再停用旧直连配置。

完整字段、预算和兼容边界见 [配置参考](REFERENCE.md)、[运维](OPERATIONS.md)
和 [直连说明](DIRECT.md)。

## 本机代理内核

本机代理应用的 mihomo 内核可以保存为本地检查，不需要采集器或 worker。本地检查
与菜单栏原有的数据源（采集器，或 Beszel/Komari 直连配置）同时运行：
`topo monitor stream` 以 `"source":"local"` 在第二个流进程中读取它们。

`topo local inspect --json-stdin` 读取一次内核，列出版本、模式、TUN 状态和可选作
`root_proxy` 的策略组；`topo local save --json-stdin` 在请求中加上
`topo local list --json` 给出的 `base_revision`，先用该密钥读取内核，再把密钥单独
保存为私密文件。地址只能是环回地址（127.0.0.0/8、`::1` 或 `localhost`），不会跨网
络读取控制器。Topophany 只读：不改内核配置、不切换策略组、不触发延迟测试，读到
的内容不离开本机；只有本地流打开时才读取。内核报告的模式、TUN 和选择只是它自己的
说法，不能证明应用流量的实际路径。字段和错误码见
[配置参考](REFERENCE.md#local-checks-this-machine)。

## 导入已有清单，再审阅启用

`topo import --format ssh --input ~/.ssh/config --output candidate.json` 生成
暂停状态的候选检查，不能直接当作已经发现的健康网络。代理、Include、Match 或
通配符造成的路由歧义会保留为清单或警告，不会执行 SSH 配置中的命令。

Compose 输入必须是已渲染的 JSON，并通过 `--host` 指定确认过的部署地址；只为
可明确解释的已发布 TCP 端口生成暂停检查。Tailscale 输入是保存的状态 JSON，
只导入对端身份和地址，不把 Online 当作应用可用，也不猜测服务端口。

先查看输出中的来源 SHA-256、警告和拓扑，再用 `topo edit --apply` 的预览与
修订校验流程合并。已有文件不会被导入命令覆盖。

## 证书、延迟和响应字段

- TLS 或 HTTPS 检查可设置 `tls_min_days`，例如 14，提前提示已验证证书即将到期。
  信任链验证失败仍然是连接失败，不会被到期提示掩盖。
- 网络检查设置 `max_duration_ms` 后，成功但耗时超限的请求记录策略异常。
  `policy_threshold` 决定连续多少次策略异常才确认；不同策略需要独立连续计数时，
  请拆为不同检查。
- HTTP 的 `json_equals` 可配置 `{"/status":"ok","/ready":true}`。
  使用 JSON Pointer，缺失字段与 null 不同，数字按原始表示精确比较。
  预期值不进入公开拓扑，响应值不进入错误信息。

这些新字段需要相关组件全部升级后启用。详细限制见英文参考文档。
