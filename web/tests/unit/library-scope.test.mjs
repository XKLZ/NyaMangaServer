import test from "node:test";
import assert from "node:assert/strict";
import {
  canonicalLibraryURL,
  libraryPolicy,
  libraryScopeChanged,
  libraryTarget,
  libraryURL,
  readLibraryPreference,
  resolveLibrary,
  savedLibraryConditions,
  switchLibraryURL,
  writeLibraryPreference,
} from "../../src/shared/libraryScope.ts";
const libraries = [
  { id: "old", display_name: "A old", disabled: true },
  { id: "enabled", display_name: "B enabled", availability: "offline" },
  { id: "other", display_name: "C other" },
  { id: "trash", display_name: "D trash", trashed_at: 1 },
];
const resolve = (input = {}) =>
  resolveLibrary(libraries, { search: "", policy: "single", ...input });
test("default resolution skips disabled and trash but keeps offline enabled libraries", () => {
  assert.equal(resolve().library.id, "enabled");
  assert.equal(resolve({ remembered: "other" }).library.id, "other");
  assert.equal(
    resolve({ tab: "enabled", remembered: "other" }).library.id,
    "enabled",
  );
  assert.equal(
    resolve({ current: "other", tab: "enabled" }).library.id,
    "other",
  );
  assert.equal(
    resolve({ search: "?library=old", current: "enabled" }).library.id,
    "old",
  );
  assert.equal(
    resolve({ search: "?library_id=enabled", owner: "other" }).library.id,
    "other",
  );
  assert.equal(resolve({ current: "old" }).library.id, "old");
});
test("missing remembered libraries fall back; explicit invalid and recycled libraries fail closed", () => {
  assert.equal(resolve({ remembered: "old" }).fallback, true);
  assert.equal(resolve({ tab: "missing" }).library.id, "enabled");
  assert.match(resolve({ search: "?library_id=missing" }).error, /不存在/);
  assert.match(resolve({ search: "?library_id=trash" }).error, /回收站/);
  assert.equal(
    resolveLibrary([], { search: "", policy: "single" }).library,
    undefined,
  );
  assert.equal(
    resolveLibrary([libraries[0], libraries[3]], {
      search: "",
      policy: "single",
    }).library,
    undefined,
  );
});
test("empty legacy parameters are unspecified, duplicate values normalize, conflicts stay errors", () => {
  assert.equal(libraryURL("?library=&library_id= ").id, "");
  assert.equal(libraryURL("?library=a&library_id=a").error, "");
  assert.match(libraryURL("?library=a&library_id=b").error, /冲突/);
  assert.match(libraryURL("?library_scope=unknown").error, /无效/);
  assert.equal(
    canonicalLibraryURL("/admin/versions", "?library=a&q=word", "a", false),
    "/admin/versions?q=word&library_id=a",
  );
});
test("explicit all is separate from search scope and cannot leak into mandatory single-library pages", () => {
  assert.equal(resolve({ search: "?scope=all" }).all, false);
  assert.equal(
    resolve({ search: "?library_scope=all", policy: "books" }).all,
    true,
  );
  assert.equal(resolve({ search: "?library_scope=all" }).all, false);
  assert.equal(
    resolve({ search: "?library_scope=all", policy: "books", owner: "other" })
      .all,
    true,
  );
  assert.equal(libraryPolicy("/admin/libraries"), "global");
  assert.equal(libraryPolicy("/admin/settings/security"), "global");
  assert.equal(libraryPolicy("/admin/imports"), "history");
});
test("switching clears object and pagination identity while keeping applied generic filters", () => {
  const next = new URL(
    switchLibraryURL(
      "/admin/books",
      "?query=stars&sort=title&scope=favorites&book=old&preview=p&page=4&snapshot_revision=s&include_disabled=true",
      "other",
    ),
    "http://local",
  );
  assert.equal(next.searchParams.get("library_id"), "other");
  assert.equal(next.searchParams.get("query"), "stars");
  assert.equal(next.searchParams.get("scope"), "favorites");
  for (const key of [
    "book",
    "preview",
    "page",
    "snapshot_revision",
    "include_disabled",
  ])
    assert.equal(next.searchParams.has(key), false);
  assert.equal(
    switchLibraryURL("/admin/jobs/old", "?member_page=2", "enabled"),
    "/admin/jobs?library_id=enabled",
  );
  assert.equal(
    switchLibraryURL("/admin/imports", "?batch=old", "enabled"),
    "/admin/imports?library_id=enabled",
  );
});
test("preferences are account-specific, tab-local, and tolerate denied storage", () => {
  const data = new Map(),
    storage = {
      getItem: (key) => data.get(key),
      setItem: (key, value) => data.set(key, value),
    };
  writeLibraryPreference(storage, "one", "enabled");
  writeLibraryPreference(storage, "one", "other", true);
  assert.equal(readLibraryPreference(storage, "one"), "enabled");
  assert.equal(readLibraryPreference(storage, "one", true), "other");
  assert.equal(readLibraryPreference(storage, "two"), "");
  assert.equal(
    readLibraryPreference(
      {
        getItem() {
          throw Error();
        },
      },
      "one",
    ),
    "",
  );
});
test("object ownership is resolved before companion lists, not per list row", () => {
  assert.deepEqual(libraryTarget("/admin/books", "?book=edition"), {
    kind: "book",
    id: "edition",
  });
  assert.deepEqual(libraryTarget("/admin/versions", "?proposal=p"), {
    kind: "work-proposal",
    id: "p",
  });
  assert.deepEqual(libraryTarget("/admin/series", "?series=s"), {
    kind: "series",
    id: "s",
  });
  assert.equal(libraryTarget("/admin/versions", "?library_id=enabled"), null);
});

test("saved format 1 strips UI scope, preference, page and frozen snapshots", () => {
  const stored = savedLibraryConditions({
    query: "anonymous",
    library_id: "",
    library_scope: "all",
    current_library: "private-preference",
    scope: "favorites",
    page: 4,
    page_size: 50,
    snapshot_revision: "stale",
    include_disabled: true,
  });
  assert.deepEqual(stored, {
    query: "anonymous",
    library_id: "",
    scope: "favorites",
    page_size: 50,
    include_disabled: true,
  });
});
test("a live current library becoming unavailable never silently selects another", () => {
  assert.match(resolve({ current: "trash" }).error, /不可用/);
  assert.match(resolve({ current: "deleted" }).error, /不可用/);
});

test("canonical aliases and the same owned object do not discard drafts", () => {
  const route = (search) => ({ pathname: "/admin/series", search });
  assert.equal(
    libraryScopeChanged(route("?library=a"), route("?library_id=a")),
    false,
  );
  assert.equal(
    libraryScopeChanged(
      route("?library_id=a&series=owned"),
      route("?library_id=b&series=owned"),
    ),
    false,
  );
  assert.equal(
    libraryScopeChanged(
      route("?library_id=a&series=owned"),
      route("?library_id=b"),
    ),
    true,
  );
});
