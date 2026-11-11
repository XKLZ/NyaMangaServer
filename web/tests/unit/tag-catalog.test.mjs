import test from "node:test";
import assert from "node:assert/strict";
import {
  catalogInput,
  tagWorkInput,
  tagWorkspaceURL,
  relationPair,
  canSuppress,
  relationLabel,
} from "../../src/admin/tags/tagCatalogFlow.ts";
import {
  libraryPolicy,
  libraryTarget,
  switchLibraryURL,
} from "../../src/shared/libraryScope.ts";
test("five catalog categories stay scoped; content retains language and unknown namespaces", () => {
  for (const category of ["parody", "character", "artist", "group", "content"])
    assert.equal(
      catalogInput(`?category=${category}`, "lib").category,
      category,
    );
  assert.equal(
    catalogInput("?category=content&namespace=unknown_ns", "lib").namespace,
    "unknown_ns",
  );
  assert.equal(
    catalogInput("?category=content&namespace=language", "lib").namespace,
    "language",
  );
  assert.equal(catalogInput("?category=bad", "lib").category, "parody");
  assert.equal(catalogInput("", "lib").include_unused, undefined);
  assert.equal(
    catalogInput("?q=中文英語&sort=usage&order=desc&page=2", "lib").q,
    "中文英語",
  );
});
test("tag navigation resets only affected independent pagination identities", () => {
  const q = new URL(
    tagWorkspaceURL(
      "?tag=a&related_tag=b&partition=evidence&r_page=3&e_page=2&page=4&book=e",
      { tag: "c" },
    ),
    "http://anonymous",
  ).searchParams;
  assert.equal(q.get("tag"), "c");
  assert.equal(q.get("page"), "4");
  for (const k of ["related_tag", "partition", "r_page", "e_page", "book"])
    assert.equal(q.has(k), false);
  const filter = new URL(
    tagWorkspaceURL("?tag=a&page=3&e_page=2", { q: "new" }),
    "http://anonymous",
  ).searchParams;
  assert.equal(filter.has("page"), false);
  assert.equal(filter.get("e_page"), "2");
  assert.equal(filter.get("tag"), "a");
});
test("tags use one chosen library, never an inferred owner; switching clears tag and all tokens", () => {
  assert.equal(libraryPolicy("/admin/tags"), "single");
  assert.equal(libraryTarget("/admin/tags", "?tag=global&book=e"), null);
  const q = new URL(
    switchLibraryURL(
      "/admin/tags",
      "?tag=a&related_tag=b&partition=evidence&r_page=2&e_page=3&snapshot=x&catalog_snapshot=c&relation_snapshot=r&edition_snapshot=e&q=title",
      "next",
    ),
    "http://anonymous",
  ).searchParams;
  assert.equal(q.get("q"), "title");
  assert.equal(q.get("library_id"), "next");
  for (const k of [
    "tag",
    "related_tag",
    "partition",
    "r_page",
    "e_page",
    "snapshot",
    "catalog_snapshot",
    "relation_snapshot",
    "edition_snapshot",
  ])
    assert.equal(q.has(k), false);
});
test("only actual opposing identities form a pair; empty and placeholder targets cannot be guessed", () => {
  const p = { id: "p", namespace: "parody" },
    c = { id: "c", namespace: "character" };
  assert.deepEqual(relationPair(c, p), { parody: p, character: c });
  assert.throws(() => relationPair({ ...p, placeholder: true }, c));
  assert.throws(() => relationPair(p, { ...c, id: "" }));
  assert.throws(() => relationPair(p, { id: "a", namespace: "artist" }));
  assert.equal(
    canSuppress({ parody_id: "", character_id: "c", decision: "auto" }),
    false,
  );
  assert.equal(
    canSuppress({ parody_id: "p", character_id: "c", decision: "confirmed" }),
    false,
  );
  assert.equal(
    canSuppress({
      parody_id: "p",
      character_id: "c",
      decision: "confirmed",
      suppressed: true,
    }),
    true,
  );
  assert.equal(relationLabel("unknown"), "未知状态");
  assert.equal(relationLabel("inferred"), "本版本唯一剩余匹配");
});

const { globalIntent, suppressionIntent } =
  await import("../../src/admin/tags/tagCatalogFlow.ts");
test("global relation intent freezes actual pair and relation CAS independently of translation revisions", () => {
  const p = {
    id: "p",
    namespace: "parody",
    name: "alpha",
    translation: "甲",
    translation_revision: 80,
  };
  const c = {
    id: "c",
    namespace: "character",
    name: "beta",
    translation: "乙",
    translation_revision: 90,
  };
  const row = { parody: p, character: c, revision: 4 };
  const intent = globalIntent(c, p, row, "confirmed");
  row.revision = 8;
  p.translation_revision = 100;
  assert.equal(intent.expected, 4);
  assert.equal(intent.parody, "p");
  assert.equal(intent.character, "c");
  assert.equal(intent.parodyLabel, "甲");
  assert.equal(globalIntent(p, c, undefined, "excluded").expected, 0);
  assert.throws(() => globalIntent(p, c, { ...row, revision: NaN }, "auto"));
  assert.throws(() =>
    globalIntent(p, c, { ...row, parody: { ...p, id: "other" } }, "confirmed"),
  );
});
test("edition suppression freezes its own CAS and rejects targetless or globally confirmed new overrides", () => {
  const edition = {
    id: "edition",
    title: "匿名漫画",
    revision: 99,
    organization_revision: 88,
  };
  const row = {
    parody_id: "p",
    character_id: "c",
    parody_name: "p",
    character_name: "c",
    decision: "auto",
    decision_revision: 22,
    suppression_revision: 3,
    suppressed: false,
  };
  const intent = suppressionIntent(edition, row);
  row.suppression_revision = 10;
  assert.equal(intent.expected, 3);
  assert.equal(intent.suppressed, true);
  assert.equal(intent.edition, "edition");
  assert.throws(() => suppressionIntent(edition, { ...row, parody_id: "" }));
  assert.throws(() =>
    suppressionIntent(edition, { ...row, decision: "confirmed" }),
  );
  assert.equal(
    suppressionIntent(edition, {
      ...row,
      decision: "confirmed",
      suppressed: true,
    }).suppressed,
    false,
  );
});

test("Work queries explicitly whitelist endpoint parameters and own page identity", () => {
  const q = tagWorkInput(
    "?category=artist&namespace=artist&include_unused=true&view=display&related_tag=c&partition=evidence&q=catalog&w_q=作品&w_page=2&w_order=desc&snapshot=foreign",
    "library",
  );
  assert.deepEqual(q, {
    library_id: "library",
    q: "作品",
    sort: "name",
    order: "desc",
    page: 2,
    page_size: 24,
  });
  assert.equal(tagWorkInput("?w_page=Infinity", "library").page, 1);
  assert.equal(tagWorkInput("?w_page=1.2", "library").page, 1);
});
test("Work and internal-version routes preserve parent filters, clear only child state, never infer library ownership", () => {
  const parent =
    "?library_id=L&category=artist&tag=T&page=2&q=artist&w_q=story&w_page=3&work=W&images=edit&details=versions&v_page=2&v_q=chapter&book=E";
  const returned = new URL(
    tagWorkspaceURL(parent, { book: null }),
    "http://anonymous",
  ).searchParams;
  assert.equal(returned.get("work"), "W");
  assert.equal(returned.get("v_page"), "2");
  assert.equal(returned.get("w_page"), "3");
  const back = new URL(
    tagWorkspaceURL(parent, { work: null }),
    "http://anonymous",
  ).searchParams;
  for (const key of ["work", "book", "details", "v_page", "v_q"])
    assert.equal(back.has(key), false);
  for (const key of ["tag", "page", "w_page", "q"])
    assert.equal(back.get(key), new URLSearchParams(parent).get(key));
  assert.equal(libraryTarget("/admin/tags", parent), null);
  const switched = new URL(
    switchLibraryURL("/admin/tags", parent, "B"),
    "http://anonymous",
  ).searchParams;
  for (const key of [
    "work",
    "book",
    "tag",
    "details",
    "w_page",
    "w_q",
    "images",
    "v_page",
    "v_q",
  ])
    assert.equal(switched.has(key), false);
});

test("display, editor and related-tag navigation retain independent page state", () => {
  const parent =
    "?tag=P&library_id=L&category=parody&d_q=hero&d_page=2&w_page=3";
  const child = new URL(
    tagWorkspaceURL(parent, {
      category: "character",
      tag: "C",
      parent_tag: "P",
    }),
    "http://anonymous",
  ).searchParams;
  assert.equal(child.get("parent_tag"), "P");
  for (const key of ["d_q", "d_page", "w_page"])
    assert.equal(child.has(key), false);
  const edit = new URL(
    tagWorkspaceURL(
      parent + "&r_page=5&e_page=4&relation_snapshot=r&display_snapshot=d",
      { relations: "edit" },
    ),
    "http://anonymous",
  ).searchParams;
  assert.equal(edit.get("d_page"), "2");
  for (const key of [
    "r_page",
    "e_page",
    "relation_snapshot",
    "display_snapshot",
  ])
    assert.equal(edit.has(key), false);
  const switched = new URL(
    switchLibraryURL(
      "/admin/tags",
      parent + "&relations=edit&parent_tag=A&display_snapshot=d",
      "B",
    ),
    "http://anonymous",
  ).searchParams;
  for (const key of [
    "d_q",
    "d_page",
    "relations",
    "parent_tag",
    "display_snapshot",
  ])
    assert.equal(switched.has(key), false);
  const intent = suppressionIntent(
    { id: "E", title: "raw search title" },
    {
      parody_id: "P",
      character_id: "C",
      decision: "auto",
      suppression_revision: 2,
    },
    "日本語表示",
  );
  assert.equal(intent.editionLabel, "日本語表示");
  assert.equal(intent.editionQuery, "raw search title");
});

test("legacy unused directory URLs normalize before reading and reset only directory page", async () => {
  const { normalizeTagDirectoryURL } =
    await import("../../src/admin/tags/tagCatalogFlow.ts");
  for (const value of ["true", "false", ""]) {
    const q = new URL(
      normalizeTagDirectoryURL(
        `?category=artist&include_unused=${value}&page=2&q=名前&tag=T&w_page=3`,
      ),
      "http://anonymous",
    ).searchParams;
    assert.equal(q.has("include_unused"), false);
    assert.equal(q.has("page"), false);
    assert.equal(q.get("q"), "名前");
    assert.equal(q.get("w_page"), "3");
    assert.equal(
      catalogInput(`?include_unused=${value}`, "L").include_unused,
      undefined,
    );
  }
  assert.equal(normalizeTagDirectoryURL("?category=artist&page=2"), null);
});
