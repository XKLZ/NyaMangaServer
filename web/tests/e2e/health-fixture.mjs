import { readFile, readdir, copyFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import path from "node:path";
import { expect } from "@playwright/test";
import { enhancementFixture } from "./enhancement-fixture.mjs";
export async function healthFixture(page, baseURL) {
  const api = await enhancementFixture(page, baseURL);
  const state = JSON.parse(
    await readFile(
      path.resolve(
        import.meta.dirname,
        `../../../.runtime/tests/e2e-state-${new URL(baseURL).port}.json`,
      ),
      "utf8",
    ),
  );
  const allowed = path.resolve(
    import.meta.dirname,
    "../../../.runtime/tests/runs/e2e-",
  );
  if (!state.run_directory.startsWith(allowed))
    throw Error("Anonymous health fixture required");
  const db = path.join(state.run_directory, "data/app.sqlite");
  function sql(statement, args = [], foreignKeys = true) {
    const result = spawnSync(
      "python3",
      [
        "-c",
        'import sqlite3,sys,json; c=sqlite3.connect("file:"+sys.argv[1]+"?mode=rw",uri=True,timeout=10); c.execute("PRAGMA foreign_keys="+sys.argv[4]); c.execute(sys.argv[2],json.loads(sys.argv[3])); c.commit(); c.close()',
        db,
        statement,
        JSON.stringify(args),
        foreignKeys ? "ON" : "OFF",
      ],
      { encoding: "utf8" },
    );
    if (result.status !== 0)
      throw Error("Anonymous SQL injection failed: " + result.stderr);
  }
  async function finish(id) {
    await expect
      .poll(
        async () => (await api.get(`/maintenance/health-checks/${id}`)).state,
        { timeout: 60000 },
      )
      .toBe("completed");
    return api.get(`/maintenance/health-checks/${id}`);
  }
  async function create(mode = "application", options = {}) {
    const result = await api.write(
      "/maintenance/health-checks",
      { request_key: crypto.randomUUID(), mode, ...options },
      "POST",
      202,
    );
    await finish(result.id);
    return result;
  }
  async function manyBooks() {
    const directory = path.join(state.run_directory, "library"),
      names = await readdir(directory),
      source = names.find((n) => /\.(cbz|zip)$/i.test(n));
    if (!source) throw Error("Anonymous archive missing");
    for (let i = 0; i < 28; i++)
      await copyFile(
        path.join(directory, source),
        path.join(
          directory,
          `Anonymous health ${String(i).padStart(2, "0")}.cbz`,
        ),
      );
    const lib = await api.library();
    const books = (
      await api.get(`/editions?library_id=${lib.id}&page_size=100`)
    ).items;
    expect(books.length).toBeGreaterThan(28);
    return { lib, books };
  }
  return { ...api, sql, finish, create, manyBooks };
}
