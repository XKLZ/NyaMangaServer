import { request } from "./api";
import type { Book, Job } from "./api";
export type TagName = { namespace: string; name: string };
export type Fields = {
  title: string | null;
  title_original: string | null;
  category: string | null;
  language: string | null;
  rating: number | null;
  published_at: string | null;
  description: string | null;
  tags: TagName[];
};
export type Field = keyof Fields;
export type PropertyField =
  "languages" | "translated" | "color_mode" | "censorship" | "extraneous_ads";
export type EditableField = Exclude<Field, "language"> | PropertyField;
export type PropertyValue<T> = {
  value: T | null;
  origin: "none" | "tags" | "title" | "source" | "manual" | "legacy_manual";
  state: "normal" | "unknown" | "conflict";
  evidence: string[];
};
export type Property<T> = PropertyValue<T> & { automatic: PropertyValue<T> };
export type BookProperties = {
  rule_version: 1;
  languages: Property<string[]>;
  translated: Property<boolean>;
  color_mode: Property<"monochrome" | "color">;
  censorship: Property<"uncensored" | "mosaic" | "full">;
  extraneous_ads: Property<boolean>;
};
export type TagSuggestion = TagName & {
  followed?: boolean;
  follow_revision?: number;
  translation: string;
  translation_origin: string;
  usage_count: number;
  local_exists: boolean;
  tag_id: string | null;
  dictionary_match: boolean | null;
};
export type TagSuggestions = {
  items: TagSuggestion[];
  total: number;
  page: number;
  page_size: number;
  snapshot_revision: string;
  dictionary_state: "ready" | "unavailable" | "not_downloaded" | "disabled";
};
export type TagSuggestionInput = {
  namespace: string;
  q: string;
  exclude: TagName[];
  page: number;
  page_size: number;
  snapshot_revision?: string;
};
export type Override = { op: "set" | "empty" | "inherit"; value?: unknown };
export type RemoteFileInfo = {
  remote_page_count?: number | null;
  remote_size_bytes?: number | null;
};
export type MetadataDetails = {
  tags: Tag[];
  properties?: BookProperties;
  fields: Fields & RemoteFileInfo;
  overrides: Partial<Record<Field | PropertyField, Override>>;
  state: "none" | "matched" | "needs_review";
  selection_revision: number;
  manual_url: string;
  source: {
    id: string;
    provider: string;
    external_id: string;
    url: string;
    match_method: string;
    matched_content_revision: number;
    fetched_at: number;
    fields: Fields & RemoteFileInfo;
  } | null;
};
export type Tag = TagName & {
  id: string;
  translation: string;
  translation_origin: string;
  revision: number;
  dictionary_match?: boolean | null;
};
export type ProviderSettings = {
  revision: number;
  site: "eh" | "ex";
  proxy_url: string;
  configured: Record<string, boolean>;
};
export type SecretChange = { op: "keep" | "clear" | "set"; value?: string };
export type TranslationSettings = {
  active: {
    version: string;
    source: string;
    sha256: string;
    activated_at: number;
  } | null;
  source: string;
  locale: string;
};
export const metadata = {
  suggestions: (input: TagSuggestionInput, signal?: AbortSignal) =>
    request<TagSuggestions>("/tag-suggestions", "POST", input, false, signal),
  edit: (
    book: string,
    expected_revision: number,
    fields: Partial<Record<EditableField, Override>>,
  ) =>
    request<Book>(`/books/${book}/overrides`, "PATCH", {
      expected_revision,
      fields,
    }),
  fetch: (
    book_ids: string[],
    gallery_urls: Record<string, string> = {},
    force_refresh = false,
  ) =>
    request<{ job_id: string }>("/metadata-jobs", "POST", {
      book_ids,
      gallery_urls,
      force_refresh,
    }),
  control: (id: string, action: "pause" | "resume") =>
    request<Job>(`/jobs/${id}/${action}`, "POST"),
  retry: (
    id: string,
    ordinals: number[],
    gallery_urls: Record<string, string> = {},
  ) =>
    request<{ job_id: string }>(`/jobs/${id}/retry`, "POST", {
      ordinals,
      gallery_urls,
    }),
  settings: () => request<ProviderSettings>("/settings/metadata-provider"),
  configure: (expected_revision: number, site: string, proxy_url: string) =>
    request<ProviderSettings>("/settings/metadata-provider", "PATCH", {
      expected_revision,
      site,
      proxy_url,
    }),
  credentials: (
    expected_revision: number,
    fields: Record<string, SecretChange>,
  ) =>
    request<ProviderSettings>("/settings/provider-credentials", "PUT", {
      expected_revision,
      fields,
    }),
  test: () =>
    request<{ reachable: boolean }>("/settings/metadata-provider/test", "POST"),
  translations: () =>
    request<TranslationSettings>("/settings/tag-translations"),
  updateTranslations: () =>
    request<{ job_id: string }>("/tag-translation-jobs", "POST"),
  tags: (q = "") =>
    request<{ items: Tag[] }>(
      `/tags?${new URLSearchParams({ q, locale: "zh-CN" })}`,
    ),
  translate: (tag: Tag, text: string | null) =>
    request(
      `/tags/${tag.id}/translations/zh-CN`,
      text === null ? "DELETE" : "PUT",
      { expected_revision: tag.revision, ...(text === null ? {} : { text }) },
    ),
};
