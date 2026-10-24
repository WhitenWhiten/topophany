// Hermetic browser acceptance: a real collector and synthetic loopback-only
// observations. Never uses saved credentials or probes production hosts.
import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { mkdtemp, readFile, writeFile, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createHash, randomBytes } from "node:crypto";
const { chromium } = await import(
  process.env.PLAYWRIGHT_MODULE
    ? pathToFileURL(process.env.PLAYWRIGHT_MODULE).href
    : "playwright"
);
const root = resolve(fileURLToPath(new URL("../../..", import.meta.url)));
const binary = process.env.TOPO_WEB_BINARY || join(root, "bin", "topo-web");
const artifacts =
  process.env.TOPO_WEB_ARTIFACTS || join(tmpdir(), "topophany-webui-browser");
await mkdir(artifacts, { recursive: true });
const work = await mkdtemp(join(tmpdir(), "topophany-web-browser-"));
const dir = join(work, "fixture");
const syntheticToken = randomBytes(32).toString("hex");
const remoteToken = randomBytes(32).toString("hex");
let collector,
  browser,
  timer,
  logs = "",
  publishError = null;
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(fn, timeout = 15000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    const result = await fn();
    if (result) return result;
    await wait(100);
  }
  throw new Error("Timed out waiting for fixture");
}
try {
  execFileSync(binary, ["init", "--dir", dir], { stdio: "pipe" });
  const file = join(dir, "topology.json");
  const c = JSON.parse(await readFile(file, "utf8"));
  c.nodes[0].location = { label: "Tokyo", lat: 35.7, lon: 139.7 };
  c.nodes[1].location = { label: "New York", lat: 40.7, lon: -74 };
  c.nodes[1].name = "Unmonitored <img src=x onerror=alert(1)>";
  c.applications = [{ id: "monitoring", name: "Monitoring" }];
  for (const s of c.services) {
    s.application = "monitoring";
    s.environment = "production";
    s.notes = "Synthetic fixture notes";
    s.runbook = "Review the observation evidence before changing routes.";
  }
  c.links = [
    {
      id: "declared-link",
      from: "collector",
      to: "home",
      transport: "declared fixture link",
    },
  ];
  c.paths[0].nodes = ["collector", "home"];
  c.paths[0].links = ["declared-link"];
  c.observers.push({
    id: "remote",
    name: "Remote contrast",
    node: "home",
    context: "Synthetic contrast only",
    stale_seconds: 30,
  });
  c.checks.push({
    ...c.checks[0],
    id: "remote-health",
    name: "Remote check",
    observer: "remote",
    path: "",
    endpoint: "remote-endpoint",
  });
  c.endpoints.push({
    ...c.endpoints[0],
    id: "remote-endpoint",
    observer: "remote",
  });
  c.checks.push({
    id: "host",
    name: "Synthetic host resources",
    observer: "demo",
    kind: "host",
    interval_seconds: 5,
    timeout_seconds: 3,
    stale_seconds: 15,
    failure_threshold: 2,
    recovery_threshold: 2,
    host: { disk_path: "/" },
  });
  await writeFile(file, JSON.stringify(c), { mode: 0o600 });
  const authFile = join(dir, "auth.json");
  const auth = JSON.parse(await readFile(authFile, "utf8"));
  auth.tokens.push(
    {
      token_sha256: createHash("sha256").update(syntheticToken).digest("hex"),
      role: "viewer",
    },
    {
      token_sha256: createHash("sha256").update(remoteToken).digest("hex"),
      role: "worker",
      observer: "remote",
    },
  );
  await writeFile(authFile, JSON.stringify(auth), { mode: 0o600 });
  const viewer = JSON.parse(
    await readFile(join(dir, "credentials.json"), "utf8"),
  ).profiles.demo.servers[0].token;
  const worker = JSON.parse(
    await readFile(join(dir, "worker-credentials.json"), "utf8"),
  ).profiles.demo.servers[0].token;
  const valid = JSON.parse(
    execFileSync(binary, ["validate", "--config", file], { encoding: "utf8" }),
  );
  collector = spawn(
    binary,
    [
      "serve",
      "--config",
      file,
      "--auth",
      authFile,
      "--data",
      join(dir, "data"),
      "--listen",
      "127.0.0.1:0",
      "--web",
      "--web-listen",
      "127.0.0.1:0",
    ],
    { stdio: ["ignore", "ignore", "pipe"] },
  );
  collector.stderr.on("data", (b) => (logs += b.toString()));
  const url = await until(
    () =>
      logs.match(/Native Web UI listening on (http:\/\/127\.0\.0\.1:\d+)/)?.[1],
  );
  const api =
    "http://" +
    (await until(
      () =>
        logs.match(/collector collector listening on (127\.0\.0\.1:\d+)/)?.[1],
    ));
  let seq = 0;
  let failed = false;
  async function publish() {
    for (const [observer, credential] of [
      ["demo", worker],
      ["remote", remoteToken],
    ]) {
      const observations = c.checks
        .filter((x) => x.observer === observer)
        .flatMap((x) =>
          [0, 1].map((k) => {
            const sequence = ++seq;
            const outcome = failed && x.id === "api-health" ? "fail" : "ok";
            const host = x.kind === "host";
            return {
              id: `${observer}.browser.${sequence}`,
              boot: "browser",
              sequence,
              project: c.project,
              contract: valid.contracts[x.id],
              check: x.id,
              observer,
              sampled_at: new Date(Date.now() - 1000 + k * 100).toISOString(),
              outcome,
              evidence: host ? "reported" : "measured",
              stage: host ? "host" : "http",
              detail: "Explicit synthetic browser fixture",
              duration_ms: host ? 0 : 12,
              connectivity: outcome === "ok",
              path_evidence: "requested_not_route_verified",
              facts: host
                ? {
                    cpu_used_percent: "24.0",
                    memory_used_percent: "50.0",
                    memory_used_bytes: "1073741824",
                    memory_total_bytes: "2147483648",
                    disk_used_percent: "25.0",
                    disk_used_bytes: "1073741824",
                    disk_total_bytes: "4294967296",
                    process_count: "42",
                  }
                : { response_proof: outcome === "ok" ? "http_response" : "" },
            };
          }),
        );
      const r = await fetch(api + "/v1/observations", {
        method: "POST",
        headers: {
          Authorization: `Bearer ${credential}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          schema: 2,
          project: c.project,
          revision: valid.revision,
          observations,
          heartbeat: {
            observer,
            boot: "browser",
            sampled_at: new Date().toISOString(),
          },
        }),
      });
      if (r.status !== 202)
        throw new Error(
          `Synthetic ingest failed ${r.status}: ${await r.text()}`,
        );
    }
  }
  await publish();
  timer = setInterval(() => publish().catch((e) => (publishError = e)), 4000);
  browser = await chromium.launch({
    headless: true,
    executablePath: process.env.CHROME || undefined,
    args: ["--no-sandbox", "--disable-dev-shm-usage"],
  });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("dialog", (d) => {
    if (d.type() === "beforeunload") d.accept();
    else {
      errors.push("Unexpected browser dialog: " + d.type());
      d.dismiss();
    }
  });
  await page.goto(url);
  await page.locator("#token").fill(viewer);
  await page.locator("#connect button").click();
  await page
    .locator("#connection")
    .filter({ hasText: "Live" })
    .waitFor({ timeout: 20000 });
  assert.equal(await page.locator("#token").inputValue(), "");
  assert.equal(await page.locator("#content img").count(), 0);
  for (let i = 0; i < 9; i++) {
    await page.locator("#tabs button").nth(i).click();
    await page.waitForFunction(
      (i) =>
        document
          .querySelectorAll("#tabs button")
          [i].getAttribute("aria-selected") === "true" &&
        document.querySelector("#connection").textContent.startsWith("Live"),
      i,
    );
    await page.screenshot({
      path: join(
        artifacts,
        `${i + 1}-${["overview", "topology", "servers", "services", "paths", "checks", "incidents", "observers", "about"][i]}.png`,
      ),
      fullPage: true,
    });
    if (i === 2) {
      assert.match(await page.locator("#content").innerText(), /CPU/);
      await page.locator("#server-list").click();
      assert.ok((await page.locator("table tbody tr").count()) > 0);
      await page.locator("#server-list").click();
    }
    if (i === 3) {
      await page.locator("#cards").click();
      assert.ok((await page.locator("#content article").count()) > 0);
      await page.locator("#cards").click();
    }
  }
  // Map semantics and native interactions, including original Braille land.
  await page.locator("#tabs button").nth(1).click();
  await page.locator("#connection").filter({ hasText: "Live" }).waitFor();
  await page.locator("#map-mode").click();
  await page.waitForFunction(() => document.querySelector(".map-land"));
  assert.ok((await page.locator(".map-land text").count()) > 0);
  const box = await page.locator(".map-wrap svg").boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  const before = await page.locator(".map-wrap svg").getAttribute("viewBox");
  await page.mouse.wheel(0, -100);
  assert.notEqual(
    await page.locator(".map-wrap svg").getAttribute("viewBox"),
    before,
  );
  await page.locator(".map-node").first().focus();
  await page.keyboard.press("Enter");
  await page.locator("#detail").waitFor();
  await page.locator("#detail-content pre").waitFor();
  await page.locator("#close-detail").click();
  // Check evidence, raw history and structured rollups from actual collector API.
  await page.locator("#tabs button").nth(5).click();
  await page.locator("#connection").filter({ hasText: "Live" }).waitFor();
  await page.locator("#by").selectOption("flat");
  await page.locator("#connection").filter({ hasText: "Live" }).waitFor();
  await page
    .locator("tr[data-row]")
    .filter({ hasText: "Collector health response" })
    .click();
  await page
    .locator("#detail-content")
    .getByText("Recent samples · gaps preserve unknown time", { exact: true })
    .waitFor();
  await page.locator("#detail-content .chart").first().waitFor();
  await page
    .locator("#detail-content")
    .getByText("Availability & latency rollups", { exact: true })
    .waitFor();
  await page.screenshot({ path: join(artifacts, "check-evidence.png") });
  await page.locator("#close-detail").click();
  // A real evidence change produces a live incident, without replacing APIs.
  failed = true;
  await publish();
  await wait(5500);
  await page.locator("#tabs button").nth(6).click();
  await page.locator("#connection").filter({ hasText: "Live" }).waitFor();
  await until(
    async () => (await page.locator("tr[data-row]").count()) > 0,
    12000,
  );
  await page.locator("tr[data-row]").first().click();
  await page.locator("#detail-content pre").waitFor();
  await page.screenshot({ path: join(artifacts, "incident-evidence.png") });
  await page.locator("#close-detail").click();
  failed = false;
  await publish();
  // Observer projection changes the server's view, not frontend conclusions.
  await page.locator("#observer").selectOption("remote");
  await page.locator("#connection").filter({ hasText: "Live" }).waitFor();
  assert.match(await page.locator("#identity").innerText(), /remote/);
  // Local-only editing with Go validation, undo and a reviewed download.
  await page.locator("#edit").click();
  await page.locator("#load-export").click();
  await until(async () => !!(await page.locator("#draft").inputValue()));
  await page.locator("#validate").click();
  await page
    .locator("#draft-status")
    .filter({ hasText: "Valid topology" })
    .waitFor();
  const draft = JSON.parse(await page.locator("#draft").inputValue());
  draft.nodes[0].notes = "Edited only in browser";
  await page.locator("#draft").fill(JSON.stringify(draft, null, 2));
  await wait(600);
  await page.locator("#undo").click();
  assert.ok(
    !(await page.locator("#draft").inputValue()).includes(
      "Edited only in browser",
    ),
  );
  const saved = page.waitForEvent("download");
  await page.locator("#download").click();
  assert.equal((await saved).suggestedFilename(), "topology.json");
  await page.locator("#close-editor").click();
  // Persist display only; malicious topology strings remain text nodes.
  await page.locator("#settings").click();
  await page.locator("#pref-uniform").check();
  await page.locator("#save-preferences").click();
  const storage = await page.evaluate(() => ({ ...localStorage }));
  assert.equal(JSON.stringify(storage).includes(viewer), false);
  assert.equal(JSON.stringify(storage).includes(worker), false);
  await page.locator("#disconnect").click();
  await page.locator("#connect").waitFor();
  assert.equal(await page.locator("#content").innerText(), "");
  // Mobile uses its own viewer admission, native overflow and touch-safe SVG.
  const mobile = await browser.newContext({
    viewport: { width: 390, height: 844 },
    isMobile: true,
    hasTouch: true,
  });
  const m = await mobile.newPage();
  m.on("pageerror", (e) => errors.push(e.message));
  await m.goto(url);
  await m.locator("#token").fill(syntheticToken);
  await m.locator("#connect button").click();
  await m.locator("#connection").filter({ hasText: "Live" }).waitFor();
  await m.screenshot({
    path: join(artifacts, "mobile-overview.png"),
    fullPage: true,
  });
  await m.locator("#tabs button").nth(3).click();
  await m.locator("#connection").filter({ hasText: "Live" }).waitFor();
  const expandedRows = await m.locator("tr[data-row]").count();
  await m.locator("#collapse").click();
  await until(
    async () => (await m.locator("tr[data-row]").count()) < expandedRows,
  );
  await m.locator("tr.group").first().click();
  await until(
    async () => (await m.locator("tr[data-row]").count()) === expandedRows,
  );
  await m.locator("#tabs button").nth(0).click();
  await m.locator("#connection").filter({ hasText: "Live" }).waitFor();
  // Kill the collector; disconnect state must withhold resource values and all
  // map current signals. Restart and fetch-based stream must recover.
  collector.kill("SIGTERM");
  await until(() => collector.exitCode !== null);
  await m
    .locator("#connection")
    .filter({ hasText: "Disconnected" })
    .waitFor({ timeout: 20000 });
  assert.equal(
    await m.locator('#content .map-node .marker[fill="#4ade80"]').count(),
    0,
  );
  collector = spawn(
    binary,
    [
      "serve",
      "--config",
      file,
      "--auth",
      authFile,
      "--data",
      join(dir, "data"),
      "--listen",
      new URL(api).host,
      "--web",
      "--web-listen",
      new URL(url).host,
    ],
    { stdio: ["ignore", "ignore", "pipe"] },
  );
  collector.stderr.on("data", (b) => (logs += b.toString()));
  await until(async () => {
    try {
      return (await fetch(api + "/healthz")).ok;
    } catch {
      return false;
    }
  }, 15000);
  publishError = null;
  await publish();
  await m
    .locator("#connection")
    .filter({ hasText: "Live" })
    .waitFor({ timeout: 45000 });
  await m.screenshot({
    path: join(artifacts, "reconnected.png"),
    fullPage: true,
  });
  // Reload revokes this connection's viewer while preserving the worker and
  // the original independent viewer. Reconnection must clear all visible data.
  const revoked = {
    ...auth,
    tokens: auth.tokens.filter(
      (t) =>
        t.token_sha256 !==
        createHash("sha256").update(syntheticToken).digest("hex"),
    ),
  };
  await writeFile(authFile, JSON.stringify(revoked), { mode: 0o600 });
  collector.kill("SIGHUP");
  await m.locator("#connect").waitFor({ timeout: 25000 });
  assert.equal(await m.locator("#content").innerText(), "");
  assert.equal(errors.length, 0, errors.join("\n"));
  if (publishError && collector.exitCode === null) throw publishError;
  await writeFile(
    join(artifacts, "result.json"),
    JSON.stringify(
      {
        passed: true,
        fixture: "synthetic observations through real collector ingestion",
        pages: 9,
        viewports: ["1440x1000", "390x844"],
        screenshots: artifacts,
        reconnect: true,
        revocation: true,
      },
      null,
      2,
    ),
  );
  console.log(
    `PASS: native Web UI browser acceptance; screenshots: ${artifacts}`,
  );
} finally {
  clearInterval(timer);
  if (browser) await browser.close();
  if (collector && collector.exitCode === null) {
    collector.kill("SIGTERM");
    await Promise.race([
      new Promise((r) => collector.once("exit", r)),
      wait(5000),
    ]);
    if (collector.exitCode === null) collector.kill("SIGKILL");
  }
  await rm(work, { recursive: true, force: true });
}
