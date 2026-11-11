export type ReadingState = {
  edition_id: string;
  parsing_revision?: number;
  current_parsing_revision?: number;
  parsing_stale?: boolean;
  page_index: number | null;
  recorded_page_count: number | null;
  content_revision: number;
  current_content_revision: number;
  current_page_count: number | null;
  stale: boolean;
  completed: boolean;
  completed_at: number | null;
  last_read_at: number | null;
  read_count: number;
  revision: number;
};
export type ReadingAction = "complete" | "uncomplete" | "reset";
export const readingStatus = (s: ReadingState) =>
  s.completed ? "已完成" : s.page_index !== null ? "阅读中" : "未读";
export function readingProgress(s: ReadingState) {
  const count =
    s.stale || s.parsing_stale ? s.recorded_page_count : s.current_page_count;
  return s.page_index === null
    ? count === null
      ? "总页数未知"
      : `${count} 页`
    : `第 ${s.page_index + 1} 页 / ${count === null ? "总页数未知" : `${count} 页`}`;
}
export function readingRequest(
  s: ReadingState,
  action: ReadingAction,
  key: string,
  accept = false,
  acceptParsing = false,
) {
  return {
    expected_revision: s.revision,
    content_revision: s.current_content_revision,
    ...(s.current_parsing_revision !== undefined
      ? { parsing_revision: s.current_parsing_revision }
      : {}),
    idempotency_key: key,
    action,
    ...(accept ? { accept_content_change: true } : {}),
    ...(acceptParsing ? { accept_parsing_change: true } : {}),
  };
}

export const readingContentChanged = (state: ReadingState) =>
  state.content_revision !== state.current_content_revision;
