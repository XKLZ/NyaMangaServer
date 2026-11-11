/** §2.8/§3.6: quiet secondary stars, separate from navigation. Existing Ant
 * tokens, 32px controls, no optimistic persistence or shared metadata CAS. */
import { createContext, useContext, useEffect, useRef, useState } from "react";
import { Alert, Button, ConfigProvider, Popover, Space, Tooltip } from "antd";
import { StarFilled, StarOutlined } from "@ant-design/icons";
import { ApiError, writesPending } from "../../shared/api";
import { resourceEvent } from "../../shared/events";
import { tagCatalog } from "../../shared/tagCatalog";
import type { CatalogTag } from "../../shared/tagCatalog";
import { changeTagFollow, followValue } from "../../shared/tagFollows";
import type { TagFollow } from "../../shared/tagFollows";
import { tagName } from "./tagCatalogFlow";
type Entry = { value: TagFollow; busy?: boolean; error?: string };
type FollowContext = {
  entries: Record<string, Entry>;
  change: (tag: CatalogTag, reread?: boolean) => Promise<boolean>;
};
const Context = createContext<FollowContext | null>(null);
function displayed(tag: CatalogTag, entry?: Entry) {
  const supplied = followValue(tag);
  if (entry?.error || entry?.busy) return entry.value;
  return entry && (!supplied || entry.value.revision > supplied.revision)
    ? entry.value
    : supplied;
}
export function TagFollowProvider({
  library,
  children,
}: {
  library: string;
  children: React.ReactNode;
}) {
  const [entries, setEntries] = useState<Record<string, Entry>>({});
  const current = useRef(entries),
    active = useRef(false),
    reads = useRef(new Set<AbortController>());
  useEffect(() => {
    active.current = true;
    const controllers = reads.current;
    return () => {
      active.current = false;
      controllers.forEach((c) => c.abort());
    };
  }, []);
  function publish(id: string, value: Entry) {
    current.current = { ...current.current, [id]: value };
    if (active.current) setEntries(current.current);
  }
  async function change(tag: CatalogTag, reread = false) {
    const entry = current.current[tag.id],
      value = displayed(tag, entry);
    if (!value || entry?.busy || writesPending() || (entry?.error && !reread))
      return false;
    const focusTarget = document.activeElement;
    publish(tag.id, { value, busy: true, error: entry?.error });
    const controller = new AbortController();
    if (reread) reads.current.add(controller);
    try {
      const result = reread
        ? followValue(
            await tagCatalog.detail(tag.id, library, controller.signal),
          )
        : await changeTagFollow(value);
      if (!result) throw Error("服务尚未提供关注状态，请升级后刷新");
      if (result.revision < value.revision)
        throw Error("关注读取已过期，请重新读取");
      if (!active.current || controller.signal.aborted) return false;
      publish(tag.id, { value: result });
      // A local notification follows an acknowledged write/read, not a guessed state.
      window.dispatchEvent(
        new CustomEvent(resourceEvent, {
          detail: { type: "tag_follow", resource_id: tag.id, focusTarget },
        }),
      );
      return true;
    } catch (error) {
      if (active.current && !controller.signal.aborted)
        publish(tag.id, {
          value,
          error:
            error instanceof ApiError &&
            error.code === "tag_follow_revision_conflict"
              ? "关注状态已变化，请重新读取并核对后再操作。"
              : reread
                ? "读取关注状态失败，原状态保留，请再次读取。"
                : "关注操作结果未确认，原状态保留，请重新读取后再操作。",
        });
      return false;
    } finally {
      reads.current.delete(controller);
    }
  }
  return (
    <Context.Provider value={{ entries, change }}>{children}</Context.Provider>
  );
}
export function TagFollowButton({
  tag,
  detail = false,
}: {
  tag: CatalogTag;
  detail?: boolean;
}) {
  const context = useContext(Context);
  if (!context) throw Error("TagFollowProvider is missing");
  const [open, setOpen] = useState(false),
    entry = context.entries[tag.id],
    value = displayed(tag, entry),
    label = `${value?.followed ? "取消关注" : "关注"}${tagName(tag)}`;
  const { componentDisabled } = ConfigProvider.useConfig();
  return (
    <Popover
      trigger="click"
      open={open && !!entry?.error}
      onOpenChange={setOpen}
      content={
        <Space orientation="vertical">
          <Alert type="warning" showIcon title={entry?.error} />
          <Button
            loading={entry?.busy}
            onClick={async () => {
              if (await context.change(tag, true)) setOpen(false);
            }}
          >
            重新读取关注状态
          </Button>
        </Space>
      }
    >
      <Tooltip
        title={
          !value
            ? "服务尚未提供关注状态，请升级后刷新"
            : entry?.error
              ? "需重新读取关注状态"
              : value.followed
                ? "取消关注"
                : "关注"
        }
        trigger={["hover", "focus"]}
      >
        <Button
          type="text"
          className={`tag-follow-button ${detail ? "tag-follow-detail" : ""}`}
          data-follow-tag={tag.id}
          data-followed={value?.followed || undefined}
          aria-label={label}
          aria-pressed={value?.followed ?? false}
          disabled={!value || entry?.busy || componentDisabled}
          loading={entry?.busy}
          icon={
            value?.followed ? (
              <StarFilled aria-hidden />
            ) : (
              <StarOutlined aria-hidden />
            )
          }
          onClick={async (event) => {
            event.stopPropagation();
            if (entry?.error) setOpen(true);
            else if (!(await context.change(tag))) setOpen(true);
          }}
        />
      </Tooltip>
    </Popover>
  );
}
