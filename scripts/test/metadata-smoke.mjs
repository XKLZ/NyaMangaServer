// Real-process M3 acceptance. Remote is the fixture tool's injected offline
// client; no private libraries, user database or internet requests are used.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { mkdir, mkdtemp, writeFile, readFile } from "node:fs/promises";
import path from "node:path";
import { bin, tests } from "../dev/runtime-paths.mjs";
await mkdir(path.join(tests, "runs"), { recursive: true, mode: 0o700 });
const run = await mkdtemp(path.join(tests, "runs", "metadata-"));
const fixture = spawnSync(
  path.join(bin, "fixtures"),
  ["--output", path.join(run, "library")],
  { stdio: "ignore" },
);
assert.equal(fixture.status, 0);
const token = randomBytes(32).toString("base64url");
await writeFile(path.join(run, "setup-token"), token, { mode: 0o600 });
const origin = "http://localhost:18082";
const config = path.join(run, "config.json");
await writeFile(
  config,
  JSON.stringify({
    listen_addr: "127.0.0.1:18082",
    public_url: origin,
    data_dir: "data",
    setup_token_file: "setup-token",
    provider_key_file: "secrets/provider-key",
    provider_credentials_file: "secrets/providers.enc",
    library_roots: [
      { root_key: "anonymous", display_name: "Anonymous", path: "library" },
    ],
  }),
  { mode: 0o600 },
);
let child;
let exited;
let cookie = "";
let csrf = "";
async function call(method, route, body, status = 200) {
  const response = await fetch(origin + "/api/v1" + route, {
    method,
    headers: {
      Origin: origin,
      Cookie: cookie,
      "X-CSRF-Token": csrf,
      "Content-Type": "application/json",
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  assert.equal(response.status, status, `${method} ${route}`);
  if (response.headers.has("set-cookie"))
    cookie = response.headers.get("set-cookie").split(";")[0];
  const result = await response.json();
  if (result.csrf_token) csrf = result.csrf_token;
  return result;
}
async function waitFor(check) {
  for (let i = 0; i < 500; i++) {
    const result = await check();
    if (result) return result;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error("anonymous M3 condition timed out");
}
async function start() {
  child = spawn(path.join(bin, "fixtures"), ["--serve", config], {
    stdio: "ignore",
  });
  exited = once(child, "exit");
  await waitFor(async () => {
    assert.equal(child.exitCode, null, "fixture server exited");
    try {
      return (await fetch(origin + "/api/v1/health/ready")).ok;
    } catch {
      return false;
    }
  });
}
async function stop() {
  const before = Date.now();
  child.kill("SIGTERM");
  const [code] = await exited;
  assert.equal(code, 0);
  assert.ok(Date.now() - before < 10000, "SIGTERM exceeded budget");
  child = null;
}
try {
  await start();
  await call("POST", "/auth/setup", {
    setup_token: token,
    password: "Anonymous M3 process password",
  });
  const lib = await call(
    "POST",
    "/libraries",
    { root_key: "anonymous", display_name: "Anonymous" },
    201,
  );
  const scan = await call("POST", `/libraries/${lib.id}/scans`, {}, 202);
  await waitFor(async () => {
    const j = await call("GET", `/jobs/${scan.job_id}`);
    return j.state === "completed";
  });
  const books = (await call("GET", "/books?state=available")).items;
  assert.equal(books.length, 2);
  const source = "https://e-hentai.org/g/1/0123456789/";
  const task = await call(
    "POST",
    "/metadata-jobs",
    {
      book_ids: books.map((b) => b.id),
      gallery_urls: Object.fromEntries(books.map((b) => [b.id, source])),
    },
    202,
  );
  const checkpoint = await waitFor(async () => {
    const j = await call("GET", `/jobs/${task.job_id}`);
    return j.done === 1 && j.state === "running" ? j : false;
  });
  const stream = await fetch(origin + "/api/v1/events", {
    headers: { Cookie: cookie },
    signal: AbortSignal.timeout(12000),
  });
  assert.equal(stream.status, 200);
  const reader = stream.body.getReader();
  const first = await reader.read();
  assert.match(Buffer.from(first.value).toString(), /resync_required/);
  await stop();
  while (!(await reader.read()).done) {
    /* the shutdown closes the authenticated stream */
  }
  await start();
  const complete = await waitFor(async () => {
    const j = await call("GET", `/jobs/${task.job_id}`);
    return j.state === "completed" ? j : false;
  });
  assert.equal(complete.done, 2);
  assert.ok(complete.generation > checkpoint.generation);
  const before = await call("GET", `/books/${books[0].id}`);
  assert.equal(before.title, "匿名来源标题");
  await call("PATCH", `/books/${books[0].id}/overrides`, {
    expected_revision: before.revision,
    fields: { title: { op: "set", value: "Anonymous manual edit" } },
  });
  const queued = await call(
    "POST",
    "/metadata-jobs",
    { book_ids: [books[0].id] },
    202,
  );
  await call("POST", `/jobs/${queued.job_id}/pause`, undefined, 202);
  await call("PUT", "/settings/provider-credentials", {
    expected_revision: 1,
    fields: { ipb_pass_hash: { op: "set", value: "anonymous-process-secret" } },
  });
  const translation = await call(
    "POST",
    "/tag-translation-jobs",
    undefined,
    202,
  );
  await waitFor(
    async () =>
      (await call("GET", `/jobs/${translation.job_id}`)).state === "completed",
  );
  await stop();
  await start();
  assert.equal((await call("GET", `/jobs/${queued.job_id}`)).state, "paused");
  assert.equal(
    (await call("GET", `/books/${books[0].id}`)).title,
    "Anonymous manual edit",
  );
  assert.equal(
    (await call("GET", "/settings/tag-translations")).active.version,
    "anonymous-v1",
  );
  const settings = await call("GET", "/settings/metadata-provider");
  assert.equal(settings.configured.ipb_pass_hash, true);
  assert.ok(!JSON.stringify(settings).includes("anonymous-process-secret"));
  await call("POST", `/jobs/${queued.job_id}/resume`, undefined, 202);
  await waitFor(
    async () =>
      (await call("GET", `/jobs/${queued.job_id}`)).state === "completed",
  );
  assert.equal(
    (await call("GET", `/books/${books[0].id}`)).title,
    "Anonymous manual edit",
  );
  const manifest = JSON.parse(
    await readFile(
      path.join(run, "library/.nyamanga-anonymous-fixtures.json"),
      "utf8",
    ),
  );
  assert.equal(Object.keys(manifest.files).length, 3);
  await stop();
  await mkdir(path.join(tests, "results"), { recursive: true, mode: 0o700 });
  await writeFile(
    path.join(tests, "results/metadata-smoke.json"),
    JSON.stringify(
      {
        passed: true,
        network: "injected offline client",
        checks: [
          "running checkpoint resumes across process restart",
          "user pause survives process restart",
          "account session survives restart",
          "source and manual metadata persist",
          "credential encryption persists",
          "dictionary version persists",
          "SSE closes on SIGTERM",
          "SIGTERM under ten seconds",
        ],
      },
      null,
      2,
    ) + "\n",
    { mode: 0o600 },
  );
  console.log(
    "M3 anonymous process acceptance: PASS (checkpoint, pause, persistence, SSE, SIGTERM)",
  );
} finally {
  if (child) await stop();
}
