import { mkdtemp, readFile, writeFile, mkdir } from "node:fs/promises";
import { spawn, spawnSync } from "node:child_process";
import path from "node:path";
import { expect } from "@playwright/test";
const root = path.resolve(import.meta.dirname, "../../..");
export const password = "anonymous recovery password";
export async function compatibilityFixture(
  page,
  baseURL,
  { recommendations = false, editorReady = false } = {},
) {
  if (!["18081", "18082"].includes(new URL(baseURL).port))
    throw Error("Anonymous ports only");
  const parent = path.join(root, ".runtime/tests/runs");
  await mkdir(parent, { recursive: true });
  const directory = path.join(
    await mkdtemp(path.join(parent, "compatibility-ui-")),
    "instance",
  );
  const fixture = path.join(root, ".runtime/bin/compatibility-ui-fixtures"),
    server = path.join(root, ".runtime/bin/compatibility-ui-server");
  const prepared = spawnSync(fixture, ["--recovery-instance", directory], {
    cwd: root,
    encoding: "utf8",
  });
  if (prepared.status) throw Error(prepared.stderr);
  const filename = path.join(directory, "config.json"),
    config = JSON.parse(await readFile(filename, "utf8"));
  config.listen_addr = `127.0.0.1:${new URL(baseURL).port}`;
  config.public_url = baseURL;
  for (const name of recommendations ? ["b"] : ["b", "c"]) {
    const archive = path.join(directory, `library-${name}`);
    const seed = spawnSync(fixture, ["--output", archive], {
      cwd: root,
      encoding: "utf8",
    });
    if (seed.status) throw Error(seed.stderr);
    config.library_roots.push({
      root_key: `fixture-${name}`,
      display_name: `匿名库 ${name.toUpperCase()}`,
      path: archive,
    });
  }
  await writeFile(filename, JSON.stringify(config), { mode: 0o600 });
  const db = path.join(config.data_dir, "app.sqlite");
  function sql(statement) {
    const result = spawnSync(
      "python3",
      [
        "-c",
        "import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.executescript(sys.argv[2]); c.commit()",
        db,
        statement,
      ],
      { encoding: "utf8" },
    );
    if (result.status) throw Error(result.stderr);
  }
  sql(
    "UPDATE libraries SET scan_interval_minutes=NULL,next_scan_at=NULL; UPDATE instance_state SET background_hold=0;",
  );
  if (editorReady)
    sql(
      "UPDATE account_profiles SET must_change_password=0 WHERE account_id='fixture-editor';",
    );
  const child = spawn(
    recommendations ? fixture : server,
    [recommendations ? "--serve" : "--config", filename],
    { cwd: root, stdio: ["ignore", "ignore", "pipe"] },
  );
  let stderr = "";
  child.stderr.on("data", (v) => (stderr += v));
  async function stop() {
    if (child.exitCode !== null) return;
    const done = new Promise((r) => child.once("exit", r));
    child.kill("SIGTERM");
    await Promise.race([
      done,
      new Promise((_, r) => {
        const t = setTimeout(() => r(Error("Anonymous stop timed out")), 10000);
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
  } catch (e) {
    await stop();
    throw e;
  }
  async function get(route, client = page.request) {
    const r = await client.get(baseURL + "/api/v1" + route);
    expect(r.status(), route).toBe(200);
    return r.json();
  }
  async function write(
    route,
    data,
    method = "POST",
    client = page.request,
    status = 200,
  ) {
    const session = await get("/auth/session", client);
    const r = await client.fetch(baseURL + "/api/v1" + route, {
      method,
      headers: { Origin: baseURL, "X-CSRF-Token": session.csrf_token },
      data,
    });
    expect(r.status(), route + " " + (await r.text())).toBe(status);
    return status === 204 ? null : r.json();
  }
  async function apiLogin(
    client = page.request,
    username = "admin",
    secret = password,
  ) {
    const r = await client.post(baseURL + "/api/v1/auth/login", {
      headers: { Origin: baseURL },
      data: { username, password: secret },
    });
    expect(r.status()).toBe(200);
    return r.json();
  }
  async function login(p = page, username = "admin", secret = password) {
    await p.goto(baseURL + "/login");
    await p.getByLabel("用户名", { exact: true }).fill(username);
    await p.getByLabel("密码", { exact: true }).fill(secret);
    await p.getByRole("button", { name: "登录", exact: true }).click();
  }
  await apiLogin();
  const libraries = [(await get("/libraries")).items[0]];
  try {
    for (const name of recommendations ? ["b"] : ["b", "c"]) {
      const lib = await write(
        "/libraries",
        {
          root_key: `fixture-${name}`,
          display_name: `匿名库 ${name.toUpperCase()}`,
        },
        "POST",
        page.request,
        201,
      );
      libraries.push(lib);
      const job = await write(
        `/libraries/${lib.id}/scans`,
        {},
        "POST",
        page.request,
        202,
      );
      await expect
        .poll(async () => (await get(`/jobs/${job.job_id}`)).state, {
          timeout: 15000,
        })
        .toBe("completed");
    }
  } catch (error) {
    await stop();
    throw error;
  }
  async function grant(id, grants, extra = {}) {
    const account = (await get("/accounts")).items.find((x) => x.id === id);
    await write(
      `/accounts/${id}`,
      {
        expected_revision: account.revision,
        role: account.role,
        enabled: account.enabled,
        library_grants: grants,
        ...extra,
      },
      "PUT",
      page.request,
      204,
    );
  }
  return {
    directory,
    db,
    sql,
    stop,
    get,
    write,
    apiLogin,
    login,
    libraries,
    grant,
  };
}
