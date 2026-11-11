import { useRecommendationEnabled } from "../../shared/recommendations";
import { EnhancementStatus } from "../EnhancementStatus";
import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Collapse,
  Space,
  Tag,
  Typography,
} from "antd";
import { discovery, proposalRiskNames } from "../../shared/workDiscovery";
import type { WorkProposal, ProposalMember } from "../../shared/workDiscovery";
import { ErrorNotice, useAction, useDirty } from "../common";
import { useVersionFeedback } from "./versionManagementViews";
import { cleanBookTitle, originalTitle } from "../books/bookTitle";
import { EditionCover, EditionTitle } from "../EditionIdentity";
export function ProposalEditor({
  proposal: incoming,
  onDone,
  selection,
  onSelection,
}: {
  proposal: WorkProposal;
  onDone: (p: WorkProposal) => void;
  selection?: string[];
  onSelection?: (ids: string[]) => void;
}) {
  const featureEnabled = useRecommendationEnabled("work_recommendations");
  const [proposal, setProposal] = useState(incoming);
  const stale =
    incoming.revision !== proposal.revision ||
    incoming.state !== proposal.state;
  const [selected, setSelected] = useState(
    selection ?? proposal.new_edition_ids,
  );
  const [resolved, setResolved] = useState(proposal);
  const [resolving, setResolving] = useState(false);
  const [resolveError, setResolveError] = useState<unknown>(null);
  const sequence = useRef(0);
  const action = useAction();
  useVersionFeedback(action);
  const dirty = selected.join() !== proposal.new_edition_ids.join();
  const release = useDirty(dirty);
  useEffect(() => {
    if (!featureEnabled) return;
    const token = ++sequence.current;
    if (selected.join() === proposal.new_edition_ids.join()) {
      // Returning to the full frozen selection cancels an in-flight subset check.
      void Promise.resolve().then(() => {
        if (sequence.current === token) {
          setResolving(false);
          setResolveError(null);
        }
      });
      return;
    }
    const timer = setTimeout(() => {
      setResolving(true);
      setResolveError(null);
      void discovery
        .resolve(proposal, selected)
        .then((p) => {
          if (sequence.current === token) setResolved(p);
        })
        .catch((e) => {
          if (sequence.current === token) setResolveError(e);
        })
        .finally(() => {
          if (sequence.current === token) setResolving(false);
        });
    }, 200);
    return () => {
      clearTimeout(timer);
      if (sequence.current === token) sequence.current = token + 1;
    };
  }, [proposal, selected, featureEnabled]);
  const current = dirty ? resolved : proposal;
  const selectionMatches =
    current.new_edition_ids.length === selected.length &&
    current.new_edition_ids.every((id) => selected.includes(id));
  const pending = proposal.state === "pending";
  async function decide(kind: "accept" | "dismiss" | "restore" | "undo") {
    if (kind === "accept" && !featureEnabled) return;
    await action.run(async () => {
      const value = await discovery.decide(
        kind === "accept" ? current : proposal,
        kind,
      );
      release();
      onDone(value);
    }, "");
  }
  const effects =
    current.effects?.filter((e) => e.from_series_id || e.to_series_id) ?? [];
  return (
    <div className="version-proposal" aria-label="版本方案">
      <EnhancementStatus feature="work_recommendations" />
      {stale && (
        <Alert
          type="warning"
          title="方案已变化，当前选择仍保留"
          action={
            <Button
              size="small"
              onClick={() => {
                setProposal(incoming);
                setResolved(incoming);
                const next = incoming.new_edition_ids.filter((id) =>
                  selected.includes(id),
                );
                setSelected(next);
                onSelection?.(next);
              }}
            >
              加载当前方案
            </Button>
          }
        />
      )}
      <div className="version-section-heading">
        <Typography.Title level={4}>
          {proposal.kind === "create_work" ? "组成版本组" : "加入版本组"}
        </Typography.Title>
        <Tag color={proposal.tier === "strong" ? "green" : "gold"}>
          {proposal.tier === "strong" ? "较强建议" : "需核对"}
        </Tag>
      </div>
      <Typography.Paragraph>
        保留版本组：
        <strong>
          {cleanBookTitle(
            originalTitle(
              current.members.find(
                (m) => m.work_id === current.target_work_id,
              ) ?? current.members[0],
            ),
          )}
        </strong>
      </Typography.Paragraph>
      {dirty && current.target_work_id !== proposal.target_work_id && (
        <Alert
          type="warning"
          showIcon
          title="保留的版本组已变化"
          description="请核对上方保留对象及系列影响，再采纳当前方案。"
        />
      )}
      {effects.length > 0 && (
        <Alert
          type="info"
          title="系列归属会随目标版本组调整"
          description={effects
            .map(
              (e) =>
                `${e.from_series_id || "无系列"} → ${e.to_series_id || "无系列"}`,
            )
            .join("；")}
        />
      )}
      <ErrorNotice error={action.error} />
      <ErrorNotice error={resolveError} />
      <div className="version-member-list">
        {proposal.members.map((m) => (
          <div
            className={`version-member ${m.role === "new" && !selected.includes(m.id) ? "version-excluded" : ""}`}
            key={m.id}
          >
            {pending && m.role === "new" && (
              <Checkbox
                aria-label={`包含 ${originalTitle(m)}`}
                checked={selected.includes(m.id)}
                disabled={action.busy}
                onChange={(e) => {
                  setResolveError(null);
                  const next = e.target.checked
                    ? [...selected, m.id]
                    : selected.filter((id) => id !== m.id);
                  setSelected(next);
                  onSelection?.(next);
                }}
              />
            )}
            <EditionCover member={m} />
            <div className="version-member-text">
              <EditionTitle member={m} />
              <MemberFacts member={m} />
              <Tag>{m.role === "existing" ? "已有版本" : "新增版本"}</Tag>
            </div>
          </div>
        ))}
      </div>
      <Collapse
        ghost
        size="small"
        items={[
          {
            key: "why",
            label: "匹配说明与风险",
            children: (
              <>
                <p>
                  综合封面和题名证据，加入时检查全部已有版本。建议仍需你核对内容。
                </p>
                {current.risks?.length ? (
                  current.risks.map((r) => (
                    <p key={r}>
                      {proposalRiskNames[r] ?? "存在需人工核对的内容差异"}
                    </p>
                  ))
                ) : (
                  <p>未发现明确的组织冲突。</p>
                )}
                <p>
                  采纳后自动维护可自动管理的预设标签；已有人工标签保持不变。
                </p>
              </>
            ),
          },
        ]}
      />
      <div className="version-sticky-actions">
        <Space wrap>
          {pending ? (
            <>
              <Button
                type="primary"
                loading={action.busy || resolving}
                disabled={
                  !featureEnabled ||
                  stale ||
                  !selectionMatches ||
                  !!resolveError ||
                  current.blocked ||
                  !selected.length
                }
                onClick={() => void decide("accept")}
              >
                {proposal.kind === "create_work" ? "组成版本组" : "加入版本组"}
              </Button>
              <Button
                disabled={action.busy}
                onClick={() => void decide("dismiss")}
              >
                忽略
              </Button>
              {(resolving || !selectionMatches) && !resolveError && (
                <Typography.Text type="secondary">
                  正在核对成员…
                </Typography.Text>
              )}
            </>
          ) : (
            <>
              <Tag>
                {proposal.state === "accepted"
                  ? "已采纳"
                  : proposal.state === "dismissed"
                    ? "已忽略"
                    : "历史方案"}
              </Tag>
              {proposal.state === "dismissed" && (
                <Button
                  onClick={() => void decide("restore")}
                  loading={action.busy}
                >
                  恢复建议
                </Button>
              )}
              {proposal.can_undo && (
                <Button
                  onClick={() => void decide("undo")}
                  loading={action.busy}
                >
                  撤销整理
                </Button>
              )}
            </>
          )}
        </Space>
      </div>
    </div>
  );
}
function MemberFacts({ member }: { member: ProposalMember }) {
  return (
    <div className="version-facts">
      {member.version_labels?.map((l) => (
        <Tag key={l}>{l}</Tag>
      ))}
    </div>
  );
}
