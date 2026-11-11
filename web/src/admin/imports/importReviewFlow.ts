import type { ImportSelection } from "../../shared/imports";

export type ReviewSelectionState = {
  selected: Record<string, ImportSelection>;
  excluded: Record<string, true>;
  stale: Record<string, true>;
};
export const emptyReviewSelection = (): ReviewSelectionState => ({
  selected: {},
  excluded: {},
  stale: {},
});
export function sameReviewIdentity(a: ImportSelection, b: ImportSelection) {
  return (
    a.expected_revision === b.expected_revision &&
    a.candidate_revision === b.candidate_revision &&
    a.expected_edition_revision === b.expected_edition_revision
  );
}
// Refresh can add newly successful items, but cannot undo a user's exclusion or
// promote a selected item's frozen candidate/member/edition versions.
export function reconcileReviewSelection(
  state: ReviewSelectionState,
  items: ImportSelection[],
): ReviewSelectionState {
  const selected = { ...state.selected },
    stale = { ...state.stale };
  const current = new Map(items.map((v) => [v.id, v]));
  for (const [id, old] of Object.entries(selected)) {
    const next = current.get(id);
    if (
      next &&
      (next.expected_revision < old.expected_revision ||
        next.candidate_revision < old.candidate_revision ||
        next.expected_edition_revision < old.expected_edition_revision)
    )
      continue;
    if (
      !next ||
      next.decision ||
      !next.candidate_acceptable ||
      !sameReviewIdentity(old, next)
    ) {
      delete selected[id];
      if (next && !next.decision) stale[id] = true;
    }
  }
  if (items.length <= 1000)
    for (const item of items) {
      if (
        item.candidate_acceptable &&
        !item.decision &&
        !selected[item.id] &&
        !state.excluded[item.id] &&
        !stale[item.id]
      )
        selected[item.id] = item;
    }
  return { selected, excluded: { ...state.excluded }, stale };
}
export function selectReviewItem(
  state: ReviewSelectionState,
  item: ImportSelection,
  checked: boolean,
): ReviewSelectionState {
  const selected = { ...state.selected },
    excluded = { ...state.excluded },
    stale = { ...state.stale };
  if (checked) {
    if (Object.keys(selected).length >= 1000 && !selected[item.id])
      throw Error("最多选择1000项，请分批处理");
    selected[item.id] = item;
    delete excluded[item.id];
    delete stale[item.id];
  } else {
    delete selected[item.id];
    excluded[item.id] = true;
    delete stale[item.id];
  }
  return { selected, excluded, stale };
}
export function clearReviewSelection(
  state: ReviewSelectionState,
  items: ImportSelection[],
) {
  return {
    selected: {},
    stale: {},
    excluded: {
      ...state.excluded,
      ...Object.fromEntries(items.map((v) => [v.id, true as const])),
    },
  };
}
export function reviewStatus(status: string) {
  return (
    (
      {
        ready: ["待确认匹配", "success"],
        reviewed: ["已处理", "default"],
        fetching: ["正在获取", "processing"],
        file_error: ["源文件异常", "error"],
        stale: ["候选已过期", "warning"],
        no_match: ["未找到匹配", "warning"],
        multiple_matches: ["找到多个结果", "warning"],
        fetch_error: ["获取失败", "error"],
      } as const
    )[status as "ready"] ?? ["等待获取", "default"]
  );
}

export type HighlightPart = { text: string; matched: boolean };
// Compare bounded normalized grapheme sequences, retaining exact display text.
// Punctuation and the filename extension do not contribute. This is visual
// overlap only, never a recommendation feature or a confidence score.
export function titleOverlap(values: string[]): HighlightPart[][] {
  const segmenter = new Intl.Segmenter(undefined, { granularity: "grapheme" });
  const units = values.map((raw, index) => {
    const text = index === 0 ? raw.replace(/\.[a-z0-9]{1,8}$/i, "") : raw;
    return [...segmenter.segment(text)]
      .flatMap((g) =>
        [...g.segment.normalize("NFKC").toLowerCase()]
          .filter((c) => /[\p{L}\p{N}]/u.test(c))
          .map((c) => ({ c, start: g.index, end: g.index + g.segment.length })),
      )
      .slice(0, 512);
  });
  const marks = units.map((u) => new Int32Array(u.length + 1));
  for (let a = 0; a < units.length; a++)
    for (let b = a + 1; b < units.length; b++) {
      let prev = new Uint16Array(units[b].length + 1);
      for (let i = 1; i <= units[a].length; i++) {
        const next = new Uint16Array(prev.length);
        for (let j = 1; j <= units[b].length; j++)
          if (units[a][i - 1].c === units[b][j - 1].c) {
            const n = prev[j - 1] + 1;
            next[j] = n;
            const min =
              /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}]/u.test(
                units[a][i - 1].c,
              )
                ? 2
                : 4;
            if (n >= min) {
              marks[a][i - n]++;
              marks[a][i]--;
              marks[b][j - n]++;
              marks[b][j]--;
            }
          }
        prev = next;
      }
    }
  return values.map((text, at) => {
    const flags = new Uint8Array(text.length);
    let count = 0;
    for (let i = 0; i < units[at].length; i++) {
      count += marks[at][i];
      if (count > 0) flags.fill(1, units[at][i].start, units[at][i].end);
    }
    const parts: HighlightPart[] = [];
    let start = 0;
    for (let i = 1; i <= text.length; i++)
      if (i === text.length || flags[i] !== flags[start]) {
        parts.push({ text: text.slice(start, i), matched: !!flags[start] });
        start = i;
      }
    return parts;
  });
}

/** Choose by covered filename characters; ties favor the original title. */
export function reviewTitle(filename: string, title = "", original = "") {
  const candidates = [title, original]
    .map((value, index) => {
      const parts = titleOverlap([filename, value]);
      const score = parts[0]
        .filter((p) => p.matched)
        .reduce(
          (n, p) =>
            n +
            [...p.text.normalize("NFKC").toLowerCase()].filter((c) =>
              /[\p{L}\p{N}]/u.test(c),
            ).length,
          0,
        );
      return { value, original: index === 1, parts, score };
    })
    .filter((v) => v.value.trim());
  return (
    candidates.sort(
      (a, b) => b.score - a.score || Number(b.original) - Number(a.original),
    )[0] ?? {
      value: "",
      original: false,
      parts: titleOverlap([filename, ""]),
      score: 0,
    }
  );
}

/** Called only following a successful explicit review/save, never from polling. */
export async function finishMetadataReview<
  T extends { stage: string; state: string; pending: number },
>(read: () => Promise<T>, advance: (batch: T) => Promise<unknown>) {
  const batch = await read();
  if (
    batch.stage !== "metadata" ||
    batch.state !== "review" ||
    batch.pending !== 0
  )
    return false;
  await advance(batch);
  return true;
}
