import type { SourceSearch, SourceResult } from "../../shared/sourceSearch";
export type SourceAttempt = {
  search: SourceSearch;
  result: SourceResult;
  member: number;
  edition: number;
  requiresReview?: boolean;
};
export type SourceChannel = {
  id: string;
  page: number;
  listScroll: number;
  expanded: Record<string, true>;
  attempt?: SourceAttempt;
};
/** Retain browsing drafts, but never migrate the old drawer's chosen result. */
export function sourceChannel(value?: Partial<SourceChannel>): SourceChannel {
  return {
    id: typeof value?.id === "string" ? value.id : "",
    page: Math.max(1, Math.min(20, Number(value?.page) || 1)),
    listScroll: Math.max(0, Number(value?.listScroll) || 0),
    expanded: value?.expanded ?? {},
    ...(value?.attempt ? { attempt: value.attempt } : {}),
  };
}

export const searchRunning = (s?: SourceSearch | null) =>
  !!s && ["queued", "running"].includes(s.state);
export function moreSearchPage(page: number, count: number) {
  return Math.min(20, count < 10 ? page : page + 1);
}
export function searchError(code: string) {
  return (
    (
      {
        provider_auth_required: "站点认证已失效，请检查站点凭据后恢复查询。",
        provider_action_required: "站点要求人工验证，请完成验证后恢复查询。",
        provider_unavailable: "站点暂不可用，请稍后重试；已加载结果保留。",
        response_too_large: "站点响应超过安全大小限制，请缩小搜索范围。",
        provider_rejected: "该画廊未返回可用资料，请重试此候选。",
        no_sha1: "当前文件没有可用的查找摘要，请改用标题查找。",
        authentication_required: "站点认证已失效，请检查站点凭据后恢复查询。",
        access_restricted: "站点限制了访问，请稍后恢复查询。",
        rate_limited: "请求受到限流，请稍后恢复查询。",
        network_error: "网络连接失败，请重试；原候选仍保留。",
        invalid_response: "站点响应无法解析，请重试或检查站点设置。",
        provider_settings_changed: "站点设置已变化，请重新创建查询。",
        search_limit_reached:
          "已达到200个画廊或32MiB资料上限，请缩小搜索范围。",
        resource_busy: "原元数据任务尚未结束，请等待后重试。",
        source_unavailable: "源文件当前不可用，请检查书库状态。",
        cancelled: "查询已取消，已加载资料保留。",
      } as Record<string, string>
    )[code] ?? "该次查询未完成，请检查任务或重试。"
  );
}

export function sourceChoiceStale(
  choice: { search: SourceSearch; result: { revision: number } } | undefined,
  search: SourceSearch | null,
  resultRevision?: number,
) {
  return (
    !!choice &&
    !!search &&
    (choice.search.id !== search.id ||
      search.revision > choice.search.revision ||
      (resultRevision !== undefined &&
        resultRevision !== choice.result.revision))
  );
}

export function sourceResultTags(
  result: Pick<SourceResult, "tags" | "fields">,
) {
  return result.tags?.length
    ? result.tags
    : (result.fields?.tags ?? []).map((t) => ({
        ...t,
        translation: "",
        translation_origin: "",
      }));
}
