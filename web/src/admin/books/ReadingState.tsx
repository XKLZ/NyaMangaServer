import { randomIdentity } from "../../shared/randomIdentity";
import { usePermissions } from "../../shared/permissions";
import { useState, useRef } from "react";
import { Alert, Button, Checkbox, Modal, Space, Tag } from "antd";
import { ApiError, request } from "../../shared/api";
import {
  readingProgress,
  readingContentChanged,
  readingRequest,
  readingStatus,
} from "../../shared/readingFlow";
import type {
  ReadingState as State,
  ReadingAction,
} from "../../shared/readingFlow";
import { time, useDirty } from "../common";
export function ReadingBadge({ state }: { state?: State }) {
  return state ? (
    <Tag
      color={
        state.completed
          ? "green"
          : state.page_index !== null
            ? "blue"
            : undefined
      }
    >
      {readingStatus(state)}
      {readingContentChanged(state)
        ? " · 内容已变化"
        : state.parsing_stale
          ? " · 页序待核对"
          : ""}
    </Tag>
  ) : null;
}
export function ReadingControls({
  state,
  onChanged,
}: {
  state: State;
  onChanged: () => void;
}) {
  const { verified } = usePermissions();
  const [draft, setDraft] = useState<{
      base: State;
      action: ReadingAction;
      key: string;
      accept: boolean;
      acceptParsing: boolean;
      blocked: boolean;
      current: State | null;
    } | null>(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState<unknown>(null);
  const lock = useRef(false);
  useDirty(!!draft);
  const names = {
    complete: "标记完成",
    uncomplete: "取消完成",
    reset: "重置阅读状态",
  };
  async function save() {
    if (!verified || !draft || lock.current || draft.blocked) return;
    lock.current = true;
    setBusy(true);
    setError(null);
    try {
      await request(
        `/editions/${encodeURIComponent(state.edition_id)}/reading-state`,
        "PUT",
        readingRequest(
          draft.base,
          draft.action,
          draft.key,
          draft.accept,
          draft.acceptParsing,
        ),
      );
      setDraft(null);
      onChanged();
    } catch (e) {
      setError(e);
      if (e instanceof ApiError && e.status === 409) {
        const current = (e.details as { current_state?: State })?.current_state;
        setDraft({ ...draft, blocked: true, current: current || null });
      }
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  function open(action: ReadingAction) {
    setError(null);
    setDraft({
      base: state,
      action,
      key: randomIdentity(),
      accept: false,
      acceptParsing: false,
      blocked: false,
      current: null,
    });
  }
  return (
    <section aria-label="阅读状态" className="reading-status">
      <Space wrap>
        <ReadingBadge state={state} />
        <span>{readingProgress(state)}</span>
        <span>
          最近阅读：
          {state.last_read_at === null ? "尚无记录" : time(state.last_read_at)}
        </span>
      </Space>
      <Space wrap>
        <Button
          onClick={() => open(state.completed ? "uncomplete" : "complete")}
        >
          {state.completed ? "取消完成" : "标记完成"}
        </Button>
        <Button onClick={() => open("reset")}>重置阅读状态</Button>
      </Space>
      <Modal
        title={draft ? names[draft.action] : "阅读状态"}
        open={!!draft}
        onCancel={() => !busy && setDraft(null)}
        mask={{ closable: !busy }}
        keyboard={!busy}
        closable={!busy}
        footer={
          <Space>
            {!verified && (
              <Button
                disabled={false}
                onClick={() =>
                  window.dispatchEvent(new Event("actor-session-refresh"))
                }
              >
                重试权限读取
              </Button>
            )}
            <Button disabled={busy} onClick={() => setDraft(null)}>
              返回
            </Button>
            <Button
              type="primary"
              loading={busy}
              disabled={
                !verified ||
                !!draft?.blocked ||
                (!!draft &&
                  readingContentChanged(draft.base) &&
                  !draft.accept) ||
                (!!draft?.base.parsing_stale && !draft.acceptParsing)
              }
              onClick={() => void save()}
            >
              保存阅读状态
            </Button>
          </Space>
        }
      >
        {draft && (
          <>
            <p>
              {names[draft.action]}只影响本账号的这个版本，不会修改其他版本。
            </p>
            {draft.action === "reset" && (
              <p>清除页码和完成状态，保留阅读次数与最近阅读时间。</p>
            )}
            {draft.action === "uncomplete" && (
              <p>只取消完成，保留页码、阅读次数和最近阅读时间。</p>
            )}
            {readingContentChanged(draft.base) && (
              <Alert
                type="warning"
                showIcon
                title="文件内容已变化，旧进度仍保留"
                description={
                  <>
                    <p>
                      接受当前内容版本后，会先清除旧页码和完成状态，再执行“
                      {names[draft.action]}”；次数和最近阅读时间保留。
                    </p>
                    <Checkbox
                      checked={draft.accept}
                      disabled={busy || draft.blocked}
                      onChange={(e) =>
                        setDraft({ ...draft, accept: e.target.checked })
                      }
                    >
                      接受当前内容版本并清除旧进度
                    </Checkbox>
                  </>
                }
              />
            )}
            {draft.base.parsing_stale && (
              <Alert
                type="warning"
                showIcon
                title="页序已变化，旧阅读位置仍保留"
                description={
                  <>
                    <p>
                      核对当前页序后，清除旧页码和完成状态，再执行本次操作；阅读次数和最近阅读时间保留。
                    </p>
                    <Checkbox
                      checked={draft.acceptParsing}
                      disabled={busy || draft.blocked}
                      onChange={(e) =>
                        setDraft({ ...draft, acceptParsing: e.target.checked })
                      }
                    >
                      已核对页序，接受并清除旧进度
                    </Checkbox>
                  </>
                }
              />
            )}
            {draft.blocked ? (
              <Alert
                type="warning"
                showIcon
                title="阅读状态、内容或页序已变化，未自动重试"
                description={
                  <>
                    {draft.current && (
                      <p>
                        当前：{readingStatus(draft.current)} ·{" "}
                        {readingProgress(draft.current)}
                      </p>
                    )}
                    <p>
                      原操作保留，请核对后明确读取当前状态，再决定是否重新操作。
                    </p>
                    <Button
                      disabled={busy}
                      onClick={() => {
                        setDraft(null);
                        onChanged();
                      }}
                    >
                      读取当前状态并返回核对
                    </Button>
                  </>
                }
              />
            ) : error ? (
              <Alert
                type="error"
                showIcon
                title={
                  error instanceof Error
                    ? error.message
                    : "保存失败，原请求身份保留，可重试"
                }
              />
            ) : null}
          </>
        )}
      </Modal>
    </section>
  );
}
