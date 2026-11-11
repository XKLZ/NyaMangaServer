import { request } from "./api";
import type { Listing } from "./api";
export type ProposalMember = {
  id: string;
  work_id: string;
  title: string;
  title_original?: string;
  languages: string[] | null;
  version_labels: string[] | null;
  content_revision: number;
  cover_revision: number;
  role: "new" | "existing";
  properties?: Record<
    string,
    { value: string | boolean | null; state: string; origin: string }
  >;
  label_suggestion?: {
    rule_version: string;
    presets: { id: string; label: string }[];
    needs_manual_review: boolean;
  };
};
export type WorkProposal = {
  id: string;
  job_id: string;
  kind: "create_work" | "add_to_work";
  target_work_id: string;
  target_work_name: string;
  tier: "strong" | "supplementary";
  primary: boolean;
  revision: number;
  state: "pending" | "dismissed" | "accepted" | "superseded" | "undone";
  members: ProposalMember[];
  new_edition_ids: string[];
  risks: string[] | null;
  blocked: boolean;
  can_undo: boolean;
  effects:
    | {
        edition_id?: string;
        from_series_id?: string;
        to_series_id?: string;
        removed_work?: boolean;
      }[]
    | null;
};
export type ProposalPage = Listing<WorkProposal>;
export const discovery = {
  start: (library_id: string, full_recompute = false) =>
    request<{ job_id: string }>("/work-discovery-jobs", "POST", {
      library_id,
      full_recompute,
    }),
  get: (id: string) =>
    request<WorkProposal>(`/work-proposals/${encodeURIComponent(id)}`),
  resolve: (p: WorkProposal, member_ids: string[]) =>
    request<WorkProposal>(
      `/work-proposals/${encodeURIComponent(p.id)}/resolve`,
      "POST",
      { expected_revision: p.revision, member_ids },
    ),
  decide: (
    p: WorkProposal,
    action: "accept" | "dismiss" | "restore" | "undo",
    member_ids?: string[],
  ) =>
    request<WorkProposal>(
      `/work-proposals/${encodeURIComponent(p.id)}/${action}`,
      "POST",
      {
        expected_revision: p.revision,
        ...(member_ids || action === "accept"
          ? { member_ids: member_ids ?? p.new_edition_ids }
          : {}),
      },
    ),
};
export const proposalRiskNames: Record<string, string> = {
  explicit_member_conflict: "存在内容差异，请核对卷篇和版本",
  cross_series_requires_manual_organization:
    "涉及不同系列，请先在作品管理处理归属",
  organization_requires_manual_review: "当前组织关系需要人工调整",
  target_work_capacity: "作品成员已达上限，请减少新增版本",
};
