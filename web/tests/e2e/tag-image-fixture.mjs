import { expect } from "@playwright/test";
import { readFile } from "node:fs/promises";
import path from "node:path";
export async function tagImageFixture(page, baseURL) {
  const fixture = JSON.parse(
    await readFile(
      path.resolve(
        import.meta.dirname,
        `../../../.runtime/tests/e2e-state-${new URL(baseURL).port}.json`,
      ),
      "utf8",
    ),
  );
  expect(
    fixture.run_directory.startsWith(
      path.resolve(import.meta.dirname, "../../../.runtime/tests/runs/e2e-"),
    ),
  ).toBe(true);
  const auth = await page.request.post(`${baseURL}/api/v1/auth/setup`, {
    headers: { Origin: baseURL },
    data: {
      setup_token: await readFile(fixture.token_file, "utf8"),
      password: "Anonymous tag images 2026",
    },
  });
  expect(auth.status()).toBe(200);
  const headers = {
    Origin: baseURL,
    "X-CSRF-Token": (await auth.json()).csrf_token,
  };
  async function get(url) {
    const r = await page.request.get(`${baseURL}/api/v1${url}`);
    expect(r.status(), url).toBe(200);
    return r.json();
  }
  async function write(url, data = {}, method = "POST", status = 200) {
    const r = await page.request.fetch(`${baseURL}/api/v1${url}`, {
      method,
      headers,
      data,
    });
    expect(r.status(), url).toBe(status);
    return r.json();
  }
  async function upload(id, role, revision, data) {
    const r = await page.request.put(
      `${baseURL}/api/v1/tag-catalog/${id}/images/${role}`,
      {
        headers: {
          ...headers,
          "Content-Type": "image/png",
          "X-Expected-Revision": String(revision),
        },
        data,
      },
    );
    expect(r.status()).toBe(200);
    return r.json();
  }
  const lib = await write(
      "/libraries",
      { root_key: "anonymous", display_name: "A 匿名图片" },
      "POST",
      201,
    ),
    other = await write(
      "/libraries",
      { root_key: "anonymous-work", display_name: "B 共享图片" },
      "POST",
      201,
    );
  for (const l of [lib, other]) {
    const job = await write(`/libraries/${l.id}/scans`, {}, "POST", 202);
    await expect
      .poll(async () => (await get(`/jobs/${job.job_id}`)).state)
      .toBe("completed");
    const books = (await get(`/editions?library_id=${l.id}&page_size=100`))
      .items;
    for (let i = 0; i < books.length; i++) {
      const b = await get(`/books/${books[i].id}`);
      await write(
        `/books/${b.id}/overrides`,
        {
          expected_revision: b.revision,
          fields: {
            title_original: { op: "set", value: `[匿名] 星の画像 ${i}` },
            tags: {
              op: "set",
              value: [
                { namespace: "parody", name: "image original" },
                {
                  namespace: "character",
                  name: l.id === lib.id ? "image hero" : "zero elsewhere",
                },
                ...Array.from({ length: l.id === lib.id ? 58 : 1 }, (_, n) => ({
                  namespace: "artist",
                  name: `artist ${String(n).padStart(2, "0")} ${n === 0 ? "名前の長い匿名画師 ".repeat(8) : ""}`,
                })),
                { namespace: "group", name: "demo circle" },
                { namespace: "language", name: "japanese" },
              ],
            },
          },
        },
        "PATCH",
      );
    }
  }
  const tag = (await get(`/tag-catalog?library_id=${lib.id}&category=parody`))
      .items[0],
    zero = (await get(`/tag-catalog?library_id=${other.id}&category=character`))
      .items[0];
  await write(
    `/parody-character-relations/${tag.id}/${zero.id}`,
    { expected_revision: 0, decision: "confirmed" },
    "PUT",
  );
  const initial = await get(`/tag-catalog/${tag.id}?library_id=${lib.id}`),
    bookBefore = (await get(`/editions?library_id=${lib.id}`)).items[0],
    metadataBefore = await get(`/books/${bookBefore.id}`);
  return {
    lib,
    other,
    tag,
    zero,
    initial,
    bookBefore,
    metadataBefore,
    get,
    write,
    upload,
    headers,
  };
}
