import type { SeriesProposal } from "../../shared/seriesDiscovery";
export function seriesOrderNotice(p: SeriesProposal) {
  switch (p.order_basis) {
    case "explicit":
      return {
        title: "已按明确卷序排列",
        description: "依据各版本题名中的卷／篇序号，可使用上移／下移调整。",
      };
    case "contextual":
      return {
        title: "已依据题名推断顺序",
        description:
          "结合共同题名、续作数字及无编号本篇排列；请核对，不会改写卷号资料。",
      };
    case "manual":
      return {
        title: "采用你调整的顺序",
        description: "已保存本次手动顺序，后续通知不会自动重排。",
      };
    case "existing":
      return {
        title: "保留已有顺序，新增作品追加末尾",
        description: "暂无可靠插入位置，请核对新增作品；不会重排已有成员。",
      };
    case "natural":
      return {
        title: "卷序待核对",
        description:
          "当前按去除括号装饰后的题名自然排序，可使用上移／下移调整。",
      };
    default:
      return {
        title: p.order_uncertain ? "卷序待核对" : "采用原方案顺序",
        description:
          p.kind === "add_to_series"
            ? "沿用旧版方案，保留已有成员相对顺序。"
            : "此为旧版方案的原有顺序；新命名和排序规则只用于新发现任务。",
      };
  }
}
export type SeriesDraft = {
  baseline: SeriesProposal;
  selected: string[];
  order: string[];
  name: string;
};
export function seriesDraft(p: SeriesProposal): SeriesDraft {
  return {
    baseline: p,
    selected: [...p.new_work_ids],
    order: [...p.order],
    name: p.name,
  };
}
export function seriesOrder(d: SeriesDraft) {
  return d.order.filter(
    (id) =>
      d.baseline.existing_work_ids.includes(id) || d.selected.includes(id),
  );
}
export function seriesDirty(d: SeriesDraft) {
  return (
    d.name !== d.baseline.name ||
    JSON.stringify([...d.selected].sort()) !==
      JSON.stringify([...d.baseline.new_work_ids].sort()) ||
    JSON.stringify(seriesOrder(d)) !== JSON.stringify(d.baseline.order)
  );
}
export function canResolveSeries(d: SeriesDraft) {
  const order = seriesOrder(d);
  return (
    d.name.trim().length > 0 &&
    Array.from(d.name).length <= 200 &&
    d.selected.length >= (d.baseline.kind === "create_series" ? 2 : 1) &&
    new Set(d.selected).size === d.selected.length &&
    d.selected.every((id) => d.baseline.new_work_ids.includes(id)) &&
    order.length === d.selected.length + d.baseline.existing_work_ids.length &&
    new Set(order).size === order.length &&
    JSON.stringify(
      order.filter((id) => d.baseline.existing_work_ids.includes(id)),
    ) === JSON.stringify(d.baseline.existing_work_ids)
  );
}
export function moveSeriesWork(
  d: SeriesDraft,
  id: string,
  delta: number,
): SeriesDraft {
  const order = seriesOrder(d),
    at = order.indexOf(id),
    next = at + delta;
  if (
    at < 0 ||
    next < 0 ||
    next >= order.length ||
    d.baseline.existing_work_ids.includes(id)
  )
    return d;
  [order[at], order[next]] = [order[next], order[at]];
  return {
    ...d,
    order: [...order, ...d.order.filter((i) => !order.includes(i))],
  };
}
export function seriesChanged(a: SeriesProposal, b: SeriesProposal) {
  return (
    a.revision > b.revision ||
    (a.revision === b.revision && a.state !== b.state)
  );
}

export function seriesDraftKey(d: SeriesDraft) {
  return JSON.stringify([
    d.baseline.id,
    d.baseline.revision,
    d.selected,
    d.name,
    seriesOrder(d),
  ]);
}
// Fetch only the original frozen pages needed to render this reordered page.
export function seriesMemberPages(
  baseline: string[],
  order: string[],
  page: number,
  size = 25,
) {
  return [
    ...new Set(
      order.slice((page - 1) * size, page * size).map((id) => {
        const at = baseline.indexOf(id);
        if (at < 0) throw new Error("方案成员不在冻结列表中，请加载当前方案");
        return Math.floor(at / size) + 1;
      }),
    ),
  ];
}

/** Explicit conflict rebase preserves new-member order and exclusions while
 * retaining the current series' immutable relative order. */
export function rebaseSeriesDraft(
  d: SeriesDraft,
  fresh: SeriesProposal,
): SeriesDraft {
  const existing = fresh.order.filter((id) =>
    fresh.existing_work_ids.includes(id),
  );
  const all = new Set(fresh.order);
  const order: string[] = [];
  let at = 0;
  for (const id of d.order) {
    if (!all.has(id)) continue;
    order.push(fresh.existing_work_ids.includes(id) ? existing[at++] : id);
  }
  order.push(...existing.slice(at));
  for (const id of fresh.order) if (!order.includes(id)) order.push(id);
  return {
    baseline: fresh,
    name: fresh.kind === "create_series" ? d.name : fresh.name,
    selected: d.selected.filter((id) => fresh.new_work_ids.includes(id)),
    order,
  };
}
