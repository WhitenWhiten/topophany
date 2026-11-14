#!/usr/bin/env python3
"""Draw the README's architecture diagram, docs/assets/architecture.<language>.svg.

One diagram for each README: the topology file, the workers that measure, the
collectors that keep the evidence and the clients that read it. The SVG keeps
its own dark ground, as the TUI captures beside it do, so it reads the same on
a light or a dark page. Usage: python3 scripts/architecture.py
"""
from __future__ import annotations

from pathlib import Path
from xml.sax.saxutils import escape

OUT = Path(__file__).resolve().parents[1] / "docs" / "assets"

BG, PANEL, EDGE, INK, INK2, TEAL, WIRE = "#0a1016", "#0f1b23", "#22394a", "#dbe7ee", "#8da3b0", "#5eead4", "#3c5f70"
MONO = "ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace"
SANS = {
    "en": "-apple-system, BlinkMacSystemFont, 'Segoe UI', 'Noto Sans', Helvetica, Arial, sans-serif",
    "ja": "'Hiragino Sans', 'Hiragino Kaku Gothic ProN', 'Yu Gothic UI', Meiryo, 'Noto Sans JP', 'Noto Sans CJK JP', -apple-system, 'Segoe UI', sans-serif",
    "zh": "'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', 'Noto Sans SC', 'Noto Sans CJK SC', -apple-system, 'Segoe UI', sans-serif",
}

TEXT = {
    "en": {
        "title": "A map of your network as seen from where you are, and the evidence for it",
        "file": ("topology.json", "what runs where, how each machine is reached, what to check",
                 "written by you or your agent · checked by topo validate"),
        "direct": ("Beszel · Komari · CF-Server-Monitor", "resource sources topo can also read",
                   "directly, with no collector"),
        "cols": [("WORKERS", "measure from where you connect"), ("COLLECTORS", "keep the evidence"),
                 ("CLIENTS", "read it and change nothing")],
        "workers": [("topo worker", "laptop at home"), ("topo worker", "VPS in Tokyo"), ("topo worker", "server at the office")],
        "collectors": [("topo serve", "collector A"), ("topo serve", "collector B")],
        "keeps": ["append-only journal, fsync before ack", "health · incidents · freshness", "alerts to ntfy · Slack · Discord"],
        "clients": [("topo", "the terminal UI"), ("topo status · show", "text or JSON for scripts and agents"),
                    ("topo monitor", "menu bar and desktop widgets")],
        "foot": "One Go binary plays every role. Workers buffer on disk while the network is down.",
    },
    "ja": {
        "title": "いる場所から見たネットワークの地図と、その根拠",
        "file": ("topology.json", "何がどこで動き、どうたどり着き、何をチェックするか",
                 "あなたかエージェントが書き、topo validate で検証"),
        "direct": ("Beszel · Komari · CF-Server-Monitor", "リソースのソースは topo が",
                   "コレクターなしで直接読むこともできる"),
        "cols": [("ワーカー", "ネットワークを使う場所から測る"), ("コレクター", "根拠を保存する"),
                 ("クライアント", "読むだけで、何も変えない")],
        "workers": [("topo worker", "自宅のノート PC"), ("topo worker", "東京の VPS"), ("topo worker", "オフィスのサーバー")],
        "collectors": [("topo serve", "コレクター A"), ("topo serve", "コレクター B")],
        "keeps": ["追記専用のジャーナル (fsync 後に応答)", "健全性 · インシデント · 鮮度", "ntfy · Slack · Discord へ通知"],
        "clients": [("topo", "ターミナル UI"), ("topo status · show", "スクリプトとエージェント向け (JSON)"),
                    ("topo monitor", "メニューバーとウィジェット")],
        "foot": "どの役割も Go のバイナリ 1 つ。ネットワークが落ちている間、ワーカーはディスクに溜めておきます。",
    },
    "zh": {
        "title": "从你所在之处看到的网络地图，以及它的依据",
        "file": ("topology.json", "什么跑在哪里、每台机器怎么连上、要检查什么",
                 "由你或你的 Agent 编写，用 topo validate 校验"),
        "direct": ("Beszel · Komari · CF-Server-Monitor", "这些资源数据源也可以",
                   "由 topo 直接读取，无需采集器"),
        "cols": [("WORKER", "从你上网的地方测量"), ("采集器", "保存证据"),
                 ("客户端", "只读，不改动任何东西")],
        "workers": [("topo worker", "家里的笔记本"), ("topo worker", "东京的 VPS"), ("topo worker", "公司的服务器")],
        "collectors": [("topo serve", "采集器 A"), ("topo serve", "采集器 B")],
        "keeps": ["只追加写入的日志，先 fsync 再确认", "健康状态 · 事件 · 新鲜度", "推送到 ntfy · Slack · Discord"],
        "clients": [("topo", "终端界面"), ("topo status · show", "文本或 JSON，给脚本和 Agent"),
                    ("topo monitor", "菜单栏和桌面小组件")],
        "foot": "所有角色都是同一个 Go 二进制。网络中断时，worker 先把结果缓存在磁盘上。",
    },
}

W, H = 1000, 500
COLS = [36, 360, 684]  # left edge of each column
CW = 280               # column width
BOX_H, GAP = 58, 14
ROW0 = 214             # top of the first box in each column


def text(x, y, s, size=13, fill=INK, family="sans", weight=400, anchor="start", spacing=None, lang="en"):
    fam = MONO if family == "mono" else SANS[lang]
    extra = f' letter-spacing="{spacing}"' if spacing else ""
    return (f'<text x="{x}" y="{y}" font-family="{fam}" font-size="{size}" font-weight="{weight}" '
            f'fill="{fill}" text-anchor="{anchor}"{extra}>{escape(s)}</text>')


def box(x, y, w, h, dashed=False, accent=False):
    dash = ' stroke-dasharray="5 4"' if dashed else ""
    stroke = TEAL if accent else EDGE
    return f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="8" fill="{PANEL}" stroke="{stroke}" stroke-width="1.2"{dash}/>'


def node(x, y, cmd, what, lang):
    return [box(x, y, CW, BOX_H),
            f'<circle cx="{x + 20}" cy="{y + 22}" r="4" fill="{TEAL}"/>',
            text(x + 34, y + 27, cmd, 14, INK, "mono", 600, lang=lang),
            text(x + 34, y + 46, what, 12.5, INK2, lang=lang)]


def arrow(x1, y1, x2, y2, dashed=False):
    dash = ' stroke-dasharray="4 4"' if dashed else ""
    return (f'<path d="M{x1} {y1} L{x2} {y2}" stroke="{WIRE}" stroke-width="1.6" fill="none"{dash} '
            f'marker-end="url(#head)"/>')


def line(points):
    d = "M" + " L".join(f"{x} {y}" for x, y in points)
    return f'<path d="{d}" stroke="{WIRE}" stroke-width="1.6" fill="none"/>'


def draw(lang):
    t = TEXT[lang]
    tag = {"en": "en", "ja": "ja", "zh": "zh-Hans"}[lang]
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" xml:lang="{tag}" lang="{tag}" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img" '
           f'aria-label="{escape(t["title"])}">',
           f'<title>{escape(t["title"])}</title>',
           f'<defs><marker id="head" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto">'
           f'<path d="M0 0 L10 5 L0 10 z" fill="{WIRE}"/></marker></defs>',
           f'<rect width="{W}" height="{H}" rx="14" fill="{BG}"/>']

    # The topology file, read by workers and collectors.
    fx, fy, fw, fh = COLS[0], 28, COLS[1] + CW - COLS[0], 76
    out += [box(fx, fy, fw, fh, accent=True),
            text(fx + 20, fy + 28, t["file"][0], 15, TEAL, "mono", 600, lang=lang),
            text(fx + 20, fy + 50, t["file"][1], 13, INK, lang=lang),
            text(fx + 20, fy + 67, t["file"][2], 12.5, INK2, lang=lang)]
    for c in (0, 1):
        cx = COLS[c] + CW // 2
        out.append(arrow(cx, fy + fh, cx, 134))

    # Resource sources topo can read without a collector.
    dx = COLS[2]
    out += [box(dx, fy, CW, fh, dashed=True),
            text(dx + 20, fy + 28, t["direct"][0], 13.5, INK, "sans", 600, lang=lang),
            text(dx + 20, fy + 50, t["direct"][1], 12.5, INK2, lang=lang),
            text(dx + 20, fy + 67, t["direct"][2], 12.5, INK2, lang=lang)]
    out.append(arrow(dx + CW // 2, fy + fh, dx + CW // 2, 134, dashed=True))

    # Column heads.
    for (head, sub), x in zip(t["cols"], COLS):
        out.append(text(x, 162, head, 13, TEAL, "mono" if lang == "en" or head.isascii() else "sans", 700,
                        spacing="1.5" if head.isascii() else None, lang=lang))
        out.append(text(x, 184, sub, 13, INK2, lang=lang))

    rows = [ROW0 + i * (BOX_H + GAP) for i in range(3)]
    mid = [y + BOX_H // 2 for y in rows]

    # Workers.
    for (cmd, what), y in zip(t["workers"], rows):
        out += node(COLS[0], y, cmd, what, lang)
    # Collectors, and what they keep.
    for (cmd, what), y in zip(t["collectors"], rows):
        out += node(COLS[1], y, cmd, what, lang)
    ky = rows[2] + 8
    for i, s in enumerate(t["keeps"]):
        out.append(f'<circle cx="{COLS[1] + 6}" cy="{ky + 10 + i * 22 - 4}" r="2.2" fill="{INK2}"/>')
        out.append(text(COLS[1] + 18, ky + 10 + i * 22, s, 12.5, INK2, lang=lang))
    # Clients.
    for (cmd, what), y in zip(t["clients"], rows):
        out += node(COLS[2], y, cmd, what, lang)

    # Every worker reports to both collectors: a bus between the columns.
    bus1 = (COLS[0] + CW + COLS[1]) // 2
    for y in mid:
        out.append(line([(COLS[0] + CW, y), (bus1, y)]))
    out.append(line([(bus1, mid[0]), (bus1, mid[2])]))
    for y in mid[:2]:
        out.append(arrow(bus1, y, COLS[1] - 2, y))
    # Every client reads either collector.
    bus2 = (COLS[1] + CW + COLS[2]) // 2
    for y in mid[:2]:
        out.append(line([(COLS[1] + CW, y), (bus2, y)]))
    out.append(line([(bus2, mid[0]), (bus2, mid[2])]))
    for y in mid:
        out.append(arrow(bus2, y, COLS[2] - 2, y))

    out.append(f'<path d="M{COLS[0]} {H - 52} L{W - 36} {H - 52}" stroke="{EDGE}" stroke-width="1"/>')
    out.append(text(COLS[0], H - 26, t["foot"], 13, INK2, lang=lang))
    out.append("</svg>")
    return "\n".join(out) + "\n"


def main() -> None:
    for lang in TEXT:
        out = OUT / f"architecture.{lang}.svg"
        out.write_text(draw(lang), encoding="utf-8")
        print(f"wrote {out.relative_to(OUT.parents[1])}")


if __name__ == "__main__":
    main()
