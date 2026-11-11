import { request } from "./api";
import type { Fields, Tag } from "./metadata";
export type SearchMode = "matches" | "title";
export type SourceSearch = {
  id: string;
  batch_id: string;
  edition_id: string;
  mode: SearchMode;
  q: string;
  site: string;
  revision: number;
  job_id: string;
  state: string;
  error_code: string;
  active_page: number;
  loaded_count: number;
  continuation: "unknown" | "more" | "end" | "limited";
  content_revision: number;
  selection_revision: number;
  edition_revision: number;
  member_revision: number;
};
export type SourceResult = {
  id: string;
  url: string;
  title: string;
  position: number;
  revision: number;
  state: "pending" | "ready" | "failed";
  error_code: string;
  fields?: Fields;
  tags: Tag[];
  page_count: number | null;
};
export type SourcePage = {
  search: SourceSearch;
  items: SourceResult[];
  page: number;
  page_size: number;
};
export function sourceSearchAPI(batch: string, member: string) {
  const base = `/import-batches/${encodeURIComponent(batch)}/members/${encodeURIComponent(member)}/source-searches`;
  const path = (id: string) => `${base}/${encodeURIComponent(id)}`;
  return {
    create: (mode: SearchMode, q: string, revision: number, key: string) =>
      request<SourceSearch>(base, "POST", {
        mode,
        q,
        expected_revision: revision,
        request_key: key,
      }),
    get: (id: string, signal?: AbortSignal) =>
      request<SourceSearch>(path(id), "GET", undefined, false, signal),
    page: (id: string, page: number, signal?: AbortSignal) =>
      request<SourcePage>(
        `${path(id)}/results?page=${page}&page_size=10`,
        "GET",
        undefined,
        false,
        signal,
      ),
    result: (id: string, result: string, signal?: AbortSignal) =>
      request<SourceResult>(
        `${path(id)}/results/${encodeURIComponent(result)}`,
        "GET",
        undefined,
        false,
        signal,
      ),
    load: (s: SourceSearch, page: number, key: string) =>
      request<SourceSearch>(`${path(s.id)}/load-page`, "POST", {
        page,
        expected_revision: s.revision,
        request_key: key,
      }),
    control: (s: SourceSearch, action: string) =>
      request<SourceSearch>(`${path(s.id)}/control/${action}`, "POST", {
        expected_revision: s.revision,
      }),
    select: (
      s: SourceSearch,
      r: SourceResult,
      versions: { member: number; edition: number },
    ) =>
      request<{ selected: boolean }>(`${path(s.id)}/select`, "POST", {
        result_id: r.id,
        expected_revision: s.revision,
        result_revision: r.revision,
        member_revision: versions.member,
        edition_revision: versions.edition,
      }),
    cover: (s: SourceSearch, r: SourceResult) =>
      `/api/v1${path(s.id)}/results/${encodeURIComponent(r.id)}/cover?result_revision=${r.revision}`,
  };
}
