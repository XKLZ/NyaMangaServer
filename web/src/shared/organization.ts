import { request } from "./api";
import type { Book, Listing } from "./api";
export type UserState = {
  favorite: boolean;
  hidden: boolean;
  revision: number;
};
export type VersionLabelState = {
  mode: "legacy" | "auto" | "manual";
  revision: number;
  rule_version: string;
  needs_review: boolean;
  items: {
    label: string;
    origin: "manual" | "automatic";
    preset_id?: string;
  }[];
};
export type Work = {
  title?: string;
  title_original?: string;
  cover_revision: number;
  content_revision: number;
  id: string;
  library_id: string;
  name: string;
  name_override: string | null;
  preferred_edition_id: string;
  revision: number;
  edition_count: number;
  series_id?: string;
  position?: number;
  edition_ids?: string[];
};
export type EditionMember = {
  id: string;
  work_id: string;
  library_id: string;
  title: string;
  content_revision: number;
  organization_revision: number;
  version_labels: string[];
  user_state: UserState;
  title_original?: string;
  languages?: string[] | null;
  cover_revision?: number;
  preferred?: boolean;
  label_state?: VersionLabelState;
};
export type OrgBook = {
  title_original?: string;
  book_id: string;
  work_id: string;
  library_id: string;
  title: string;
  content_revision: number;
  user_state: UserState;
};
export type WorkCoverSummary = {
  work_id: string;
  edition_id: string;
  content_revision: number;
  cover_revision: number;
};
export type Series = {
  cover?: WorkCoverSummary | null;
  id: string;
  library_id: string;
  name: string;
  revision: number;
  work_ids: string[];
  work_names?: Record<string, string>;
  member_count: number;
};
export type OrgContext = {
  works: Work[];
  editions: EditionMember[];
  series: Series[];
  expected_revisions: Record<string, number>;
  snapshot_revision: string;
};
export type Command = {
  action: string;
  id?: string;
  library_id?: string;
  target_work_id?: string;
  target_series_id?: string;
  new_work_id?: string;
  new_work_ids?: Record<string, string>;
  edition_id?: string;
  edition_ids?: string[];
  work_ids?: string[] | null;
  preferred_edition_id?: string;
  remaining_preferred_edition_id?: string;
  keep_series?: boolean;
  position?: number;
  name?: string;
  name_op?: string;
  version_labels?: string[] | null;
  value?: boolean;
  expected_revisions: Record<string, number>;
  snapshot_revision?: string;
};
export type OrgResult = {
  id: string;
  context: OrgContext;
  command: Command;
  preview: boolean;
  effects: {
    edition_id?: string;
    work_id?: string;
    from_work_id?: string;
    to_work_id?: string;
    from_series_id?: string;
    to_series_id?: string;
    removed_work?: boolean;
  }[];
};
export type Members<T> = Listing<T> & {
  expected_revisions: Record<string, number>;
};
export const organization = {
  context: (
    editions: string[] = [],
    works: string[] = [],
    series: string[] = [],
  ) =>
    request<OrgContext>(
      `/work-organization/context?${new URLSearchParams({ edition_ids: editions.join(","), work_ids: works.join(","), series_ids: series.join(",") })}`,
    ),
  preview: (command: Command) =>
    request<OrgResult>("/work-organization/preview", "POST", command),
  change: (c: Command) => {
    let path = "",
      method = "POST";
    switch (c.action) {
      case "work_name":
        path = `/works/${c.id}`;
        method = "PATCH";
        break;
      case "work_preferred":
        path = `/works/${c.id}/preferred`;
        method = "PUT";
        break;
      case "work_favorite":
        path = `/works/${c.id}/favorite`;
        method = "PUT";
        break;
      case "work_merge":
      case "work_split":
      case "work_separate":
        path = `/works/${c.id}/${c.action.slice(5)}`;
        break;
      case "edition_move":
        path = `/editions/${c.edition_id}/move`;
        break;
      case "edition_labels":
        path = `/editions/${c.edition_id}/version-labels`;
        method = "PUT";
        break;
      case "series_create":
        path = "/series";
        break;
      case "series_rename":
        path = `/series/${c.id}`;
        method = "PATCH";
        break;
      case "series_delete":
        path = `/series/${c.id}`;
        method = "DELETE";
        break;
      case "series_members":
        path = `/series/${c.id}/members`;
        method = "PUT";
        break;
      case "series_add":
        path = `/series/${c.id}/members`;
        break;
      case "series_transfer":
        path = `/series/${c.id}/transfers`;
        break;
      case "series_remove":
        path = `/series/${c.id}/members/${c.work_ids?.[0]}`;
        method = "DELETE";
        break;
      case "series_move":
        path = `/series/${c.id}/members/${c.work_ids?.[0]}/move`;
        break;
      case "series_sort":
        path = `/series/${c.id}/sort`;
        break;
      default:
        return Promise.reject(new Error("未知组织操作"));
    }
    // A path member is authoritative; the HTTP endpoint rejects duplicate selectors.
    const body =
      c.action === "series_move" || c.action === "series_remove"
        ? { ...c, work_ids: undefined }
        : c;
    return request<OrgResult>(path, method, body);
  },
  state: (
    book: Book,
    change: Partial<Pick<UserState, "favorite" | "hidden">>,
  ) =>
    request<UserState>(`/editions/${book.id}/user-state`, "PATCH", {
      expected_revision: book.user_state?.revision ?? 0,
      ...change,
    }),
};
