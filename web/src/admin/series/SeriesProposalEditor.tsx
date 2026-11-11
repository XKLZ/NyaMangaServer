import { useRecommendationEnabled } from "../../shared/recommendations";
import { EnhancementStatus } from "../EnhancementStatus";
import { useEffect, useRef, useState } from "react";
import {
  Alert,
  App as AntApp,
  Button,
  Checkbox,
  Input,
  Space,
  Spin,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import { LockOutlined, UnlockOutlined } from "@ant-design/icons";
import { ApiError, request } from "../../shared/api";
import { seriesDiscovery } from "../../shared/seriesDiscovery";
import type {
  SeriesProposal,
  SeriesMember,
  SeriesMemberPage,
} from "../../shared/seriesDiscovery";
import {
  canResolveSeries,
  seriesChanged,
  seriesDirty,
  seriesMemberPages,
  seriesOrder,
  rebaseSeriesDraft,
} from "./seriesDiscoveryFlow";
import type { SeriesDraft } from "./seriesDiscoveryFlow";
import { ErrorNotice, useAction } from "../common";
import { Pager, useVersionFeedback } from "../versions/versionManagementViews";
import { originalTitle } from "../books/bookTitle";
import { EditionCover, EditionTitle } from "../EditionIdentity";
import {
  SeriesDragHandle,
  SeriesDragRow,
  SeriesDropZone,
  useSeriesInteractionBusy,
} from "./SeriesDrag";
import { placeSeriesBlock } from "./seriesDragFlow";
export function SeriesProposalEditor({
  incoming,
  draft,
  onDraft,
  onDone,
  onReload,
  onBusy,
  reviewLocked,
  onReviewLocked,
  continuousControls = true,
  reviewMode = false,
  transport,
  onOpenBook,
  externalDisabled = false,
}: {
  incoming: SeriesProposal;
  draft: SeriesDraft;
  onDraft: (d: SeriesDraft) => void;
  onDone: (p: SeriesProposal) => void | Promise<void>;
  onReload: (p: SeriesProposal, draft?: SeriesDraft) => void;
  onBusy: (value: boolean) => void;
  reviewLocked: boolean;
  onReviewLocked: (value: boolean) => void;
  continuousControls?: boolean;
  reviewMode?: boolean;
  externalDisabled?: boolean;
  onOpenBook?: (id: string) => void;
  transport?: {
    get: typeof seriesDiscovery.get;
    compose: typeof seriesDiscovery.compose;
    decide: typeof seriesDiscovery.decide;
    members: (
      id: string,
      page: number,
      signal?: AbortSignal,
    ) => Promise<SeriesMemberPage>;
  };
}) {
  const featureEnabled = useRecommendationEnabled("series_recommendations");
  const interactionBusy = useSeriesInteractionBusy();
  const [showExisting, setShowExisting] = useState(false);
  const [reviewConflict, setReviewConflict] = useState(false);
  const commands = transport ?? seriesDiscovery;
  const p = draft.baseline,
    pending = p.state === "pending",
    dirty = seriesDirty(draft),
    stale = seriesChanged(incoming, p),
    valid = canResolveSeries(draft);
  const [page, setPage] = useState(1),
    [members, setMembers] = useState<Record<string, SeriesMember>>({}),
    [memberError, setMemberError] = useState<unknown>(null),
    [loading, setLoading] = useState(false),
    [retry, setRetry] = useState(0);
  const cache = useRef(new Map<number, SeriesMemberPage>()),
    action = useAction(),
    { modal } = AntApp.useApp();
  useVersionFeedback(action);
  const visibleOrder =
    reviewMode && !showExisting
      ? draft.order.filter((id) => !p.existing_work_ids.includes(id))
      : draft.order;
  const displayIDs = visibleOrder.slice((page - 1) * 25, page * 25),
    needed = seriesMemberPages(p.order, visibleOrder, page).join(",");
  useEffect(() => {
    onBusy(action.busy);
    return () => onBusy(false);
  }, [action.busy, onBusy]);
  useEffect(() => {
    const controller = new AbortController();
    void Promise.resolve().then(async () => {
      setLoading(true);
      setMemberError(null);
      try {
        const pages = needed ? needed.split(",").map(Number) : [];
        let next = 0;
        const worker = async () => {
          while (!controller.signal.aborted && next < pages.length) {
            const index = pages[next++];
            if (cache.current.has(index)) continue;
            const value = transport
              ? await transport.members(p.id, index, controller.signal)
              : await request<SeriesMemberPage>(
                  `/series-proposals/${encodeURIComponent(p.id)}/members?page=${index}&page_size=25`,
                  "GET",
                  undefined,
                  false,
                  controller.signal,
                );
            if (value.revision !== p.revision)
              throw new Error("成员版本已变化，请明确加载当前方案后重试");
            if (!controller.signal.aborted) cache.current.set(index, value);
          }
        };
        await Promise.all([worker(), worker()]);
        if (!controller.signal.aborted)
          setMembers(
            Object.fromEntries(
              [...cache.current.values()]
                .flatMap((v) => v.items)
                .map((m) => [m.work_id, m]),
            ),
          );
      } catch (error) {
        if (!controller.signal.aborted) setMemberError(error);
      } finally {
        if (!controller.signal.aborted) setLoading(false);
      }
    });
    return () => controller.abort();
  }, [needed, p.id, p.revision, retry, transport]);
  async function decide(kind: "accept" | "dismiss" | "restore" | "undo") {
    if (externalDisabled || (kind === "accept" && !featureEnabled)) return;
    await action.run(async () => {
      try {
        const result =
          kind === "accept" && p.kind === "create_series"
            ? await commands.compose(
                p,
                draft.selected,
                draft.name,
                seriesOrder(draft),
              )
            : await commands.decide(
                p,
                kind,
                kind === "accept" ? seriesOrder(draft) : undefined,
              );
        await onDone(result);
      } catch (e) {
        if (reviewMode && e instanceof ApiError && e.status === 409)
          setReviewConflict(true);
        throw e;
      }
    }, "");
  }
  function reload() {
    void action.run(async () => {
      const fresh = await commands.get(p.id);
      const apply = () => {
        cache.current.clear();
        setMembers({});
        setPage(1);
        setRetry((r) => r + 1);
        setReviewConflict(false);
        onReload(
          fresh,
          reviewMode ? rebaseSeriesDraft(draft, fresh) : undefined,
        );
      };
      if (dirty)
        modal.confirm({
          title: reviewMode
            ? "核对后使用当前建议？"
            : "放弃草稿并加载当前方案？",
          content: reviewMode
            ? "保留名称和仍有效的成员选择及顺序；已有成员的相对顺序以当前系列为准。"
            : "名称、成员选择和顺序将替换为当前值。",
          okText: reviewMode ? "保留修改并重新核对" : "加载当前方案",
          cancelText: "保留草稿",
          onOk: apply,
        });
      else apply();
    }, "");
  }
  const scope = `proposal:${p.id}`;
  return (
    <div
      className={`version-proposal series-proposal ${reviewMode ? "import-series-review" : ""}`}
      data-testid={`series-proposal-${p.id}`}
      aria-label="系列方案"
    >
      <EnhancementStatus feature="series_recommendations" />
      <div className="series-proposal-heading">
        <div className="version-section-heading">
          <Typography.Title level={4}>
            {reviewMode
              ? `${p.kind === "create_series" ? "组成系列" : "加入系列"}${p.kind === "add_to_series" ? `：${p.name}` : ""}`
              : p.kind === "create_series"
                ? "组成系列"
                : "加入系列"}
          </Typography.Title>
          {!reviewMode && (
            <Tag color={p.tier === "strong" ? "green" : "gold"}>
              {p.tier === "strong" ? "建议核对" : "需重点核对"}
            </Tag>
          )}
        </div>
        {(!reviewMode || p.kind === "create_series") && (
          <label>
            系列名称
            <Input
              aria-label="系列名称"
              maxLength={200}
              value={draft.name}
              disabled={
                externalDisabled ||
                !pending ||
                p.kind === "add_to_series" ||
                action.busy ||
                interactionBusy
              }
              onChange={(e) => onDraft({ ...draft, name: e.target.value })}
            />
          </label>
        )}
        {reviewMode && p.existing_work_ids.length > 0 && (
          <Button
            type="text"
            aria-expanded={showExisting}
            onClick={() => {
              setShowExisting(!showExisting);
              setPage(1);
            }}
          >
            {showExisting ? "收起" : "查看"}已有 {p.existing_work_ids.length}{" "}
            本作品
          </Button>
        )}
        {!valid && pending && (
          <Alert
            type="warning"
            title={
              !draft.name.trim()
                ? "请填写有效系列名称"
                : p.kind === "create_series"
                  ? "请选择至少两本作品"
                  : "请选择新增作品，并保持已有作品的相对顺序"
            }
          />
        )}
        {(stale || reviewConflict) && (
          <Alert
            type="warning"
            title="方案已变化，草稿仍保留"
            action={<Button onClick={reload}>加载当前方案</Button>}
          />
        )}
        <ErrorNotice error={action.error || memberError} retry={reload} />
      </div>
      <div className="series-proposal-scroll">
        <Spin spinning={loading}>
          <SeriesDropZone
            scope={scope}
            onDrop={(item, drop) => {
              if (
                item.kind !== "proposal" ||
                item.scope !== scope ||
                !pending ||
                stale
              )
                return;
              const order = placeSeriesBlock(draft.order, [item.id], drop);
              onDraft({ ...draft, order });
              const shown =
                reviewMode && !showExisting
                  ? order.filter((id) => !p.existing_work_ids.includes(id))
                  : order;
              setPage(Math.floor(shown.indexOf(item.id) / 25) + 1);
            }}
          >
            {displayIDs.map((id) => {
              const m = members[id];
              if (!m)
                return (
                  <div key={id} className="version-member">
                    正在加载作品…
                  </div>
                );
              const selected = m.existing || draft.selected.includes(id),
                at = seriesOrder(draft).indexOf(id);
              return (
                <SeriesDragRow
                  key={id}
                  scope={scope}
                  id={id}
                  className={`version-member series-proposal-member ${selected ? "" : "version-excluded"}`}
                >
                  {pending && (!reviewMode || !m.existing) && (
                    <Checkbox
                      aria-label={`包含作品：${originalTitle(m)}`}
                      checked={selected}
                      disabled={
                        externalDisabled ||
                        p.kind !== "create_series" ||
                        m.existing ||
                        interactionBusy ||
                        action.busy ||
                        stale
                      }
                      onChange={(e) =>
                        onDraft({
                          ...draft,
                          selected: e.target.checked
                            ? [...draft.selected, id]
                            : draft.selected.filter((v) => v !== id),
                        })
                      }
                    />
                  )}
                  {pending && (!reviewMode || !m.existing) && (
                    <SeriesDragHandle
                      disabled={
                        externalDisabled ||
                        !selected ||
                        m.existing ||
                        action.busy ||
                        interactionBusy ||
                        stale
                      }
                      item={{
                        kind: "proposal",
                        id,
                        scope,
                        label: originalTitle(m),
                        order: [...draft.order],
                      }}
                    />
                  )}
                  <span className="series-position">
                    {at >= 0 ? at + 1 : "—"}
                  </span>
                  <EditionCover
                    onOpen={onOpenBook}
                    disabled={action.busy || interactionBusy}
                    member={{
                      id: m.preferred_edition_id,
                      content_revision: m.content_revision,
                      cover_revision: m.cover_revision,
                    }}
                  />
                  <div className="version-member-text">
                    <EditionTitle member={m} />
                    {reviewMode && (
                      <Tag>{m.existing ? "已有作品" : "本次加入"}</Tag>
                    )}
                  </div>
                </SeriesDragRow>
              );
            })}
          </SeriesDropZone>
        </Spin>
      </div>
      <Pager
        data={{ page, page_size: 25, total: visibleOrder.length }}
        onChange={setPage}
      />
      <div className="version-sticky-actions series-proposal-actions">
        <Space wrap>
          {pending ? (
            <>
              <Button
                type="primary"
                loading={action.busy}
                disabled={
                  !featureEnabled ||
                  externalDisabled ||
                  stale ||
                  reviewConflict ||
                  !valid ||
                  !!memberError ||
                  !!action.error ||
                  loading ||
                  interactionBusy
                }
                onClick={() => void decide("accept")}
              >
                {p.kind === "create_series" ? "组成系列" : "加入系列"}
              </Button>
              <Button
                disabled={
                  externalDisabled ||
                  action.busy ||
                  stale ||
                  !!action.error ||
                  interactionBusy
                }
                onClick={() => void decide("dismiss")}
              >
                {reviewMode ? "不采用" : "忽略方案"}
              </Button>
            </>
          ) : (
            <>
              <Tag>
                {p.state === "accepted"
                  ? "已采纳"
                  : p.state === "dismissed"
                    ? "已忽略"
                    : p.state === "undone"
                      ? "已撤销"
                      : "已过期"}
              </Tag>
              {p.state === "dismissed" && (
                <Button onClick={() => void decide("restore")}>恢复方案</Button>
              )}
              {p.can_undo && (
                <Button onClick={() => void decide("undo")}>
                  撤销本次整理
                </Button>
              )}
            </>
          )}
        </Space>
        {p.kind === "create_series" && continuousControls && (
          <Tooltip
            title={
              reviewLocked
                ? "已锁定：组成或忽略后留在推荐新系列，自动打开下一项。点击解锁恢复普通跳转。"
                : "锁定连续处理：组成或忽略后自动打开下一项推荐。"
            }
            trigger={["hover", "focus"]}
          >
            <Button
              aria-label={reviewLocked ? "解锁连续处理" : "锁定连续处理"}
              aria-pressed={reviewLocked}
              disabled={action.busy || interactionBusy}
              icon={reviewLocked ? <LockOutlined /> : <UnlockOutlined />}
              onClick={() => onReviewLocked(!reviewLocked)}
            />
          </Tooltip>
        )}
      </div>
    </div>
  );
}
