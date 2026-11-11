import { cleanBookTitle, originalTitle } from "../books/bookTitle";
import type { OrganizationProposal } from "./importOrganizationFlow";
export function suggestionName(p: OrganizationProposal) {
  if ("new_work_ids" in p) return p.name || "未命名系列";
  const member =
    p.members.find((m) => m.work_id === p.target_work_id) ?? p.members[0];
  return (
    cleanBookTitle(originalTitle(member) || p.target_work_name) || "未命名作品"
  );
}
export function suggestionAction(p: OrganizationProposal) {
  return {
    create_work: "组成版本组",
    add_to_work: "加入版本组",
    create_series: "组成系列",
    add_to_series: "加入系列",
  }[p.kind];
}
