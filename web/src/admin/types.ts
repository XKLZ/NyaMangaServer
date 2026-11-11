import type { Book, Listing } from "../shared/api";
import type { Fields } from "../shared/metadata";
export type CoverSelection = {
  parsing_revision?: number;
  mode: "auto" | "page" | "asset";
  page_index?: number;
  asset_id?: string;
  revision: number;
  content_revision: number;
  state: string;
};
export type MaintenancePlan = {
  job_id?: string;
  id: string;
  operation: string;
  expires_at: number;
  summary: unknown;
  affected: unknown;
  protected: unknown;
};
export type SourcePreview = {
  job_id: string;
  expected_revision: number;
  selection_revision: number;
  content_revision: number;
  id: string;
  state: string;
  expires_at: number;
  fields: Fields;
  source: { url?: string; fields?: Fields };
  current: unknown;
  shared_book_count: number;
};
export type AdminBooks = Listing<Book> & { snapshot_revision: string };
export type RootEntry = {
  name: string;
  relative_path: string;
  kind: "directory" | "archive";
};
export type TrashEntry = {
  id: string;
  kind?: string;
  title?: string;
  display_name?: string;
  relative_path?: string;
  revision: number;
  trashed_at?: number;
  organization?: unknown;
};
