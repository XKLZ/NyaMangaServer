// Anonymous Work/Edition process restart, organization and saved-search acceptance.
// Fixture-only remote; no private files or internet requests.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { mkdir, mkdtemp, writeFile, readFile } from "node:fs/promises";
import path from "node:path";
import { bin, tests } from "../dev/runtime-paths.mjs";
await mkdir(path.join(tests, "runs"), { recursive: true, mode: 0o700 });
const run = await mkdtemp(path.join(tests, "runs", "organization-"));
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
  throw new Error("anonymous M4 condition timed out");
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
    password: "Anonymous M4 process password",
  });
  const lib = await call(
    "POST",
    "/libraries",
    { root_key: "anonymous", display_name: "Anonymous" },
    201,
  );
  const scan = await call("POST", `/libraries/${lib.id}/scans`, {}, 202);
  await waitFor(
    async () =>
      (await call("GET", `/jobs/${scan.job_id}`)).state === "completed",
  );
  const books = (await call("GET", "/books?state=available")).items;
  assert.equal(books.length, 2);
  const task = await call(
    "POST",
    "/metadata-jobs",
    {
      book_ids: [books[0].id],
      gallery_urls: { [books[0].id]: "https://e-hentai.org/g/1/0123456789/" },
    },
    202,
  );
  await waitFor(
    async () =>
      (await call("GET", `/jobs/${task.job_id}`)).state === "completed",
  );
  let book = await call("GET", `/books/${books[0].id}`);
  await call("PATCH", `/books/${book.id}/overrides`, {
    expected_revision: book.revision,
    fields: { title: { op: "set", value: "匿名人工第10卷" } },
  });
  await call("PATCH", `/books/${book.id}/user-state`, {
    expected_revision: 0,
    favorite: true,
  });
  let context = await call(
    "GET",
    `/work-organization/context?edition_ids=${books.map((b) => b.id).join(",")}`,
  );
  const targetWork = context.editions.find((e) => e.id === books[1].id).work_id;
  const sourceWork = context.editions.find((e) => e.id === books[0].id).work_id;
  const merge = await call("POST", "/work-organization/preview", {
    action: "work_merge",
    id: targetWork,
    work_ids: [sourceWork],
    expected_revisions: context.expected_revisions,
  });
  await call("POST", `/works/${targetWork}/merge`, merge.command);
  const series = await call("POST", "/series", {
    library_id: lib.id,
    name: "匿名持久系列",
    work_ids: [],
    expected_revisions: {},
  });
  context = await call(
    "GET",
    `/work-organization/context?work_ids=${targetWork}&series_ids=${series.id}`,
  );
  await call("PUT", `/series/${series.id}/members`, {
    work_ids: [targetWork],
    expected_revisions: context.expected_revisions,
  });
  const result = await call("POST", "/books/search", {
    query: "人工",
    collapse: "series",
  });
  assert.equal(result.total, 1);
  assert.equal(result.items[0].kind, "series");
  assert.equal(result.items[0].other_version_matches, true);
  assert.equal(result.items[0].favorite_state, "partial");
  const saved = await call("POST", "/saved-searches", {
    name: "匿名持久搜索",
    query_version: 1,
    expected_revision: 0,
    conditions: {
      query: "人工",
      collapse: "series",
      page: 5,
      snapshot_revision: result.snapshot_revision,
    },
  });
  assert.equal(saved.conditions.page, undefined);
  assert.equal(saved.conditions.snapshot_revision, undefined);
  const before = await call("GET", `/books/${book.id}`);
  await stop();
  await start();
  await call("GET", "/auth/session");
  const after = await call("GET", `/books/${book.id}`);
  assert.deepEqual(after, before);
  assert.equal(
    (await call("GET", `/saved-searches/${saved.id}`)).name,
    saved.name,
  );
  assert.equal(
    (await call("GET", `/series/${series.id}/members`)).items[0].id,
    targetWork,
  );
  assert.equal((await call("GET", `/jobs/${task.job_id}`)).state, "completed");
  const repeated = await call("POST", "/books/search", {
    query: "人工",
    collapse: "series",
  });
  assert.deepEqual(repeated, result);
  await call("PATCH", `/books/${book.id}/user-state`, {
    expected_revision: 1,
    hidden: true,
  });
  await call(
    "POST",
    "/books/search",
    {
      query: "人工",
      collapse: "series",
      page: 2,
      snapshot_revision: result.snapshot_revision,
    },
    409,
  );
  assert.equal(
    (await call("POST", "/books/search", { scope: "hidden" })).total,
    1,
  );
  const rescan = await call("POST", `/libraries/${lib.id}/scans`, {}, 202);
  await waitFor(
    async () =>
      (await call("GET", `/jobs/${rescan.job_id}`)).state === "completed",
  );
  book = await call("GET", `/books/${book.id}`);
  assert.equal(book.title, "匿名人工第10卷");
  assert.equal(book.user_state.hidden, true);
  assert.equal(book.organization.works[0].id, targetWork);
  await stop();
  await mkdir(path.join(tests, "results"), { recursive: true });
  await writeFile(
    path.join(tests, "results", "organization-smoke.json"),
    JSON.stringify(
      {
        passed: true,
        platform: process.platform,
        architecture: process.arch,
        checks: [
          "session restart",
          "metadata and manual values",
          "physical states",
          "series and works",
          "saved search restart",
          "snapshot conflict",
          "rescan preservation",
          "SIGTERM",
        ],
        run,
      },
      null,
      2,
    ),
    { mode: 0o600 },
  );
  console.log(
    "Anonymous Work/Edition restart/organization/search preservation: PASS",
  );
} finally {
  if (child) {
    child.kill("SIGTERM");
    await exited;
  }
}
