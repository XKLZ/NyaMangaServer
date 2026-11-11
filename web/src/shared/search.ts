import { savedLibraryConditions } from "./libraryScope";
import { request } from "./api";
import {
  propertiesFromURL,
  savedConditions,
  writeSearchParams,
} from "./propertyFilters";
import type { PropertyFilter } from "./propertyFilters";
import type { Book } from "./api";
import type { UserState, OrgBook } from "./organization";
export type Conditions = {
  properties?: PropertyFilter;
  include_disabled?: boolean;
  metadata_state?: string;
  query: string;
  library_id: string;
  file_state: string;
  scope: string;
  collapse: string;
  sort: string;
  order: string;
  page: number;
  page_size: number;
  snapshot_revision?: string;
};
export type Hit = {
  kind: "edition" | "work" | "series";
  id: string;
  name: string;
  representative: Book;
  match_count: number;
  match_ids: string[];
  other_version_matches: boolean;
  favorite_state: string;
  user_state: UserState;
};
export type Results = {
  items: Hit[];
  total: number;
  page: number;
  page_size: number;
  snapshot_revision: string;
};
export type Matches = {
  items: OrgBook[];
  total: number;
  page: number;
  page_size: number;
  snapshot_revision: string;
};
export type Saved = {
  id: string;
  name: string;
  query_version: number;
  conditions: Conditions;
  revision: number;
};
export const defaults: Conditions = {
  query: "",
  library_id: "",
  file_state: "",
  scope: "all",
  collapse: "series",
  sort: "indexed_at",
  order: "desc",
  page: 1,
  page_size: 24,
};
export function fromURL(location: string): Conditions {
  const q = new URL(location, window.location.origin).searchParams;
  const v = { ...defaults };
  for (const key of [
    "query",
    "library_id",
    "file_state",
    "scope",
    "collapse",
    "sort",
    "order",
  ] as const) {
    if (q.has(key)) v[key] = q.get(key)!;
  }
  if (v.collapse === "versions") v.collapse = "works";
  v.properties = propertiesFromURL(q);
  if (q.has("include_disabled"))
    v.include_disabled = q.get("include_disabled") === "true";
  if (q.has("metadata_state")) v.metadata_state = q.get("metadata_state")!;
  v.page = Number(q.get("page") || 1);
  v.page_size = Number(q.get("page_size") || 24);
  return v;
}
export function toURL(v: Conditions) {
  const q = new URLSearchParams();
  for (const key of Object.keys(defaults) as (keyof typeof defaults)[]) {
    if (v[key] !== defaults[key]) q.set(key, String(v[key]));
  }
  writeSearchParams(q, {
    properties: v.properties,
    include_disabled: v.include_disabled,
    metadata_state: v.metadata_state,
  });
  return "/" + (q.size ? `?${q}` : "");
}
export const search = {
  run: (v: Conditions) => request<Results>("/books/search", "POST", v),
  matches: (hit: Hit, v: Conditions, page: number, snapshot: string) => {
    const q = new URLSearchParams();
    writeSearchParams(q, { ...v, page, snapshot_revision: snapshot });
    return request<Matches>(
      `/search-results/${hit.kind}/${hit.id}/matches?${q}`,
    );
  },
  saved: () => request<{ items: Saved[] }>("/saved-searches"),
  save: (name: string, conditions: Conditions, saved?: Saved) =>
    request<Saved>(
      saved ? `/saved-searches/${saved.id}` : "/saved-searches",
      saved ? "PATCH" : "POST",
      {
        name,
        conditions: savedLibraryConditions(savedConditions(conditions)),
        query_version: 1,
        expected_revision: saved?.revision ?? 0,
      },
    ),
  remove: (saved: Saved) =>
    request<void>(`/saved-searches/${saved.id}`, "DELETE", {
      expected_revision: saved.revision,
    }),
};
