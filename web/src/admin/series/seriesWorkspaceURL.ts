export function seriesWorkspaceURL(value: string, origin = "http://localhost") {
  const url = new URL(value, origin);
  if (url.pathname === "/admin/series-recommendations") {
    const oldTab = url.searchParams.get("tab");
    const history = url.searchParams.get("state");
    if (oldTab === "pending") {
      url.searchParams.set("panel", "pending");
      url.searchParams.set("tab", "new");
    } else if (oldTab === "add_to_series") {
      url.searchParams.set("tab", "history");
      url.searchParams.set("kind", "add_to_series");
    } else
      url.searchParams.set(
        "tab",
        history && history !== "pending" ? "history" : "new",
      );
    url.pathname = "/admin/series";
  } else if (/^\/admin\/series\/[^/]+$/.test(url.pathname)) {
    url.searchParams.set(
      "series",
      decodeURIComponent(url.pathname.split("/").at(-1)!),
    );
    url.pathname = "/admin/series";
  }
  if (url.pathname === "/admin/series") {
    if (url.searchParams.has("query") && !url.searchParams.has("q"))
      url.searchParams.set("q", url.searchParams.get("query")!);
  }
  return url.pathname + url.search;
}
