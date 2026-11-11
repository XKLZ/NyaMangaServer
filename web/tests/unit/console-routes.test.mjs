import test from "node:test";
import assert from "node:assert/strict";
import {
  consoleURL,
  maintenanceSection,
  settingsSection,
  taskID,
  taskURL,
  sectionURL,
} from "../../src/shared/consoleRoutes.mjs";
import {
  libraryPolicy,
  libraryTarget,
  switchLibraryURL,
} from "../../src/shared/libraryScope.ts";
const url = (value) => new URL(value, "http://anonymous");
test("legacy metadata preserves explicit conditions and supplies only the former default", () => {
  const old = url(
    consoleURL("/admin/metadata?library_id=l&book=b&scope=favorites"),
  );
  assert.equal(old.pathname, "/admin/books");
  assert.equal(old.searchParams.get("metadata_state"), "none");
  assert.equal(old.searchParams.get("library_id"), "l");
  assert.equal(old.searchParams.get("book"), "b");
  assert.equal(
    url(consoleURL("/admin/metadata?metadata_state=")).searchParams.get(
      "metadata_state",
    ),
    "",
  );
  assert.equal(
    url(consoleURL("/admin/metadata?metadata_state=matched")).searchParams.get(
      "metadata_state",
    ),
    "matched",
  );
});
test("old tasks preserve identity, filters, scope and pagination in the maintenance center", () => {
  const next = url(
    consoleURL(
      "/admin/jobs/j?library_scope=all&type=metadata&state=failed&page=3&member_page=2",
    ),
  );
  assert.equal(next.pathname, "/admin/maintenance");
  assert.equal(taskID(next.pathname, next.search), "j");
  assert.equal(next.searchParams.get("section"), "tasks");
  assert.equal(next.searchParams.get("member_page"), "2");
  const back = url(taskURL("", next.search));
  assert.equal(back.searchParams.get("page"), "3");
  assert.equal(back.searchParams.get("state"), "failed");
  assert.equal(back.searchParams.get("library_scope"), "all");
  assert.equal(back.searchParams.has("task"), false);
  assert.equal(back.searchParams.has("member_page"), false);
});
test("old global maintenance and settings tabs retain their meaning", () => {
  const trash = url(consoleURL("/admin/maintenance?view=trash&page=3"));
  assert.equal(trash.searchParams.get("trash_page"), "3");
  assert.equal(trash.searchParams.has("page"), false);
  assert.equal(
    maintenanceSection(
      url(consoleURL("/admin/maintenance?view=storage")).search,
    ),
    "storage",
  );
  assert.equal(
    maintenanceSection(
      url(consoleURL("/admin/maintenance?view=trash&kind=library")).search,
    ),
    "trash",
  );
  assert.equal(
    settingsSection(
      url(consoleURL("/admin/settings/metadata?view=secrets")).search,
    ),
    "metadata",
  );
  assert.equal(
    settingsSection(
      url(consoleURL("/admin/settings/security?view=translations")).search,
    ),
    "security",
  );
  assert.equal(
    consoleURL("/admin/settings?section=translations"),
    "/admin/settings?section=enhancements&focus=chinese_dictionary",
  );
});
test("only the active maintenance task section is scoped and owns a task", () => {
  assert.equal(libraryPolicy("/admin/maintenance"), "history");
  assert.equal(
    libraryPolicy("/admin/maintenance", "?section=tasks&task=j"),
    "history",
  );
  for (const section of ["trash", "storage"]) {
    const search = `?section=${section}&task=j`;
    assert.equal(libraryPolicy("/admin/maintenance", search), "global");
    assert.equal(libraryTarget("/admin/maintenance", search), null);
  }
  assert.deepEqual(
    libraryTarget("/admin/maintenance", "?section=tasks&task=j"),
    { kind: "job", id: "j" },
  );
  assert.equal(libraryPolicy("/admin/settings", "?section=security"), "global");
});
test("switching libraries leaves task detail and clears snapshots without losing its section", () => {
  const next = url(
    switchLibraryURL(
      "/admin/maintenance",
      "?section=tasks&task=j&page=3&type=metadata&snapshot_revision=s",
      "new",
    ),
  );
  assert.equal(next.searchParams.get("section"), "tasks");
  assert.equal(next.searchParams.get("type"), "metadata");
  assert.equal(next.searchParams.get("library_id"), "new");
  for (const key of ["task", "page", "snapshot_revision"])
    assert.equal(next.searchParams.has(key), false);
});
test("tab changes keep per-section URL state and normalized links are idempotent", () => {
  const current = sectionURL(
    "/admin/maintenance",
    "?section=tasks&type=metadata&page=3&task=j",
    "trash",
  );
  assert.equal(url(current).searchParams.get("page"), "3");
  for (const value of [
    current,
    taskURL("j"),
    "/admin/settings?section=security",
    "/admin/books?metadata_state=none",
  ]) {
    assert.equal(consoleURL(consoleURL(value)), consoleURL(value));
  }
});

test("settings default to overview, legacy sections keep library scope and focus the right group", () => {
  for (const value of ["provider", "credentials", "secrets"]) {
    const url = new URL(
      consoleURL(`/admin/settings?section=${value}&library_id=l`),
      "http://anonymous",
    );
    assert.equal(url.searchParams.get("section"), "metadata");
    assert.equal(url.searchParams.get("library_id"), "l");
  }
  const translated = new URL(
    consoleURL("/settings/metadata?view=translations&library=l"),
    "http://anonymous",
  );
  assert.equal(translated.searchParams.get("section"), "enhancements");
  assert.equal(translated.searchParams.get("focus"), "chinese_dictionary");
  const old = new URL(
    consoleURL("/admin/settings?section=translations&library_id=l"),
    "http://anonymous",
  );
  assert.equal(old.searchParams.get("section"), "enhancements");
  assert.equal(old.searchParams.get("focus"), "chinese_dictionary");
  assert.equal(settingsSection(""), "overview");
  assert.equal(settingsSection("?section=unknown"), "overview");
  assert.equal(
    new URL(
      sectionURL(
        "/admin/settings",
        "?section=enhancements&focus=chinese_dictionary&library_id=l",
        "security",
      ),
      "http://a",
    ).searchParams.has("focus"),
    false,
  );
  assert.equal(
    new URL(
      consoleURL("/settings/security?section=provider"),
      "http://a",
    ).searchParams.get("section"),
    "security",
  );
});
