import { usePermissions, EditOnly } from "../../shared/permissions";
import { useEffect, useState } from "react";
import {
  App as AntApp,
  Button,
  Dropdown,
  Empty,
  Popover,
  Space,
  Spin,
  Tag,
  Typography,
} from "antd";
import { DeleteOutlined, MoreOutlined, PlusOutlined } from "@ant-design/icons";
import { ApiError, request } from "../../shared/api";
import { organization } from "../../shared/organization";
import type {
  EditionMember,
  Members,
  OrgContext,
} from "../../shared/organization";
import { ErrorNotice, query, useAction, useDirty, FormLock } from "../common";
import { VersionLabelsInput } from "../VersionLabelsInput";
import {
  Pager,
  useVersionFeedback,
  useVersionRemote as useRemote,
} from "./versionManagementViews";
import { cleanBookTitle, originalTitle } from "../books/bookTitle";
import { EditionCover, EditionTitle } from "../EditionIdentity";
import { VersionActionDialog } from "./VersionActions";
import type { VersionAction } from "./VersionActions";

export function GroupMembers({
  work,
  refresh,
  onRefresh,
  onLoaded,
}: {
  work: string;
  refresh: number;
  onRefresh: () => void;
  onLoaded: (work: string) => void;
}) {
  const [page, setPage] = useState(1);
  const members = useRemote<Members<EditionMember>>(
    `/works/${work}/editions?${query({ page, page_size: 25, refresh })}`,
  );
  const context = useRemote<OrgContext>(
    `/work-organization/context?work_ids=${encodeURIComponent(work)}&refresh=${refresh}`,
  );
  const action = useAction(),
    { modal } = AntApp.useApp();
  useVersionFeedback(action);
  const [labelDraft, setLabelDraft] = useState<{
    member: EditionMember;
    values: string[];
    expected: Record<string, number>;
  } | null>(null);
  const [labelBlocked, setLabelBlocked] = useState(false);
  const [dialog, setDialog] = useState<{
    mode: VersionAction;
    base: OrgContext;
    member?: EditionMember;
  } | null>(null);
  const labelChanged =
    !!labelDraft &&
    JSON.stringify(labelDraft.values) !==
      JSON.stringify(labelDraft.member.version_labels);
  useDirty(labelChanged || !!dialog || action.busy);
  const group = context.data?.works.find((w) => w.id === work);
  const editable = usePermissions().edit(group?.library_id || "");
  useEffect(() => {
    if (group) onLoaded(group.id);
  }, [group, onLoaded]);
  function reload() {
    members.reload();
    context.reload();
    onRefresh();
  }
  function closeLabels() {
    if (action.busy) return;
    if (labelChanged)
      modal.confirm({
        title: "放弃尚未保存的版本标签？",
        okText: "放弃修改",
        cancelText: "继续编辑",
        onOk: () => setLabelDraft(null),
      });
    else setLabelDraft(null);
  }
  async function preferred(m: EditionMember) {
    await action.run(async () => {
      if (!context.data) throw new Error("请等待当前版本组加载完成");
      await organization.change({
        action: "work_preferred",
        id: work,
        preferred_edition_id: m.id,
        expected_revisions: context.data.expected_revisions,
      });
      reload();
    }, "已设为主版本");
  }
  function openAction(mode: VersionAction, member?: EditionMember) {
    if (context.data && !labelDraft && !action.busy)
      setDialog({ mode, base: context.data, member });
  }
  const busy = action.busy || !!dialog;
  return (
    <>
      <div className="version-section-heading version-centered-heading">
        <div>
          <Typography.Title level={4}>
            {group ? cleanBookTitle(originalTitle(group)) : "版本组成员"}
          </Typography.Title>
          <div className="version-facts">
            <Tag>{members.data?.total ?? 0} 个版本</Tag>
            <Tag color={group?.series_id ? "blue" : undefined}>
              {group?.series_id ? "已归属系列" : "未归属系列"}
            </Tag>
          </div>
        </div>
        <EditOnly library={group?.library_id || ""}>
          <Dropdown
            menu={{
              items: [
                {
                  key: "edition_move",
                  label: "加入版本",
                  disabled: !group || group.edition_count >= 100,
                },
                { key: "work_name", label: "修改组名" },
                { key: "work_merge", label: "合并其他作品" },
                {
                  key: "work_separate",
                  label: "移除该组",
                  danger: true,
                  disabled: !group || group.edition_count < 2,
                },
                { type: "divider" },
                { key: "generate", label: "生成智能标签" },
              ],
              onClick: ({ key }) => {
                if (key === "generate" && context.data)
                  void action.run(async () => {
                    await request(`/works/${work}/version-labels`, "POST", {
                      expected_revisions: context.data!.expected_revisions,
                      restore_edition_ids: [],
                    });
                    reload();
                  }, "已生成智能标签，人工值保持不变");
                else openAction(key as VersionAction);
              },
            }}
          >
            <Button
              disabled={busy || !!labelDraft || !group}
              icon={<MoreOutlined />}
              aria-label="版本组更多操作"
            />
          </Dropdown>
        </EditOnly>
      </div>
      <ErrorNotice error={action.error} />
      <ErrorNotice
        error={members.error || context.error}
        retry={() => {
          members.reload();
          context.reload();
        }}
      />
      <div className="version-scroll">
        <Spin spinning={members.loading}>
          <div className="version-member-list">
            {members.data?.items.map((m) => (
              <div key={m.id} className="version-member">
                <EditionCover member={m} />
                <div className="version-member-text">
                  <EditionTitle member={m} />
                  <div className="version-identity">
                    {m.preferred ? (
                      <Tag color="purple">主版本</Tag>
                    ) : (
                      <Tag>
                        {editable ? (
                          <button
                            type="button"
                            disabled={busy || !!labelDraft}
                            className="version-tag-toggle"
                            aria-label={`将 ${originalTitle(m)} 设为主版本`}
                            onClick={() => void preferred(m)}
                          >
                            副版本
                          </button>
                        ) : (
                          "副版本"
                        )}
                      </Tag>
                    )}
                  </div>
                  <div className="version-labels">
                    {(
                      m.label_state?.items ??
                      m.version_labels.map((label) => ({
                        label,
                        origin: "manual",
                      }))
                    ).map((l, i) => (
                      <Tag
                        key={`${l.origin}-${i}`}
                        color={l.origin === "automatic" ? "cyan" : undefined}
                        title={
                          l.origin === "automatic"
                            ? "自动维护的预设标签"
                            : "人工标签"
                        }
                      >
                        {l.label}
                      </Tag>
                    ))}
                    {editable && (
                      <Popover
                        open={labelDraft?.member.id === m.id}
                        placement="bottom"
                        trigger="click"
                        onOpenChange={(open) => {
                          if (open && context.data && !busy && !labelDraft) {
                            setLabelBlocked(false);
                            action.setError(null);
                            setLabelDraft({
                              member: m,
                              values: [...m.version_labels],
                              expected: { ...context.data.expected_revisions },
                            });
                          }
                        }}
                        title="编辑版本标签"
                        content={
                          labelDraft?.member.id === m.id && (
                            <div className="version-label-popover">
                              <FormLock busy={action.busy}>
                                <VersionLabelsInput
                                  value={labelDraft.values}
                                  onChange={(values) =>
                                    setLabelDraft({ ...labelDraft, values })
                                  }
                                  label="版本标签"
                                />
                              </FormLock>
                              <ErrorNotice error={action.error} />
                              {labelBlocked && (
                                <Button
                                  disabled={action.busy}
                                  onClick={() =>
                                    void action.run(async () => {
                                      const current =
                                        await organization.context([m.id]);
                                      const edition = current.editions.find(
                                        (e) => e.id === m.id,
                                      );
                                      modal.confirm({
                                        title: "核对当前版本标签",
                                        content: `当前标签：${edition?.version_labels.join("、") || "无"}。保留输入，确认后使用当前版本重试。`,
                                        okText: "确认并保留输入",
                                        cancelText: "继续查看",
                                        onOk: () => {
                                          setLabelDraft((d) =>
                                            d
                                              ? {
                                                  ...d,
                                                  expected: {
                                                    ...current.expected_revisions,
                                                  },
                                                }
                                              : d,
                                          );
                                          setLabelBlocked(false);
                                        },
                                      });
                                    }, "")
                                  }
                                >
                                  重新核对
                                </Button>
                              )}
                              <Space>
                                <Button
                                  disabled={action.busy}
                                  onClick={closeLabels}
                                >
                                  取消
                                </Button>
                                <Button
                                  type="primary"
                                  loading={action.busy}
                                  disabled={!labelChanged || labelBlocked}
                                  onClick={() =>
                                    void action.run(async () => {
                                      try {
                                        await organization.change({
                                          action: "edition_labels",
                                          edition_id: m.id,
                                          version_labels: labelDraft.values,
                                          expected_revisions:
                                            labelDraft.expected,
                                        });
                                      } catch (error) {
                                        if (
                                          error instanceof ApiError &&
                                          error.status === 409
                                        )
                                          setLabelBlocked(true);
                                        throw error;
                                      }
                                      setLabelDraft(null);
                                      reload();
                                    }, "人工标签已保存")
                                  }
                                >
                                  保存
                                </Button>
                              </Space>
                            </div>
                          )
                        }
                      >
                        <Button
                          className="version-quick-action"
                          size="small"
                          disabled={
                            busy ||
                            (!!labelDraft && labelDraft.member.id !== m.id)
                          }
                          icon={<PlusOutlined />}
                          aria-label={`编辑 ${originalTitle(m)} 的版本标签`}
                        />
                      </Popover>
                    )}
                  </div>
                  {m.label_state?.needs_review && (
                    <Typography.Text type="secondary">
                      版本差异待核对
                    </Typography.Text>
                  )}
                </div>
                {editable && (
                  <Button
                    danger
                    className="version-quick-action"
                    disabled={
                      busy || !!labelDraft || !group || group.edition_count < 2
                    }
                    icon={<DeleteOutlined />}
                    aria-label={`移出 ${originalTitle(m)}`}
                    onClick={() => openAction("work_split", m)}
                  />
                )}
              </div>
            ))}
          </div>
          {!members.loading && members.data?.total === 0 && (
            <Empty description="尚无成员" />
          )}
        </Spin>
      </div>
      <Pager data={members.data} onChange={setPage} />
      {dialog && (
        <VersionActionDialog
          mode={dialog.mode}
          base={dialog.base}
          group={dialog.base.works.find((w) => w.id === work)!}
          member={dialog.member}
          onClose={() => setDialog(null)}
          onDone={() => {
            setDialog(null);
            reload();
          }}
        />
      )}
    </>
  );
}
