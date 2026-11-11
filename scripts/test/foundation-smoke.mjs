// Real process acceptance, restricted to a fresh anonymous repository under tests.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import {
  mkdir,
  mkdtemp,
  writeFile,
  readFile,
  rename,
  rm,
} from "node:fs/promises";
import path from "node:path";
import { bin, tests } from "../dev/runtime-paths.mjs";

await mkdir(path.join(tests, "runs"), { recursive: true, mode: 0o700 });
const repository = await mkdtemp(path.join(tests, "runs", "foundation-"));
const dev = path.join(repository, ".runtime/dev");
const library = path.join(repository, ".runtime/libraries/manual");
const data = path.join(dev, "data");
await mkdir(path.join(dev, "secrets"), { recursive: true, mode: 0o700 });
await writeFile(
  path.join(repository, "go.mod"),
  "module nyamangaserver.local/server\n",
  { mode: 0o600 },
);
const fixture = spawnSync(path.join(bin, "fixtures"), ["--output", library], {
  encoding: "utf8",
});
assert.equal(fixture.status, 0, "anonymous fixture generation");
await writeFile(
  path.join(dev, "secrets/setup-token"),
  randomBytes(32).toString("base64url"),
  { mode: 0o600 },
);
const origin = "http://localhost:18083";
const config = path.join(dev, "config.json");
await writeFile(
  config,
  JSON.stringify({
    listen_addr: "127.0.0.1:18083",
    public_url: origin,
    data_dir: "data",
    setup_token_file: "secrets/setup-token",
    library_roots: [
      {
        root_key: "anonymous",
        display_name: "Anonymous",
        path: "../libraries/manual",
      },
    ],
  }),
  { mode: 0o600 },
);
const originalManifest = await readFile(
  path.join(library, ".nyamanga-anonymous-fixtures.json"),
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
  const next = response.headers.get("set-cookie");
  if (next) cookie = next.split(";")[0];
  if (response.headers.get("content-type")?.startsWith("image/"))
    return Buffer.from(await response.arrayBuffer());
  const result = await response.json();
  if (result.csrf_token) csrf = result.csrf_token;
  return result;
}
async function start() {
  child = spawn(path.join(bin, "server"), ["--config", config], {
    stdio: "ignore",
  });
  exited = once(child, "exit");
  for (let attempt = 0; attempt < 100; attempt++) {
    assert.equal(
      child.exitCode,
      null,
      "anonymous service exited during startup",
    );
    try {
      if ((await fetch(origin + "/api/v1/health/ready")).ok) return;
    } catch {
      /* wait for bind */
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error("anonymous service readiness timeout");
}
async function stop() {
  if (!child || child.exitCode !== null) return;
  child.kill("SIGTERM");
  const result = await Promise.race([
    exited,
    new Promise((_, reject) => {
      const timeout = setTimeout(
        () => reject(new Error("graceful stop timeout")),
        12000,
      );
      timeout.unref();
    }),
  ]);
  assert.equal(result[0], 0, "graceful process exit");
}
const reset = () =>
  spawnSync(path.join(bin, "dev-reset"), ["--repository", repository], {
    encoding: "utf8",
  });
try {
  await start();
  assert.deepEqual(await call("GET", "/auth/status"), { initialized: false });
  const duplicate = spawnSync(path.join(bin, "server"), ["--config", config], {
    encoding: "utf8",
  });
  assert.notEqual(duplicate.status, 0, "duplicate instance accepted");
  assert.match(duplicate.stderr, /already in use/);
  assert.notEqual(reset().status, 0, "reset of running service accepted");
  await call("POST", "/auth/setup", {
    setup_token: await readFile(path.join(dev, "secrets/setup-token"), "utf8"),
    password: randomBytes(32).toString("base64url"),
  });
  assert.equal(
    (await call("GET", "/diagnostics")).database_baseline,
    "foundation-v1",
  );
  const saved = await call(
    "POST",
    "/libraries",
    { root_key: "anonymous", display_name: "Anonymous" },
    201,
  );
  const job = await call("POST", `/libraries/${saved.id}/scans`, {}, 202);
  let finished;
  for (let attempt = 0; attempt < 100; attempt++) {
    finished = await call("GET", `/jobs/${job.job_id}`);
    if (!["queued", "running"].includes(finished.state)) break;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  assert.equal(finished.state, "completed");
  assert.equal(finished.done, 3);
  const books = await call("GET", "/books");
  const book = books.items.find((v) => v.file_state === "available");
  const cover = `/books/${book.id}/cover?content_revision=${book.content_revision}`;
  const before = await call("GET", cover);
  await stop();
  await start();
  await call("GET", "/auth/session");
  assert.equal((await call("GET", "/books")).total, 3);
  assert.equal((await call("GET", `/jobs/${job.job_id}`)).state, "completed");
  await rename(library, library + "-offline");
  assert.deepEqual(await call("GET", cover), before);
  await call(
    "GET",
    `/books/${book.id}/pages/1/image?content_revision=${book.content_revision}`,
    undefined,
    409,
  );
  await rename(library + "-offline", library);
  await stop();
  await rm(path.join(data, "cache"), { recursive: true });
  await start();
  assert.deepEqual(await call("GET", cover), before);
  await stop();
  await writeFile(path.join(data, "reset-in-progress"), "interrupted", {
    mode: 0o600,
  });
  const half = spawnSync(path.join(bin, "server"), ["--config", config], {
    encoding: "utf8",
  });
  assert.notEqual(half.status, 0);
  assert.match(half.stderr, /reset incomplete/);
  assert.equal(reset().status, 0, "explicit anonymous reset");
  cookie = "";
  csrf = "";
  await start();
  assert.deepEqual(await call("GET", "/auth/status"), { initialized: false });
  // The sample manifest remains byte-for-byte intact after reset.
  assert.deepEqual(
    await readFile(path.join(library, ".nyamanga-anonymous-fixtures.json")),
    originalManifest,
  );
  console.log(
    "Anonymous process startup / exclusive lock / scan / restart persistence / offline cover / cache rebuild / interrupted reset / empty reinitialization: PASS",
  );
} finally {
  await stop();
}
