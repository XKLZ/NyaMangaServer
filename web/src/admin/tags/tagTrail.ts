/** Serializable browsing state: only one node per tag, in this library. URLs
 * keep the directory's category and filters; nodes own only their detail state. */
export type TrailNode = { id: string; search: string };
export type TagTrail = {
  version: 1;
  library: string;
  key: string;
  directory: string;
  nodes: TrailNode[];
};
const detailKeys = [
  "tag",
  "parent_tag",
  "images",
  "relations",
  "d_q",
  "d_page",
  "d_sort",
  "d_order",
  "display_snapshot",
  "work",
  "book",
  "preview",
  "details",
  "w_q",
  "w_page",
  "w_order",
  "v_q",
  "v_page",
  "related_tag",
  "partition",
  "r_page",
  "r_q",
  "r_sort",
  "r_order",
  "e_page",
  "e_q",
  "relation_snapshot",
  "edition_snapshot",
];
const origin = "http://tag-workspace.invalid";
function params(value: string): URLSearchParams | null {
  if (!(value.startsWith("?") || value.startsWith("/admin/tags?"))) return null;
  try {
    const url = new URL(
      value.startsWith("?") ? `/admin/tags${value}` : value,
      origin,
    );
    return url.origin === origin && url.pathname === "/admin/tags"
      ? url.searchParams
      : null;
  } catch {
    return null;
  }
}
const query = (q: URLSearchParams) => `?${q}`;
export const tagPath = (search: string) => `/admin/tags${search}`;
export function tagDirectorySearch(search: string) {
  const q = params(search) || new URLSearchParams();
  for (const key of detailKeys) q.delete(key);
  return query(q);
}
export function tagDetailSearch(directory: string, id: string) {
  const q = new URLSearchParams(tagDirectorySearch(directory));
  const library = q.get("library_id");
  q.delete("library_id");
  q.set("tag", id);
  if (library) q.set("library_id", library);
  return query(q);
}
export function readTagTrail(
  state: unknown,
  library: string,
  id: string,
): TagTrail | null {
  const value = (state as { tagTrail?: TagTrail } | null)?.tagTrail;
  if (
    !value ||
    value.version !== 1 ||
    value.library !== library ||
    typeof value.key !== "string" ||
    !value.key ||
    !Array.isArray(value.nodes) ||
    !value.nodes.length
  )
    return null;
  const directory =
    typeof value.directory === "string" ? params(value.directory) : null;
  if (
    !directory ||
    directory.get("library_id") !== library ||
    directory.has("tag")
  )
    return null;
  const ids = new Set<string>();
  for (const node of value.nodes) {
    if (
      !node ||
      typeof node.id !== "string" ||
      !node.id ||
      typeof node.search !== "string" ||
      ids.has(node.id)
    )
      return null;
    const q = params(node.search);
    if (
      !q ||
      q.get("library_id") !== library ||
      q.get("tag") !== node.id ||
      q.has("parent_tag")
    )
      return null;
    ids.add(node.id);
  }
  return value.nodes.at(-1)?.id === id ? value : null;
}
export function currentTrail(trail: TagTrail, search: string): TagTrail {
  return {
    ...trail,
    nodes: trail.nodes.map((node, i) =>
      i === trail.nodes.length - 1 ? { ...node, search } : node,
    ),
  };
}
export function startTagTrail(
  search: string,
  library: string,
  id: string,
  key: string,
): TagTrail {
  const directory = tagDirectorySearch(search),
    root = tagDetailSearch(directory, id);
  return { version: 1, library, key, directory, nodes: [{ id, search: root }] };
}
export function resolveTagTrail(
  search: string,
  state: unknown,
  library: string,
  key: string,
): TagTrail | null {
  const q = new URLSearchParams(search),
    id = q.get("tag") || "";
  if (!id) return null;
  const valid = readTagTrail(state, library, id);
  if (valid) return currentTrail(valid, search);
  let trail = startTagTrail(search, library, id, key);
  const parent = q.get("parent_tag"),
    legacy = (state as { tagReturn?: unknown } | null)?.tagReturn;
  const old = typeof legacy === "string" ? params(legacy) : null;
  // Only a single verified-scope legacy entry is imported. Never recurse through
  // tagReturn or accept self/cyclic/foreign parents. URL-only deep links are roots.
  if (
    parent &&
    parent !== id &&
    old?.get("library_id") === library &&
    old.get("tag") === parent &&
    !old.has("parent_tag")
  ) {
    const directory = tagDirectorySearch(query(old));
    trail = {
      ...trail,
      directory,
      nodes: [
        { id: parent, search: query(old) },
        { id, search: tagDetailSearch(directory, id) },
      ],
    };
  }
  return trail;
}
export function visitTag(
  trail: TagTrail,
  search: string,
  id: string,
): TagTrail {
  const saved = currentTrail(trail, search),
    index = saved.nodes.findIndex((node) => node.id === id);
  return index >= 0
    ? { ...saved, nodes: saved.nodes.slice(0, index + 1) }
    : {
        ...saved,
        nodes: [
          ...saved.nodes,
          { id, search: tagDetailSearch(saved.directory, id) },
        ],
      };
}
export function backTag(trail: TagTrail): TagTrail | null {
  return trail.nodes.length > 1
    ? { ...trail, nodes: trail.nodes.slice(0, -1) }
    : null;
}
export const trailState = (trail: TagTrail | null) =>
  trail ? { tagTrail: trail } : null;
export function preservedTagIDs(
  current: { search: string; state?: unknown },
  next: { search: string; state?: unknown },
): string[] | false {
  const a = new URLSearchParams(current.search),
    b = new URLSearchParams(next.search),
    library = a.get("library_id") || "";
  const from = readTagTrail(current.state, library, a.get("tag") || ""),
    to = readTagTrail(next.state, library, b.get("tag") || "");
  if (from && to && from.key === to.key) return to.nodes.map((node) => node.id);
  return a.get("tag") && a.get("tag") === b.get("tag")
    ? [a.get("tag")!]
    : false;
}
export function dropsTagSessions(
  current: { search: string; state?: unknown },
  next: { search: string; state?: unknown },
) {
  const a = new URLSearchParams(current.search),
    b = new URLSearchParams(next.search),
    library = a.get("library_id") || "";
  const from = readTagTrail(current.state, library, a.get("tag") || ""),
    to = readTagTrail(next.state, library, b.get("tag") || "");
  return (
    !!from &&
    (!to ||
      from.nodes.some((node) => !to.nodes.some((kept) => kept.id === node.id)))
  );
}
export function normalizeTagDetailSearch(
  search: string,
  namespace?: string,
): string {
  const q = new URLSearchParams(search);
  q.delete("parent_tag");
  q.delete("images");
  if (namespace === "character" && q.get("relations") !== "edit")
    for (const key of [
      "d_q",
      "d_page",
      "d_sort",
      "d_order",
      "display_snapshot",
    ])
      q.delete(key);
  return query(q);
}
