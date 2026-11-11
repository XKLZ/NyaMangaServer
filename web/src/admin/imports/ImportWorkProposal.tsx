import { useRecommendationEnabled } from "../../shared/recommendations";
import { EnhancementStatus } from "../EnhancementStatus";
import { originalTitle } from "../books/bookTitle";
import { suggestionName } from "./importOrganizationPresentation";
/** One concrete result, compact editable members, one fixed action row. */
import { useEffect, useState } from "react";
import {
  Alert,
  App,
  Button,
  Checkbox,
  Pagination,
  Space,
  Tag,
  Typography,
} from "antd";
import { ApiError, request } from "../../shared/api";
import type { WorkProposal } from "../../shared/workDiscovery";
import { proposalRiskNames } from "../../shared/workDiscovery";
import { importProposalTransport } from "../../shared/imports";
import { EditionCover, EditionTitle } from "../EditionIdentity";
import { ErrorNotice, useAction } from "../common";
import { workReviewDirty } from "./importOrganizationFlow";
import type { WorkReviewDraft } from "./importOrganizationFlow";

export function ImportWorkProposal({
  incoming,
  draft,
  onDraft,
  onDone,
  onBusy,
  transport,
  onOpenBook,
  disabled = false,
}: {
  incoming: WorkProposal;
  draft: WorkReviewDraft;
  onDraft: (d: WorkReviewDraft) => void;
  onDone: () => Promise<void>;
  onBusy: (v: boolean) => void;
  transport: ReturnType<typeof importProposalTransport>;
  onOpenBook: (id: string) => void;
  disabled?: boolean;
}) {
  const featureEnabled = useRecommendationEnabled("work_recommendations");
  const p = draft.baseline,
    action = useAction(),
    { modal } = App.useApp();
  const [resolved, setResolved] = useState<WorkProposal | null>(null),
    [page, setPage] = useState(1),
    [showExisting, setShowExisting] = useState(false),
    [conflicted, setConflicted] = useState(false),
    [submitted, setSubmitted] = useState(false);
  const [seriesNames, setSeriesNames] = useState<Record<string, string>>({}),
    [namesError, setNamesError] = useState<unknown>(null),
    [retry, setRetry] = useState(0);
  const current = resolved ?? p,
    stale = incoming.revision !== p.revision || incoming.state !== p.state;
  const affected = [
    ...new Set(
      current.effects
        ?.flatMap((e) => [e.from_series_id, e.to_series_id])
        .filter((v): v is string => !!v) ?? [],
    ),
  ]
    .sort()
    .join(",");
  useEffect(() => {
    onBusy(action.busy);
    return () => onBusy(false);
  }, [action.busy, onBusy]);
  useEffect(() => {
    const controller = new AbortController();
    const ids = affected ? affected.split(",") : [];
    void Promise.resolve().then(async () => {
      setNamesError(null);
      const next: Record<string, string> = {};
      let index = 0;
      const worker = async () => {
        while (index < ids.length && !controller.signal.aborted) {
          const id = ids[index++];
          const value = await request<{ name: string }>(
            `/series/${encodeURIComponent(id)}`,
            "GET",
            undefined,
            false,
            controller.signal,
          );
          next[id] = value.name;
        }
      };
      try {
        await Promise.all([worker(), worker()]);
        if (!controller.signal.aborted) setSeriesNames(next);
      } catch (e) {
        if (!controller.signal.aborted) setNamesError(e);
      }
    });
    return () => controller.abort();
  }, [affected, retry]);
  const visible = p.members.filter((m) => m.role === "new" || showExisting),
    existing = p.members.filter((m) => m.role === "existing").length;
  const valid = draft.selected.length >= (p.kind === "create_work" ? 2 : 1);
  async function accept() {
    if (disabled || !featureEnabled) return;
    await action.run(async () => {
      try {
        let final = current;
        if (workReviewDirty(draft) && !resolved) {
          final = await transport.workResolve(p, draft.selected);
          setResolved(final);
          if (final.target_work_id !== p.target_work_id)
            throw Error(
              `调整后的结果为“${suggestionName(final)}”，请核对后再次提交。`,
            );
        }
        if (final.blocked)
          throw Error("当前成员不能组成该版本组，请调整选择。");
        await transport.workDecide(final, "accept");
        setSubmitted(true);
      } catch (e) {
        if (e instanceof ApiError && e.status === 409) setConflicted(true);
        throw e;
      }
      await onDone();
    }, "");
  }
  function reload() {
    void action.run(async () => {
      const fresh = await transport.workGet(p.id);
      const apply = () => {
        onDraft({
          baseline: fresh,
          selected: draft.selected.filter((id) =>
            fresh.new_edition_ids.includes(id),
          ),
        });
        setResolved(null);
        setConflicted(false);
      };
      if (workReviewDirty(draft))
        modal.confirm({
          title: "核对后使用当前建议？",
          content: "保留仍有效的成员选择，使用当前归属版本重新核对。",
          okText: "使用当前建议",
          cancelText: "保留草稿",
          onOk: apply,
        });
      else apply();
    }, "");
  }
  return (
    <section className="import-work-proposal" aria-label="版本方案">
      <EnhancementStatus feature="work_recommendations" />
      <div className="import-proposal-heading">
        <Typography.Title level={4}>
          {p.kind === "create_work" ? "组成版本组" : "加入版本组"}：
          {suggestionName(current)}
        </Typography.Title>
        {existing > 0 && (
          <Button
            type="text"
            aria-expanded={showExisting}
            onClick={() => {
              setShowExisting(!showExisting);
              setPage(1);
            }}
          >
            {showExisting ? "收起" : "查看"}已有 {existing} 个版本
          </Button>
        )}
        {(stale || conflicted) && (
          <Alert
            type="warning"
            title="归属或建议需要重新核对，成员选择仍保留"
            action={<Button onClick={reload}>核对当前建议</Button>}
          />
        )}
        {!valid && (
          <Alert
            type="warning"
            title={
              p.kind === "create_work"
                ? "请选择至少两个版本"
                : "请选择要加入的版本"
            }
          />
        )}
        {current.risks
          ?.filter(
            (r) =>
              r === "explicit_member_conflict" ||
              r === "cross_series_requires_manual_organization" ||
              r === "target_work_capacity" ||
              current.blocked,
          )
          .map((r) => (
            <Alert
              key={r}
              type="warning"
              title={proposalRiskNames[r] ?? "当前归属无法合并，请调整成员"}
            />
          ))}
        {affected && (
          <div className="import-effect-facts">
            {[
              ...new Set(
                current.effects
                  ?.filter((e) => e.from_series_id !== e.to_series_id)
                  .map(
                    (e) =>
                      `${e.from_series_id ? (seriesNames[e.from_series_id] ?? "正在加载系列名称…") : "未归入系列"} → ${e.to_series_id ? (seriesNames[e.to_series_id] ?? "正在加载系列名称…") : "未归入系列"}`,
                  ),
              ),
            ].map((text) => (
              <div key={text}>{text}</div>
            ))}
          </div>
        )}
        <ErrorNotice
          error={action.error || namesError}
          retry={namesError ? () => setRetry((r) => r + 1) : reload}
        />
      </div>
      <div className="import-proposal-members">
        <div className="import-proposal-grid">
          {visible.slice((page - 1) * 25, page * 25).map((m) => (
            <div
              className={`version-member ${m.role === "new" && !draft.selected.includes(m.id) ? "version-excluded" : ""}`}
              key={m.id}
              data-testid={`review-version-${m.id}`}
            >
              {m.role === "new" && (
                <Checkbox
                  aria-label={`包含 ${originalTitle(m)}`}
                  disabled={disabled || action.busy || stale}
                  checked={draft.selected.includes(m.id)}
                  onChange={(e) => {
                    onDraft({
                      ...draft,
                      selected: e.target.checked
                        ? [...draft.selected, m.id]
                        : draft.selected.filter((id) => id !== m.id),
                    });
                    setResolved(null);
                  }}
                />
              )}
              <EditionCover
                member={m}
                disabled={disabled || action.busy}
                onOpen={onOpenBook}
              />
              <div className="version-member-text">
                <EditionTitle member={m} />
                <Space size={4} wrap>
                  <Tag>{m.role === "existing" ? "已有版本" : "本次加入"}</Tag>
                  {m.version_labels?.map((label) => (
                    <Tag key={label}>{label}</Tag>
                  ))}
                </Space>
              </div>
            </div>
          ))}
        </div>
      </div>
      {visible.length > 25 && (
        <Pagination
          size="small"
          current={page}
          total={visible.length}
          pageSize={25}
          showSizeChanger={false}
          onChange={setPage}
        />
      )}
      <div className="import-proposal-footer">
        {submitted && !action.busy && (
          <Button onClick={() => void action.run(onDone, "")}>
            加载下一项
          </Button>
        )}
        <Space wrap>
          <Button
            type="primary"
            loading={action.busy}
            disabled={
              !featureEnabled ||
              disabled ||
              p.state !== "pending" ||
              stale ||
              conflicted ||
              submitted ||
              !valid ||
              current.blocked ||
              !!namesError ||
              (affected !== "" &&
                affected.split(",").some((id) => !seriesNames[id]))
            }
            onClick={() => void accept()}
          >
            {p.kind === "create_work" ? "组成版本组" : "加入版本组"}
          </Button>
          <Button
            disabled={
              disabled ||
              p.state !== "pending" ||
              action.busy ||
              stale ||
              submitted
            }
            onClick={() =>
              void action.run(async () => {
                await transport.workDecide(p, "dismiss");
                await onDone();
              }, "")
            }
          >
            不采用
          </Button>
        </Space>
      </div>
    </section>
  );
}
