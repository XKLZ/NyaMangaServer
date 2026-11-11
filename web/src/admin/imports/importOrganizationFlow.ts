import type { WorkProposal } from "../../shared/workDiscovery";
import type { SeriesProposal } from "../../shared/seriesDiscovery";
import type { OrganizationReview } from "../../shared/imports";
export type OrganizationProposal = WorkProposal | SeriesProposal;
export type WorkReviewDraft = { baseline: WorkProposal; selected: string[] };
export function workReviewDraft(p: WorkProposal): WorkReviewDraft {
  return { baseline: p, selected: [...p.new_edition_ids] };
}
export function workReviewDirty(d: WorkReviewDraft) {
  return (
    JSON.stringify([...d.selected].sort()) !==
    JSON.stringify([...d.baseline.new_edition_ids].sort())
  );
}
export function nextSuggestionIndex(length: number, previous: number) {
  return Math.max(0, Math.min(previous, length - 1));
}
export function shouldCompleteReview(
  intent: boolean,
  review: OrganizationReview,
  hasDrafts: boolean,
) {
  return (
    intent &&
    !hasDrafts &&
    review.can_complete &&
    !review.recommendation_skipped &&
    !review.failure &&
    !review.active_tasks &&
    !review.revalidating &&
    review.pending_proposals === 0
  );
}
