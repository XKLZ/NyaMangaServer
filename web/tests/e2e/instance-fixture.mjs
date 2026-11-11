import { mkdtemp, readFile, writeFile, mkdir } from "node:fs/promises";
import { spawn, spawnSync } from "node:child_process";
import path from "node:path";
import { expect } from "@playwright/test";
const root = path.resolve(import.meta.dirname, "../../..");
export async function instanceFixture(page, baseURL) {
  const port = new URL(baseURL).port;
  if (!["18081", "18082"].includes(port))
    throw Error("Anonymous recovery ports only");
  const parent = path.join(root, ".runtime/tests/runs");
  await mkdir(parent, { recursive: true });
  const temp = await mkdtemp(path.join(parent, `recovery-ui-${port}-`));
  const directory = path.join(temp, "instance");
  const executable = path.join(root, ".runtime/bin/recovery-ui-fixtures");
  const seeded = spawnSync(executable, ["--recovery-instance", directory], {
    cwd: root,
    encoding: "utf8",
  });
  if (seeded.status !== 0)
    throw Error("Anonymous recovery seed failed: " + seeded.stderr);
  const config = path.join(directory, "config.json");
  const settings = JSON.parse(await readFile(config, "utf8"));
  settings.listen_addr = `127.0.0.1:${port}`;
  settings.public_url = baseURL;
  await writeFile(config, JSON.stringify(settings), { mode: 0o600 });
  let process;
  async function start() {
    process = spawn(executable, ["--serve", config], {
      cwd: root,
      stdio: ["ignore", "ignore", "pipe"],
    });
    let error = "";
    process.stderr.on("data", (v) => (error += v));
    await expect
      .poll(
        async () => {
          if (process.exitCode !== null)
            throw Error("Anonymous server stopped: " + error);
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
  }
  async function stop() {
    if (!process || process.exitCode !== null) return;
    const done = new Promise((resolve) => process.once("exit", resolve));
    process.kill("SIGTERM");
    await Promise.race([
      done,
      new Promise((_, reject) => {
        const t = setTimeout(
          () => reject(Error("Anonymous graceful stop timeout")),
          15000,
        );
        t.unref();
      }),
    ]);
  }
  await start();
  async function login() {
    await page.goto("/login");
    await page
      .getByLabel("管理员密码", { exact: true })
      .fill("anonymous recovery password");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(page).toHaveURL(/\/admin\//);
  }
  async function get(path) {
    const response = await page.request.get(baseURL + "/api/v1" + path);
    expect(response.status()).toBe(200);
    return response.json();
  }
  async function write(path, data, method = "POST", expected = 200) {
    const session = await get("/auth/session");
    const response = await page.request.fetch(baseURL + "/api/v1" + path, {
      method,
      headers: { Origin: baseURL, "X-CSRF-Token": session.csrf_token },
      data,
    });
    expect(response.status()).toBe(expected);
    return response.json();
  }
  return { directory, temp, config, start, stop, login, get, write };
}
