// Anonymous management process acceptance; no private comics or remote traffic.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { mkdir, mkdtemp, writeFile, rename } from "node:fs/promises";
import path from "node:path";
import { bin, tests } from "../dev/runtime-paths.mjs";
await mkdir(path.join(tests, "runs"), { recursive: true, mode: 0o700 });
const run = await mkdtemp(path.join(tests, "runs", "admin-"));
assert.equal(
  spawnSync(
    path.join(bin, "fixtures"),
    ["--output", path.join(run, "library")],
    { stdio: "ignore" },
  ).status,
  0,
);
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
let child,
  exited,
  cookie = "",
  csrf = "";
async function call(
  method,
  route,
  body,
  status = 200,
  raw = false,
  extra = {},
) {
  const response = await fetch(origin + "/api/v1" + route, {
    method,
    headers: {
      Origin: origin,
      Cookie: cookie,
      "X-CSRF-Token": csrf,
      "Content-Type": raw ? "image/png" : "application/json",
      ...extra,
    },
    body: body === undefined ? undefined : raw ? body : JSON.stringify(body),
  });
  const text = await response.text();
  assert.equal(response.status, status, `${method} ${route}: ${text}`);
  if (response.headers.has("set-cookie"))
    cookie = response.headers.get("set-cookie").split(";")[0];
  const result = text ? JSON.parse(text) : undefined;
  if (result?.csrf_token) csrf = result.csrf_token;
  return result;
}
async function waitFor(check) {
  for (let i = 0; i < 1000; i++) {
    const result = await check();
    if (result) return result;
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  throw new Error("anonymous admin condition timed out");
}
async function start() {
  child = spawn(path.join(bin, "fixtures"), ["--serve", config], {
    stdio: ["ignore", "ignore", "pipe"],
  });
  child.stderr.on("data", (data) => process.stderr.write(data));
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
  assert.equal((await exited)[0], 0);
  assert.ok(Date.now() - before < 10000);
  child = null;
}
async function job(id) {
  return waitFor(async () => {
    const value = await call("GET", `/jobs/${id}`);
    assert.ok(
      !["failed", "paused", "cancelled"].includes(value.state),
      JSON.stringify(value),
    );
    return value.state === "completed" && value;
  });
}
async function maintenance(operation, book, options) {
  const plan = await call("POST", "/maintenance/plans", {
    operation,
    book_ids: [book.id],
    expected_revisions: { [`book:${book.id}`]: book.revision },
    ...(options ? { options } : {}),
  });
  const accepted = await call(
    "POST",
    `/maintenance/plans/${plan.id}/execute`,
    {},
    202,
  );
  const repeated = await call(
    "POST",
    `/maintenance/plans/${plan.id}/execute`,
    {},
    202,
  );
  assert.equal(repeated.job_id, accepted.job_id);
  await job(accepted.job_id);
  return plan;
}
const checks = [];
try {
  await start();
  await call("POST", "/auth/setup", {
    setup_token: token,
    password: "Anonymous administration password",
  });
  const library = await call(
    "POST",
    "/libraries",
    { root_key: "anonymous", display_name: "匿名管理书库" },
    201,
  );
  await job(
    (await call("POST", `/libraries/${library.id}/scans`, {}, 202)).job_id,
  );
  const listing = await call("POST", "/admin/books/search", {
    page: 1,
    page_size: 1,
    file_state: "available",
  });
  assert.equal(listing.total, 2);
  assert.equal(listing.items.length, 1);
  let book = await call("GET", `/books/${listing.items[0].id}`);
  checks.push("physical admin pagination");
  let selected = await call("GET", `/books/${book.id}/cover-selection`);
  selected = await call("PUT", `/books/${book.id}/cover-selection`, {
    expected_revision: selected.revision,
    mode: "page",
    page_index: 1,
    content_revision: book.content_revision,
  });
  await job(
    (
      await call(
        "POST",
        `/books/${book.id}/cover-rebuilds`,
        { expected_revision: selected.revision },
        202,
      )
    ).job_id,
  );
  const image = await fetch(
    `${origin}/api/v1/books/${book.id}/pages/1/image?content_revision=${book.content_revision}`,
    { headers: { Cookie: cookie } },
  );
  assert.equal(image.status, 200);
  selected = await call(
    "POST",
    `/books/${book.id}/cover-assets`,
    Buffer.from(await image.arrayBuffer()),
    200,
    true,
    { "X-Expected-Revision": String(selected.revision) },
  );
  assert.equal(selected.mode, "asset");
  checks.push("page selection / rebuild / persistent cover asset");
  book = await call("GET", `/books/${book.id}`);
  const previewJob = await call(
    "POST",
    `/books/${book.id}/source-previews`,
    {
      gallery_url: "https://e-hentai.org/g/1/0123456789/",
      expected_revision: book.revision,
      selection_revision: book.metadata.selection_revision,
      content_revision: book.content_revision,
    },
    202,
  );
  await job(previewJob.job_id);
  const previewItems = await call("GET", `/jobs/${previewJob.job_id}/items`);
  const previewID = previewItems.items[0].preview_id;
  assert.ok(previewID);
  const preview = await call(
    "GET",
    `/books/${book.id}/source-previews/${previewID}`,
  );
  assert.equal(preview.id, previewID);
  assert.equal(
    (await call("GET", `/books/${book.id}`)).metadata.source,
    null,
    "preview must not adopt source",
  );
  await call("POST", `/books/${book.id}/source-previews/${previewID}/confirm`, {
    expected_revision: book.revision,
    selection_revision: book.metadata.selection_revision,
    content_revision: book.content_revision,
  });
  book = await call("GET", `/books/${book.id}`);
  await call("PATCH", `/books/${book.id}/overrides`, {
    expected_revision: book.revision,
    fields: { title: { op: "set", value: "匿名保留人工标题" } },
  });
  book = await call("GET", `/books/${book.id}`);
  await call("DELETE", `/books/${book.id}/source`, {
    expected_revision: book.revision,
    selection_revision: book.metadata.selection_revision,
  });
  book = await call("GET", `/books/${book.id}`);
  assert.equal(book.title, "匿名保留人工标题");
  assert.equal(book.metadata.source, null);
  checks.push(
    "source preview / explicit adoption / unlink preserves manual values",
  );
  await maintenance("trash", book);
  assert.equal((await call("POST", "/admin/books/search", {})).total, 2);
  await job(
    (await call("POST", `/libraries/${library.id}/scans`, {}, 202)).job_id,
  );
  assert.equal(
    (await call("POST", "/admin/books/search", {})).total,
    2,
    "scan cannot undo trash",
  );
  book = await call("GET", `/books/${book.id}`);
  await maintenance("restore", book);
  book = await call("GET", `/books/${book.id}`);
  assert.equal(book.title, "匿名保留人工标题");
  const before = await call("GET", `/books/${book.id}/cover-selection`);
  await stop();
  await start();
  await call("GET", "/auth/session");
  assert.deepEqual(
    await call("GET", `/books/${book.id}/cover-selection`),
    before,
  );
  assert.equal((await call("GET", `/books/${book.id}`)).title, book.title);
  checks.push("trash / scan exclusion / restore / restart preservation");
  const currentLibrary = (await call("GET", "/libraries")).items.find(
    (item) => item.id === library.id,
  );
  const disabled = await call("PATCH", `/libraries/${library.id}/status`, {
    expected_revision: currentLibrary.revision,
    disabled: true,
  });
  await call("POST", `/libraries/${library.id}/scans`, {}, 409);
  const cover = await fetch(
    `${origin}/api/v1/books/${book.id}/cover?content_revision=${book.content_revision}`,
    { headers: { Cookie: cookie } },
  );
  assert.equal(cover.status, 200);
  await call("PATCH", `/libraries/${library.id}/status`, {
    expected_revision: disabled.revision,
    disabled: false,
  });
  checks.push("disabled library blocks source work / cached cover retained");
  const newRelative = "relinked-anonymous.zip";
  await rename(
    path.join(run, "library", book.relative_path),
    path.join(run, "library", newRelative),
  );
  book = await call("GET", `/books/${book.id}`);
  await maintenance("relink", book, {
    root_key: "anonymous",
    relative_path: newRelative,
    confirm_replacement: true,
  });
  book = await call("GET", `/books/${book.id}`);
  assert.equal(book.relative_path, newRelative);
  assert.equal(book.title, "匿名保留人工标题");
  checks.push("explicit relink retains book ID and manual values");
  await maintenance("trash", book);
  book = await call("GET", `/books/${book.id}`);
  await maintenance("purge", book);
  await job(
    (await call("POST", `/libraries/${library.id}/scans`, {}, 202)).job_id,
  );
  assert.equal(
    (await call("POST", "/admin/books/search", {})).total,
    2,
    "purged path remains excluded",
  );
  checks.push("permanent index purge / durable exclusion");
  const storage = await call("GET", "/maintenance/storage");
  assert.ok(Array.isArray(storage.categories));
  const history = await call(
    "GET",
    "/jobs?page=1&page_size=1&type=maintenance",
  );
  assert.equal(history.items.length, 1);
  assert.ok(history.total >= 5);
  checks.push("storage diagnostics / task pagination");
  await stop();
  await mkdir(path.join(tests, "results"), { recursive: true });
  await writeFile(
    path.join(tests, "results", "admin-smoke.json"),
    JSON.stringify(
      {
        passed: true,
        platform: process.platform,
        architecture: process.arch,
        checks,
        run,
      },
      null,
      2,
    ),
    { mode: 0o600 },
  );
  console.log("Anonymous administration lifecycle/restart: PASS");
} finally {
  if (child) {
    child.kill("SIGTERM");
    await exited;
  }
}
