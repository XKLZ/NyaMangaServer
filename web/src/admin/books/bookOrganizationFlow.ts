import type { OrgContext, Command } from "../../shared/organization";
export const relationshipPageSize = 8;
export function currentMembership(
  context: OrgContext | undefined,
  editionID: string,
) {
  const edition = context?.editions.find((e) => e.id === editionID);
  const work = context?.works.find((w) => w.id === edition?.work_id);
  return {
    edition,
    work,
    series: context?.series.find((s) => s.id === work?.series_id),
  };
}
export function memberPage(position: number, size = relationshipPageSize) {
  return Math.floor(Math.max(0, position - 1) / size) + 1;
}
/** A notification or target lookup must never silently rebase an existing draft. */
export function frozenRevisions(base: OrgContext, extras: OrgContext[]) {
  return Object.assign(
    {},
    ...extras.map((c) => c.expected_revisions),
    base.expected_revisions,
  );
}
export function seriesDestination(
  currentSeries: string | undefined,
  target: string,
  workID: string,
): Partial<Command> {
  if (!target || target === currentSeries)
    throw new Error("请选择另一个系列。");
  return currentSeries
    ? {
        action: "series_transfer",
        id: currentSeries,
        target_series_id: target,
        work_ids: [workID],
      }
    : { action: "series_add", id: target, work_ids: [workID] };
}
