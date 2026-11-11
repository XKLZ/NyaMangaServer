import { usePermissions } from "./permissions";
import { canStartRecommendation } from "./enhancementFlow";
import { useEffect, useState } from "react";
import { useEnhancements } from "./enhancements";
import { resourceEvent } from "./events";
import { ApiError, request } from "./api";
import type { Job, Listing } from "./api";
import type { Command, OrgResult } from "./organization";
export type Relation = "same_work" | "same_series" | "unrelated" | "uncertain";
export const relations: { value: Relation; label: string }[] = [
  { value: "same_work", label: "同一作品，不同版本" },
  { value: "same_series", label: "同系列，不同作品" },
  { value: "unrelated", label: "两者都不是" },
  { value: "uncertain", label: "无法确定" },
];
export type Capability = {
  series_discovery?: boolean;
  series_proposals?: boolean;
  discovery?: boolean;
  proposals?: boolean;
  resource_state?: string;
  work_enabled?: boolean;
  series_enabled?: boolean;
  enabled: boolean;
  available: boolean;
  reason?: string;
  algorithm_version: string;
};
export type LabelRow = {
  edition_id: string;
  languages: string[];
  existing_custom_labels: string[];
  suggested_presets: { id: string; label: string }[];
  needs_manual_review: boolean;
  properties: Record<
    string,
    { value: string | null; state: string; evidence: unknown[] }
  >;
};
export type Recommendation = {
  id: string;
  job_id: string;
  query_edition_id: string | null;
  target_work_id: string | null;
  tier: "strong" | "supplementary";
  rank: number;
  revision: number;
  accepted: boolean;
  feedback?: {
    relation: Relation;
    note: string;
    labels?: { assessment?: string };
  } | null;
  result: {
    query_edition_id: string;
    work_id: string;
    supporting_edition_id: string;
    preferred_edition_id: string;
    matching_score: number | null;
    risks: string[];
    conflicting_member_ids: string[];
    member_evidence: {
      edition_id: string;
      signal: {
        sscd: number | null;
        title_similarity: number | null;
        page_ratio: number | null;
      };
      decision: { tier: string; reasons: string[] };
    }[];
    label_suggestions: { editions: LabelRow[] };
    algorithm_version: string;
  };
};
export type LayerSummary = {
  tier: string;
  same_work: number;
  same_series: number;
  unrelated: number;
  uncertain: number;
  unreviewed: number;
  observed_precision: number | null;
};
export type RecommendationPage = Listing<Recommendation> & {
  pool_count: number;
  prepared_count: number;
  input_errors?: Record<string, string>;
  query_summaries?: Record<
    string,
    { candidate_count: number; truncated: boolean; max_results: number }
  >;
  resources?: {
    cache_hits: number;
    computed_inputs: number;
    model_loads: number;
    inference_seconds: number;
    operation_seconds: number;
    peak_rss_bytes: number;
  };
  summary?: { layers: LayerSummary[]; recorded_misses: number; scope: string };
};
export type Miss = {
  id: string;
  query_edition_id: string | null;
  target_work_id: string | null;
  note: string;
  input_snapshot: unknown;
  created_at: number;
};
export type RecommendationPreview = {
  recommendation_id: string;
  revision: number;
  organization: OrgResult;
};
export const recommendations = {
  start: (ids: string[]) =>
    request<{ job_id: string }>("/work-recommendation-jobs", "POST", {
      edition_ids: ids,
    }),
  feedback: (
    r: Recommendation,
    relation: Relation,
    note: string,
    assessment: string,
  ) =>
    request(`/work-recommendations/${r.id}/feedback`, "POST", {
      expected_revision: r.revision,
      relation,
      note,
      labels: { assessment },
    }),
  preview: (r: Recommendation, command: Command) =>
    request<RecommendationPreview>(
      `/work-recommendations/${r.id}/preview`,
      "POST",
      command,
    ),
  accept: (preview: RecommendationPreview, note: string) =>
    request<OrgResult>(
      `/work-recommendations/${preview.recommendation_id}/accept`,
      "POST",
      {
        expected_revision: preview.revision,
        command: preview.organization.command,
        note,
      },
    ),
  miss: (job: string, edition: string, work: string, note: string) =>
    request(`/work-recommendation-jobs/${job}/misses`, "POST", {
      query_edition_id: edition,
      target_work_id: work,
      note,
    }),
  jobs: () =>
    request<Listing<Job>>("/jobs?type=work_recommendation&page=1&page_size=50"),
};
/** Capability probes never compute features. Older servers' JSON 404 is normal. */
export function useRecommendationCapability(enabled = true) {
  const permissions = usePermissions();
  const enhancements = useEnhancements(enabled);
  const [epoch, setEpoch] = useState(0);
  useEffect(() => {
    if (!enabled) return;
    const changed = (e: Event) => {
      if ((e as CustomEvent<{ type?: string }>).detail?.type === "enhancements")
        setEpoch((v) => v + 1);
    };
    window.addEventListener(resourceEvent, changed);
    return () => window.removeEventListener(resourceEvent, changed);
  }, [enabled]);
  const [capability, setCapability] = useState<Capability | null>(null);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    void request<Capability>(
      "/settings/work-recommendations",
      "GET",
      undefined,
      true,
      controller.signal,
    )
      .then((value) => {
        if (!controller.signal.aborted) {
          setCapability(value);
          setError(null);
        }
      })
      .catch((e) => {
        if (controller.signal.aborted) return;
        if (e instanceof ApiError && (e.status === 404 || e.status === 410))
          setCapability(null);
        else setError(e);
      });
    return () => controller.abort();
  }, [enabled, epoch]);
  const canStartFor = (kind: "work" | "series") =>
    permissions.admin
      ? canStartRecommendation(
          enhancements.data?.features[
            kind === "work" ? "work_recommendations" : "series_recommendations"
          ],
          kind === "work"
            ? !!capability?.discovery && !!capability?.proposals
            : !!capability?.series_discovery && !!capability?.series_proposals,
        )
      : !!capability &&
        !!(kind === "work"
          ? capability.work_enabled
          : capability.series_enabled) &&
        !!(kind === "work"
          ? capability.discovery && capability.proposals
          : capability.series_discovery && capability.series_proposals) &&
        ["configured_unverified", "unverified", "ready"].includes(
          capability.resource_state || "",
        );
  return { capability, error, enhancements: enhancements.data, canStartFor };
}
export const riskNames: Record<string, string> = {
  explicit_creator_conflict: "创作者明确不一致",
  different_explicit_volume_or_chapter: "卷篇序号冲突",
  ordinal_type_ambiguity: "序号类型存在歧义",
  compilation_vs_single_work: "合集与单篇风险",
  large_page_count_difference: "页数差距较大",
  different_ambiguous_position: "疑似篇章不同",
  ordinal_missing_one_side: "一侧缺少序号",
  missing_title: "标题缺失",
  missing_creator_metadata: "创作者资料缺失",
  missing_cover_feature: "缺少封面特征",
  missing_page_count: "页数缺失",
};
export function tierName(tier: string) {
  return tier === "strong" ? "较强建议" : "补充候选";
}

/** Uses the public read-only capability for library editors; no synthesized global configuration. */
export function useRecommendationEnabled(
  key?: import("./enhancementFlow").FeatureKey,
) {
  const { capability, enhancements } = useRecommendationCapability(!!key);
  const admin = usePermissions().admin;
  if (!key) return true;
  if (admin) return !!enhancements?.features[key]?.effective_enabled;
  return key === "work_recommendations"
    ? !!capability?.work_enabled
    : key === "series_recommendations"
      ? !!capability?.series_enabled
      : false;
}
