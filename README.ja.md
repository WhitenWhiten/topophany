<h1 align="center">
  <img src="docs/assets/banner.png" alt="Topophany — 場所の顕現" width="100%">
</h1>

<p align="center"><b>ターミナルで動く、セルフホストのネットワーク／稼働監視 — 何が落ちているか、<em>どこから見て</em>、なぜかを示します。</b></p>

<p align="center"><i>どこから、どの経路で、何に届いたのか — そして、何を根拠に。</i></p>

<p align="center">
  <a href="https://topophany.com/ja/demo/"><img src="https://img.shields.io/badge/%E2%96%B6%20ライブデモ-ブラウザで試す-5eead4?style=for-the-badge&labelColor=0a1016" alt="ライブデモ：ブラウザで試す"></a>
</p>

<p align="center">
  <a href="https://topophany.com/ja/"><b>topophany.com</b></a> ·
  <a href="https://github.com/WhitenWhiten/topophany/releases">ダウンロード</a> ·
  <a href="docs/GUIDE.md">ユーザーガイド (英語)</a>
</p>

<p align="center"><sub><a href="README.md">English</a> · <b>日本語</b> · <a href="README.zh.md">简体中文</a></sub></p>

<p align="center">
  <a href="https://github.com/WhitenWhiten/topophany/actions/workflows/ci.yml"><img src="https://github.com/WhitenWhiten/topophany/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/WhitenWhiten/topophany"><img src="https://pkg.go.dev/badge/github.com/WhitenWhiten/topophany.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
  <a href="https://linux.do"><img src="https://img.shields.io/badge/LINUX%20DO-コミュニティ-FFB003?logo=linux" alt="LINUX DO"></a>
</p>

<p align="center">
  <img src="docs/assets/outage.ja.gif" alt="Overview に障害が起きる様子：シナリオの選択画面が開き、サンプルが届くたびにチェックが失敗して 5 件のインシデントが開き、失敗した経路が地図上で琥珀色になる。問題を選ぶたびにその経路が浮かび上がり、最後に 1 件のインシデントの根拠が開く。続いて地図が全画面に広がり、場所のカードと点を切り替えてからパネル表示に戻る" width="100%">
  <br><sub><code>topo preview --lang ja</code> で <code>x</code> を押すと、自宅からロサンゼルスの回線サーバーへの接続が遮断されます。問題を選ぶとその経路が浮かび上がり、<code>Enter</code> で根拠を表示します。続いて地図が全画面に広がり (<code>f</code>)、場所のカードが点に切り替わります (<code>p</code>)。</sub>
</p>

**Topophany** は、マシンと、それらを結ぶ経路と、その上のサービスをセルフホストで監視し、ターミナルで見るためのツールです。ワーカーが実際につなぐ場所 (自宅、オフィス、海外の VPS) からチェックし、何が壊れているのか、それがどこから見えたのか、根拠は何かを示します。すべては Go のバイナリ 1 つ、`topo` です。ワーカー、コレクター、クライアントを兼ね、CGO もデータベースも外部の Go モジュールもありません。

## 誰のためのものか

- **自宅のホームラボ、VPS が数台、Cloudflare の後ろにあるサイト。** 机の下の箱、別の街のマシン、CDN が応答しているサイトをひとつの画面に。自宅からの経路が切れただけでは「サーバーダウン」とは言いません。
- **単一地点のプローブにうんざりしている人。** 大阪からは届かないのにニューヨークからは届くなら、それは大阪からの経路の障害です。Topophany は「何が失敗したか」「どこから見えたか」「どれだけ新しいか」を別の事実として保ちます。通ったチェックなしに緑になるものはなく、誰もチェックしていないサービスは `unknown` のままです。
- **ターミナルとオーバーレイ網で暮らしている人。** プロキシチェーン (SSH `-W`、HTTP/HTTPS CONNECT、SOCKS5) と、読み取り専用の Tailscale、WireGuard、Mihomo が、TUI として動かしている同じ静的バイナリに入っています。

<p align="center">
  <img src="docs/assets/overview.ja.png" alt="障害をシミュレートしている Overview：仮想地図には東京からフランクフルトまでの場所がカードで並び、失敗している経路が琥珀色で描かれている。ステータスカードには 5 件のインシデント、その横に対処が必要な項目の一覧と、アプリケーションごとにまとまったサービスがあり、プロキシ経路に対処が必要と表示されている" width="100%">
  <br><sub>障害をシミュレートしている Overview。<code>topo preview --lang ja --scenario 1</code> より。</sub>
</p>

## 何が違うのか

優劣の表ではありません。[Uptime Kuma](https://github.com/louislam/uptime-kuma) と [Gatus](https://github.com/TwiN/gatus) は公開ステータスページと通知チャネルの多さで勝ります。[SmokePing](https://github.com/oetiker/SmokePing) は遅延の歴史家です。Topophany は、同じサービスを複数の地点から、宣言したトポロジーとともに、ターミナルで見るためのものです。

| | Topophany | Uptime Kuma | Gatus | SmokePing |
|---|---|---|---|---|
| **役割** | トポロジー + 可用性、ターミナル | サービスの稼働、ブラウザ | エンドポイントのヘルスダッシュボード | 遅延と損失のグラフ |
| **UI** | TUI (点字の世界地図、ネットワーク図) | Web UI + ステータスページ | Web ダッシュボード + ステータスページ | Web グラフ |
| **導入** | Go バイナリ 1 つ。DB デーモンなし | Docker/Node。SQLite または MariaDB | Go バイナリまたは Docker。メモリ、SQLite または Postgres | Perl + rrdtool |
| **どこから測るか** | 置く場所に名前を付けたワーカー (自宅、オフィス、VPS) | そのインスタンスが動くホスト。プッシュ監視もあり | そのプロセスが動くホスト。プッシュ / 外部エンドポイントもあり | マスターと、任意のリモートスレーブ |
| **経路 vs 障害** | 一方の観測点は失敗、他方は成功 → 経路の故障であり「サーバーダウン」ではない | インスタンスごとに独自の up/down を報告 | インスタンスごとに独自の up/down を報告 | スレーブごとの遅延を比較。サービス健全性のモデルではない |
| **未計測 / 古い** | 未設定は `unknown`。キャッシュはキャッシュと明示。通ったチェックなしに緑にならない | 設定済みモニターの直近結果が状態 | 設定済みエンドポイントの直近結果が状態 | プローブした対象だけを描く |
| **CDN / オリジン** | エッジの応答ヘッダー (Cloudflare、Vercel、CloudFront、Fastly、Netlify)。521 が証明するのはエッジでありオリジンではない | HTTP ステータスがモニター対象 | HTTP ステータスがエンドポイント | 対象外 |
| **プロキシ / オーバーレイ** | HTTP/HTTPS CONNECT、SOCKS5、SSH `-W` (最大 12 ホップ)。読み取り専用の Tailscale、WireGuard、Mihomo | 一部モニターの HTTP(S) プロキシ | HTTP プロキシ | プローブによる |
| **通知** | ntfy、Slack、Discord、JSON | 90 以上の通知サービス | Slack、PagerDuty、Discord など | メールなど |
| **公開ステータスページ** | なし | あり | あり | なし (グラフ) |

[trippy](https://github.com/fujiapple852/trippy) は今この経路を調べる対話的な traceroute TUI であり、常時監視ではありません。[Netdata](https://github.com/netdata/netdata) はホストとインフラの毎秒メトリクスです。Topophany はどちらも置き換えません。

## クイックスタート

事前ビルドのアーカイブは [リリースページ](https://github.com/WhitenWhiten/topophany/releases) にあり、Linux、macOS (`darwin`)、Windows の amd64 / arm64 をカバーします。各リリースに `SHA256SUMS.txt` が付きます。展開して `topo` を実行してください (Windows では `topo.exe`)。Docker イメージも Homebrew の formula も、まだありません。

または **Go 1.26.9** 以降で:

```sh
go install github.com/WhitenWhiten/topophany/cmd/topo@latest
```

準備なしで、合成の根拠を使って UI 全体を見られます。`x` を押して、何かを壊してみてください。組み込みのネットワークとシナリオは日本語・英語・中国語で書かれていてロケールに従い、`--lang ja` で選べます:

```sh
topo preview
```

デモ用のトポロジーと新しい認証情報で、本物のコレクターを手元で動かします:

```sh
topo init --dir ./demo
topo serve --config ./demo/topology.json --auth ./demo/auth.json \
  --data ./demo/collector-data --observer demo

# in another terminal
topo --credentials ./demo/credentials.json
```

Linux 向けの堅牢化した systemd ユニットは [`deploy/`](deploy/) にあります。

## エージェントに書かせる

監視でいちばん手間がかかるのは、何を監視するかを書き起こすことです。そしてそれは、コーディングエージェントが得意とする仕事でもあります。普段使っているエージェント (Claude Code、Codex、Gemini CLI、Cursor など、コマンドを実行できるもの) に、マシンが既に持っている情報を読ませてトポロジーを書かせましょう。エージェントは `topo validate` で自分の仕事を確かめ、あなたは何かを動かす前に `topo preview` で仕上がりを確認できます。たとえば、こう頼みます:

> 私のネットワークを Topophany のトポロジーとして `topology.json` に書いてください。形式は `topo preview --print-topology` の出力と、github.com/WhitenWhiten/topophany の docs/REFERENCE.md を参考にしてください。SSH の設定、`tailscale status`、見つかる Compose ファイルとリバースプロキシの設定を読み、各マシンについて場所、動いているサービス、サービスが属するアプリケーション、待ち受けているアドレスを宣言し、ワーカーを動かす予定のマシンからのチェックを加え、分かったことはそれぞれのマシンの notes に書いてください。秘密情報はファイルに書かないでください。`topo validate --config topology.json` が通るまで直し、最後に、確かめたことと推測したことを分けて教えてください。

書き上がったら、あなたとエージェントの両方で使い続けられます:

- **ネットワークのメモとして。** 何がどこで動き、各マシンにどう届くのかが、ノートや手順書と一緒にこのファイルにまとまります。エージェントは手を動かす前にこれを読み、あなたはその変更をコードと同じように差分でレビューします。`topo edit` を使えば TUI でも編集できます。
- **すぐ引ける窓口として。** `topo status`、`topo show`、`topo history` が 1 行の文字で、`--json` を付ければ JSON で答えます。いま何かおかしいか、あのサーバーで何が動いているか、あのチェックが夜のあいだどうだったか。コレクターを動かす前でも、`topo preview --config topology.json show servers` ならファイルだけから答えます。

## しくみ

```text
  WORKERS                          COLLECTORS                  CLIENTS
  measure from where they are      keep the evidence           read it

  topo worker   (home laptop)  ─┬─▶  topo serve  (A)  ─┬─▶  topo          the TUI
  topo worker   (Tokyo server) ─┴─▶  topo serve  (B)  ─┴─▶  topo status   for scripts and agents
```

- **ワーカー**は、レビュー済みのローカルのトポロジーファイルにあるチェックを実行し、観測結果を 1 つまたは 2 つのコレクターに届けます。ネットワークが落ちている間はディスクにためておきます。
- **コレクター**は追記専用のジャーナルを持ち (ACK の前に fsync)、健全性、インシデント、鮮度を導きます。各インシデントを発生から終了まで、各チェックの調子を数か月にわたって記憶し、インシデントを Webhook (ntfy、Slack、Discord、JSON) に送ることも、自分でチェックを実行することもできます。
- **クライアント**は読むだけです。TUI を閉じても監視は止まらず、Topophany がルート、プロキシ、サービスを変更することは一切ありません。

| コマンド | 役割 |
|---|---|
| `topo` | 保存した認証情報でリモートのコレクターの TUI を開く |
| `topo status` / `topo show TAB [ID]` | TUI が示す内容をテキストまたは `--json` で出力する。`status` は終了ステータスでも答える |
| `topo login` | コレクターへの接続を保存し、検証する |
| `topo serve` | コレクターを動かす。自前のチェックも実行できる |
| `topo worker` | このマシンからチェックを実行して報告する |
| `topo edit` / `topo validate` | トポロジーファイルを TUI で編集する / 検査してリビジョンとコントラクトを出力する |
| `topo preview` | 合成の根拠で TUI を開く |
| `topo init` | 新しい認証情報でローカルのデモを作る |

すべてのコマンドは `topo help` で確認できます。

## 自分のネットワークを監視する

1. **書き起こす。** トポロジーは、ノード、リンク、エンドポイント、サービス、経路、観測点、チェックを並べた JSON です。上のようにエージェントに書かせるか、[`examples/homelab.json`](examples/homelab.json) から始めて `topo edit --config topology.json` で編集します。どちらの場合も `topo validate` で検査します。
2. **コレクターを動かす。** 機器から届く場所で、TLS を有効にして：`topo serve --config topology.json --auth auth.json --data collector-data --listen 0.0.0.0:7447 --tls-cert server.pem --tls-key server-key.pem`
3. **ワーカーを動かす。** 計測の起点にしたいすべてのマシンで：`topo worker --config topology.json --observer <id> --credentials worker-credentials.json --data worker-data`
4. **どこからでも見る。** `topo login --server https://your-collector:7447 --project <name>` のあとは `topo` だけです。

[運用ガイド](docs/OPERATIONS.md) (英語) ではデュアルコレクター、systemd ユニット、各種上限を、[セキュリティモデル](docs/SECURITY.md) (英語) ではトークンと通信を扱います。既存インストール向けの破壊的変更は [UPGRADING.md](UPGRADING.md) (英語) にあります。

## 計測できるもの

- **ネイティブプローブ:** HTTP (ステータス、本文、ヘッダー、出口 IP)、TCP、証明書を検証する TLS、SSH バナー、DNS A/AAAA、UDP エコー、ICMP (IPv4)。
- **ホストリソース（Linux / macOS / Windows）:** CPU、メモリ、ファイルシステム容量 (Unix では inode も含む)、プロセス数、指定プロセスの RSS。リソースしきい値とプロセス数の検証にも対応。[`examples/host-resources.json`](examples/host-resources.json) を参照。
- **プロキシチェーン:** HTTP CONNECT、HTTPS CONNECT、SOCKS5、OpenSSH `-W` を、最大 12 ホップにわたって連結。
- **読み取り専用アダプター:** Mihomo、Tailscale、WireGuard。
- **エッジとプラットフォーム:** Cloudflare (Workers と Pages を含む)、Vercel、Supabase をトポロジーのノードとして。HTTP プローブは Cloudflare、Vercel、CloudFront、Fastly、Netlify の応答ヘッダーも読みます。エッジからの応答が証明するのはエッジであって、その背後のオリジンではありません。

## ターミナル UI

9 つのタブ：**Overview**、**Topology** (仮想地図、地図、ネットワークの 3 つのマップ)、**Servers**、**Services**、**Paths**、**Checks**、**Incidents**、**Observers**、**About**。サービスは宣言したアプリケーションごとに、チェックは確かめる対象のサービスごとにまとまり、`b` でアプリケーション、環境、ホスト別に見て回れます。

<p align="center">
  <img src="docs/assets/views.ja.gif" alt="大阪の MacBook から見た Overview では回線サーバーへの経路がダウンし、ニューヨークのサーバーから見ると正常。続いて Topology タブの仮想地図、全画面に広がって場所のカードと点を切り替える地図、ネットワーク図" width="100%">
  <br><sub>大阪からはダウン、ニューヨークからは正常。各観測点は見えるものだけを報告します (<code>v</code>)。続いて 3 つのマップ (<code>m</code>)：仮想地図、地図、ネットワーク。地図は全画面に広がり (<code>f</code>)、場所のカードが点に切り替わります (<code>p</code>)。</sub>
</p>

| キー | |
|---|---|
| `1`–`9`、`Tab` | タブを切り替える |
| `j` / `k`、矢印キー | リストを移動する |
| `Enter` | 選んだ行の根拠を表示する |
| `/` | 絞り込む |
| `v` | 視点にする観測点を切り替える |
| `m` / `z` / `f` / `p` | マップを切り替える / ズームする / マップだけを表示する / 地図は点と破線だけにする |
| `s` | サーバーカードを同じ大きさに揃える |
| `b` | サービスとチェックをアプリケーション、環境、ホスト別に見る |
| `q` | 終了する |

マウスも使えます。色は truecolor、256 色、`NO_COLOR` の端末に合わせて変わります。[ユーザーガイド](docs/GUIDE.md) (英語) では、すべてのタブとマップ、そして `topo status`、`topo show`、`topo history` でスクリプトやエージェントからすべてを読み取る方法を説明しています。

## ドキュメント

ドキュメントは英語です。

| | |
|---|---|
| [ユーザーガイド](docs/GUIDE.md) | TUI、編集、プレビューモード、セマンティクス、プローブ、複数拠点での運用 |
| [設定と API のリファレンス](docs/REFERENCE.md) | すべてのエンティティ、プローブのパラメーター、API エンドポイント |
| [運用](docs/OPERATIONS.md) | デュアルコレクター、ストレージ、復旧、リソースの上限 |
| [セキュリティモデル](docs/SECURITY.md) | 認証情報、通信、権限の境界 ([レビュー](docs/SECURITY-REVIEW-2026-10-14.md)) |
| [シナリオ](docs/SCENARIOS.md) · [受け入れ](docs/ACCEPTANCE.md) | テストが証明すること、実機での確認が必要なこと |
| [アップグレード](UPGRADING.md) · [変更履歴](CHANGELOG.md) | 既存インストール向けの破壊的変更と、リリースの履歴 |

## リポジトリの構成

```text
cmd/topo        the single binary
internal/       model, config, probes, scheduler, journal, engine, API, client, outbox, view, TUI, preview
examples/       sample topologies and credential templates
deploy/         hardened systemd units for the collector and the worker
docs/           guides, reference and verification records
scripts/        acceptance and verification suites
site/           the topophany.com homepage (a separate module, not part of go install)
```

## 開発

```sh
go test ./...                # unit and integration tests
go test -race ./...
python3 scripts/verify.py    # the full acceptance suite (Linux, Go, Python 3)
make site                    # re-record the homepage's TUI replays and rewrite its pages
```

ビルドにモジュールのダウンロードは不要です。変更を送る前に [CONTRIBUTING.md](CONTRIBUTING.md) と [行動規範](CODE_OF_CONDUCT.md) を読み、脆弱性は [SECURITY.md](SECURITY.md) にあるとおり非公開で報告してください。

## ライセンス

[MIT](LICENSE)

壊れた経路を見つけるのに役立てたら、このリポジトリに ⭐ を付けてもらえると、次の人の目に留まりやすくなります。不具合や提案は [Issue](https://github.com/WhitenWhiten/topophany/issues/new/choose) へ。
