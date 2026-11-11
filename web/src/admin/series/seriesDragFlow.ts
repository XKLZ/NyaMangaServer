import type { SeriesProposal } from "../../shared/seriesDiscovery";
export type SeriesDrop = {
  scope: string;
  row: string;
  edge: "before" | "after";
};
/** Drop a complete block while keeping every other member's relative order. */
export function placeSeriesBlock(
  order: string[],
  block: string[],
  target: Pick<SeriesDrop, "row" | "edge">,
): string[] {
  if (
    new Set(order).size !== order.length ||
    new Set(block).size !== block.length ||
    block.some((id) => !order.includes(id))
  )
    throw new Error("拖动成员不属于当前快照");
  if (target.row && !order.includes(target.row))
    throw new Error("落点已不在当前成员中，请刷新核对");
  if (block.includes(target.row)) return [...order];
  const rest = order.filter((id) => !block.includes(id));
  const at = target.row
    ? rest.indexOf(target.row) + (target.edge === "after" ? 1 : 0)
    : rest.length;
  rest.splice(at, 0, ...block);
  return rest;
}
export function seriesJoinOrder(
  p: SeriesProposal,
  target?: Pick<SeriesDrop, "row" | "edge">,
) {
  const added = p.order.filter((id) => p.new_work_ids.includes(id));
  if (target) return placeSeriesBlock(p.order, added, target);
  return ["explicit", "contextual", "manual"].includes(p.order_basis ?? "")
    ? [...p.order]
    : [...p.existing_work_ids, ...added];
}
export function nextSeriesReview<T extends { id: string }>(
  items: T[],
  oldIndex: number,
): T | undefined {
  return items[Math.max(0, Math.min(oldIndex, items.length - 1))];
}
