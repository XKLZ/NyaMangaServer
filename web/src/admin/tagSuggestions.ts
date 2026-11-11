import type {
  TagName,
  TagSuggestion,
  TagSuggestionInput,
  TagSuggestions,
} from "../shared/metadata";
const key = (tag: TagName) => `${tag.namespace}:${tag.name}`;
export function suggestionConditions(
  namespace: string,
  q: string,
  exclude: TagName[],
) {
  return {
    namespace,
    q,
    exclude: [...exclude].sort((a, b) => key(a).localeCompare(key(b))),
    page_size: 50,
  };
}
export type SuggestionPages = { key: string; result: TagSuggestions };
export function mergeSuggestionPage(
  previous: SuggestionPages | null,
  conditionKey: string,
  result: TagSuggestions,
): SuggestionPages {
  if (result.page === 1) return { key: conditionKey, result };
  if (
    !previous ||
    previous.key !== conditionKey ||
    previous.result.snapshot_revision !== result.snapshot_revision ||
    result.page !== previous.result.page + 1
  )
    throw new Error("标签候选页已变化，请重新查询。");
  return {
    key: conditionKey,
    result: {
      ...result,
      items: [
        ...new Map(
          [...previous.result.items, ...result.items].map((tag) => [
            key(tag),
            tag,
          ]),
        ).values(),
      ],
    },
  };
}
export async function requestSuggestionPage(
  input: TagSuggestionInput,
  signal: AbortSignal,
  fetchPage: (
    input: TagSuggestionInput,
    signal: AbortSignal,
  ) => Promise<TagSuggestions>,
) {
  try {
    const result = await fetchPage(input, signal);
    return signal.aborted ? null : result;
  } catch (error) {
    if (signal.aborted) return null;
    if (
      error &&
      typeof error === "object" &&
      "code" in error &&
      error.code === "tag_suggestions_snapshot_expired"
    ) {
      const result = await fetchPage(
        { ...input, page: 1, snapshot_revision: undefined },
        signal,
      );
      return signal.aborted ? null : result;
    }
    throw error;
  }
}
export function dictionaryLabel(value: boolean | null | undefined) {
  return value === true
    ? "已收录"
    : value === false
      ? "译库未收录"
      : "译库待核对";
}
export function utf8QueryValid(q: string) {
  return new TextEncoder().encode(q).length <= 512;
}
export function suggestionKey(tag: TagSuggestion) {
  return key(tag);
}

export function selectedDictionaryLabel(value: boolean | null | undefined) {
  return value === false ? "未收录" : "";
}
