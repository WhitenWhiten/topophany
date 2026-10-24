import {
  tabs,
  el,
  badge,
  date,
  safePreferences,
  queryFor,
  request,
  stream,
  download,
  outcomeClass,
  resourceSeries,
  APIError,
} from "./core.js";
import { NetworkMap } from "./map.js";
const $ = (id) => document.getElementById(id);
let prefs;
try {
  prefs = safePreferences(localStorage);
} catch {
  prefs = safePreferences({ getItem: () => null });
}
const state = {
  tab: tabs.indexOf(prefs.page),
  map: prefs.map,
  cards: prefs.cards,
  uniform: prefs.uniform,
  collapsed: prefs.collapsed,
  expanded: new Set(),
  folded: new Set(),
  observer: "",
  search: "",
  attention: false,
  by: "application",
  offset: 0,
  selection: null,
  labels: "cards",
  full: false,
  application: "",
  environment: "",
  service: "",
};
let token = "",
  data = null,
  rows = [],
  selected = 0,
  controller = null,
  retryTimer = null,
  session = 0,
  lastReceived = 0,
  detailAbort = null;
let map = new NetworkMap(selectMap);
let focusProjection = null;
let localDraft = false;
let serverList = false;
document.body.classList.toggle("no-color", prefs.noColor);
function button(text, onclick, attrs = {}) {
  return el("button", { type: "button", onclick, ...attrs }, text);
}
function panel(title, ...children) {
  return el(
    "section",
    { class: "panel" },
    el("h2", { text: title }),
    ...children,
  );
}
function errorText(err) {
  return err instanceof Error ? err.message : String(err);
}
function connection(text, error = false) {
  $("connection").textContent = text;
  $("connection").classList.toggle("error", error);
}
function setSelection(row) {
  state.selection = row;
  selected = Math.max(
    0,
    rows.findIndex((r) => r.kind === row.kind && r.id === row.id),
  );
  highlight();
}
function highlight(scroll = false) {
  document.querySelectorAll("[data-row]").forEach((n) => {
    const index = Number(n.dataset.row);
    n.classList.toggle("selected", index === selected);
    n.setAttribute("aria-selected", String(index === selected));
  });
  const active = document.querySelector(`[data-row="${selected}"]`);
  if (scroll) active?.scrollIntoView({ block: "nearest" });
}
function stopStream() {
  session++;
  controller?.abort();
  controller = null;
  clearTimeout(retryTimer);
  retryTimer = null;
}
function startStream() {
  stopStream();
  if (!token || document.hidden) return;
  const run = session,
    abort = new AbortController();
  controller = abort;
  connection(data ? "Updating…" : "Connecting…");
  let deadline = setTimeout(() => abort.abort("Stream startup timeout"), 20000);
  stream(`/v1/web/events?${queryFor(state)}`, token, abort.signal, (next) => {
    if (run !== session) return;
    clearTimeout(deadline);
    deadline = setTimeout(() => abort.abort("No update for 20 seconds"), 20000);
    lastReceived = Date.now();
    data = next;
    render();
    connection("Live · collector evidence · server updates every 5s");
  })
    .then(() => {
      if (run !== session) return;
      retryTimer = setTimeout(startStream, 300);
    })
    .catch((err) => {
      if (run !== session || document.hidden || !token) return;
      if (
        err instanceof APIError &&
        (err.status === 401 || err.status === 403)
      ) {
        disconnect();
        $("connect").append(
          el("p", {
            class: "error",
            text: `Connection refused: ${errorText(err)}`,
          }),
        );
        return;
      }
      connection(`Disconnected · retrying: ${errorText(err)}`, true);
      markCached();
      const delay = Math.min(
        30000,
        Math.max(1500, (Date.now() - lastReceived) / 2 || 1500),
      );
      retryTimer = setTimeout(startStream, delay);
    })
    .finally(() => clearTimeout(deadline));
}
function disconnect() {
  stopStream();
  detailAbort?.abort();
  token = "";
  data = null;
  rows = [];
  lastReceived = 0;
  focusProjection = null;
  $("detail-content").replaceChildren();
  state.selection = null;
  $("content").replaceChildren();
  $("workspace").hidden = true;
  $("connect").hidden = false;
  $("disconnect").hidden = true;
  $("token").value = "";
  $("identity").textContent =
    "From where, through which path, reaching what — and on what evidence.";
  $("freshness").textContent = "Disconnected";
  for (const id of ["detail", "editor", "preferences"])
    if ($(id).open) $(id).close();
  map = new NetworkMap(selectMap);
}
function markCached() {
  if (!data) return;
  if (!$("content").querySelector("[data-cached-notice]"))
    $("content").prepend(
      el("p", {
        "data-cached-notice": "",
        class: "cached",
        text: "CACHED — current status not established. Remaining evidence is historical.",
      }),
    );
  document.querySelectorAll("[data-state]").forEach((n) => {
    n.textContent = "○ cached";
    n.className = "cached";
  });
  document.querySelectorAll("[data-live]").forEach((n) => {
    n.textContent = "○ CACHED — current status not established";
    n.className = "cached";
  });
  // Keep historical details, but do not leave green current readings on screen.
  $("content").classList.add("is-cached");
  document
    .querySelectorAll(
      "#content .up,#content .down,#content .degraded,#content .affected,#detail .up,#detail .down,#detail .degraded",
    )
    .forEach((n) => {
      n.classList.remove("up", "down", "degraded", "affected");
      n.classList.add("cached");
    });
  if (data.map) {
    const cached = {
      ...data.map,
      globe: data.map.globe?.map((dot) => ({ ...dot, color: "#6b7a8c" })),
      nodes: data.map.nodes.map((n) => ({
        ...n,
        health: "unknown",
        display_signal: "unknown",
        affected: false,
      })),
      edges: data.map.edges.map((e) => ({
        ...e,
        health: "unknown",
        signal: "unknown",
      })),
    };
    map.update(cached, {
      labels: state.labels,
      filter: $("node-search").value,
      full: state.full,
    });
  }
  document
    .querySelectorAll("#content [data-resource],#detail [data-resource]")
    .forEach((n) =>
      n.replaceChildren(
        el("p", {
          class: "cached",
          text: "CACHED — current resource reading withheld",
        }),
      ),
    );
  if (
    $("detail").open &&
    !$("detail-content").querySelector("[data-cached-notice]")
  )
    $("detail-content").prepend(
      el("p", {
        "data-cached-notice": "",
        class: "cached",
        text: "CACHED — this evidence is historical; current status is not established.",
      }),
    );
}
function setTab(tab) {
  state.tab = (tab + tabs.length) % tabs.length;
  state.offset = 0;
  state.selection = null;
  selected = 0;
  state.expanded.clear();
  state.folded.clear();
  history.replaceState(null, "", `#${tabs[state.tab].toLowerCase()}`);
  startStream();
  renderTabs();
}
function renderTabs() {
  const nav = $("tabs");
  if (nav.dataset.tab === String(state.tab)) return;
  nav.dataset.tab = String(state.tab);
  nav.replaceChildren(
    ...tabs.map((name, i) =>
      button(`${i + 1} ${name}`, () => setTab(i), {
        role: "tab",
        "aria-selected": String(i === state.tab),
      }),
    ),
  );
  nav.setAttribute("role", "tablist");
}
function statusCounts() {
  return el(
    "div",
    { class: "counts" },
    ...Object.entries(data.counts).map(([key, count]) =>
      el("span", {
        class: key === "warning" ? "degraded" : key,
        text: `${count} ${key}`,
      }),
    ),
    el("span", { text: `${data.live}/${data.observer_count} observers` }),
  );
}
function render() {
  const previous = state.selection;
  rows = data.rows;
  if (previous) {
    const i = rows.findIndex(
      (r) => r.kind === previous.kind && r.id === previous.id,
    );
    if (i >= 0) selected = i;
    else selected = Math.min(selected, Math.max(0, rows.length - 1));
  } else selected = Math.min(selected, Math.max(0, rows.length - 1));
  $("identity").textContent =
    `${data.project} · ${data.instance} · ${data.observer || "all observers"} · ${data.version}`;
  $("workspace").hidden = false;
  $("connect").hidden = true;
  $("disconnect").hidden = false;
  const observer = $("observer"),
    known = JSON.stringify((data.observers || []).map((o) => [o.id, o.name]));
  if (observer.dataset.options !== known) {
    observer.replaceChildren(
      el("option", { value: "", text: "All observers" }),
      ...(data.observers || []).map((o) =>
        el("option", { value: o.id, text: o.name || o.id }),
      ),
    );
    observer.dataset.options = known;
  }
  observer.value = state.observer;
  renderTabs();
  $("by-label").hidden = ![3, 5].includes(state.tab);
  $("cards").hidden = ![2, 3].includes(state.tab);
  $("cards").textContent =
    state.tab === 2
      ? `Layout: ${state.uniform ? "uniform" : "wall"} s`
      : `View: ${state.cards ? "cards" : "list"} s`;
  $("collapse").hidden = ![3, 5].includes(state.tab);
  $("server-list").hidden = state.tab !== 2;
  $("server-list").textContent = serverList ? "Server cards" : "Server list";
  for (const [id, options, key] of [
    ["app-filter", data.applications || [], "application"],
    [
      "env-filter",
      (data.environments || []).map((e) => ({
        id: e || "-",
        name: e || "Unspecified",
      })),
      "environment",
    ],
    ["service-filter", data.service_options || [], "service"],
  ]) {
    const control = $(id);
    const signature = JSON.stringify(options);
    $(id + "-label")?.removeAttribute("hidden");
    if (control.dataset.options !== signature) {
      control.replaceChildren(
        el("option", { value: "", text: "All" }),
        ...options.map((o) =>
          el("option", { value: o.id, text: o.name || o.id }),
        ),
      );
      control.dataset.options = signature;
    }
    control.value = state[key];
    control.parentElement.hidden = ![3, 5].includes(state.tab);
  }
  $("map-tools").hidden = !data.map || state.tab === 8;
  $("map-mode").textContent = `Map: ${state.map} m`;
  $("labels").textContent = `Labels: ${state.labels} p`;
  $("scopes").hidden =
    !state.application && !state.environment && !state.service;
  if (!$("scopes").hidden)
    $("scopes").replaceChildren(
      el("span", {
        text: [state.application, state.environment, state.service]
          .filter(Boolean)
          .join(" / "),
      }),
      button("Clear scope", () => {
        state.application = state.environment = state.service = "";
        state.offset = 0;
        startStream();
      }),
    );
  const content = $("content");
  const opened = [...content.querySelectorAll(".summary details[open]")].map(
    (d) => d.querySelector("summary")?.textContent,
  );
  const focused = content.contains(document.activeElement)
    ? {
        tag: document.activeElement.tagName,
        text: document.activeElement.textContent,
        row: document.activeElement.closest("[data-row]")?.dataset.row,
      }
    : null;
  content.classList.remove("is-cached");
  content.replaceChildren();
  if (state.tab === 0) {
    content.append(overview());
    if (data.map) content.append(renderMap());
    if (!state.full) content.append(panel("Needs attention", table(rows)));
  } else if (state.tab === 8) content.append(about());
  else {
    if (data.map) content.append(renderMap());
    if (!state.full || !data.map) {
      if (state.tab === 2)
        content.append(serverList ? table(rows) : serverCards());
      else if (state.tab === 3 && state.cards) content.append(serviceCards());
      else content.append(table(rows));
    }
  }
  for (const d of content.querySelectorAll(".summary details"))
    if (opened.includes(d.querySelector("summary")?.textContent)) d.open = true;
  if (focused) {
    const target = [...content.querySelectorAll(focused.tag)].find(
      (n) =>
        n.textContent === focused.text &&
        n.closest("[data-row]")?.dataset.row === focused.row,
    );
    target?.focus({ preventScroll: true });
  }
  const pager = $("pager");
  pager.replaceChildren();
  if (data.total > 100)
    pager.append(
      button(
        "← Previous",
        () => {
          state.offset = Math.max(0, state.offset - 100);
          startStream();
        },
        { disabled: data.offset === 0 },
      ),
      el("span", {
        text: `${data.offset + 1}–${Math.min(data.total, data.offset + data.rows.length)} / ${data.total}`,
      }),
      button(
        "Next →",
        () => {
          state.offset += 100;
          startStream();
        },
        { disabled: data.offset + data.rows.length >= data.total },
      ),
    );
  $("freshness").textContent =
    `${date(data.generated_at)} · ${data.total} rows · ${data.revision.slice(0, 12)}`;
  highlight();
}
function overview() {
  const head = panel(
    "Network standing",
    el("strong", {
      class: `verdict ${data.verdict.state === "clear" ? "up" : data.verdict.state === "incidents" ? "down" : "unknown"}`,
      "data-live": "",
      text: `${data.verdict.glyph} ${data.verdict.text}`,
    }),
    statusCounts(),
    el("p", {
      class: "muted",
      text: "Health, evidence source and freshness are independent. An edge response does not establish origin health.",
    }),
  );
  const groups = (data.summary_services || []).map((g) =>
    el(
      "div",
      { class: "card-lines" },
      button(
        `${g.Name}${g.Environment ? " / " + g.Environment : ""} · ${g.Health}`,
        () => {
          state.application = g.ID;
          state.environment = g.Environment;
          setTab(3);
        },
      ),
      ...(g.Rows || []).map((r) =>
        button(`${r.title} · ${r.note || r.health}`, () => openDetail(r), {
          class: "text-button",
        }),
      ),
    ),
  );
  const observers = (data.summary_observers || []).map((r) =>
    el(
      "p",
      {},
      badge(r),
      " ",
      button(r.title, () => openDetail(r), { class: "text-button" }),
      " ",
      r.cols?.join(" · "),
    ),
  );
  return el(
    "div",
    { class: "summary" },
    head,
    panel("Applications & services", ...groups),
    panel("Observers", ...observers),
    ...[
      ["Servers", data.summary_servers],
      ["Paths", data.summary_paths],
    ].map(([name, list]) =>
      panel(
        name,
        ...(list || []).map((r) =>
          el(
            "p",
            {},
            badge(r),
            " ",
            button(r.title, () => openDetail(r), { class: "text-button" }),
          ),
        ),
      ),
    ),
    panel(
      "Collector",
      el("p", { text: `${data.instance} · ${data.version}` }),
      el("p", { text: `Revision ${data.revision.slice(0, 16)}` }),
      data.storage
        ? el(
            "details",
            {},
            el("summary", { text: "Storage budgets and delivery state" }),
            structured(data.storage),
          )
        : null,
      data.ingress
        ? el(
            "details",
            {},
            el("summary", { text: "Ingress evidence" }),
            structured(data.ingress),
          )
        : null,
      button("Servers →", () => setTab(2)),
      button("Paths →", () => setTab(4)),
    ),
  );
}
function table(list) {
  if (!list.length)
    return el("p", {
      class: "muted",
      text: "No matching entries. Unmeasured targets remain unknown.",
    });
  const cols = data.columns || ["ITEM"];
  const body = el("tbody");
  list.forEach((row, i) => {
    const tr = el("tr", {
      "data-row": i,
      tabindex: 0,
      class: row.kind === "group" ? "group" : "",
      "aria-selected": String(i === selected),
      onclick: () => {
        selected = i;
        setSelection(row);
        if (row.kind === "group") toggleGroup(row);
        else openDetail(row);
      },
      ondblclick: () => {
        if (row.kind === "group") scopeGroup(row);
      },
      onkeydown: (e) => {
        if (e.key === "Enter") {
          e.stopPropagation();
          selected = i;
          row.kind === "group" ? toggleGroup(row) : openDetail(row);
        }
      },
    });
    tr.append(
      el("td", {}, badge(row)),
      el(
        "td",
        {},
        row.tree || "",
        row.kind === "group"
          ? (state.collapsed && !state.expanded.has(row.id)) ||
            (!state.collapsed && state.folded.has(row.id))
            ? "▸ "
            : "▾ "
          : "",
        row.title,
        row.meta
          ? el("small", { class: "muted", text: ` · ${row.meta}` })
          : null,
      ),
    );
    for (const col of row.cols || []) tr.append(el("td", { text: col }));
    body.append(tr);
  });
  return el(
    "div",
    { class: "table-wrap" },
    el(
      "table",
      {},
      el(
        "thead",
        {},
        el(
          "tr",
          {},
          el("th", { text: "STATE" }),
          ...cols.map((c) => el("th", { text: c })),
        ),
      ),
      body,
    ),
  );
}
function toggleGroup(row) {
  if (state.search || state.attention) {
    scopeGroup(row);
    return;
  }
  const overrides = state.collapsed ? state.expanded : state.folded;
  if (overrides.has(row.id)) overrides.delete(row.id);
  else overrides.add(row.id);
  state.selection = null;
  state.offset = 0;
  startStream();
}
function scopeGroup(row) {
  // Group details are the authoritative scope; expanded members remain available
  // without depending on any frontend health/group aggregation.
  $("detail-title").textContent = row.title;
  $("detail-content").replaceChildren(
    el("pre", { text: (row.details || []).join("\n") }),
  );
  $("detail").showModal();
}
function renderMap() {
  let geometry = data.map;
  if (
    focusProjection &&
    state.selection?.id === focusProjection.id &&
    focusProjection.revision === data.revision &&
    focusProjection.map.mode === geometry.mode
  ) {
    const nodeFocus = new Map(
      focusProjection.map.nodes.map((n) => [n.id, n.focus]),
    );
    const edgeFocus = new Map(
      focusProjection.map.edges.map((e) => [
        e.id + "/" + e.from + "/" + e.to,
        e.focus,
      ]),
    );
    geometry = {
      ...geometry,
      nodes: geometry.nodes.map((n) => ({
        ...n,
        focus: nodeFocus.get(n.id) || false,
      })),
      edges: geometry.edges.map((e) => ({
        ...e,
        focus: edgeFocus.get(e.id + "/" + e.from + "/" + e.to) || false,
      })),
    };
  }
  map.update(geometry, {
    labels: state.labels,
    filter: $("node-search").value,
    full: state.full,
  });
  return map.container;
}
function resource(r) {
  const box = el("section", { "data-resource": "", class: "card-lines" });
  box.append(
    el("h3", {
      text: `Resources · ${r.observer?.name || r.observer?.id || ""}`,
    }),
    el("p", {
      class: r.health,
      text: `${r.health} · ${r.freshness} · ${r.external ? "reported" : "measured"} · ${date(r.sampled_at)}`,
    }),
    r.reason ? el("p", { class: "muted", text: r.reason }) : "",
  );
  if (r.freshness === "fresh" && r.facts) {
    const dl = el("dl", { class: "resource-grid" });
    for (const [key, value] of data.resource_values?.[r.check] ||
      Object.entries(r.facts))
      dl.append(
        el("dt", { text: key.replaceAll("_", " ") }),
        el("dd", { text: value }),
      );
    box.append(dl);
  } else
    box.append(
      el("p", { class: "unknown", text: "No current resource reading" }),
    );
  if (r.origin) box.append(structured(r.origin));
  box.append(
    button(
      "Resource history →",
      () => openDetail({ kind: "check", id: r.check }),
      { class: "text-button" },
    ),
  );
  return box;
}
function serverCards() {
  const grid = el("div", { class: `cards ${state.uniform ? "uniform" : ""}` });
  rows.forEach((row, i) => {
    const c = data.cards?.[row.id];
    if (!c) return;
    const card = el("article", {
      class: "panel",
      "data-row": i,
      tabindex: 0,
      onclick: (e) => {
        if (!e.target.closest("button,details")) openDetail(row);
      },
      onkeydown: (e) => {
        if (e.key === "Enter" && e.target === card) openDetail(row);
      },
    });
    card.append(
      el(
        "div",
        { class: "panel-title" },
        el("strong", { text: row.title }),
        badge(row),
      ),
      el("div", {
        class: "card-sub",
        text: [c.kind, c.group, c.where].filter(Boolean).join(" · "),
      }),
    );
    for (const r of c.resources || []) card.append(resource(r));
    for (const s of c.services || [])
      card.append(
        el(
          "div",
          {},
          el("span", { class: s.health, text: `${s.monitored ? "●" : "○"} ` }),
          button(
            s.name || s.id,
            () => openDetail({ kind: "service", id: s.id }),
            { class: "text-button" },
          ),
          s.runtime ? ` · ${s.runtime}` : "",
        ),
      );
    for (const ep of c.addresses || [])
      card.append(
        el("p", { class: "muted", text: `${ep.network}: ${ep.address}` }),
      );
    for (const host of c.hosts || [])
      card.append(
        el("p", {
          text: `└ ${host.name || host.id} · ${(host.runs || []).join(", ")}`,
        }),
      );
    for (const link of c.links || [])
      card.append(
        button(
          `${link.inbound ? "←" : "→"} ${link.other.name || link.other.id} · ${link.transport} · ${(link.reading || []).join(" · ")}`,
          () => openDetail({ kind: "link", id: link.id }),
          { class: "text-button" },
        ),
      );
    if (c.answered_from?.length)
      card.append(
        el("p", { text: `Answered from: ${c.answered_from.join(" · ")}` }),
      );
    if (c.memo) card.append(el("pre", { class: "card-lines", text: c.memo }));
    grid.append(card);
  });
  return grid;
}
function serviceCard(c) {
  const box = el("section", { class: "card-lines" });
  box.append(
    el("p", {
      class: c.health,
      text: `${c.note || c.health} · ${c.evidence || "unknown evidence"} · ${c.reason || ""}`,
    }),
    el("p", {
      class: "muted",
      text: `${c.application.name || "Ungrouped"} / ${c.environment || "Unspecified"} · ${c.host.name || c.host.id} · ${c.host.where || ""}`,
    }),
  );
  for (const seen of c.seen_from || []) {
    box.append(
      el("h3", {
        text: `${seen.label || seen.observer.name} · ${seen.health}`,
      }),
    );
    for (const check of seen.checks || [])
      box.append(
        button(
          `${check.name} · ${check.health} / ${check.freshness} · up 24h ${check.up_24h === null ? "unknown" : check.up_24h + "%"}${check.latency ? " · " + JSON.stringify(check.latency) : ""}${check.colo ? " · " + check.colo : ""}`,
          () => openDetail({ kind: "check", id: check.id }),
          { class: "text-button" },
        ),
      );
  }
  for (const ep of c.endpoints || [])
    box.append(
      el(
        "p",
        {},
        button(ep.address, () => openDetail({ kind: "endpoint", id: ep.id }), {
          class: "text-button",
        }),
        ` · ${ep.network}${ep.via?.length ? " · via " + ep.via.map((v) => v.name || v.id).join(" → ") : ""}`,
      ),
    );
  for (const [title, refs] of [
    ["Depends on", c.depends_on],
    ["Used by", c.used_by],
  ])
    if (refs?.length)
      box.append(
        el("h3", { text: title }),
        ...refs.map((ref) =>
          button(
            `${ref.name || ref.id} · ${ref.affected ? "affected" : ref.health}`,
            () => openDetail({ kind: "service", id: ref.id }),
            { class: "text-button" },
          ),
        ),
      );
  if (c.answered_from)
    box.append(
      el("p", {
        text: `Answered from: ${(c.answered_from.pops || []).join(" · ")} · cache: ${(c.answered_from.cache || []).join(" · ")}`,
      }),
    );
  if (c.memo)
    box.append(el("h3", { text: "Notes" }), el("pre", { text: c.memo }));
  if (c.runbook)
    box.append(el("h3", { text: "Runbook" }), el("pre", { text: c.runbook }));
  return box;
}
function serviceCards() {
  const grid = el("div", { class: "cards" });
  rows.forEach((row, i) => {
    if (row.kind === "group") {
      grid.append(
        el(
          "section",
          { class: "panel" },
          button(
            `${(state.collapsed && !state.expanded.has(row.id)) || (!state.collapsed && state.folded.has(row.id)) ? "▸" : "▾"} ${row.title}`,
            () => toggleGroup(row),
          ),
          button("Group evidence", () => scopeGroup(row)),
        ),
      );
      return;
    }
    const c = data.services?.[row.id];
    if (!c) return;
    grid.append(
      el(
        "article",
        {
          class: "panel",
          "data-row": i,
          tabindex: 0,
          onkeydown: (e) => {
            if (e.key === "Enter" && e.target === e.currentTarget)
              openDetail(row);
          },
        },
        el(
          "div",
          { class: "panel-title" },
          button(c.name, () => openDetail(row)),
          badge(row),
        ),
        serviceCard(c),
      ),
    );
  });
  return grid;
}
function about() {
  const keys = [
    ["1–9", "Switch pages"],
    ["Tab", "Browser focus navigation"],
    ["j / k / ↑ / ↓", "Move through rows"],
    ["Enter", "Evidence / expand group"],
    ["/", "Search"],
    ["v", "Cycle observer"],
    ["m / z / f / p", "Map mode / fit / full map / labels"],
    ["s", "Service cards / uniform server cards"],
    ["b / C / Space", "Group by / collapse all / toggle group"],
    ["i / c", "Evidence / service checks"],
    ["!", "Needs attention only"],
    ["r", "Refresh / reconnect"],
    [",", "Display preferences"],
    ["?", "About"],
    ["q", "Disconnect; monitoring continues"],
    ["Esc", "Close dialog / clear filter"],
  ];
  return el(
    "div",
    {},
    el(
      "section",
      { class: "panel" },
      el("h1", { text: "◈ TOPOPHANY" }),
      el("p", { text: "τόπος   tópos   place" }),
      el("p", { text: "φαίνω   phaínō  to bring to light · to appear" }),
      el("p", {
        text: "topophany — the appearance of a place; the moment a network shows where it actually is.",
      }),
      el("p", {
        text: "From where, through which path, reaching what, and on what evidence.",
      }),
      el("p", {
        text: `Topophany ${data.version} · MIT · self-hosted · ${data.project} · ${data.instance} · ${data.topology_counts?.nodes ?? 0} nodes · ${data.topology_counts?.links ?? 0} links · ${data.topology_counts?.checks ?? 0} checks`,
      }),
      el(
        "p",
        {},
        el("a", {
          href: "https://topophany.com",
          rel: "noreferrer",
          text: "Homepage",
        }),
        " · ",
        el("a", {
          href: "https://github.com/WhitenWhiten/topophany",
          rel: "noreferrer",
          text: "Source",
        }),
      ),
      el(
        "div",
        { class: "help" },
        ...keys.flatMap(([key, text]) => [
          el("kbd", { text: key }),
          el("span", { text }),
        ]),
      ),
      el("p", {
        class: "muted",
        text: "Local editor and synthetic preview remain distinct from live monitoring. Browser drafts never modify the collector. Browser tokens live only in memory.",
      }),
    ),
    renderMap(),
  );
}
function structured(value) {
  if (value === null || value === undefined)
    return el("span", { class: "muted", text: "unknown" });
  if (typeof value !== "object") return el("span", { text: String(value) });
  const list = el("dl", { class: "resource-grid" });
  for (const [key, item] of Object.entries(value)) {
    list.append(
      el("dt", { text: key.replaceAll("_", " ") }),
      el(
        "dd",
        {},
        typeof item === "object" && item !== null
          ? el(
              "details",
              {},
              el("summary", {
                text: Array.isArray(item)
                  ? `${item.length} entries`
                  : "Details",
              }),
              structured(item),
            )
          : String(item ?? "unknown"),
      ),
    );
  }
  return list;
}
async function selectMap(row) {
  if (row.kind === "colo") {
    scopeGroup(row);
    return;
  }
  if (row.edge) {
    // Non-link SVG edges can be containment, via or a path hop. The Go model
    // supplies exact evidence text; only declared entity IDs are navigable.
    if (!row.edge.target_id) {
      $("detail-title").textContent = row.edge.label || row.edge.id;
      $("detail-content").replaceChildren(structured(row.edge));
      $("detail").showModal();
      return;
    }
    row = { kind: row.edge.target_kind, id: row.edge.target_id };
  }
  await openDetail(row);
}
async function openDetail(row) {
  if (row.kind === "group") {
    scopeGroup(row);
    return;
  }
  if (!token) return;
  setSelection(row);
  detailAbort?.abort();
  const abort = new AbortController();
  detailAbort = abort;
  const marker = ++openDetail.serial;
  $("detail-title").textContent = row.title || row.id;
  $("detail-content").replaceChildren(el("p", { text: "Loading evidence…" }));
  if (!$("detail").open) $("detail").showModal();
  const q = queryFor({ ...state, selection: row });
  try {
    const detail = await request(`/v1/web/view?${q}`, token, {
      signal: abort.signal,
    });
    if (marker !== openDetail.serial) return;
    const body = $("detail-content");
    body.replaceChildren();
    const r = detail.detail;
    if (!r) throw new Error("Entity evidence unavailable");
    $("detail-title").textContent = r.title;
    body.append(badge(r), el("pre", { text: (r.details || []).join("\n") }));
    if (detail.map) {
      focusProjection = {
        id: row.id,
        revision: detail.revision,
        map: detail.map,
      };
      data.map = detail.map;
      map.update(detail.map, {
        labels: state.labels,
        filter: $("node-search").value,
        full: state.full,
      });
    }
    const card = detail.cards?.[r.id];
    if (card) {
      for (const resourceReport of card.resources || [])
        body.append(resource(resourceReport));
      body.append(
        el(
          "details",
          {},
          el("summary", { text: "Full server card" }),
          structured(card),
        ),
      );
    }
    const service = detail.services?.[r.id];
    if (service) body.append(serviceCard(service));
    if (r.kind === "path") {
      const route = detail.routes?.find((p) => p.id === r.id);
      if (route) {
        body.append(
          el("h3", { text: "Declared route — not packet-level evidence" }),
          el(
            "div",
            { class: "route" },
            ...(route.nodes || []).flatMap((id, i) => [
              i ? "→" : "",
              button(id, () => openDetail({ kind: "node", id })),
            ]),
          ),
        );
        if (route.proxies?.length)
          body.append(
            el("h3", { text: "Explicit proxy chain" }),
            structured(route.proxies),
          );
        body.append(
          structured({
            candidates: route.candidates || [],
            depends_on: route.depends_on || [],
            opaque: route.opaque,
          }),
        );
      }
    }
    if (detail.related_checks?.length)
      body.append(
        el("h3", { text: "Checks & evidence" }),
        ...detail.related_checks.map((c) =>
          el(
            "p",
            {},
            badge(c),
            " ",
            button(c.title, () => openDetail(c), { class: "text-button" }),
          ),
        ),
      );
    if (r.kind === "observer")
      body.append(
        button("Use this observer view", () => {
          state.observer = r.id;
          $("observer").value = r.id;
          $("detail").close();
          startStream();
        }),
      );
    if (r.kind === "check") {
      const history = el("section", {});
      body.append(history);
      await checkHistory(r.id, history, abort.signal);
    }
    if (Date.now() - lastReceived > 15000) markCached();
  } catch (err) {
    if (abort.signal.aborted) return;
    $("detail-content").replaceChildren(
      el("p", {
        class: "error",
        text: `Evidence unavailable: ${errorText(err)}`,
      }),
    );
  }
}
openDetail.serial = 0;
async function checkHistory(id, target, signal) {
  target.replaceChildren(
    el("h3", { text: "Recent samples" }),
    el("p", { text: "Loading history…" }),
  );
  try {
    const h = await request(
      `/v1/web/history?${new URLSearchParams({ check: id })}`,
      token,
      { signal },
    );
    target.replaceChildren(
      el("h3", { text: "Recent samples · gaps preserve unknown time" }),
      el("p", { text: h.latency }),
    );
    const strip = el("div", { class: "strip" });
    for (const s of h.strip.slots || []) {
      const height =
        s.outcome === "none"
          ? 10
          : Math.max(6, Math.min(55, 10 + s.elapsed_ms / 10));
      const slot = el("span", {
        class: outcomeClass(s.outcome),
        title: `${s.outcome} · ${date(s.sampled_at)} · ${s.elapsed_ms || 0} ms`,
      });
      slot.style.height = `${height}px`;
      strip.append(slot);
    }
    target.append(strip);
    target.append(
      lineChart(
        (h.strip.slots || []).map((s) =>
          s.outcome === "ok" ? s.elapsed_ms : null,
        ),
        "Passing-sample latency (ms); gaps are unmeasured",
      ),
    );
    const samples = el(
      "details",
      {},
      el("summary", { text: "Raw observations and diagnostic facts" }),
    );
    for (const entry of h.entries || [])
      samples.append(
        el(
          "section",
          { class: "history-entry" },
          el("h3", {
            text: `${date(entry.sampled_at)} · ${entry.outcome} · ${entry.evidence}`,
          }),
          structured(entry),
        ),
      );
    target.append(samples);
    // Resource histories render measured facts without deriving health or
    // combining reports from different source identities.
    const numeric = new Set();
    for (const entry of h.entries || [])
      for (const [key, value] of Object.entries(entry.facts || {}))
        if (
          /cpu|memory|disk|filesystem|process/.test(key) &&
          value !== "" &&
          Number.isFinite(Number(value))
        )
          numeric.add(key);
    for (const key of [...numeric].slice(0, 12))
      target.append(
        lineChart(
          resourceSeries(h.entries || [], h.strip.slots || [], key),
          key.replaceAll("_", " "),
        ),
      );
    const rollupBox = el("section", {});
    target.append(el("h3", { text: "Availability & latency rollups" }));
    const step = el(
      "select",
      { "aria-label": "History aggregation" },
      ...["5m", "1h", "1d"].map((s) => el("option", { value: s, text: s })),
    );
    step.value = "1h";
    target.append(step, rollupBox);
    let rollupAbort = null;
    const load = async () => {
      rollupAbort?.abort();
      const own = new AbortController();
      rollupAbort = own;
      signal.addEventListener("abort", () => own.abort(), { once: true });
      rollupBox.replaceChildren(el("p", { text: "Loading rollups…" }));
      try {
        const r = await request(
          `/v1/rollups?${new URLSearchParams({ check: id, step: step.value })}`,
          token,
          { signal: own.signal },
        );
        rollupBox.replaceChildren(
          el("p", {
            class: "muted",
            text: "Unknown and unmeasured time are separate from passing/failing time.",
          }),
          structured(r.total),
          el(
            "details",
            {},
            el("summary", { text: "All time slots" }),
            structured(r.slots),
          ),
        );
      } catch (err) {
        if (!own.signal.aborted)
          rollupBox.replaceChildren(
            el("p", { class: "error", text: errorText(err) }),
          );
      }
    };
    step.addEventListener("change", load);
    await load();
  } catch (err) {
    if (!signal.aborted)
      target.replaceChildren(
        el("p", {
          class: "error",
          text: `History unavailable: ${errorText(err)}`,
        }),
      );
  }
}
function lineChart(values, label) {
  const NS = "http://www.w3.org/2000/svg",
    svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", "0 0 800 130");
  svg.setAttribute("class", "chart");
  svg.setAttribute("role", "img");
  svg.setAttribute("aria-label", label);
  const title = document.createElementNS(NS, "title");
  title.textContent = label;
  svg.append(title);
  const finite = values.filter((n) => n !== null && Number.isFinite(n));
  if (!finite.length)
    return el(
      "section",
      {},
      el("h3", { text: label }),
      el("p", { class: "unknown", text: "No numeric history" }),
    );
  const max = Math.max(1, ...finite),
    min = Math.min(0, ...finite);
  let path = "";
  let connected = false;
  values.forEach((value, i) => {
    if (value === null || !Number.isFinite(value)) {
      connected = false;
      return;
    }
    path += `${connected ? "L" : "M"}${10 + (i * 780) / Math.max(1, values.length - 1)},${110 - ((value - min) * 95) / (max - min)} `;
    connected = true;
  });
  const p = document.createElementNS(NS, "path");
  p.setAttribute("d", path);
  p.setAttribute("fill", "none");
  p.setAttribute("stroke", "#5eead4");
  p.setAttribute("stroke-width", "2");
  svg.append(p);
  return el(
    "section",
    {},
    el("h3", { text: label }),
    el("p", { class: "muted", text: `Range ${min} – ${max}` }),
    svg,
  );
}
function settings() {
  for (const [id, value] of [
    ["pref-page", prefs.page],
    ["pref-map", state.map],
  ])
    $(id).value = value;
  $("pref-cards").checked = state.cards;
  $("pref-uniform").checked = state.uniform;
  $("pref-collapsed").checked = state.collapsed;
  $("pref-color").checked = document.body.classList.contains("no-color");
  $("preferences").showModal();
}
function savePreferences() {
  prefs = {
    page: $("pref-page").value,
    map: $("pref-map").value,
    cards: $("pref-cards").checked,
    uniform: $("pref-uniform").checked,
    collapsed: $("pref-collapsed").checked,
    noColor: $("pref-color").checked,
  };
  try {
    localStorage.setItem("topophany.web.display", JSON.stringify(prefs));
  } catch {}
  state.map = prefs.map;
  state.cards = prefs.cards;
  state.uniform = prefs.uniform;
  state.collapsed = prefs.collapsed;
  document.body.classList.toggle("no-color", prefs.noColor);
  $("preferences").close();
  startStream();
}
$("pref-page").append(...tabs.map((t) => el("option", { text: t })));
$("connect").addEventListener("submit", (e) => {
  e.preventDefault();
  token = $("token").value.trim();
  $("token").value = "";
  if (!token) return;
  lastReceived = Date.now();
  startStream();
});
$("disconnect").onclick = disconnect;
$("settings").onclick = settings;
$("save-preferences").onclick = savePreferences;
$("reset-preferences").onclick = () => {
  try {
    localStorage.removeItem("topophany.web.display");
  } catch {}
  prefs = safePreferences({ getItem: () => null });
  state.map = prefs.map;
  state.cards = state.uniform = state.collapsed = false;
  state.expanded.clear();
  state.folded.clear();
  document.body.classList.remove("no-color");
  $("preferences").close();
  startStream();
};
$("close-detail").onclick = () => {
  $("detail").close();
  detailAbort?.abort();
};
$("detail").addEventListener("close", () => detailAbort?.abort());
$("observer").onchange = () => {
  state.observer = $("observer").value;
  state.offset = 0;
  state.selection = null;
  startStream();
};
let searchTimer;
$("search").oninput = () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    state.search = $("search").value;
    state.offset = 0;
    startStream();
  }, 400);
};
$("attention").onchange = () => {
  state.attention = $("attention").checked;
  state.offset = 0;
  startStream();
};
$("by").onchange = () => {
  state.by = $("by").value;
  state.offset = 0;
  state.expanded.clear();
  state.folded.clear();
  startStream();
};
$("server-list").onclick = () => {
  serverList = !serverList;
  if (data) render();
};
for (const [id, key] of [
  ["app-filter", "application"],
  ["env-filter", "environment"],
  ["service-filter", "service"],
])
  $(id).onchange = () => {
    state[key] = $(id).value;
    state.offset = 0;
    startStream();
  };
$("cards").onclick = () => {
  if (state.tab === 2) state.uniform = !state.uniform;
  else state.cards = !state.cards;
  if (data) render();
};
$("collapse").onclick = () => {
  state.collapsed = !state.collapsed;
  state.expanded.clear();
  state.folded.clear();
  state.selection = null;
  state.offset = 0;
  startStream();
};
$("refresh").onclick = startStream;
$("map-mode").onclick = () => {
  const modes = ["virtual", "geo", "network"];
  state.map = modes[(modes.indexOf(state.map) + 1) % 3];
  startStream();
};
$("zoom-in").onclick = () => map.zoom(1.4);
$("zoom-out").onclick = () => map.zoom(1 / 1.4);
$("fit").onclick = () => map.fit();
$("fullmap").onclick = () => {
  state.full = !state.full;
  if (data) render();
};
$("labels").onclick = () => {
  const labels = ["cards", "locations", "points"];
  state.labels = labels[(labels.indexOf(state.labels) + 1) % 3];
  if (data) render();
};
$("node-search").oninput = () => {
  if (data?.map) renderMap();
};
// Display stale evidence explicitly even when a stream stalls without closing.
setInterval(() => {
  if (data && Date.now() - lastReceived > 15000) {
    connection("CACHED — no current collector update; reconnecting", true);
    markCached();
  }
}, 1000);
document.addEventListener("visibilitychange", () => {
  if (document.hidden) stopStream();
  else if (token) startStream();
});
window.addEventListener("pagehide", disconnect);
window.addEventListener("hashchange", () => {
  const i = tabs.findIndex((t) => t.toLowerCase() === location.hash.slice(1));
  if (i >= 0 && i !== state.tab) setTab(i);
});
const initial = tabs.findIndex(
  (t) => t.toLowerCase() === location.hash.slice(1),
);
if (initial >= 0) state.tab = initial;
document.addEventListener("keydown", (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  const editing =
    /INPUT|TEXTAREA|SELECT/.test(e.target.tagName) ||
    e.target.isContentEditable;
  if (document.querySelector("dialog[open]")) return;
  if (editing) {
    if (e.key === "Escape" && e.target === $("search")) {
      $("search").value = state.search = "";
      $("search").blur();
      startStream();
    }
    return;
  }
  if (!token) {
    if (e.key === ",") settings();
    return;
  }
  if (/^[1-9]$/.test(e.key)) {
    e.preventDefault();
    setTab(Number(e.key) - 1);
    return;
  }
  if (
    (e.key === "Enter" || e.key === " ") &&
    e.target.closest("button,a,summary")
  )
    return;
  const actions = {
    h: () => setTab(state.tab - 1),
    l: () => setTab(state.tab + 1),
    j: () => move(1),
    ArrowDown: () => move(1),
    k: () => move(-1),
    ArrowUp: () => move(-1),
    Home: () => move(-rows.length),
    End: () => move(rows.length),
    g: () => move(-rows.length),
    G: () => move(rows.length),
    PageDown: () => move(10),
    PageUp: () => move(-10),
    Enter: () => {
      const r = rows[selected];
      if (r) r.kind === "group" ? toggleGroup(r) : openDetail(r);
    },
    "/": () => {
      $("search").focus();
    },
    v: () => {
      const opts = [...$("observer").options];
      const i = opts.findIndex((o) => o.value === state.observer);
      $("observer").value = opts[(i + 1) % opts.length].value;
      $("observer").onchange();
    },
    m: () => $("map-mode").click(),
    z: () => $("fit").click(),
    f: () => $("fullmap").click(),
    p: () => $("labels").click(),
    s: () => $("cards").click(),
    b: () => {
      const opts = [...$("by").options];
      $("by").value =
        opts[
          (opts.findIndex((o) => o.value === state.by) + 1) % opts.length
        ].value;
      $("by").onchange();
    },
    C: () => $("collapse").click(),
    c: () => {
      const r = rows[selected];
      if (r?.kind === "service") {
        state.service = r.id;
        setTab(5);
      } else if (r?.kind === "check") {
        state.service = "";
        setTab(3);
      }
    },
    " ": () => {
      if (rows[selected]?.kind === "group") toggleGroup(rows[selected]);
    },
    i: () => {
      const r = rows[selected];
      if (r) r.kind === "group" ? scopeGroup(r) : openDetail(r);
    },
    "!": () => {
      $("attention").checked = !state.attention;
      $("attention").onchange();
    },
    r: startStream,
    ",": settings,
    "?": () => setTab(8),
    q: disconnect,
    Escape: () => {
      state.search = "";
      $("search").value = "";
      state.selection = null;
      startStream();
    },
  };
  if (actions[e.key]) {
    e.preventDefault();
    actions[e.key]();
  }
});
function move(delta) {
  selected = Math.max(0, Math.min(rows.length - 1, selected + delta));
  state.selection = rows[selected] || null;
  highlight(true);
}

// Local, reversible topology editor. A validation response never overwrites the
// local input with its redacted copy. Download preserves the reviewed draft.
const kinds = [
  "nodes",
  "endpoints",
  "services",
  "applications",
  "observers",
  "links",
  "paths",
  "checks",
  "sources",
  "addresses",
];
let undo = [],
  draftBefore = "",
  validated = "",
  draftTimer;
$("entity-kind").append(
  ...kinds.map((k) => el("option", { value: k, text: k })),
);
function remember() {
  const text = $("draft").value;
  if (text === draftBefore) return;
  if (draftBefore) undo.push(draftBefore);
  while (
    undo.length > 20 ||
    undo.reduce((n, s) => n + s.length, 0) > 16 * 1024 * 1024
  )
    undo.shift();
  draftBefore = text;
  validated = "";
  localDraft = true;
}
function setDraft(value) {
  const text =
    typeof value === "string" ? value : JSON.stringify(value, null, 2);
  if (text.length > 4 * 1024 * 1024) throw new Error("Draft exceeds 4 MiB");
  $("draft").value = text;
  remember();
  editorEntities();
  $("draft-status").textContent =
    "LOCAL DRAFT — changes not applied to any collector.";
}
function editorEntities() {
  try {
    const c = JSON.parse($("draft").value),
      kind = $("entity-kind").value;
    const entries =
      kind === "addresses"
        ? Object.keys(c.addresses || {}).map((id) => ({ id }))
        : c[kind] || [];
    $("entity-id").replaceChildren(
      ...entries.map((e) =>
        el("option", {
          value: e.id,
          text: e.name ? `${e.id} · ${e.name}` : e.id,
        }),
      ),
    );
  } catch {
    $("entity-id").replaceChildren();
  }
}
function editorError(err) {
  $("draft-status").textContent = errorText(err);
}
$("edit").onclick = () => {
  $("editor").showModal();
};
$("close-editor").onclick = () => $("editor").close();
$("draft").oninput = () => {
  clearTimeout(draftTimer);
  validated = "";
  draftTimer = setTimeout(() => {
    remember();
    editorEntities();
  }, 500);
};
$("import").onchange = async () => {
  try {
    const file = $("import").files[0];
    if (!file) return;
    if (file.size > 4 * 1024 * 1024) throw new Error("File exceeds 4 MiB");
    setDraft(await file.text());
  } catch (err) {
    editorError(err);
  } finally {
    $("import").value = "";
  }
};
$("load-export").onclick = async () => {
  if (!token) {
    editorError(
      new Error(
        "Connect with a viewer token first, or import a local topology.",
      ),
    );
    return;
  }
  try {
    const c = await request("/v1/config", token);
    setDraft(c.topology);
  } catch (err) {
    editorError(err);
  }
};
$("undo").onclick = () => {
  if (!undo.length) return;
  $("draft").value = undo.pop();
  draftBefore = $("draft").value;
  validated = "";
  editorEntities();
  $("draft-status").textContent = "Undone — local draft only.";
};
async function validateDraft() {
  if (!token)
    throw new Error(
      "Connect to validate using the Go configuration validator.",
    );
  const text = $("draft").value;
  if (new TextEncoder().encode(text).length > 4 * 1024 * 1024)
    throw new Error("Draft exceeds 4 MiB");
  JSON.parse(text);
  const result = await request("/v1/web/validate", token, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: text,
  });
  validated = text;
  $("draft-status").textContent =
    `Valid topology · public revision ${result.revision}\nDownload and review locally before deployment. Private fields are preserved in your local draft.`;
  return result;
}
$("validate").onclick = () => validateDraft().catch(editorError);
$("download").onclick = async () => {
  try {
    if (validated !== $("draft").value) await validateDraft();
    download("topology.json", $("draft").value + "\n");
  } catch (err) {
    editorError(err);
  }
};
$("entity-kind").onchange = editorEntities;
function entityForm(add) {
  try {
    const kind = $("entity-kind").value,
      id = $("entity-id").value,
      c = JSON.parse($("draft").value || '{"schema":1,"project":"network"}');
    const entity =
      kind === "addresses"
        ? add
          ? { id: "address", value: "" }
          : { id, value: c.addresses[id] }
        : add
          ? { id: "new-entity" }
          : (c[kind] || []).find((e) => e.id === id);
    if (!entity) throw new Error("Select an entity first.");
    $("entity-json").value = JSON.stringify(entity, null, 2);
    $("entity-form").hidden = false;
    $("entity-form").dataset.original = add ? "" : id;
    $("entity-json").focus();
  } catch (err) {
    editorError(err);
  }
}
$("entity-add").onclick = () => entityForm(true);
$("entity-edit").onclick = () => entityForm(false);
$("entity-cancel").onclick = () => {
  $("entity-form").hidden = true;
};
$("entity-apply").onclick = () => {
  try {
    const c = JSON.parse(
        $("draft").value ||
          '{"schema":1,"project":"network","nodes":[],"endpoints":[],"services":[],"observers":[],"paths":[],"checks":[]}',
      ),
      kind = $("entity-kind").value,
      original = $("entity-form").dataset.original,
      entity = JSON.parse($("entity-json").value);
    if (!entity.id || typeof entity.id !== "string")
      throw new Error("Entity needs a string id.");
    if (kind === "addresses") {
      c.addresses ??= {};
      if (entity.id !== original && Object.hasOwn(c.addresses, entity.id))
        throw new Error("Duplicate address id");
      if (original) delete c.addresses[original];
      c.addresses[entity.id] = entity.value;
    } else {
      c[kind] ??= [];
      if (c[kind].some((e) => e.id === entity.id && e.id !== original))
        throw new Error("Duplicate entity id");
      const index = c[kind].findIndex((e) => e.id === original);
      if (index < 0) c[kind].push(entity);
      else c[kind][index] = entity;
    }
    setDraft(c);
    $("entity-form").hidden = true;
  } catch (err) {
    editorError(err);
  }
};
$("entity-delete").onclick = () => {
  try {
    const c = JSON.parse($("draft").value),
      kind = $("entity-kind").value,
      id = $("entity-id").value;
    if (!id) return;
    if (kind === "addresses") delete c.addresses[id];
    else c[kind] = c[kind].filter((e) => e.id !== id);
    setDraft(c);
  } catch (err) {
    editorError(err);
  }
};
$("editor").addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key === "s") {
    e.preventDefault();
    $("download").click();
  }
  if (
    (e.ctrlKey || e.metaKey) &&
    e.key === "z" &&
    e.target.tagName !== "TEXTAREA"
  ) {
    e.preventDefault();
    $("undo").click();
  }
  if (
    !["TEXTAREA", "INPUT", "SELECT"].includes(e.target.tagName) &&
    !e.ctrlKey &&
    !e.metaKey
  ) {
    const action = {
      a: "entity-add",
      e: "entity-edit",
      d: "entity-delete",
      u: "undo",
    }[e.key];
    if (action) {
      e.preventDefault();
      $(action).click();
    }
  }
});
window.addEventListener("beforeunload", (e) => {
  if (localDraft) {
    e.preventDefault();
    e.returnValue = "";
  }
});
renderTabs();
