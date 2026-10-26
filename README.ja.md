<h1 align="center">
  <img src="docs/assets/banner.png" alt="Topophany — 場所の顕現" width="100%">
</h1>

<p align="center"><b>どこから、どの経路で、何に届いたのか — そして、何を根拠に。</b></p>

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

**Topophany** は、ネットワーク全体をひとつの画面に描き出します。どのマシンがどこにあり、互いがどの経路でつながり、その上でどんなサービスが動いているのか。ワーカーは、あなたが実際にネットワークを使う場所 (自宅、オフィス、海外の VPS) からチェックを行い、ターミナル UI は、何が壊れているのか、それがどこから見えたのか、そして何を根拠にそう言えるのかを示します。すべてはセルフホストできる Go のバイナリ 1 つ、`topo` に収まっています。

<p align="center">
  <img src="docs/assets/outage.ja.gif" alt="Overview に障害が起きる様子：シナリオの選択画面が開き、サンプルが届くたびにチェックが失敗して 5 件のインシデントが開き、失敗した経路が地図上で琥珀色になる。問題を選ぶたびにその経路が浮かび上がり、最後に 1 件のインシデントの根拠が開く。続いて地図が全画面に広がり、場所のカードと点を切り替えてからパネル表示に戻る" width="100%">
  <br><sub><code>topo preview --lang ja</code> で <code>x</code> を押すと、自宅からロサンゼルスの回線サーバーへの接続が切れます。問題を選ぶとその経路が浮かび上がり、<code>Enter</code> で根拠が開きます。続いて地図が全画面に広がり (<code>f</code>)、場所のカードが点に切り替わります (<code>p</code>)。</sub>
</p>

## Topophany を選ぶ理由

- **壊れたことだけでなく、どこで壊れたのかを。** チェックはすべて、名前を付けた観測点から行います。大阪からは届かないのにニューヨークからは届くなら、それは大阪からの経路の障害です。Topophany はサーバーダウンと決めつけず、そのまま報告します。
- **色ではなく根拠を。** 健全性、根拠の出どころ、鮮度はそれぞれ別のフィールドです。成功したチェックがなければ緑にはならず、誰もチェックしていないサービスは `unknown` のまま。キャッシュを表示しているときは、そうはっきり示します。
- **ネットワークの備忘録に。** 何がどこで動き、各マシンにどうたどり着き、各経路がどのプロキシチェーンを通るのか。レビュー済みの JSON ファイル 1 つに、メモや手順書と一緒にまとまります。あなたが読み、エージェントが読み、監視もこのファイルを基準にチェックします。
- **眺めていたくなる画面。** 点字で描く世界地図は、チェックに応答した Cloudflare のコロをその場所に置きます。場所の並びを保った模式図、ネットワークグラフ、サーバーごとのカード。サービスは属するアプリケーションごとにまとまります。どのターミナルでも動き、マウスも使えます。
- **バイナリ 1 つ、ほかには何も要らない。** CGO も、データベースのデーモンも、Node.js も不要で、外部の Go モジュールにも一切依存しません。

<p align="center">
  <img src="docs/assets/overview.ja.png" alt="障害をシミュレートしている Overview：仮想地図には東京からフランクフルトまでの場所がカードで並び、失敗している経路が琥珀色で描かれている。ステータスカードには 5 件のインシデント、その横に対処が必要な項目の一覧と、アプリケーションごとにまとまったサービスがあり、プロキシ経路に対処が必要と表示されている" width="100%">
  <br><sub>障害をシミュレートしている Overview。<code>topo preview --lang ja --scenario 1</code> より。</sub>
</p>

## 1 分で試す

[リリースページ](https://github.com/WhitenWhiten/topophany/releases) (Linux、macOS、Windows · amd64、arm64) からダウンロードするか、Go 1.26.10 以降でインストールします。

```sh
go install github.com/WhitenWhiten/topophany/cmd/topo@latest
```

何も準備しなくても、合成データで UI 全体を試せます。`x` を押せば障害を起こせます。組み込みのネットワークとシナリオには日本語・英語・中国語版があり、ロケールに合わせて切り替わります。`--lang ja` で指定することもできます。

```sh
topo preview
```

デモ用のトポロジーと新しく生成した認証情報で、本物のコレクターを手元で動かすこともできます。

```sh
topo init --dir ./demo
topo serve --config ./demo/topology.json --auth ./demo/auth.json \
  --data ./demo/collector-data --observer demo

# in another terminal
topo --credentials ./demo/credentials.json
```

## エージェントに書かせる

監視でいちばん手間がかかるのは、何を監視するのかを書き出す作業です。そしてこれは、コーディングエージェントの得意分野でもあります。普段使っているエージェント (Claude Code、Codex、Gemini CLI、Cursor など、コマンドを実行できるもの) に、マシン上にすでにある情報を読ませて、トポロジーを書かせましょう。エージェントは `topo validate` で自分の書いたものを検証し、あなたは実際に動かす前に `topo preview` で仕上がりを確認できます。たとえば、こんなふうに頼みます。

> 私のネットワークを Topophany のトポロジーとして `topology.json` に書いてください。形式は `topo preview --print-topology` の出力と、github.com/WhitenWhiten/topophany の docs/REFERENCE.md を参考にしてください。SSH の設定、`tailscale status`、見つかる限りの Compose ファイルとリバースプロキシの設定を読み、各マシンについて、場所、動いているサービス、サービスが属するアプリケーション、待ち受けているアドレスを書いてください。ワーカーを動かす予定のマシンからのチェックも加え、各マシンについて分かったことはその notes に書いてください。秘密情報はファイルに書かないでください。`topo validate --config topology.json` が通るまで修正し、最後に、確認できたことと推測したことを分けて教えてください。

一度書き上げれば、あなたにもエージェントにも役立ち続けます。

- **ネットワークの備忘録として。** 何がどこで動き、各マシンにどうたどり着くのかが、メモや手順書と一緒にこのファイルにまとまっています。エージェントは作業の前にこれを読み、あなたはその変更をコードと同じように差分でレビューできます。`topo edit` を使えば TUI でも編集できます。
- **すぐに答えが返る窓口として。** `topo status`、`topo show`、`topo history` は 1 行で、`--json` を付ければ JSON で答えます。いま何か問題はないか、あのサーバーで何が動いているか、あのチェックが夜のあいだどうだったか。コレクターを動かす前でも、`topo preview --config topology.json show servers` ならファイルだけを読んで答えます。

## しくみ

```text
  WORKERS                          COLLECTORS                  CLIENTS
  measure from where they are      keep the evidence           read it

  topo worker   (home laptop)  ─┬─▶  topo serve  (A)  ─┬─▶  topo          the TUI
  topo worker   (Tokyo server) ─┴─▶  topo serve  (B)  ─┴─▶  topo status   for scripts and agents
```

- **ワーカー** (計測する側) は、レビュー済みのローカルのトポロジーファイルに書かれたチェックを実行し、観測結果を 1 台か 2 台のコレクターに送ります。ネットワークが落ちているあいだは、ディスクにためておきます。
- **コレクター** (根拠を保管する側) は追記専用のジャーナルを持ち (fsync してから ACK を返します)、健全性、インシデント、鮮度を判定します。各インシデントを発生から収束まで、各チェックの成績を数か月にわたって記録し、インシデントを Webhook (ntfy、Slack、Discord、JSON) に通知することも、自らチェックを実行することもできます。
- **クライアント** (読む側) は読むだけです。TUI を閉じても監視は止まらず、Topophany がルート、プロキシ、サービスを書き換えることは一切ありません。

| コマンド | 役割 |
|---|---|
| `topo` | 保存した認証情報で、リモートのコレクターを TUI で開く |
| `topo status` / `topo show TAB [ID]` | TUI と同じ内容をテキストか `--json` で出力する。`status` は終了コードでも結果を返す |
| `topo login` | コレクターへの接続を確認して保存する |
| `topo serve` | コレクターを動かす。コレクター自身でチェックを実行することもできる |
| `topo worker` | このマシンからチェックを実行して報告する |
| `topo edit` / `topo validate` | トポロジーファイルを TUI で編集する / 検証してリビジョンとコントラクトを表示する |
| `topo preview` | 合成データで TUI を開く |
| `topo monitor` | 状態をデスクトップのウィジェットや [topophany-bar](https://github.com/WhitenWhiten/topophany-bar) などのメニューバーに、ファイルか NDJSON ストリームで届ける |
| `topo init` | 新しい認証情報でローカルのデモを作る |

すべてのコマンドは `topo help` で確認できます。

## 自分のネットワークを監視する

1. **書き出す。** トポロジーは、ノード、リンク、エンドポイント、サービス、経路、観測点、チェックを並べた JSON です。上のようにエージェントに書かせるか、[`examples/homelab.json`](examples/homelab.json) をもとに `topo edit --config topology.json` で編集します。どちらの場合も `topo validate` で検証します。Cloudflare のゾーンなら、[`examples/cloudflare.json`](examples/cloudflare.json) が手本になる完成した例です。プロキシされたオリジンとその直接チェック、Worker と Pages サイト、Tunnel とコネクターの `cloudflared` チェック、サービス自身のループバックチェック、trace と DoT のチェック、Access 保護されたホスト名が入っています。`topo preview` は、チャレンジページ、宣言した一覧の外のコロからの応答、Tunnel コネクターの停止をシミュレートするので、実際に動かす前にそれぞれがどう判定されるかを確認できます。
2. **コレクターを動かす。** 各マシンから届く場所で、TLS を有効にして起動します: `topo serve --config topology.json --auth auth.json --data collector-data --listen 0.0.0.0:7447 --tls-cert server.pem --tls-key server-key.pem`
3. **ワーカーを動かす。** 計測の起点にしたいマシンそれぞれで起動します: `topo worker --config topology.json --observer <id> --credentials worker-credentials.json --data worker-data`
4. **どこからでも見る。** `topo login --server https://your-collector:7447 --project <name>` を一度実行すれば、あとは `topo` だけです。

デュアルコレクター、systemd ユニット、各種の上限は[運用ガイド](docs/OPERATIONS.md)、小さなマシンでのサイズの見積もりは[メモリ予算](docs/MEMORY.md)、トークンと通信は[セキュリティモデル](docs/SECURITY.md)で説明しています (いずれも英語)。

### アップグレード時の注意

- **1.5.1 へ更新するときは、worker より先に collector を更新してください。** 1.5.1 の worker は旧版の collector が拒否する可能性のある配信診断と collector エイリアスを報告します。複数の collector を使う worker には `collector_id` を返す collector が必要です。`availability_scope` を使うトポロジーは collector、worker、閲覧クライアントをすべて更新し、`allow_insecure` を使う直接接続プロファイルはローカルの 1.5.1 バイナリが必要です。詳しくは [1.5.1 のリリースノート](docs/releases/v1.5.1.md)を参照してください。
- **Komari を有効にする前に 1.5.0 へ更新してください。** Komari のリソース収集と直接接続プロファイルには新しいアダプターが必要です。更新前に、新しい送信先ごとの容量枠を超える worker キューを排出または保管し、拡張された外部状態を戻す場合に備えて collector のバックアップを保持してください。詳しくは [1.5.0 のリリースノート](docs/releases/v1.5.0.md)を参照してください。
- **外部ソースを有効にする前に、すべてを更新してください。** `sources` と `external` チェックには 1.4.2 の collector、worker、閲覧クライアントが必要です。旧版は新しいフィールドを受け付けません。バッチとスナップショットは schema 3 ですが、schema 2 のネイティブ通信は引き続き読めます。collector が外部レポートを確認するとチェックポイントは schema 2 になり、旧版はそれを拒否します。ロールバックにはアップグレード前の対応するバックアップが必要です。詳しくは [1.4.2 のリリースノート](docs/releases/v1.4.2.md)を参照してください。
- **1.4.0 の新しいフィールドを使う前に、collector とすべての閲覧クライアントを更新してください。** 旧版は新しい Cloudflare アダプターのオプション、DNS トランスポート、秘密のリクエストヘッダー、trace と colo のアサーションを受け付けません。新しいチェックを実行する worker も更新してください。ストリームのプロトコルは 1 のままです。`service.card` と `services.list` を要求する前にメトリクスの一覧を確認してください。[1.4.0 のリリースノート](docs/releases/v1.4.0.md)に詳細があります。
- **バンドルのインストールにはレビューの控えが必要です。** `topo install --bundle DIR --dry-run` が `bundle_sha256` を表示するので、バンドルとは別の場所に控えておき、インストール時に `--bundle-sha256` で渡してください。レビューした後にバンドルが変わっていれば、インストールは拒否されます。
- **コレクターとワーカーにメモリの目標値が設定されます。** 既定値は `serve` が 128 MiB、`worker` が 64 MiB です。`--memory-limit` で変更でき (`0` なら `GOMEMLIMIT` に従います)、Linux では `--memory-max` でハードリミットもかけられます。詳しくは[メモリ予算](docs/MEMORY.md) (英語) を参照してください。
- **トークンはコマンドラインに書きません。** `--credential TOKEN` は廃止されました。認証情報は `topo login` で保存してください。一時的に別のトークンを使うときは、標準入力か非公開のファイル (POSIX では `0600`、Windows では ACL を制限) から読み込みます。両方を同時には使えず、複数のサーバーを持つプロファイルでは `--server` も指定します。

  ```sh
  topo --json --token-stdin < token.txt
  topo --credential-file token.txt
  ```

- **ログは非公開にします。** POSIX では、`--log-file` で指定したファイルと番号付きのバックアップの権限を `0600` 以下にしてください。そうでなければ `serve` と `worker` は起動しません。

それ以前のリリースの注意点は[変更履歴](CHANGELOG.md) (英語) にあります。

## 計測できるもの

- **ネイティブプローブ:** HTTP (ステータス、本文、ヘッダー、送信元 IP)、TCP、証明書を検証する TLS、SSH バナー、DNS A/AAAA (UDP、TCP、DoT、DoH)、UDP エコー、ICMP (IPv4)。
- **ホストリソース (Linux、macOS、Windows):** CPU、メモリ、ファイルシステムの容量と inode、プロセス数、指定したプロセスの RSS。リソースのしきい値とプロセス数も検証できます。[`examples/host-resources.json`](examples/host-resources.json) を参照してください。
- **プロキシチェーン:** HTTP CONNECT、HTTPS CONNECT、SOCKS5、OpenSSH `-W` を、何段でもつないで使えます。
- **読み取り専用のアダプター:** Mihomo、Tailscale、WireGuard、cloudflared、Cloudflare API。
- **外部リソース:** Beszel Hub と Komari Monitor の CPU、メモリ、ディスクのサンプル。サンプルの識別子と鮮度を保持します。Beszel は報告されたシステム状態も取得でき、Komari は集計ディスクを含むリソースレポートのみを提供します。[Beszel ガイド](internal/adapters/beszel/README.md)、[`examples/beszel.json`](examples/beszel.json)、[Komari ガイド](internal/adapters/komari/README.md)、[`examples/komari.json`](examples/komari.json) を参照してください。
- **エッジとプラットフォーム:** Cloudflare (Workers と Pages を含む)、Vercel、Supabase。エッジが応答したからといって証明されるのはエッジまでで、その奥のオリジンではありません。

## コレクターなしの Beszel / Komari

direct モードでは、各 `topo` プロセスが Beszel Hub や Komari サーバーから CPU、メモリー、ディスクを直接読みます。コレクターは要りません：`topo --source direct --profile desktop`。同じプロファイルを `--json`、`status`、`show`、`monitor configure`/`run` でも使えます。プロファイルは `topo sources inspect|save|remove --json-stdin` で設定し、`topo sources list --json` で一覧できます。コレクターへの接続とは別に保存され、ひとつのプロファイルに Beszel と Komari のソースを混在させられます。既定では 60 秒ごとに収集して 5 秒ごとに表示し、データは 180 秒間有効です。単発の読み取りは最初のサンプルを最大 15 秒待ちます (`--wait`)。

direct モードはリソースの履歴を持たず (`history --source direct` は未対応と答えます)、バックグラウンドサービスも動かさず、リソースの読み取りからネットワークの到達性を推測しません。対応するのは Beszel v0.21.0 のリソース形式と Komari 1.6.0 です。Komari は API キーか公開ノードへの匿名アクセスで読み、ディスクは集計値です。任意のバージョンや完全な MFA/SSO への対応は保証しません。[設定と制限](docs/DIRECT.md)、[Komari の設定例](docs/DIRECT.md#configure-komari)、[D1 契約](docs/contracts/direct-v1/contract.md) (いずれも英語) を参照してください。

## Cloudflare：エッジからオリジンまで

Topophany 1.4.0 では、Cloudflare の公開ホスト名、オリジン、Tunnel コネクター、コントロールプレーンをまとめて確認できます。HTTP チェックはチャレンジページと Access のログインリダイレクトを識別し、エッジ接続の時間や `/cdn-cgi/trace` を読み取り、キャッシュと colo の割合を集計します。マップには応答が報告した colo が表示され、`expected_pops` と trace のアサーションで宣言したポリシーからのずれを検出できます。Access のサービス・トークンは worker の環境変数からリクエストヘッダーに渡します。ローカルの `cloudflared` チェックは Tunnel のセッションを、読み取り専用の Cloudflare API チェックは Tunnel の状態、DNS レコード、ロードバランサープールの正常性を報告します。DNS チェックは DoT と DoH にも対応しています。

[Cloudflare の詳しい設定ガイド](docs/CLOUDFLARE.md)と [`examples/cloudflare.json`](examples/cloudflare.json) から始めてください。ガイドでは、オリジンへの直接チェック、Access の認証情報、コネクターのローカルメトリクス、API トークンの権限、ポリシーのアサーションを説明しています。エッジからの応答や確立済みの Tunnel セッションだけでは、オリジンのアプリケーションが動いているかは分かりません。アプリケーション自体の独立したチェックも用意してください。

## ターミナル UI

タブは 9 つ: **Overview**、**Topology** (仮想地図、地図、ネットワークの 3 種類のマップ)、**Servers**、**Services**、**Paths**、**Checks**、**Incidents**、**Observers**、**About**。サービスは宣言したアプリケーションごとに、チェックは検証するサービスごとにまとまり、`b` でアプリケーション、環境、ホスト別に切り替えて見られます。

<p align="center">
  <img src="docs/assets/views.ja.gif" alt="大阪の MacBook から見た Overview では回線サーバーへの経路がダウンし、ニューヨークのサーバーから見ると正常。続いて Topology タブの仮想地図、全画面に広がって場所のカードと点を切り替える地図、ネットワーク図" width="100%">
  <br><sub>大阪からはダウン、ニューヨークからは正常。各観測点は自分に見えるものだけを報告します (<code>v</code>)。続いて 3 種類のマップ (<code>m</code>): 仮想地図、地図、ネットワーク。地図は全画面に広がり (<code>f</code>)、場所のカードが点に切り替わります (<code>p</code>)。</sub>
</p>

**Service card モード。** **Services** タブで `s` を押すと、アプリケーション別にサービスごとのカードを表示し、ホストの色で見分けられます。カードには観測点と各チェック、最近のサンプル、当日の遅延と稼働率、エンドポイント、依存関係、エッジの colo がまとまります。`Enter` で開くと、個々のチェック、キャッシュの割合、メモ、runbook まで確認できます。選択は `service_cards` として保存され、`topo --page services --service-cards` で直接開くこともできます。デスクトップウィジェットでも `topo monitor stream` の `service.card` と `services.list` で同じビューを利用できます。[メトリクスのリファレンス](docs/REFERENCE.md#template-and-metric-contract)を参照してください。

| キー | |
|---|---|
| `1`–`9`、`Tab` | タブを切り替える |
| `j` / `k`、矢印キー | リストを移動する |
| `Enter` | 選んだ行の根拠を表示する |
| `/` | 絞り込む |
| `v` | どの観測点から見るかを切り替える |
| `m` / `z` / `f` / `p` | マップを切り替える / ズームする / マップだけを表示する / 地図の表示を場所のカード、地名、点の順に切り替える |
| `s` | サーバーカードの大きさを揃える。Services ではサービスごとのカードを表示 |
| `b` | サービスとチェックをアプリケーション、環境、ホスト別に見る |
| `q` | 終了する |

マウスでも操作できます。色は truecolor、256 色、`NO_COLOR` の各ターミナルに合わせて調整されます。すべてのタブとマップの見方、そして `topo status`、`topo show`、`topo history` でスクリプトやエージェントから同じ情報を読み取る方法は、[ユーザーガイド](docs/GUIDE.md) (英語) で説明しています。

## macOS のメニューバーで

<p align="center">
  <a href="https://github.com/WhitenWhiten/topophany-bar"><img src="docs/assets/topophany-bar.jpg" alt="topophany-bar を動かしている macOS のデスクトップ。メニューバーには白夜のアイコンがあり、部分月食の月の横に 2 の数字。その下に開いたパネルには、警告 2 件で低下したネットワーク、それを見ているオブザーバー、地図が表示され、デスクトップにはステータス、サーバー、タイムゾーン、サービス、地図のウィジェットが並ぶ" width="100%"></a>
  <br><sub>警告が 2 件あるデモネットワークを表示した topophany-bar。合成データを使い、アプリ自身のビューで描いています。</sub>
</p>

[**topophany-bar**](https://github.com/WhitenWhiten/topophany-bar) は Topophany の macOS 用コンパニオンです。`topo monitor stream` を通してコレクターを読み、状態をアイコン、件数、短い言葉でメニューバーに表示します。パネルでは問題がどこから見えているかが分かり、そのまま TUI で開けます。デスクトップのウィジェットはサーバー、サービス、地図、タイムゾーンを表示し続けます。コレクターなしで Beszel と Komari を直接読むこともできます。macOS 26 以降が必要で、現在はプレリリースです。試すには[ソースからビルド](https://github.com/WhitenWhiten/topophany-bar/blob/main/docs/DEVELOPMENT.md#building) (英語) してください。

## ドキュメント

ドキュメントは英語のみです。

| | |
|---|---|
| [ユーザーガイド](docs/GUIDE.md) | TUI、編集、プレビューモード、表示の意味、プローブ、複数拠点での運用 |
| [設定と API のリファレンス](docs/REFERENCE.md) | すべてのエンティティ、プローブのパラメーター、API エンドポイント |
| [運用](docs/OPERATIONS.md) · [メモリ](docs/MEMORY.md) | デュアルコレクター、ストレージ、復旧、リソースの上限、メモリ予算 |
| [セキュリティモデル](docs/SECURITY.md) | 認証情報、通信、権限の境界 ([レビュー](docs/SECURITY-REVIEW-2026-10-14.md)) |
| [シナリオ](docs/SCENARIOS.md) · [受け入れ](docs/ACCEPTANCE.md) | テストで証明できること、実機で確かめる必要があること |
| [外部アダプター](docs/EXTERNAL-ADAPTERS.md) · [実装契約](docs/MULTI-PROBE-IMPLEMENTATION.md) | ソースアダプターの作成、レポート・時刻・重複排除の意味、予算、アップグレードとロールバック |
| [変更履歴](CHANGELOG.md) | リリースの履歴 |

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

ビルドにモジュールのダウンロードは要りません。変更を送る前に [CONTRIBUTING.md](CONTRIBUTING.md) と[行動規範](CODE_OF_CONDUCT.md)を読んでください。脆弱性は [SECURITY.md](SECURITY.md) の手順に従って非公開で報告してください。

## ライセンス

[MIT](LICENSE)
