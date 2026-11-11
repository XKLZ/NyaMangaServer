import { randomBytes } from "node:crypto";
import { mkdir, writeFile, access, readFile } from "node:fs/promises";
import path from "node:path";
import { dev as root, runtime } from "./runtime-paths.mjs";
async function exists(file) {
  try {
    await access(file);
    return true;
  } catch (error) {
    if (error.code === "ENOENT") return false;
    throw error;
  }
}
const record = path.join(runtime, "maintenance/promote-pilot/state.json");
if (await exists(record)) {
  const { phase } = JSON.parse(await readFile(record, "utf8"));
  if (!["complete", "rolled_back"].includes(phase))
    throw new Error(
      "Instance migration incomplete; resume or roll back before initialization.",
    );
}
const configs = ["config.json", "vite-config.json"].map((name) =>
  path.join(root, name),
);
const present = await Promise.all(configs.map(exists));
if (present.some(Boolean)) {
  if (!present.every(Boolean))
    throw new Error(
      "Incomplete existing configuration; refusing to replace it.",
    );
  for (const file of configs) JSON.parse(await readFile(file, "utf8"));
  console.log(
    "Existing development configuration preserved; no secrets generated or changed.",
  );
} else {
  if (await exists(root))
    throw new Error(
      "Existing instance without configuration; refusing to create an empty replacement.",
    );
  await mkdir(path.join(root, "../libraries/manual"), {
    recursive: true,
    mode: 0o700,
  });
  await mkdir(path.join(root, "secrets"), { recursive: true, mode: 0o700 });
  await writeFile(
    path.join(root, "secrets/setup-token"),
    randomBytes(32).toString("base64url"),
    { mode: 0o600, flag: "wx" },
  );
  for (const [index, publicURL] of [
    "http://127.0.0.1:8080",
    "http://127.0.0.1:5173",
  ].entries()) {
    await writeFile(
      configs[index],
      JSON.stringify(
        {
          listen_addr: "127.0.0.1:8080",
          public_url: publicURL,
          data_dir: "data",
          setup_token_file: "secrets/setup-token",
          provider_key_file: "secrets/provider-key",
          provider_credentials_file: "secrets/providers.enc",
          library_roots: [
            {
              root_key: "dev-manual",
              display_name: "本机测试书库",
              path: "../libraries/manual",
            },
          ],
        },
        null,
        2,
      ) + "\n",
      { mode: 0o600, flag: "wx" },
    );
  }
  console.log(
    "New development configuration ready in .runtime/dev. Read secrets/setup-token locally for first setup.",
  );
}
