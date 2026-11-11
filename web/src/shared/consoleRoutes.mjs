import { seriesWorkspaceURL } from "../admin/series/seriesWorkspaceURL.ts";
export function maintenanceSection(search) {
  const q = new URLSearchParams(search),
    value = q.get("section") || q.get("view");
  return ["trash", "storage", "health", "recovery"].includes(value)
    ? value
    : "tasks";
}
export function settingsSection(search) {
  const q = new URLSearchParams(search),
    value = q.get("section") || q.get("view");
  if (["provider", "credentials", "secrets", "metadata"].includes(value))
    return "metadata";
  if (["translations", "enhancements"].includes(value)) return "enhancements";
  return value === "security" ? "security" : "overview";
}
export function taskID(path, search) {
  if (path.startsWith("/admin/jobs/"))
    return decodeURIComponent(path.split("/")[3]);
  return path === "/admin/maintenance" && maintenanceSection(search) === "tasks"
    ? new URLSearchParams(search).get("task") || ""
    : "";
}
export function taskURL(id = "", search = "") {
  const q = new URLSearchParams(search);
  q.delete("view");
  q.set("section", "tasks");
  q.delete("member_page");
  if (id) q.set("task", id);
  else q.delete("task");
  return `/admin/maintenance?${q}`;
}
export function sectionURL(path, search, section) {
  const q = new URLSearchParams(search);
  q.delete("view");
  q.set("section", section);
  if (path === "/admin/settings") q.delete("focus");
  return `${path}?${q}`;
}
export function consoleURL(value, origin = "http://localhost") {
  const url = new URL(value, origin);
  const path = url.pathname;
  if (path === "/" || path === "/admin" || path === "/admin/")
    url.pathname = "/admin/books";
  else if (
    /^\/(?:books|editions)\/[^/]+$/.test(path) ||
    /^\/admin\/editions\/[^/]+$/.test(path)
  ) {
    url.pathname = "/admin/books";
    url.searchParams.set("book", path.split("/").at(-1));
  } else if (
    path.startsWith("/version-groups") ||
    path.startsWith("/admin/version-groups")
  ) {
    url.pathname = "/admin/works";
    url.searchParams.set("model_migrated", "1");
  } else if (path.startsWith("/settings/libraries"))
    url.pathname = "/admin/libraries";
  else if (
    path.startsWith("/settings/") ||
    path.startsWith("/jobs") ||
    path.startsWith("/series") ||
    path.startsWith("/works")
  )
    url.pathname = `/admin${path}`;
  if (
    url.pathname === "/admin/recommendations" ||
    url.pathname.startsWith("/admin/works")
  ) {
    if (url.pathname === "/admin/recommendations") {
      const state = url.searchParams.get("state");
      url.searchParams.set(
        "tab",
        state && state !== "pending" ? "history" : "new",
      );
      if (!url.searchParams.has("page") && url.searchParams.has("create_page"))
        url.searchParams.set("page", url.searchParams.get("create_page"));
    } else if (url.pathname.split("/")[3])
      url.searchParams.set("work", url.pathname.split("/")[3]);
    if (url.searchParams.has("query") && !url.searchParams.has("q"))
      url.searchParams.set("q", url.searchParams.get("query"));
    url.pathname = "/admin/versions";
  }
  const normalized = new URL(
    seriesWorkspaceURL(url.pathname + url.search, origin),
    origin,
  );
  if (normalized.pathname === "/admin/metadata") {
    normalized.pathname = "/admin/books";
    if (!normalized.searchParams.has("metadata_state"))
      normalized.searchParams.set("metadata_state", "none");
  }
  if (
    normalized.pathname === "/admin/jobs" ||
    normalized.pathname.startsWith("/admin/jobs/")
  ) {
    const id = normalized.pathname.split("/")[3];
    normalized.pathname = "/admin/maintenance";
    normalized.searchParams.set("section", "tasks");
    normalized.searchParams.delete("view");
    if (id) normalized.searchParams.set("task", decodeURIComponent(id));
  } else if (normalized.pathname === "/admin/maintenance") {
    if (
      normalized.searchParams.get("view") === "trash" &&
      normalized.searchParams.has("page") &&
      !normalized.searchParams.has("trash_page")
    ) {
      normalized.searchParams.set(
        "trash_page",
        normalized.searchParams.get("page"),
      );
      normalized.searchParams.delete("page");
    }
    normalized.searchParams.set(
      "section",
      maintenanceSection(normalized.search),
    );
    normalized.searchParams.delete("view");
  } else if (normalized.pathname.startsWith("/admin/settings")) {
    const legacy =
      normalized.searchParams.get("section") ||
      normalized.searchParams.get("view");
    const section =
      normalized.pathname === "/admin/settings/security"
        ? "security"
        : !legacy && normalized.pathname === "/admin/settings/metadata"
          ? "metadata"
          : settingsSection(normalized.search);
    if (!normalized.searchParams.has("focus")) {
      if (section === "enhancements" && legacy === "translations")
        normalized.searchParams.set("focus", "chinese_dictionary");
      else if (
        section === "metadata" &&
        (legacy === "credentials" || legacy === "secrets")
      )
        normalized.searchParams.set("focus", "credentials");
      else if (
        section === "metadata" &&
        (legacy === "provider" ||
          normalized.pathname === "/admin/settings/metadata")
      )
        normalized.searchParams.set("focus", "provider");
    }
    normalized.pathname = "/admin/settings";
    normalized.searchParams.set("section", section);
    normalized.searchParams.delete("view");
  }
  return normalized.pathname + normalized.search;
}
