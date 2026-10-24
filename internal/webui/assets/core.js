// Rendering helpers contain no monitoring policy. Health and freshness arrive
// from Go's view model, including gaps and cached/unknown distinctions.
export const tabs = [
  "Overview",
  "Topology",
  "Servers",
  "Services",
  "Paths",
  "Checks",
  "Incidents",
  "Observers",
  "About",
];
export const colors = {
  up: "#4ade80",
  carried: "#4ade80",
  down: "#f87171",
  degraded: "#fbbf24",
  affected: "#fbbf24",
  unknown: "#6b7a8c",
};
export const glyphs = {
  up: "●",
  carried: "┄",
  down: "✖",
  degraded: "▲",
  affected: "◇",
  unknown: "○",
};
export function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === undefined || value === null) continue;
    if (key.startsWith("on")) node.addEventListener(key.slice(2), value);
    else if (key === "class") node.className = value;
    else if (key === "text") node.textContent = value;
    else if (key === "hidden" || key === "disabled") node[key] = !!value;
    else node.setAttribute(key, String(value));
  }
  for (const child of children.flat(Infinity))
    if (child !== null && child !== undefined)
      node.append(
        child instanceof Node ? child : document.createTextNode(String(child)),
      );
  return node;
}
export function stateLabel(row) {
  return row.note || row.health || "unknown";
}
export function badge(row) {
  const h = stateLabel(row);
  return el("span", {
    class: row.health || "unknown",
    "data-state": "",
    text: `${glyphs[row.health] || "○"} ${h}`,
  });
}
export function date(value) {
  if (!value || value.startsWith("0001-")) return "never";
  const d = new Date(value);
  return Number.isNaN(+d) ? value : d.toLocaleString();
}
export function safePreferences(storage) {
  try {
    const p = JSON.parse(storage.getItem("topophany.web.display") || "{}");
    return {
      page: tabs.includes(p.page) ? p.page : "Overview",
      map: ["virtual", "geo", "network"].includes(p.map) ? p.map : "virtual",
      cards: p.cards === true,
      uniform: p.uniform === true,
      collapsed: p.collapsed === true,
      noColor: p.noColor === true,
    };
  } catch {
    return {
      page: "Overview",
      map: "virtual",
      cards: false,
      uniform: false,
      collapsed: false,
      noColor: false,
    };
  }
}
export function queryFor(state) {
  const q = new URLSearchParams({
    tab: String(state.tab),
    observer: state.observer,
    search: state.search,
    by: state.by,
    attention: String(state.attention),
    offset: String(state.offset),
    map: state.map,
    collapsed: String(state.collapsed || false),
  });
  for (const id of state.expanded || []) q.append("expanded", id);
  for (const id of state.folded || []) q.append("folded", id);
  if (state.selection && state.selection.kind !== "group") {
    q.set("kind", state.selection.kind);
    q.set("id", state.selection.id);
  }
  for (const key of ["application", "environment", "service"])
    if (state[key]) q.set(key, state[key]);
  return q;
}
export class APIError extends Error {
  constructor(status, code) {
    super(code);
    this.status = status;
  }
}
export async function request(path, token, options = {}) {
  const response = await fetch(path, {
    ...options,
    credentials: "omit",
    redirect: "error",
    cache: "no-store",
    headers: { Authorization: `Bearer ${token}`, ...options.headers },
  });
  const text = await response.text();
  if (text.length > 20 * 1024 * 1024)
    throw new Error("Response exceeds browser budget");
  let body;
  try {
    body = JSON.parse(text);
  } catch {
    throw new APIError(response.status, "invalid_response");
  }
  if (!response.ok)
    throw new APIError(
      response.status,
      body.detail || body.error || `HTTP ${response.status}`,
    );
  return body;
}
// Fetch-based SSE carries the bearer in a header. Its parser handles split UTF-8,
// CRLF, split frames, bounded memory, cancellation and malformed frame failures.
export async function stream(
  path,
  token,
  signal,
  onView,
  onConnect = () => {},
) {
  const response = await fetch(path, {
    signal,
    credentials: "omit",
    redirect: "error",
    cache: "no-store",
    headers: { Authorization: `Bearer ${token}`, Accept: "text/event-stream" },
  });
  if (!response.ok) {
    let code = `HTTP ${response.status}`;
    try {
      code = (await response.json()).error || code;
    } catch {}
    throw new APIError(response.status, code);
  }
  if (!response.headers.get("content-type")?.startsWith("text/event-stream"))
    throw new Error("Expected event stream");
  if (!response.body) throw new Error("Streaming unavailable");
  const reader = response.body.getReader(),
    decoder = new TextDecoder();
  let buffer = "";
  let got = false;
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      if (buffer.length > 20 * 1024 * 1024)
        throw new Error("Stream frame exceeds browser budget");
      // Servers emit LF; normalize complete CRLF pairs, including across chunks.
      buffer = buffer.replace(/\r\n/g, "\n");
      let end;
      while ((end = buffer.indexOf("\n\n")) >= 0) {
        const frame = buffer.slice(0, end);
        buffer = buffer.slice(end + 2);
        const lines = frame.split("\n");
        const event = lines
          .find((l) => l.startsWith("event:"))
          ?.slice(6)
          .trim();
        if (event !== "view") continue;
        const data = lines
          .filter((l) => l.startsWith("data:"))
          .map((l) => l.slice(5).trimStart())
          .join("\n");
        const view = JSON.parse(data);
        if (view.schema !== 1 || !Array.isArray(view.rows))
          throw new Error("Unsupported Web projection");
        if (!got) {
          onConnect();
          got = true;
        }
        onView(view);
      }
    }
    if (!got) throw new Error("Stream ended before first view");
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
export function download(name, text) {
  const url = URL.createObjectURL(
    new Blob([text], { type: "application/json" }),
  );
  const a = el("a", { href: url, download: name });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
export function outcomeClass(outcome) {
  return (
    {
      ok: "up",
      fail: "down",
      policy: "degraded",
      error: "unknown",
      unknown: "unknown",
      none: "none",
    }[outcome] || "unknown"
  );
}

// Arrange raw numeric facts on the time slots already supplied by Go. Missing
// values remain gaps; newest-first storage order never reverses a chart.
export function resourceSeries(entries, slots, key) {
  const facts = new Map(entries.map((e) => [e.sampled_at, e.facts]));
  return slots.map((slot) => {
    if (slot.outcome === "none") return null;
    const value = facts.get(slot.sampled_at)?.[key];
    return value !== undefined && value !== "" && Number.isFinite(Number(value))
      ? Number(value)
      : null;
  });
}
