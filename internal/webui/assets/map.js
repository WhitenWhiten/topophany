import { el, colors, glyphs } from "./core.js";
const NS = "http://www.w3.org/2000/svg";
function svg(tag, attrs = {}, ...children) {
  const n = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, String(v));
  for (const c of children.flat())
    n.append(typeof c === "string" ? document.createTextNode(c) : c);
  return n;
}
export class NetworkMap {
  constructor(onSelect) {
    this.onSelect = onSelect;
    this.camera = null;
    this.moves = new Map();
    this.filter = "";
    this.labels = "cards";
    this.container = el("section", {
      class: "map-wrap",
      "aria-label": "Interactive network map",
    });
    this.diagram = svg("svg", {
      role: "img",
      "aria-label":
        "Network topology. Drag to pan, scroll to zoom; select a node for evidence.",
    });
    this.container.append(this.diagram);
    this.legend = el("div", { class: "map-legend" });
    this.container.append(this.legend);
    this.drag = null;
    this.bind();
  }
  bind() {
    this.diagram.addEventListener(
      "wheel",
      (e) => {
        e.preventDefault();
        this.zoom(e.deltaY < 0 ? 1.15 : 1 / 1.15);
      },
      { passive: false },
    );
    this.diagram.addEventListener("pointerdown", (e) => {
      if (e.button !== 0) return;
      const target = e.target.closest("[data-node]");
      const p = this.point(e);
      this.drag = {
        id: e.pointerId,
        node: target?.getAttribute("data-node"),
        start: p,
        last: p,
        moved: false,
      };
      this.diagram.setPointerCapture(e.pointerId);
    });
    this.diagram.addEventListener("pointermove", (e) => {
      if (!this.drag || e.pointerId !== this.drag.id) return;
      const p = this.point(e),
        d = this.drag;
      const dx = p.x - d.last.x,
        dy = p.y - d.last.y;
      if (Math.abs(p.x - d.start.x) + Math.abs(p.y - d.start.y) > 4)
        d.moved = true;
      if (d.node && d.moved) {
        const n = this.data.nodes.find((n) => n.id === d.node);
        if (n) {
          const old = this.moves.get(n.id) || [n.x, n.y];
          this.moves.set(n.id, [old[0] + dx, old[1] + dy]);
          this.paint();
        }
      } else if (!d.node) {
        this.camera[0] -= dx;
        this.camera[1] -= dy;
        this.setCamera();
      }
      d.last = this.point(e);
    });
    this.diagram.addEventListener("pointerup", (e) => {
      if (!this.drag) return;
      const d = this.drag;
      this.drag = null;
      if (d.node && !d.moved) this.selectNode(d.node);
      if (this.diagram.hasPointerCapture(e.pointerId))
        this.diagram.releasePointerCapture(e.pointerId);
    });
    this.diagram.addEventListener("pointercancel", () => {
      this.drag = null;
    });
    this.diagram.addEventListener("keydown", (e) => {
      const id = e.target.closest("[data-node]")?.getAttribute("data-node");
      if (id && (e.key === "Enter" || e.key === " ")) {
        e.preventDefault();
        e.stopPropagation();
        this.selectNode(id);
      }
    });
  }
  point(e) {
    const p = new DOMPoint(e.clientX, e.clientY);
    const matrix = this.diagram.getScreenCTM();
    return matrix ? p.matrixTransform(matrix.inverse()) : p;
  }
  selectNode(id) {
    const node = this.data.nodes.find((n) => n.id === id);
    if (node?.kind === "colo") {
      this.onSelect({
        kind: "colo",
        id,
        title: node.name,
        details: [
          `Reported edge: ${node.name}`,
          `Signal: ${node.display_signal}`,
          `Place: ${node.place || "unknown"}`,
          "A reported colo is not proof of origin application health.",
        ],
      });
      return;
    }
    this.onSelect({ kind: "node", id });
  }
  update(data, { labels = "cards", filter = "", full = false } = {}) {
    const changed =
      this.data?.mode !== data.mode || this.data?.width !== data.width;
    this.data = data;
    this.labels = labels;
    this.filter = filter.toLowerCase();
    this.container.classList.toggle("full", full);
    if (changed) {
      this.camera = null;
      this.moves.clear();
    }
    if (!this.camera) this.camera = [0, 0, data.width, data.height];
    this.setCamera();
    this.paint();
  }
  setCamera() {
    if (this.camera)
      this.diagram.setAttribute("viewBox", this.camera.join(" "));
  }
  zoom(factor) {
    if (!this.camera) return;
    const [x, y, w, h] = this.camera;
    const nw = Math.max(120, Math.min(this.data.width * 4, w / factor)),
      nh = (nw * h) / w;
    this.camera = [x + (w - nw) / 2, y + (h - nh) / 2, nw, nh];
    this.setCamera();
  }
  fit() {
    this.camera = [0, 0, this.data.width, this.data.height];
    this.moves.clear();
    this.setCamera();
    this.paint();
  }
  paint() {
    const d = this.data;
    if (!d) return;
    this.diagram.replaceChildren();
    this.legend.replaceChildren();
    if (d.notice)
      this.legend.append(el("span", { class: "error", text: d.notice }));
    for (const [sig, color] of Object.entries(colors))
      this.legend.append(
        el("span", { class: sig, text: `${glyphs[sig]} ${sig}` }),
      );
    this.legend.append(
      el("span", {
        text: "Drag nodes / background · scroll to zoom · click for evidence · declared routes ≠ measured links",
      }),
    );
    if (d.globe) {
      const dots = svg("g", {
        "aria-label": "Declared places on the original Topophany globe",
      });
      for (const dot of d.globe)
        dots.append(
          svg("circle", { cx: dot.x, cy: dot.y, r: 1.5, fill: dot.color }),
        );
      this.diagram.append(dots);
      this.legend.replaceChildren(
        el("span", {
          text: "Each lit point on the globe is a place you declared. Drag to pan; scroll to zoom.",
        }),
      );
    }
    if (d.land) {
      const land = svg("g", { "aria-hidden": "true", class: "map-land" });
      d.land.forEach((line, i) =>
        land.append(
          svg(
            "text",
            {
              x: 0,
              y: i * 16 + 14,
              textLength: d.width,
              lengthAdjust: "spacingAndGlyphs",
            },
            line,
          ),
        ),
      );
      this.diagram.append(land);
    }
    for (const p of d.places || []) {
      if (d.mode === "globe") {
        this.diagram.append(
          svg("text", { x: p.x, y: p.y, class: "map-place-label" }, p.name),
        );
        continue;
      }
      this.diagram.append(
        svg("rect", {
          x: p.x,
          y: p.y,
          width: p.w,
          height: p.h,
          class: "map-place",
        }),
      );
      this.diagram.append(
        svg(
          "text",
          { x: p.x + 5, y: p.y + 12, class: "map-place-label" },
          p.name,
        ),
      );
    }
    const visible = new Map(
      (d.nodes || [])
        .filter(
          (n) =>
            !this.filter ||
            `${n.name} ${n.id} ${n.place || ""} ${n.group || ""}`
              .toLowerCase()
              .includes(this.filter),
        )
        .map((n) => [n.id, n]),
    );
    const xy = (n) => this.moves.get(n.id) || [n.x, n.y];
    for (const e of d.edges || []) {
      const a = visible.get(e.from),
        b = visible.get(e.to);
      if (!a || !b) continue;
      const [x1, y1] = xy(a),
        [x2, y2] = xy(b);
      let path;
      if (
        e.points?.length &&
        !this.moves.has(e.from) &&
        !this.moves.has(e.to)
      ) {
        path = e.points
          .map(([x, y], i) => `${i ? "L" : "M"}${x},${y}`)
          .join(" ");
      } else if (d.mode === "geo") {
        const mid = (x1 + x2) / 2,
          bend = Math.min(150, Math.abs(x2 - x1) * 0.25);
        path = `M${x1},${y1} Q${mid},${Math.min(y1, y2) - bend} ${x2},${y2}`;
      } else {
        const mid = (x1 + x2) / 2;
        path = `M${x1},${y1} H${mid} V${y2} H${x2}`;
      }
      const wire = svg(
        "path",
        {
          d: path,
          stroke: colors[e.signal] || colors.unknown,
          class: `map-edge${e.focus ? " focus" : ""}`,
          "stroke-dasharray": ["carried", "unknown"].includes(e.signal)
            ? "5 5"
            : e.kind === "contains"
              ? "2 4"
              : "",
          tabindex: 0,
          role: "button",
          "aria-label": `${e.label || e.id}: ${e.signal}`,
        },
        svg("title", {}, `${e.label || e.id} · ${e.signal} · ${e.kind}`),
      );
      const select = () =>
        this.onSelect({
          kind: e.kind === "link" ? "link" : "path",
          id: e.id,
          edge: e,
        });
      wire.addEventListener("click", (e) => {
        e.stopPropagation();
        select();
      });
      wire.addEventListener("pointerdown", (e) => e.stopPropagation());
      wire.addEventListener("keydown", (e) => {
        if (e.key === "Enter") {
          e.stopPropagation();
          select();
        }
      });
      this.diagram.append(wire);
    }
    for (const n of visible.values()) {
      const [x, y] = xy(n),
        g = svg("g", {
          transform: `translate(${x},${y})`,
          class: `map-node${n.focus ? " focus" : ""}`,
          "data-node": n.id,
          tabindex: 0,
          role: "button",
          "aria-label": `${n.name}: ${n.display_signal}`,
        });
      const title = `${n.name} · ${n.display_signal} · ${n.place || "no declared location"}${n.inherited ? " (inherited)" : ""}`;
      g.append(svg("title", {}, title));
      if (this.labels === "cards") {
        g.append(
          svg("rect", {
            x: -6,
            y: -15,
            width: Math.min(250, Math.max(60, n.name.length * 8 + 32)),
            height: 24,
          }),
        );
      }
      g.append(
        svg(
          "text",
          {
            x: 0,
            y: 2,
            fill: colors[n.display_signal] || colors.unknown,
            class: "marker",
          },
          glyphs[n.display_signal] || "○",
        ),
      );
      if (this.labels !== "points")
        g.append(
          svg(
            "text",
            { x: 20, y: 2 },
            this.labels === "locations" ? n.place || n.name : n.name,
          ),
        );
      this.diagram.append(g);
    }
  }
}
