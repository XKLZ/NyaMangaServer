import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { randomBytes } from "node:crypto";
import { spawn, spawnSync } from "node:child_process";
import path from "node:path";
const port = Number(process.argv[2] ?? 18081);
if (![18081, 18082].includes(port)) throw new Error("unsupported test port");
import { tests as root } from "../dev/runtime-paths.mjs";
await mkdir(root, { recursive: true, mode: 0o700 });
await mkdir(path.join(root, "runs"), { recursive: true, mode: 0o700 });
const run = await mkdtemp(path.join(root, "runs", "e2e-"));
const library = path.join(run, "library");
for (const directory of [library, path.join(run, "work-library")]) {
  const fixture = spawnSync(
    path.join(root, "..", "bin", "fixtures"),
    ["--output", directory],
    { stdio: "inherit" },
  );
  if (fixture.status !== 0) throw new Error("fixture generation failed");
}
const token = path.join(run, "setup-token");
await writeFile(token, randomBytes(32).toString("base64url"), { mode: 0o600 });
const config = path.join(run, "config.json");
await writeFile(
  config,
  JSON.stringify({
    listen_addr: `127.0.0.1:${port}`,
    public_url: `http://localhost:${port}`,
    data_dir: "data",
    setup_token_file: "setup-token",
    provider_key_file: "secrets/provider-key",
    provider_credentials_file: "secrets/providers.enc",
    library_roots: [
      { root_key: "anonymous", display_name: "匿名验收目录", path: "library" },
      {
        root_key: "anonymous-work",
        display_name: "匿名Work验收目录",
        path: "work-library",
      },
    ],
  }),
  { mode: 0o600 },
);
await writeFile(
  path.join(root, `e2e-state-${port}.json`),
  JSON.stringify({ token_file: token, run_directory: run }),
  { mode: 0o600 },
);
const child = spawn(
  path.join(root, "..", "bin", "fixtures"),
  ["--serve", config],
  {
    stdio: "inherit",
  },
);
for (const signal of ["SIGTERM", "SIGINT"])
  process.on(signal, () => child.kill(signal));
child.on("exit", (code) => process.exit(code ?? 0));
