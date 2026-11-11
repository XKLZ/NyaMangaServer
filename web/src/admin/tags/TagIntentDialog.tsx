import { useEffect, useRef, useState } from "react";
import { Alert, Button, Modal, Space } from "antd";
import { tagCatalog } from "../../shared/tagCatalog";
import type {
  CatalogEdition,
  CharacterRelation,
} from "../../shared/tagCatalog";
import type { TagIntent } from "./tagCatalogFlow";
import { ErrorNotice } from "../common";
import { relationLabel } from "./tagCatalogFlow";
export function TagIntentDialog({
  initial,
  anchor,
  library,
  onClose,
  onChanged,
}: {
  initial: TagIntent;
  anchor: string;
  library: string;
  onClose: () => void;
  onChanged: () => void;
}) {
  const [intent, setIntent] = useState(initial),
    [busy, setBusy] = useState(false),
    [error, setError] = useState<unknown>(null),
    [needsReview, setNeedsReview] = useState(false),
    [current, setCurrent] = useState<{
      revision: number;
      label: string;
    } | null>(null);
  const abort = useRef<AbortController | null>(null);
  useEffect(() => () => abort.current?.abort(), []);
  const verb =
    intent.kind === "global"
      ? intent.decision === "confirmed"
        ? "将此角色关联到该原作"
        : intent.decision === "excluded"
          ? "排除此关联"
          : "恢复自动匹配"
      : intent.suppressed
        ? "仅此版本取消自动匹配"
        : "恢复此版本自动匹配";
  async function submit() {
    if (busy || needsReview) return;
    setBusy(true);
    setError(null);
    try {
      if (intent.kind === "global")
        await tagCatalog.relation(
          intent.parody,
          intent.character,
          intent.expected,
          intent.decision,
        );
      else
        await tagCatalog.suppression(
          intent.edition!,
          intent.parody,
          intent.character,
          intent.expected,
          !!intent.suppressed,
        );
      onChanged();
      onClose();
    } catch (error) {
      setError(error);
      setNeedsReview(true);
    } finally {
      setBusy(false);
    }
  }
  async function check() {
    setBusy(true);
    setCurrent(null);
    setError(null);
    abort.current?.abort();
    const c = new AbortController();
    abort.current = c;
    try {
      const other = anchor === intent.parody ? intent.character : intent.parody;
      if (intent.kind === "global") {
        const list = await tagCatalog.page<CharacterRelation>(
          `/tag-catalog/${encodeURIComponent(anchor)}/relations`,
          {
            library_id: library,
            view: "all",
            related_tag_id: other,
            page: 1,
            page_size: 1,
          },
          c.signal,
        );
        const r = list.items.find(
          (r) =>
            r.parody.id === intent.parody &&
            r.character.id === intent.character,
        );
        if (!r)
          throw Error("当前没有可核对的关系记录，请保留意图并重新选择目标。");
        if (!c.signal.aborted)
          setCurrent({
            revision: r.revision,
            label: relationLabel(r.state, r.state_label),
          });
      } else {
        // Reread only this title and pair. Never scan a library to recover CAS.
        let found = false;
        for (const partition of (intent.suppressed
          ? ["all"]
          : ["suppressed", "all"]) as ("all" | "suppressed")[]) {
          const list = await tagCatalog.page<CatalogEdition>(
            `/tag-catalog/${encodeURIComponent(anchor)}/editions`,
            {
              library_id: library,
              related_tag_id: other,
              partition,
              q: intent.editionQuery || intent.editionLabel,
              page: 1,
              page_size: 100,
            },
            c.signal,
          );
          const r = list.items
            .find((e) => e.id === intent.edition)
            ?.relations.find(
              (r) =>
                r.parody_id === intent.parody &&
                r.character_id === intent.character,
            );
          if (r) {
            found = true;
            if (!c.signal.aborted)
              setCurrent({
                revision: r.suppression_revision,
                label: r.suppressed
                  ? "此版本已取消自动匹配"
                  : "此版本保留自动匹配",
              });
            break;
          }
        }
        if (!found)
          throw Error(
            "此版本已离开当前标签范围，无法取得提交版本；请取消并在有效分区重新核对。",
          );
      }
    } catch (error) {
      if (!c.signal.aborted) setError(error);
    } finally {
      if (!c.signal.aborted) setBusy(false);
    }
  }
  return (
    <Modal
      open
      title={verb}
      onCancel={() => !busy && onClose()}
      closable={!busy}
      keyboard={!busy}
      mask={{ closable: !busy }}
      footer={
        <Space>
          <Button onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button
            type="primary"
            danger={
              (intent.kind === "global" && intent.decision === "excluded") ||
              (intent.kind === "edition" && intent.suppressed)
            }
            loading={busy}
            disabled={needsReview}
            onClick={() => void submit()}
          >
            {intent.kind === "global" && intent.decision === "confirmed"
              ? "确认归属"
              : `确认${verb}`}
          </Button>
        </Space>
      }
    >
      <p>
        <strong>{intent.parodyLabel}</strong> ↔{" "}
        <strong>{intent.characterLabel}</strong>
      </p>
      {intent.kind === "global" ? (
        <Alert
          type="warning"
          title="全局人工决定 · 影响所有书库"
          description="改变所有书库中这对原作与角色的关联，不增删原始标签。恢复自动匹配后，将按各库现有依据重新匹配。"
        />
      ) : (
        <Alert
          type="info"
          title={`仅针对《${intent.editionLabel}》的自动匹配`}
          description="只影响这一版本的自动匹配，不删除原始标签，不影响其他版本，也不改变全局人工关联。"
        />
      )}
      <ErrorNotice error={error} />
      {needsReview && (
        <div className="tag-review-conflict">
          <Button loading={busy} onClick={() => void check()}>
            读取当前状态以核对
          </Button>
          {current && (
            <>
              <p>当前状态：{current.label}</p>
              <Button
                onClick={() => {
                  setIntent({ ...intent, expected: current.revision });
                  setCurrent(null);
                  setNeedsReview(false);
                  setError(null);
                }}
              >
                保留此操作并采用当前版本
              </Button>
            </>
          )}
        </div>
      )}
    </Modal>
  );
}
