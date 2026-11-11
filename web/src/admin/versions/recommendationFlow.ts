import type {
  Recommendation,
  RecommendationPage,
} from "../../shared/recommendations";
/** A local feedback baseline only changes after save or explicit reload. */
export function feedbackDraft(r: Recommendation) {
  return {
    relation: r.feedback?.relation ?? undefined,
    note: r.feedback?.note ?? "",
    assessment: r.feedback?.labels?.assessment ?? "unreviewed",
  };
}
export function feedbackDirty(
  r: Recommendation,
  draft: ReturnType<typeof feedbackDraft>,
) {
  const initial = feedbackDraft(r);
  return Object.keys(initial).some(
    (k) =>
      initial[k as keyof typeof initial] !== draft[k as keyof typeof draft],
  );
}
/** Export covers the whole task, independently of display filters and page size. */
export function feedbackExportPages(summary: RecommendationPage["summary"]) {
  const rows =
    summary?.layers.reduce(
      (n, v) =>
        n +
        v.same_work +
        v.same_series +
        v.unrelated +
        v.uncertain +
        v.unreviewed,
      0,
    ) ?? 0;
  return Math.max(
    1,
    Math.ceil(Math.max(rows, summary?.recorded_misses ?? 0) / 50),
  );
}
