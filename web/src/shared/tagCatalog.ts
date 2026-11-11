import { request } from "./api";
export type TagCategory =
  "parody" | "character" | "artist" | "group" | "content";
export type TagPartition =
  | "all"
  | "related"
  | "unclassified"
  | "evidence"
  | "inferred"
  | "competing"
  | "suppressed";
export type CatalogTag = {
  followed?: boolean;
  follow_revision?: number;
  images?: import("./tagImages").TagImages;
  id: string;
  namespace: string;
  name: string;
  translation: string;
  translation_origin: string;
  translation_revision: number;
  category: TagCategory;
  placeholder: boolean;
  edition_count: number;
  work_count: number;
};
export type CatalogInput = {
  library_id: string;
  category?: TagCategory;
  namespace?: string;
  q?: string;
  sort?: "name" | "usage";
  order?: "asc" | "desc";
  page?: number;
  page_size?: number;
  snapshot?: string;
  include_unused?: boolean;
  related_tag_id?: string;
  partition?: TagPartition;
  view?: "all" | "display";
};
export type DirectoryInput = CatalogInput & { followed_only?: true };
export type CatalogPage<T> = {
  items: T[];
  total: number;
  page: number;
  page_size: number;
  snapshot: string;
};
export type CatalogDetail = CatalogTag & {
  library_id: string;
  library_disabled: boolean;
  unclassified_edition_count: number;
  snapshot: string;
};
export type TagWork = {
  title_original?: string;
  id: string;
  library_id: string;
  name: string;
  revision: number;
  series: null | {
    id: string;
    name: string;
    position: number;
    revision: number;
  };
  cover: import("./organization").WorkCoverSummary;
};
export type TagWorkPage = CatalogPage<TagWork>;
export type CharacterRelation = {
  parody: CatalogTag;
  character: CatalogTag;
  decision: "auto" | "confirmed" | "excluded";
  revision: number;
  state: string;
  state_label: string;
  evidence_edition_count: number;
  seed_edition_count: number;
  edition_count: number;
  work_count: number;
  inferred_edition_count: number;
  suppressed_edition_count: number;
  competing_edition_count: number;
};
export type EditionRelation = {
  parody_id: string;
  character_id: string;
  parody_name: string;
  parody_translation: string;
  character_name: string;
  character_translation: string;
  state: string;
  state_label: string;
  decision: "auto" | "confirmed" | "excluded";
  decision_revision: number;
  suppressed: boolean;
  suppression_revision: number;
};
export type CatalogEdition = {
  title_original?: string;
  id: string;
  library_id: string;
  work_id: string;
  title: string;
  file_state: string;
  revision: number;
  organization_revision: number;
  relations: EditionRelation[];
};
export type RelationPage = CatalogPage<CharacterRelation> & {
  unclassified_edition_count: number;
};
export type EditionPage = CatalogPage<CatalogEdition> & { work_count: number };
export type RelationDecision = {
  parody_id: string;
  character_id: string;
  decision: "auto" | "confirmed" | "excluded";
  revision: number;
  scope: "global";
};
export type SuppressionDecision = {
  edition_id: string;
  parody_id: string;
  character_id: string;
  suppressed: boolean;
  revision: number;
  scope: "edition_automatic";
};
export function tagCatalogPath(endpoint: string, input: DirectoryInput) {
  if (!input.library_id.trim()) throw Error("请先选择书库");
  const q = new URLSearchParams();
  for (const [key, value] of Object.entries(input))
    if (key !== "followed_only" || endpoint === "/tag-catalog")
      if (value !== undefined && value !== "") q.set(key, String(value));
  return endpoint + `?${q}`;
}
const pairPath = (parody: string, character: string) =>
  `/parody-character-relations/${encodeURIComponent(parody)}/${encodeURIComponent(character)}`;
export const tagCatalog = {
  page: <T>(endpoint: string, input: CatalogInput, signal?: AbortSignal) =>
    request<CatalogPage<T>>(
      tagCatalogPath(endpoint, input),
      "GET",
      undefined,
      false,
      signal,
    ),
  detail: (id: string, library: string, signal?: AbortSignal) =>
    request<CatalogDetail>(
      tagCatalogPath(`/tag-catalog/${encodeURIComponent(id)}`, {
        library_id: library,
      }),
      "GET",
      undefined,
      false,
      signal,
    ),
  relation: (
    parody: string,
    character: string,
    expected: number,
    decision: "auto" | "confirmed" | "excluded",
  ) =>
    request<RelationDecision>(
      pairPath(parody, character),
      decision === "auto" ? "DELETE" : "PUT",
      {
        expected_revision: expected,
        ...(decision === "auto" ? {} : { decision }),
      },
    ),
  suppression: (
    edition: string,
    parody: string,
    character: string,
    expected: number,
    suppressed: boolean,
  ) =>
    request<SuppressionDecision>(
      `/editions/${encodeURIComponent(edition)}/character-suppressions/${encodeURIComponent(parody)}/${encodeURIComponent(character)}`,
      suppressed ? "PUT" : "DELETE",
      { expected_revision: expected },
    ),
  translate: (id: string, expected: number, text: string | null) =>
    request(
      `/tags/${encodeURIComponent(id)}/translations/zh-CN`,
      text === null ? "DELETE" : "PUT",
      { expected_revision: expected, ...(text === null ? {} : { text }) },
    ),
};
