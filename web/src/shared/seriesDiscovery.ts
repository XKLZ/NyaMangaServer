import { request } from "./api";
import type { WorkCoverSummary } from "./organization";
import type { Listing } from "./api";
export type SeriesProposal = {
  new_member_previews?: SeriesMember[] | null;
  cover?: WorkCoverSummary | null;
  id: string;
  job_id: string;
  library_id: string;
  kind: "create_series" | "add_to_series";
  target_series_id: string;
  name: string;
  new_work_ids: string[];
  existing_work_ids: string[];
  order: string[];
  order_uncertain: boolean;
  name_required?: boolean;
  order_basis?: "explicit" | "contextual" | "natural" | "existing" | "manual";
  tier: "strong" | "supplementary";
  risks: string[] | null;
  primary: boolean;
  revision: number;
  state: "pending" | "dismissed" | "accepted" | "superseded" | "undone";
  can_undo: boolean;
  member_count: number;
};
export type SeriesMember = {
  title?: string;
  title_original?: string;
  work_id: string;
  name: string;
  preferred_edition_id: string;
  edition_count: number;
  existing: boolean;
  content_revision: number;
  cover_revision: number;
};
export type SeriesPending = {
  title?: string;
  title_original?: string;
  work_id: string;
  name: string;
  preferred_edition_id: string;
  content_revision: number;
  cover_revision: number;
  reason: string;
  options: {
    proposal_id: string;
    kind: "create_series" | "join_series";
    target_work_ids: string[];
    tier: string;
  }[];
};
export type SeriesMemberPage = Listing<SeriesMember> & { revision: number };
export const seriesDiscovery = {
  start: (library_id: string) =>
    request<{ job_id: string }>("/series-discovery-jobs", "POST", {
      library_id,
    }),
  get: (id: string) =>
    request<SeriesProposal>(`/series-proposals/${encodeURIComponent(id)}`),
  resolve: (
    p: SeriesProposal,
    work_ids: string[],
    name: string,
    order: string[],
    signal?: AbortSignal,
  ) =>
    request<SeriesProposal>(
      `/series-proposals/${encodeURIComponent(p.id)}/resolve`,
      "POST",
      { expected_revision: p.revision, work_ids, name, order },
      false,
      signal,
    ),
  compose: (
    p: SeriesProposal,
    work_ids: string[],
    name: string,
    order: string[],
  ) =>
    request<SeriesProposal>(
      `/series-proposals/${encodeURIComponent(p.id)}/accept`,
      "POST",
      { expected_revision: p.revision, customize: true, work_ids, name, order },
    ),
  decide: (
    p: SeriesProposal,
    action: "accept" | "dismiss" | "restore" | "undo",
    order?: string[],
  ) =>
    request<SeriesProposal>(
      `/series-proposals/${encodeURIComponent(p.id)}/${action}`,
      "POST",
      {
        expected_revision: p.revision,
        ...(order ? { order } : {}),
        ...(action === "accept" ? { work_ids: p.new_work_ids } : {}),
      },
    ),
};
export const seriesRiskNames: Record<string, string> = {
  explicit_creator_conflict: "创作者资料不一致，请核对",
  creator_support_missing: "共同创作者资料不足",
  semantic_feature_missing: "缺少标题语义特征，当前主要依据题名",
  ordinal_type_ambiguity: "卷／话序号类型不一致",
  same_position_needs_work_or_part_review:
    "序号相同，请核对是否为不同篇章或遗漏的版本",
  distinct_work_relationship_needs_review: "不同作品的证据不足，请核对",
  different_title_subject: "题名主体存在差异",
};
