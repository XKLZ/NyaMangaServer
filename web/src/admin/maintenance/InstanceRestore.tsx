import { randomIdentity } from "../../shared/randomIdentity";
/** §2.1/§3.6: result and mapping first; one final replace confirmation.
 * Ant Design theme, 8/16/24px groups. Fingerprints and object counts stay secondary. */
import {
  Alert,
  Button,
  Checkbox,
  Descriptions,
  Empty,
  Grid,
  Modal,
  Select,
  Space,
  Steps,
} from "antd";
import { useEffect, useRef, useState } from "react";
import { ApiError, request } from "../../shared/api";
import type {
  InstanceOperation,
  InstancePreview,
  RestoreDetail,
} from "../../shared/instanceFlow";
import {
  initialMappings,
  instanceActive,
  instanceErrors,
} from "../../shared/instanceFlow";
import { healthBytes } from "../../shared/healthFlow";
import { ErrorNotice } from "../common";
import type { RestoreDraft } from "./instanceDrafts";
export function InstanceRestore({
  detail,
  draft,
  onDraft,
  busy,
  run,
  reload,
  error,
}: {
  detail: RestoreDetail;
  draft: RestoreDraft | undefined;
  onDraft: (d: RestoreDraft) => void;
  busy: boolean;
  run: (work: () => Promise<void>) => Promise<void>;
  reload: () => void;
  error: unknown;
}) {
  const op = detail.operation;
  const desktop = !!Grid.useBreakpoint().md;
  const [confirm, setConfirm] = useState<{
    revision: number;
    key: string;
    preview: InstancePreview;
  } | null>(null);
  const submitted = useRef(false);
  useEffect(() => {
    if (!draft && detail.summary)
      onDraft({
        revision: op.revision,
        mappings: initialMappings(detail),
        acknowledge: !!detail.preview,
        preview: detail.preview,
        blocked: false,
        dirty: false,
      });
  }, [detail, draft, onDraft, op.revision]);
  const change = (patch: Partial<RestoreDraft>) => {
    if (draft) onDraft({ ...draft, ...patch, preview: null, dirty: true });
  };
  async function preview() {
    if (!draft) return;
    await run(async () => {
      try {
        const value = await request<InstancePreview>(
          `/maintenance/restores/${op.id}/preview`,
          "POST",
          {
            expected_revision: draft.revision,
            root_mappings: draft.mappings,
            acknowledge_stat_only: true,
          },
        );
        onDraft({
          ...draft,
          revision: value.operation.revision,
          preview: value,
          dirty: false,
          blocked: false,
        });
        reload();
      } catch (e) {
        if (e instanceof ApiError && e.status === 409)
          onDraft({ ...draft, blocked: true });
        throw e;
      }
    });
  }
  async function confirmReplace() {
    if (!confirm || !draft || submitted.current) return;
    submitted.current = true;
    await run(async () => {
      try {
        await request<InstanceOperation>(
          `/maintenance/restores/${op.id}/confirm`,
          "POST",
          { expected_revision: confirm.revision, request_key: confirm.key },
        );
        onDraft({ ...draft, dirty: false, preview: null });
        setConfirm(null);
        reload();
      } catch (e) {
        if (e instanceof ApiError && e.status === 409)
          onDraft({ ...draft, blocked: true });
        throw e;
      }
    });
    submitted.current = false;
  }
  const frozen =
    busy ||
    instanceActive(op.state) ||
    ["awaiting_restart", "completed", "abandoned", "interrupted"].includes(
      op.state,
    );
  const prepared = ["preparing", "awaiting_restart"].includes(op.state);
  const step = ["uploaded", "validating", "pending"].includes(op.state)
    ? 0
    : op.state === "validated"
      ? 1
      : op.state === "previewed"
        ? 2
        : 3;
  return (
    <section aria-label="恢复步骤" className="instance-restore">
      <Steps
        responsive={false}
        size="small"
        current={step}
        items={[
          { title: desktop ? "上传与校验" : "校验" },
          { title: desktop ? "漫画目录映射" : "目录" },
          { title: desktop ? "替换确认" : "确认" },
          { title: desktop ? "重启与结果" : "重启" },
        ]}
      />
      {op.error_code && (
        <Alert
          type="error"
          showIcon
          title={
            instanceErrors[op.error_code] || "恢复未完成，请核对当前阶段。"
          }
        />
      )}
      {op.state === "uploaded" && (
        <Button
          type="primary"
          disabled={busy}
          onClick={() =>
            void run(async () => {
              await request(`/maintenance/restores/${op.id}/validate`, "POST", {
                expected_revision: op.revision,
              });
              reload();
            })
          }
        >
          校验备份包
        </Button>
      )}
      {op.state === "validating" && (
        <p role="status">
          正在校验备份结构、摘要、数据库和凭据配对。可离开后返回查看。
        </p>
      )}
      {op.state === "abandon_requested" && (
        <Alert
          type="info"
          showIcon
          title="正在放弃，等待验证收尾；不会将迟到结果用于恢复。"
        />
      )}
      {op.state === "awaiting_restart" && (
        <Alert
          type="warning"
          showIcon
          title="已准备，尚未恢复"
          description="请通过原有服务管理方式正常停止并重新启动应用。重启激活后旧会话失效，使用备份中的账号密码重新登录，再核对激活结果。"
        />
      )}
      {op.state === "preparing" && (
        <p role="status">
          正在保全目标和准备替换。当前实例仍受保护，尚未恢复。
        </p>
      )}
      {detail.summary && prepared && (
        <details>
          <summary>
            已核对的目录映射 · {detail.summary.source_roots.length} 个书库
          </summary>
          <ul>
            {detail.summary.source_roots.map((root) => {
              const key = detail.root_mappings.find(
                (v) => v.library_id === root.library_id,
              )?.target_root_key;
              return (
                <li key={root.library_id}>
                  {root.display_name}：
                  {key
                    ? detail.target_roots.find((v) => v.root_key === key)
                        ?.display_name || "已配置目录"
                    : "暂不关联"}
                </li>
              );
            })}
          </ul>
        </details>
      )}
      {detail.summary &&
        !prepared &&
        draft &&
        !["completed", "abandoned"].includes(op.state) && (
          <>
            <div className="instance-summary">
              <span>
                {detail.summary.files} 个包内文件 ·{" "}
                {healthBytes(detail.summary.expanded_bytes)}
              </span>
              <span>{detail.summary.source_roots.length} 个书库</span>
            </div>
            <div className="instance-mappings">
              {detail.summary.source_roots.map((root) => (
                <label key={root.library_id}>
                  <span>{root.display_name}</span>
                  <Select
                    aria-label={`${root.display_name}的目标目录`}
                    disabled={frozen}
                    value={
                      draft.mappings.find(
                        (v) => v.library_id === root.library_id,
                      )?.target_root_key ?? ""
                    }
                    options={[
                      { value: "", label: "暂不关联漫画目录" },
                      ...detail.target_roots.map((v) => ({
                        value: v.root_key,
                        label: v.display_name,
                      })),
                    ]}
                    onChange={(value) =>
                      change({
                        mappings: draft.mappings.map((v) =>
                          v.library_id === root.library_id
                            ? { ...v, target_root_key: value || null }
                            : v,
                        ),
                      })
                    }
                  />
                </label>
              ))}
            </div>
            <p className="instance-muted">
              仅核对文件属性，不能证明内容相同。请选择对应漫画及相对结构一致的已配置目录；Docker使用容器内已配置根。暂不关联将保留资料和缓存，阻止相应源文件访问。
            </p>
            <Checkbox
              disabled={frozen}
              checked={draft.acknowledge}
              onChange={(e) => change({ acknowledge: e.target.checked })}
            >
              我已核对关联目录的内容和相对结构；未准备好的书库暂不关联
            </Checkbox>
            {draft.blocked && !confirm ? (
              <Alert
                type="warning"
                showIcon
                title="操作或目标已变化，目录选择保留"
                description={
                  <Button
                    disabled={busy}
                    onClick={() => {
                      onDraft({
                        ...draft,
                        revision: op.revision,
                        blocked: false,
                        preview: null,
                      });
                      setConfirm(null);
                    }}
                  >
                    重新核对当前版本
                  </Button>
                }
              />
            ) : null}
            {!frozen && !draft.preview && (
              <Button
                type="primary"
                disabled={busy || !draft.acknowledge || draft.blocked}
                onClick={() => void preview()}
              >
                预览替换影响
              </Button>
            )}
            {draft.preview && (
              <div className="instance-preview">
                <Descriptions
                  size="small"
                  column={{ xs: 1, sm: 2 }}
                  items={[
                    {
                      key: "linked",
                      label: "对应文件",
                      children: draft.preview.linked_files,
                    },
                    {
                      key: "unlinked",
                      label: "暂不关联书库",
                      children: draft.preview.unlinked_libraries,
                    },
                    {
                      key: "required",
                      label: "额外所需空间",
                      children: healthBytes(
                        draft.preview.space.required_additional_bytes,
                      ),
                    },
                    {
                      key: "available",
                      label: "当前可用空间",
                      children: healthBytes(
                        draft.preview.space.available_bytes,
                      ),
                    },
                  ]}
                />
                <details>
                  <summary>对象数量与空间明细</summary>
                  <Descriptions
                    size="small"
                    column={2}
                    items={Object.entries(draft.preview.object_counts)
                      .filter(([, n]) => n > 0)
                      .map(([key, value]) => ({
                        key,
                        label: key,
                        children: value,
                      }))}
                  />
                  <p>
                    上传 {healthBytes(draft.preview.space.upload_bytes)} ·
                    校验材料 {healthBytes(draft.preview.space.validated_bytes)}{" "}
                    · 准备副本 {healthBytes(draft.preview.space.prepared_bytes)}{" "}
                    · 目标保全{" "}
                    {healthBytes(draft.preview.space.target_preservation_bytes)}{" "}
                    · 临时安装{" "}
                    {healthBytes(draft.preview.space.temporary_install_bytes)} ·
                    安全余量 {healthBytes(draft.preview.space.safety_bytes)}
                  </p>
                </details>
                {!frozen && (
                  <Button
                    type="primary"
                    disabled={busy || draft.blocked}
                    onClick={() =>
                      setConfirm({
                        revision: draft.preview!.operation.revision,
                        key: randomIdentity(),
                        preview: draft.preview!,
                      })
                    }
                  >
                    安排整实例替换
                  </Button>
                )}
              </div>
            )}
          </>
        )}
      {op.state === "completed" && (
        <p>此操作已完成。请以上方主库外激活结果核对实际恢复状态。</p>
      )}
      {op.state === "abandoned" && (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description="已放弃此恢复操作"
        />
      )}
      {!["completed", "abandoned", "abandon_requested"].includes(op.state) && (
        <Button
          danger
          disabled={busy}
          onClick={() =>
            void run(async () => {
              await request(`/maintenance/restores/${op.id}/abandon`, "POST", {
                expected_revision: op.revision,
              });
              onDraft(
                draft
                  ? { ...draft, dirty: false, preview: null }
                  : {
                      revision: op.revision,
                      mappings: [],
                      acknowledge: false,
                      preview: null,
                      blocked: false,
                      dirty: false,
                    },
              );
              reload();
            })
          }
        >
          放弃此恢复
        </Button>
      )}
      <Modal
        title="确认整实例替换"
        open={!!confirm}
        onCancel={() => !busy && setConfirm(null)}
        mask={{ closable: !busy }}
        keyboard={!busy}
        closable={!busy}
        footer={
          <Space>
            <Button disabled={busy} onClick={() => setConfirm(null)}>
              返回核对
            </Button>
            <Button
              danger
              type="primary"
              loading={busy}
              disabled={draft?.blocked}
              onClick={() => void confirmReplace()}
            >
              确认替换并等待重启
            </Button>
          </Space>
        }
        styles={{ body: { maxHeight: "60dvh", overflow: "auto" } }}
      >
        <p>
          这将替换整个应用实例，不合并资料。当前账号将被备份中的账号替换，重启后须重新登录。
        </p>
        <p>
          目标现状会先保全；原运行／排队任务转为暂停，后台自动处理保持暂停。备份不含源漫画、模型或程序。
        </p>
        <p>
          激活后发生新的业务写入将不能整实例回退。需要通过原服务管理方式正常重启，当前尚未恢复。
        </p>
        <p>{confirm?.preview.unlinked_libraries} 个书库暂不关联源文件。</p>
        {draft?.blocked && (
          <Alert
            type="warning"
            title="目标已变化，请返回映射区重新核对并预览。"
          />
        )}
        <ErrorNotice error={draft?.blocked ? null : error} />
      </Modal>
    </section>
  );
}
