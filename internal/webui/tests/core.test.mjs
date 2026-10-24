import test from "node:test";
import assert from "node:assert/strict";
import {
  safePreferences,
  queryFor,
  stream,
  request,
  APIError,
  outcomeClass,
  resourceSeries,
} from "../assets/core.js";
test("preferences whitelist fields and never persist authentication", () => {
  const p = safePreferences({
    getItem: () =>
      JSON.stringify({
        page: "evil",
        map: "evil",
        token: "secret",
        cards: true,
      }),
  });
  assert.equal(p.page, "Overview");
  assert.equal(p.map, "virtual");
  assert.equal(p.cards, true);
  assert.equal(p.token, undefined);
  assert.equal(
    safePreferences({
      getItem: () => {
        throw Error("denied");
      },
    }).page,
    "Overview",
  );
});
test("query carries scope and selection but never a token", () => {
  const q = queryFor({
    tab: 3,
    observer: "home",
    search: "日本 & <script>",
    by: "host",
    attention: true,
    offset: 0,
    map: "geo",
    selection: { kind: "service", id: "web" },
    application: "shop",
  });
  assert.equal(q.get("search"), "日本 & <script>");
  assert.equal(q.get("id"), "web");
  assert.equal(q.get("application"), "shop");
  assert.equal(q.has("token"), false);
});
test("missing/error history is never rendered as passing", () => {
  assert.equal(outcomeClass("error"), "unknown");
  assert.equal(outcomeClass("none"), "none");
  assert.equal(outcomeClass("fail"), "down");
});
test("stream handles UTF-8 and frame boundaries with auth header only", async () => {
  const original = globalThis.fetch;
  const encoder = new TextEncoder();
  const body = encoder.encode(
    'event: view\r\ndata: {"schema":1,"rows":[],"project":"日本"}\r\n\r\nevent: view\ndata: {"schema":1,"rows":[],"project":"second"}\n\n',
  );
  let views = [];
  globalThis.fetch = async (path, options) => {
    assert.equal(options.headers.Authorization, "Bearer secret");
    assert.equal(path.includes("secret"), false);
    assert.equal(options.credentials, "omit");
    return new Response(
      new ReadableStream({
        start(c) {
          for (let i = 0; i < body.length; i += 3)
            c.enqueue(body.slice(i, i + 3));
          c.close();
        },
      }),
      { headers: { "Content-Type": "text/event-stream" } },
    );
  };
  try {
    await stream(
      "/v1/web/events",
      "secret",
      new AbortController().signal,
      (v) => views.push(v),
    );
    assert.deepEqual(
      views.map((v) => v.project),
      ["日本", "second"],
    );
  } finally {
    globalThis.fetch = original;
  }
});
test("revoked credentials fail and malformed stream fails closed", async () => {
  const original = globalThis.fetch;
  try {
    globalThis.fetch = async () =>
      new Response('{"error":"invalid_token"}', { status: 401 });
    await assert.rejects(
      () => stream("/events", "bad", new AbortController().signal, () => {}),
      (e) => e instanceof APIError && e.status === 401,
    );
    globalThis.fetch = async () =>
      new Response('event: view\ndata: {"schema":99,"rows":[]}\n\n', {
        headers: { "Content-Type": "text/event-stream" },
      });
    await assert.rejects(
      () => stream("/events", "x", new AbortController().signal, () => {}),
      /Unsupported/,
    );
  } finally {
    globalThis.fetch = original;
  }
});
test("request rejects redirects and propagates validation diagnostics", async () => {
  const original = globalThis.fetch;
  try {
    globalThis.fetch = async (path, options) => {
      assert.equal(options.redirect, "error");
      return new Response(
        '{"error":"invalid_topology","detail":"unknown node"}',
        { status: 400 },
      );
    };
    await assert.rejects(
      () => request("/v1/web/validate", "x"),
      /unknown node/,
    );
  } finally {
    globalThis.fetch = original;
  }
});

test("resource charts follow server slots and preserve missing readings", () => {
  const entries = [
    { sampled_at: "new", facts: { cpu: "8" } },
    { sampled_at: "empty", facts: { cpu: "" } },
    { sampled_at: "old", facts: { cpu: "3" } },
  ];
  const slots = [
    { sampled_at: "old", outcome: "ok" },
    { outcome: "none" },
    { sampled_at: "empty", outcome: "ok" },
    { sampled_at: "new", outcome: "ok" },
  ];
  assert.deepEqual(resourceSeries(entries, slots, "cpu"), [3, null, null, 8]);
});
