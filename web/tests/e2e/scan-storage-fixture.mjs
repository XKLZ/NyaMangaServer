import { mkdtemp, readFile, writeFile, mkdir } from "node:fs/promises";
import { spawn, spawnSync } from "node:child_process";
import path from "node:path";
import { expect } from "@playwright/test";
const root = path.resolve(import.meta.dirname, "../../..");
export async function scanStorageFixture(page, baseURL, options = {}) {
  if (!["18081", "18082"].includes(new URL(baseURL).port))
    throw Error("Anonymous ports only");
  const parent = path.join(root, ".runtime/tests/runs");
  await mkdir(parent, { recursive: true });
  const directory = path.join(
    await mkdtemp(path.join(parent, "scan-storage-ui-")),
    "instance",
  );
  const binary = path.join(root, ".runtime/tests/scan-storage-ui/bin");
  const prep = spawnSync(
    path.join(binary, "fixtures"),
    ["--recovery-instance", directory],
    { cwd: root, encoding: "utf8" },
  );
  if (prep.status) throw Error(prep.stderr);
  const filename = path.join(directory, "config.json");
  const config = JSON.parse(await readFile(filename, "utf8"));
  config.listen_addr = `127.0.0.1:${new URL(baseURL).port}`;
  config.public_url = baseURL;
  config.image_memory_mib = 32;
  await writeFile(filename, JSON.stringify(config), { mode: 0o600 });
  const db = path.join(config.data_dir, "app.sqlite");
  function sql(statement) {
    const out = spawnSync(
      "python3",
      [
        "-c",
        "import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.executescript(sys.argv[2]); c.commit()",
        db,
        statement,
      ],
      { encoding: "utf8" },
    );
    if (out.status) throw Error(out.stderr);
  }
  sql(
    "UPDATE libraries SET scan_interval_minutes=NULL,next_scan_at=NULL; UPDATE instance_state SET background_hold=0; UPDATE jobs SET state='cancelled' WHERE state='paused';",
  );
  const seed = spawnSync(
    "python3",
    [path.join(import.meta.dirname, "scan-storage-seed.py"), filename],
    { encoding: "utf8" },
  );
  if (seed.status) throw Error(seed.stderr);
  if (options.seriesOrigin) {
    sql(
      "UPDATE series SET name='匿名系列编号' WHERE id=(SELECT series_id FROM series_members LIMIT 1); UPDATE series_members SET position=position-1 WHERE series_id=(SELECT id FROM series WHERE name='匿名系列编号');",
    );
  }
  const child = spawn(path.join(binary, "server"), ["--config", filename], {
    cwd: root,
    stdio: ["ignore", "ignore", "pipe"],
  });
  let stderr = "";
  child.stderr.on("data", (v) => (stderr += v));
  async function stop() {
    if (child.exitCode !== null) return;
    const done = new Promise((r) => child.once("exit", r));
    child.kill("SIGTERM");
    await Promise.race([
      done,
      new Promise((_, reject) => {
        const t = setTimeout(
          () => reject(Error("Anonymous stop timed out")),
          10000,
        );
        t.unref();
      }),
    ]);
  }
  try {
    await expect
      .poll(
        async () => {
          if (child.exitCode !== null) throw Error(stderr);
          try {
            return (
              await page.request.get(baseURL + "/api/v1/health/ready")
            ).status();
          } catch {
            return 0;
          }
        },
        { timeout: 20000 },
      )
      .toBe(200);
    const response = await page.request.post(baseURL + "/api/v1/auth/login", {
      headers: { Origin: baseURL },
      data: { password: "anonymous recovery password" },
    });
    expect(response.status()).toBe(200);
  } catch (e) {
    await stop();
    throw e;
  }
  async function get(route) {
    const r = await page.request.get(baseURL + "/api/v1" + route);
    expect(r.status(), route + " " + (await r.text())).toBe(200);
    return r.json();
  }
  async function write(route, data, method = "POST", status = 200) {
    const session = await get("/auth/session");
    const r = await page.request.fetch(baseURL + "/api/v1" + route, {
      method,
      headers: { Origin: baseURL, "X-CSRF-Token": session.csrf_token },
      data,
    });
    expect(r.status(), route + " " + (await r.text())).toBe(status);
    return status === 204 ? null : r.json();
  }
  return { directory, config, sql, get, write, stop };
}
