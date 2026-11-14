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

**Topophany** は、ネットワーク全体をひとつの画面に描き出します。どのマシンがどこにあり、互いがどの経路でつながり、その上でどんなサービスが動いているのか。ワーカーはあなたが実際にネットワークを使う場所からチェックを行い、ターミナル UI は、何が壊れているのか、それがどこから見えたのか、そして何を根拠にそう言えるのかを示します。すべてはセルフホストできる Go のバイナリ 1 つ、`topo` に収まっています。

<p align="center">
  <img src="docs/assets/outage.ja.gif" alt="Overview に障害が起きる様子：シナリオの選択画面が開き、サンプルが届くたびにチェックが失敗して 5 件のインシデントが開き、失敗した経路が地図上で琥珀色になる。問題を選ぶたびにその経路が浮かび上がり、最後に 1 件のインシデントの根拠が開く。続いて地図が全画面に広がり、場所のカードと点を切り替えてからパネル表示に戻る" width="100%">
  <br><sub><code>topo preview --lang ja</code> で <code>x</code> を押すと障害が起きます。問題を選ぶとその経路が浮かび上がり、<code>Enter</code> で根拠が開きます。</sub>
</p>

## 主な機能

- **壊れたことだけでなく、どこで壊れたのかを。** チェックはすべて、名前を付けた観測点から行います。大阪からは届かないのにニューヨークからは届くなら、それはサーバーダウンではなく、大阪からの経路の障害です。
- **色ではなく根拠を。** 健全性、根拠の出どころ、鮮度はそれぞれ別に扱います。成功したチェックがなければ緑にはならず、誰もチェックしていないサービスは `unknown` のままです。
- **ネットワークをファイル 1 つに。** 何がどこで動き、各マシンにどうたどり着き、何をチェックするのか。レビュー済みの JSON のトポロジーに、メモと一緒にまとまります。コーディングエージェントに書かせることもでき、`topo validate` で検証します。
- **通信が通る道のままにチェック。** HTTP、TLS、TCP、DNS (DoT・DoH を含む)、SSH、UDP、ICMP、ホストのリソースを、複数段のプロキシチェーン越しにも。読み取り専用のアダプターで Mihomo、Tailscale、WireGuard、cloudflared、Cloudflare API、Beszel、Komari、CF-Server-Monitor を読みます。Cloudflare、Vercel、Supabase などのエッジからの応答を、その後ろのオリジンの応答と取り違えることはありません。
- **眺めていたくなる画面。** 点字で描く世界地図、模式図、ネットワークグラフ、サーバーとサービスごとのカード。どのターミナルでも動き、マウスも使えます。
- **プロダクトモード。** アプリやサイトを運営する人のために、ユーザーが触れる部分ごとに 1 行、それが載っているものすべてと一緒に並べます。
- **スクリプトやエージェントからも。** `topo status`、`topo show`、`topo history` が 1 行か JSON で答え、`topo monitor` はメニューバーやデスクトップウィジェットに状態を届けます。
- **バイナリ 1 つ、ほかには何も要らない。** CGO も、データベースのデーモンも、Node.js も不要で、外部の Go モジュールにも一切依存しません。

<p align="center">
  <img src="docs/assets/overview.ja.png" alt="障害をシミュレートしている Overview：仮想地図には東京からフランクフルトまでの場所がカードで並び、失敗している経路が琥珀色で描かれている。ステータスカードには 5 件のインシデント、その横に対処が必要な項目の一覧と、アプリケーションごとにまとまったサービスがあり、プロキシ経路に対処が必要と表示されている" width="100%">
  <br><sub>障害をシミュレートしている Overview。<code>topo preview --lang ja --scenario 1</code> より。</sub>
</p>

## すぐに試す

[リリースページ](https://github.com/WhitenWhiten/topophany/releases) (Linux、macOS、Windows · amd64、arm64) からダウンロードするか、Go 1.26.11 以降でインストールします。

```sh
go install github.com/WhitenWhiten/topophany/cmd/topo@latest
```

何も準備しなくても、合成データで UI 全体を試せます。`x` を押せば障害を起こせます。

```sh
topo preview
```

自分のネットワークを監視するには、[ユーザーガイド](docs/GUIDE.md) (英語) に沿って、[`examples/`](examples/) のトポロジーから始めてください。設定項目はすべて[リファレンス](docs/REFERENCE.md) (英語) にあります。アップグレードする場合は [1.7.0 のリリースノート](docs/releases/v1.7.0.md) (英語) を読んでください。

## しくみ

<p align="center">
  <img src="docs/assets/architecture.ja.svg" alt="自宅のノート PC、東京の VPS、オフィスのサーバーで動くワーカーが 2 台のコレクターに報告し、コレクターは根拠を追記専用のジャーナルに保存する。それを TUI、スクリプトとエージェント、メニューバーのウィジェットが読む。トポロジーファイルがワーカーとコレクターに何をチェックするかを伝え、Beszel、Komari、CF-Server-Monitor はコレクターなしで直接読むこともできる" width="100%">
</p>

ワーカーがチェックを行い、見たものを 1 つか 2 つのコレクターに届けます。コレクターは追記専用のジャーナルを持ち、健全性、インシデント、鮮度を判断します。クライアントは読むだけです。TUI を閉じても監視は止まらず、Topophany がルート、プロキシ、サービスを変更することはありません。

## プロダクトモード

<p align="center">
  <img src="docs/assets/products.ja.png" alt="障害中のプロダクトモード：4 つのプロダクトとその各部分が並び、それぞれの状態、エッジからデータベースまで載っているもの、遅延の中央値、最近のサンプル、稼働率を示す。Cloudflare の後ろにある 2 つの部分がリスボンから見て HTTP 521 で失敗しており、期限の近い証明書が最下部に並ぶ" width="100%">
  <br><sub>Cloudflare の後ろのサーバーが応答しなくなった状態。<code>topo preview --config examples/products.json --mode products --scenario 2</code> より。</sub>
</p>

`P` を押すと Overview がボードに変わります。プロダクトの各部分が 1 行ずつ並び、エッジからデータベースまで何の上に載っているか、遅延、最近のサンプル、稼働率を示します。問題のある部分には、何が、いつから、どこから見て失敗しているかが添えられます。ボードのマークはどれもネットワークビューと同じです。[プロダクトモードのガイド](docs/PRODUCTS.md) (英語)

## ターミナル UI

<p align="center">
  <img src="docs/assets/views.ja.gif" alt="大阪の MacBook から見た Overview では回線サーバーへの経路がダウンし、ニューヨークのサーバーから見ると正常。続いて Topology タブの仮想地図、全画面に広がって場所のカードと点を切り替える地図、ネットワーク図" width="100%">
  <br><sub>大阪からはダウン、ニューヨークからは正常。各観測点は自分に見えるものだけを報告します (<code>v</code>)。続いて 3 種類のマップ (<code>m</code>)。</sub>
</p>

Overview から Incidents まで 9 つのタブがあり、サービスは属するアプリケーションごとに、チェックは検証するサービスごとにまとまります。すべてのタブ、マップ、キーは[ユーザーガイド](docs/GUIDE.md) (英語) で説明しています。

## macOS のメニューバーで

<p align="center">
  <a href="https://github.com/WhitenWhiten/topophany-bar"><img src="docs/assets/topophany-bar.jpg" alt="topophany-bar を動かしている macOS のデスクトップ。メニューバーには白夜のアイコンがあり、部分月食の月の横に 2 の数字。その下に開いたパネルには、警告 2 件で低下したネットワーク、それを見ているオブザーバー、地図が表示され、デスクトップにはステータス、サーバー、タイムゾーン、サービス、地図のウィジェットが並ぶ" width="100%"></a>
</p>

[**topophany-bar**](https://github.com/WhitenWhiten/topophany-bar) は Topophany の macOS 用コンパニオンです。状態をメニューバーに、各問題がどこから見えているかをパネルに、サーバー、サービス、地図、タイムゾーンをデスクトップのウィジェットに表示します。現在はプレリリースで、macOS 26 以降が必要です。

## ドキュメント

ドキュメントは英語のみです (中国語のタスク別ガイドを除く)。

| | |
|---|---|
| [ユーザーガイド](docs/GUIDE.md) · [中国語ガイド](docs/GUIDE.zh.md) | 導入、TUI、プレビューモード、プローブ、複数拠点での運用 |
| [設定と API のリファレンス](docs/REFERENCE.md) | すべてのエンティティ、プローブのパラメーター、API エンドポイント |
| [プロダクトモード](docs/PRODUCTS.md) | アプリやサイトを出す人のための Overview |
| [Cloudflare](docs/CLOUDFLARE.md) · [直接接続モード](docs/DIRECT.md) | エッジからオリジンまで。コレクターなしで読むリソースのソース |
| [運用](docs/OPERATIONS.md) · [メモリ](docs/MEMORY.md) | デュアルコレクター、systemd ユニット、ストレージ、復旧、メモリ予算 |
| [セキュリティモデル](docs/SECURITY.md) · [リモート管理](docs/MANAGEMENT.md) | 認証情報、通信、オプトインの設定配信と更新 |
| [シナリオ](docs/SCENARIOS.md) · [受け入れ](docs/ACCEPTANCE.md) | テストで証明できること、実機で確かめる必要があること |
| [サンプル](examples/) | 出発点になるトポロジーの例 |
| [変更履歴](CHANGELOG.md) · [リリースノート](docs/releases/) | リリースの履歴とアップグレードの注意 |

## 開発

```sh
go test ./...                # unit and integration tests
python3 scripts/verify.py    # the full acceptance suite (Linux, Go, Python 3)
make captures architecture   # redraw the README's TUI captures and diagram
```

変更を送る前に [CONTRIBUTING.md](CONTRIBUTING.md) と[行動規範](CODE_OF_CONDUCT.md)を読んでください。脆弱性は [SECURITY.md](SECURITY.md) の手順に従って非公開で報告してください。

## ライセンス

[MIT](LICENSE)
