import { request } from "./api";
export type TagFollow = { tag_id: string; followed: boolean; revision: number };
export type FollowSummary = {
  id: string;
  followed?: boolean;
  follow_revision?: number;
};
export function followValue(tag: FollowSummary): TagFollow | undefined {
  return tag.id &&
    typeof tag.followed === "boolean" &&
    Number.isSafeInteger(tag.follow_revision) &&
    tag.follow_revision! >= 0
    ? { tag_id: tag.id, followed: tag.followed, revision: tag.follow_revision! }
    : undefined;
}
export function followChange(value: TagFollow) {
  if (
    !value.tag_id ||
    !Number.isSafeInteger(value.revision) ||
    value.revision < 0 ||
    value.revision >= Number.MAX_SAFE_INTEGER
  )
    throw Error("缺少可提交的关注版本，请重新读取");
  return {
    path: `/tags/${encodeURIComponent(value.tag_id)}/follow`,
    method: value.followed ? "DELETE" : "PUT",
    body: { expected_revision: value.revision },
  };
}
export async function changeTagFollow(value: TagFollow) {
  const change = followChange(value);
  const result = await request<TagFollow>(
    change.path,
    change.method,
    change.body,
  );
  if (
    result?.tag_id !== value.tag_id ||
    result.followed !== !value.followed ||
    !Number.isSafeInteger(result.revision) ||
    result.revision <= value.revision
  )
    throw Error("服务未返回可确认的关注结果，请重新读取");
  return result;
}
