import { request } from "./api";
import type { Job, Listing } from "./api";
import type { Book } from "./api";
import type { Tag, MetadataDetails, Override, EditableField } from "./metadata";
import type { WorkProposal } from "./workDiscovery";
import type { SeriesProposal, SeriesMemberPage } from "./seriesDiscovery";
import type { Fields } from "./metadata";
export type ImportStage =
  "scan" | "metadata" | "versions" | "series" | "finished";
export type ImportFailure = {
  job_id: string;
  stage: string;
  error_code: string;
  phase?: string;
  reason?: string;
};
export type ImportBatch = {
  enhancement_stages?: Record<
    string,
    {
      enabled: boolean;
      state: "required" | "disabled_skipped" | "blocked" | "skipped";
      reason: string;
    }
  >;
  created_at?: number;
  task_failures?: ImportFailure[];
  current_failure?: ImportFailure | null;
  can_retry?: boolean;
  stage_decisions?: Record<
    string,
    {
      decision: "skipped" | "skipped_disabled";
      job_id: string;
      created_at: number;
    }
  >;
  skipped_stages?: string[];
  counts?: Record<string, number> | null;
  id: string;
  library_id: string;
  stage: ImportStage;
  state: string;
  revision: number;
  error_code: string;
  total: number;
  pending: number;
  jobs: Job[] | null;
};
export type ImportMember = {
  title_original?: string;
  match_count?: number;
  match_list_complete?: boolean;
  source_search_id?: string;
  work_name?: string;
  series_name?: string;
  requested_url?: string;
  edition_id: string;
  edition_revision: number;
  cover_revision: number;
  has_metadata: boolean;
  candidate_acceptable: boolean;
  status: string;
  id: string;
  filename: string;
  title: string;
  change_kind: string;
  error_code: string;
  scan_error?: string;
  decision: string;
  revision: number;
  content_revision: number;
  work_id: string;
  series_id: string;
  candidate?: {
    revision: number;
    request_generation: number;
    content_revision: number;
    selection_revision: number;
    url: string;
    match_method: string;
    fields: Fields;
    tags?: Tag[] | null;
    shared_editions: number;
  } | null;
};
export const importStageNames: Record<ImportStage, string> = {
  scan: "扫描入库",
  metadata: "元数据审核",
  versions: "版本归属审核",
  series: "系列归属审核",
  finished: "完成",
};
export type OrganizationReview = {
  failure?: ImportFailure | null;
  can_retry?: boolean;
  recommendation_skipped?: boolean;
  stage: "versions" | "series";
  state: string;
  expected_revision: number;
  pending_proposals: number;
  revalidating: boolean;
  active_tasks: boolean;
  can_complete: boolean;
  snapshot_token: string;
};
export const importAPI = {
  organizationReview: (id: string) =>
    request<OrganizationReview>(`/import-batches/${id}/organization-review`),
  completeOrganization: (id: string, review: OrganizationReview) =>
    request<ImportBatch>(
      `/import-batches/${id}/organization-review/complete`,
      "POST",
      {
        stage: review.stage,
        expected_revision: review.expected_revision,
        snapshot_token: review.snapshot_token,
      },
    ),
  selection: (batch: string, page = 1, signal?: AbortSignal) =>
    request<Listing<ImportSelection>>(
      `/import-batches/${batch}/review-selection?page=${page}&page_size=100`,
      "GET",
      undefined,
      false,
      signal,
    ),
  editor: (batch: string, id: string, signal?: AbortSignal) =>
    request<ImportEditor>(
      `/import-batches/${batch}/members/${id}/editor`,
      "GET",
      undefined,
      false,
      signal,
    ),
  manual: (
    batch: string,
    id: string,
    context: ImportEditor,
    revision: number,
    fields: Partial<Record<EditableField, Override>>,
  ) =>
    request<Book>(
      `/import-batches/${batch}/members/${id}/manual-review`,
      "POST",
      {
        expected_revision: revision,
        expected_member_revision: context.member_revision,
        candidate_revision: context.candidate_revision,
        fields,
      },
    ),

  retryFiles: (b: ImportBatch, edition_ids: string[]) =>
    request<{ job_id: string }>(`/import-batches/${b.id}/retry-files`, "POST", {
      expected_revision: b.revision,
      edition_ids,
    }),
  create: (library_id: string, request_key: string) =>
    request<ImportBatch>("/import-batches", "POST", {
      library_id,
      request_key,
      allow_metadata_network: true,
    }),
  control: (b: ImportBatch, action: string) =>
    request<ImportBatch>(`/import-batches/${b.id}/control/${action}`, "POST", {
      expected_revision: b.revision,
    }),
  review: (
    b: ImportBatch,
    items: {
      id: string;
      expected_revision: number;
      expected_edition_revision?: number;
      candidate_revision?: number;
      decision: string;
    }[],
  ) => request(`/import-batches/${b.id}/reviews`, "POST", { items }),
  refetch: (batch: string, item: ImportMember, url: string) =>
    request<{ job_id: string }>(
      `/import-batches/${batch}/members/${item.id}/refetch`,
      "POST",
      { expected_revision: item.revision, gallery_url: url },
    ),
};
export type ImportMemberPage = Listing<ImportMember>;

export type ImportSelection = {
  has_metadata: boolean;
  id: string;
  expected_revision: number;
  expected_edition_revision: number;
  candidate_revision: number;
  candidate_acceptable: boolean;
  status: string;
  decision: string;
};
export type ImportEditor = {
  book: Book;
  metadata: MetadataDetails;
  member_revision: number;
  candidate_revision: number;
};
export function memberSelection(v: ImportMember): ImportSelection {
  return {
    id: v.id,
    has_metadata: v.has_metadata,
    expected_revision: v.revision,
    expected_edition_revision: v.edition_revision,
    candidate_revision: v.candidate?.revision ?? 0,
    candidate_acceptable: v.candidate_acceptable,
    status: v.status,
    decision: v.decision,
  };
}

export function importProposalTransport(batch: string) {
  const work = `/import-batches/${batch}/work-proposals`,
    series = `/import-batches/${batch}/series-proposals`;
  return {
    workGet: (id: string) =>
      request<WorkProposal>(`${work}/${encodeURIComponent(id)}`),
    workResolve: (p: WorkProposal, ids: string[]) =>
      request<WorkProposal>(
        `${work}/${encodeURIComponent(p.id)}/resolve`,
        "POST",
        { expected_revision: p.revision, member_ids: ids },
      ),
    workDecide: (p: WorkProposal, action: string) =>
      request<WorkProposal>(
        `${work}/${encodeURIComponent(p.id)}/${action}`,
        "POST",
        {
          expected_revision: p.revision,
          ...(action === "accept" ? { member_ids: p.new_edition_ids } : {}),
        },
      ),
    get: (id: string) =>
      request<SeriesProposal>(`${series}/${encodeURIComponent(id)}`),
    members: (id: string, page: number, signal?: AbortSignal) =>
      request<SeriesMemberPage>(
        `${series}/${encodeURIComponent(id)}/members?page=${page}&page_size=25`,
        "GET",
        undefined,
        false,
        signal,
      ),
    compose: (
      p: SeriesProposal,
      ids: string[],
      name: string,
      order: string[],
    ) =>
      request<SeriesProposal>(
        `${series}/${encodeURIComponent(p.id)}/accept`,
        "POST",
        {
          expected_revision: p.revision,
          customize: true,
          work_ids: ids,
          name,
          order,
        },
      ),
    decide: (p: SeriesProposal, action: string, order?: string[]) =>
      request<SeriesProposal>(
        `${series}/${encodeURIComponent(p.id)}/${action}`,
        "POST",
        {
          expected_revision: p.revision,
          ...(order ? { order } : {}),
          ...(action === "accept" ? { work_ids: p.new_work_ids } : {}),
        },
      ),
  };
}
