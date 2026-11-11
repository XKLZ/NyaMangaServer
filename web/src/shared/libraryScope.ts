import type { Library } from "./api";
import { maintenanceSection, taskID } from "./consoleRoutes.mjs";
export type LibraryPolicy = "books" | "single" | "history" | "global";
export function libraryPolicy(path: string, search = ""): LibraryPolicy {
  if (["/admin/books", "/admin/metadata"].includes(path)) return "books";
  if (["/admin/versions", "/admin/series", "/admin/tags"].includes(path))
    return "single";
  if (
    path.startsWith("/admin/jobs") ||
    path === "/admin/imports" ||
    (path === "/admin/maintenance" && maintenanceSection(search) === "tasks")
  )
    return "history";
  return "global";
}
export function libraryURL(search: string) {
  const params = new URLSearchParams(search);
  const id = params.get("library_id")?.trim() || "";
  const old = params.get("library")?.trim() || "";
  const scope = params.get("library_scope")?.trim() || "";
  return {
    id: id || old,
    all: scope === "all",
    error:
      id && old && id !== old
        ? "链接中的书库参数冲突，请重新选择书库。"
        : scope && scope !== "all"
          ? "书库范围参数无效。"
          : "",
  };
}
export function enabledLibrary(library: Library) {
  return !library.disabled && !library.trashed_at;
}
export function resolveLibrary(
  libraries: Library[],
  input: {
    search: string;
    policy: LibraryPolicy;
    owner?: string;
    current?: string;
    tab?: string;
    remembered?: string;
  },
) {
  const url = libraryURL(input.search);
  const all =
    url.all &&
    ["books", "history"].includes(input.policy) &&
    (!input.owner || input.policy === "books");
  if (url.error) return { all, error: url.error };
  const explicit = input.owner || url.id;
  const selected = explicit
    ? libraries.find((l) => l.id === explicit)
    : undefined;
  if (explicit && !selected)
    return { all, error: "链接指定的书库不存在，请选择其他书库。" };
  if (selected?.trashed_at)
    return {
      all,
      library: selected,
      error: "当前书库已在回收站，请恢复书库或选择其他书库。",
    };
  if (!explicit && input.current) {
    const existing = libraries.find((l) => l.id === input.current);
    if (!existing || existing.trashed_at)
      return {
        all,
        library: existing,
        error: "当前书库已不可用，请恢复书库或选择其他书库。",
      };
  }
  const current = libraries.find(
    (l) => l.id === input.current && !l.trashed_at,
  );
  const remembered = [input.tab, input.remembered]
    .map((id) => libraries.find((l) => l.id === id && enabledLibrary(l)))
    .find(Boolean);
  const library =
    selected || current || remembered || libraries.find(enabledLibrary);
  const fallback =
    !explicit && !current && !!(input.tab || input.remembered) && !remembered;
  return { all, library, error: "", fallback };
}
export function canonicalLibraryURL(
  path: string,
  search: string,
  id: string | undefined,
  all: boolean,
) {
  const params = new URLSearchParams(search);
  params.delete("library");
  params.delete("library_id");
  params.delete("library_scope");
  if (all) params.set("library_scope", "all");
  else if (id) params.set("library_id", id);
  if (!all) params.delete("include_disabled");
  return path + (params.size ? `?${params}` : "");
}
export function switchLibraryURL(
  path: string,
  search: string,
  id: string | undefined,
  all = false,
) {
  if (path.startsWith("/admin/jobs/")) path = "/admin/jobs";
  const params = new URLSearchParams(search);
  for (const key of [
    "book",
    "preview",
    "work",
    "series",
    "proposal",
    "job",
    "task",
    "batch",
    "page",
    "batches_page",
    "member_page",
    "candidate_page",
    "snapshot_revision",
    "parent_tag",
    "relations",
    "images",
    "d_page",
    "d_q",
    "display_snapshot",
    "details",
    "w_page",
    "w_q",
    "w_order",
    "v_page",
    "v_q",
    "work_snapshot",
    "tag",
    "related_tag",
    "partition",
    "r_page",
    "r_q",
    "e_q",
    "e_page",
    "catalog_snapshot",
    "relation_snapshot",
    "edition_snapshot",
    "snapshot",
    "include_disabled",
  ])
    params.delete(key);
  return canonicalLibraryURL(path, params.toString(), id, all);
}
export function readLibraryPreference(
  storage: Pick<Storage, "getItem">,
  account: string,
  tab = false,
) {
  try {
    return (
      storage.getItem(
        `nyamanga.ui.current-library.${tab ? "tab" : "account"}.v1:${account}`,
      ) || ""
    );
  } catch {
    return "";
  }
}
export function writeLibraryPreference(
  storage: Pick<Storage, "setItem">,
  account: string,
  id: string,
  tab = false,
) {
  try {
    storage.setItem(
      `nyamanga.ui.current-library.${tab ? "tab" : "account"}.v1:${account}`,
      id,
    );
  } catch {
    /* Memory and URL remain usable. */
  }
}
export type LibraryTarget = {
  kind:
    | "book"
    | "work"
    | "series"
    | "work-proposal"
    | "series-proposal"
    | "batch"
    | "job";
  id: string;
};
export function libraryTarget(
  path: string,
  search: string,
): LibraryTarget | null {
  if (path === "/admin/tags") return null;
  const params = new URLSearchParams(search);
  const task = taskID(path, search);
  const book = params.get("book");
  if (book) return { kind: "book", id: book };
  if (path === "/admin/imports" && params.get("batch"))
    return { kind: "batch", id: params.get("batch")! };
  if (task) return { kind: "job", id: task };
  if (path === "/admin/versions") {
    if (params.get("proposal"))
      return { kind: "work-proposal", id: params.get("proposal")! };
    if (params.get("work")) return { kind: "work", id: params.get("work")! };
  }
  if (path === "/admin/series") {
    if (params.get("proposal"))
      return { kind: "series-proposal", id: params.get("proposal")! };
    if (params.get("series"))
      return { kind: "series", id: params.get("series")! };
  }
  if (["/admin/versions", "/admin/series"].includes(path) && params.get("job"))
    return { kind: "job", id: params.get("job")! };
  return null;
}

/** Saved format 1 has no UI scope, account preference, page or snapshot. */
export function savedLibraryConditions(value: Record<string, unknown>) {
  return Object.fromEntries(
    [
      "query",
      "library_id",
      "file_state",
      "scope",
      "collapse",
      "sort",
      "order",
      "page_size",
      "properties",
      "include_disabled",
      "metadata_state",
    ]
      .filter((key) => value[key] !== undefined)
      .map((key) => [key, value[key]]),
  );
}

export function libraryScopeChanged(
  current: { pathname: string; search: string },
  next: { pathname: string; search: string },
) {
  const before = libraryTarget(current.pathname, current.search),
    after = libraryTarget(next.pathname, next.search);
  if (before && after && before.kind === after.kind && before.id === after.id)
    return false;
  const a = libraryURL(current.search),
    b = libraryURL(next.search);
  return a.id !== b.id || a.all !== b.all;
}
