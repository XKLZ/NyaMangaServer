import { usePermissions, EditOnly } from "../../shared/permissions";
import { useState } from "react";
import {
  App as AntApp,
  Button,
  Input,
  Modal,
  Space,
  Spin,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import {
  CloseOutlined,
  DeleteOutlined,
  PlusOutlined,
  SaveOutlined,
  SortAscendingOutlined,
  SwapOutlined,
} from "@ant-design/icons";
import { request } from "../../shared/api";
import { organization } from "../../shared/organization";
import type {
  Command,
  Members,
  OrgContext,
  Series,
  Work,
  OrgResult,
} from "../../shared/organization";
import type { SeriesProposal } from "../../shared/seriesDiscovery";
import { useAction, ErrorNotice, FormLock, query, useDirty } from "../common";
import { OrganizationPicker, prepareCommand } from "../OrganizationControls";
import {
  Pager,
  useVersionFeedback,
  useVersionRemote,
} from "../versions/versionManagementViews";
import { EditionCover, EditionTitle } from "../EditionIdentity";
import { originalTitle } from "../books/bookTitle";
import { SeriesDragHandle, SeriesDragRow, SeriesDropZone } from "./SeriesDrag";
import { placeSeriesBlock } from "./seriesDragFlow";
import type { SeriesDrop } from "./seriesDragFlow";

type Dialog = {
  kind: "add" | "sort" | "delete" | "remove" | "transfer";
  command: Command;
  targets: (Work | Series)[];
  member?: Work;
};
const dialogTitles = {
  add: "添加成员",
  sort: "自动排序",
  delete: "删除系列",
  remove: "移出系列",
  transfer: "转移到其他系列",
};
export function SeriesMembers({
  series,
  page,
  refresh,
  onPage,
  onChanged,
  onJoin,
  disabled = false,
}: {
  series: Series;
  page: number;
  refresh: number;
  onPage: (p: number) => void;
  onChanged: (r?: OrgResult) => void;
  onJoin: (p: SeriesProposal, drop?: SeriesDrop) => Promise<void>;
  disabled?: boolean;
}) {
  const editable = usePermissions().edit(series.library_id);
  const members = useVersionRemote<Members<Work>>(
    `/series/${encodeURIComponent(series.id)}/members?${query({ page, page_size: 25, refresh })}`,
  );
  const action = useAction(),
    { modal } = AntApp.useApp();
  useVersionFeedback(action);
  const [nameDraft, setNameDraft] = useState<{
    value: string;
    original: string;
    revision: number;
  } | null>(null);
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const nameDirty = !!nameDraft && nameDraft.value !== nameDraft.original;
  useDirty(nameDirty || !!dialog?.targets.length || action.busy);
  const busy = disabled || action.busy || !!dialog;
  function base(expected?: Record<string, number>): OrgContext {
    return {
      works: [],
      editions: [],
      series: [],
      snapshot_revision: "",
      expected_revisions: expected ?? {
        ...members.data?.expected_revisions,
        [`series:${series.id}`]: series.revision,
      },
    };
  }
  function close() {
    if (action.busy) return;
    if (dialog?.targets.length)
      modal.confirm({
        title: "放弃未提交的选择？",
        okText: "放弃选择",
        cancelText: "继续编辑",
        onOk: () => setDialog(null),
      });
    else setDialog(null);
  }
  async function open(kind: Dialog["kind"], member?: Work) {
    await action.run(async () => {
      const c: Command = {
        action: {
          add: "series_add",
          sort: "series_sort",
          delete: "series_delete",
          remove: "series_remove",
          transfer: "series_transfer",
        }[kind],
        id: series.id,
        work_ids: member ? [member.id] : [],
        expected_revisions: {},
      };
      setDialog({
        kind,
        member,
        command: await prepareCommand(c, base()),
        targets: [],
      });
    }, "");
  }
  async function submit() {
    await action.run(async () => {
      if (!dialog) return;
      const c = { ...dialog.command };
      if (dialog.kind === "add") c.work_ids = dialog.targets.map((t) => t.id);
      if (dialog.kind === "transfer")
        c.target_series_id = dialog.targets[0]?.id;
      const expected = {
        ...Object.fromEntries(
          dialog.targets.map((t) => [
            `${dialog.kind === "transfer" ? "series" : "work"}:${t.id}`,
            t.revision,
          ]),
        ),
        ...c.expected_revisions,
      };
      const command = await prepareCommand(c, base(expected));
      const result = await organization.change(command);
      setDialog(null);
      members.reload();
      onChanged(result);
    }, "系列已更新");
  }
  async function saveName() {
    if (!nameDraft || !nameDirty || action.error) return;
    await action.run(async () => {
      const c = await prepareCommand(
        {
          action: "series_rename",
          id: series.id,
          name: nameDraft.value,
          expected_revisions: {},
        },
        base({ [`series:${series.id}`]: nameDraft.revision }),
      );
      const result = await organization.change(c);
      setNameDraft(null);
      onChanged(result);
    }, "系列名称已保存");
  }
  const scope = `series:${series.id}`;
  return (
    <>
      <div className="series-heading">
        <div className="series-title-row">
          {nameDraft ? (
            <>
              <Input
                autoFocus
                aria-label="系列名称"
                maxLength={200}
                value={nameDraft.value}
                disabled={action.busy}
                onChange={(e) =>
                  setNameDraft({ ...nameDraft, value: e.target.value })
                }
                onPressEnter={() => void saveName()}
              />
              <Tooltip title="保存系列名称">
                <Button
                  aria-label="保存系列名称"
                  icon={<SaveOutlined />}
                  disabled={
                    !nameDirty ||
                    !nameDraft.value.trim() ||
                    busy ||
                    !!action.error
                  }
                  onClick={() => void saveName()}
                />
              </Tooltip>
              {!!action.error && (
                <Button
                  disabled={action.busy}
                  onClick={() => {
                    const previousError = action.error;
                    void action.run(async () => {
                      const current = await request<Series>(
                        `/series/${encodeURIComponent(series.id)}`,
                      );
                      modal.confirm({
                        title: "重新核对系列名称？",
                        content: (
                          <>
                            <p>
                              当前名称：{current.name}（版本 {current.revision}
                              ）
                            </p>
                            <p>保留输入：{nameDraft.value}</p>
                          </>
                        ),
                        okText: "重新核对",
                        cancelText: "继续编辑",
                        onCancel: () => action.setError(previousError),
                        onOk: () =>
                          setNameDraft({
                            ...nameDraft,
                            original: current.name,
                            revision: current.revision,
                          }),
                      });
                    }, "");
                  }}
                >
                  重新核对名称
                </Button>
              )}
              <Button
                aria-label="取消名称修改"
                icon={<CloseOutlined />}
                disabled={action.busy}
                onClick={() => {
                  if (nameDirty)
                    modal.confirm({
                      title: "放弃名称修改？",
                      okText: "放弃修改",
                      cancelText: "继续编辑",
                      onOk: () => setNameDraft(null),
                    });
                  else setNameDraft(null);
                }}
              />
            </>
          ) : !editable ? (
            <Typography.Title level={4}>{series.name}</Typography.Title>
          ) : (
            <Tooltip title="点击修改系列名称" trigger={["hover", "focus"]}>
              <Typography.Title level={4}>
                <button
                  className="series-name-button"
                  disabled={busy}
                  onClick={() =>
                    setNameDraft({
                      value: series.name,
                      original: series.name,
                      revision: series.revision,
                    })
                  }
                >
                  {series.name}
                </button>
              </Typography.Title>
            </Tooltip>
          )}
        </div>
        <Tag>{series.member_count} 个作品</Tag>
        <EditOnly library={series.library_id}>
          <Space>
            {(
              [
                { key: "add", label: "添加成员", icon: <PlusOutlined /> },
                {
                  key: "sort",
                  label: "自动排序",
                  icon: <SortAscendingOutlined />,
                },
                { key: "delete", label: "删除系列", icon: <DeleteOutlined /> },
              ] as const
            ).map((item) => (
              <Tooltip title={item.label} key={item.key}>
                <Button
                  aria-label={item.label}
                  icon={item.icon}
                  danger={item.key === "delete"}
                  disabled={
                    busy ||
                    !!nameDraft ||
                    (item.key === "sort" && series.member_count < 2)
                  }
                  onClick={() => void open(item.key)}
                />
              </Tooltip>
            ))}
          </Space>
        </EditOnly>
      </div>
      <ErrorNotice
        error={members.error || action.error}
        retry={members.reload}
      />
      <div className="version-scroll">
        <Spin spinning={members.loading}>
          <SeriesDropZone
            scope={scope}
            onDrop={async (item, drop) => {
              if (!editable) return;
              if (item.kind === "recommendation" && item.proposal) {
                await onJoin(item.proposal, drop);
                return;
              }
              await action.run(async () => {
                if (item.scope !== scope || item.kind !== "member")
                  throw Error("只能调整当前系列成员");
                const next = placeSeriesBlock(item.order, [item.id], drop);
                if (next.join() === item.order.join()) return;
                const c = await prepareCommand(
                  {
                    action: "series_move",
                    id: series.id,
                    work_ids: [item.id],
                    position: next.indexOf(item.id),
                    expected_revisions: {},
                  },
                  base(item.expected),
                );
                const result = await organization.change(c);
                onPage(Math.floor(next.indexOf(item.id) / 25) + 1);
                members.reload();
                onChanged(result);
              }, "");
            }}
          >
            {members.data?.items.map((w) => (
              <SeriesDragRow
                key={w.id}
                scope={scope}
                id={w.id}
                className="version-member series-member-row"
              >
                <SeriesDragHandle
                  disabled={!editable || busy || !!nameDraft}
                  item={{
                    kind: "member",
                    id: w.id,
                    scope,
                    label: originalTitle(w),
                    order: [...series.work_ids],
                    expected: {
                      ...members.data?.expected_revisions,
                      [`series:${series.id}`]: series.revision,
                    },
                  }}
                />
                <span className="series-position">{w.position}</span>
                <EditionCover
                  disabled={busy}
                  member={{
                    id: w.preferred_edition_id,
                    content_revision: w.content_revision,
                    cover_revision: w.cover_revision,
                  }}
                />
                <div className="version-member-text">
                  <EditionTitle member={w} />
                </div>
                <EditOnly library={series.library_id}>
                  <Space size={4} className="series-member-actions">
                    <Tooltip title="移出系列">
                      <Button
                        className="version-quick-action"
                        danger
                        aria-label={`移出 ${originalTitle(w)}`}
                        size="small"
                        icon={<DeleteOutlined />}
                        disabled={busy || !!nameDraft}
                        onClick={() => void open("remove", w)}
                      />
                    </Tooltip>
                    <Tooltip title="转移到其他系列">
                      <Button
                        className="version-quick-action"
                        aria-label={`转移 ${originalTitle(w)}`}
                        size="small"
                        icon={<SwapOutlined />}
                        disabled={busy || !!nameDraft}
                        onClick={() => void open("transfer", w)}
                      />
                    </Tooltip>
                  </Space>
                </EditOnly>
              </SeriesDragRow>
            ))}
            {members.data?.total === 0 && (
              <p className="version-empty">
                暂无成员，可添加作品或拖入推荐方案。
              </p>
            )}
          </SeriesDropZone>
        </Spin>
      </div>
      <Pager data={members.data} onChange={onPage} />
      {dialog && (
        <Modal
          open
          centered
          width={
            dialog.kind === "add" || dialog.kind === "transfer" ? 800 : 520
          }
          title={`${dialogTitles[dialog.kind]} · ${series.name}`}
          styles={{
            body: {
              maxHeight: "min(640px, calc(100dvh - 220px))",
              overflowY: "auto",
            },
          }}
          okText="确认"
          okButtonProps={{
            danger: dialog.kind === "delete" || dialog.kind === "remove",
            disabled:
              action.busy ||
              !!action.error ||
              ((dialog.kind === "add" || dialog.kind === "transfer") &&
                !dialog.targets.length),
          }}
          confirmLoading={action.busy}
          onCancel={close}
          closable={!action.busy}
          keyboard={!action.busy}
          mask={{ closable: !action.busy }}
          onOk={() => void submit()}
        >
          <FormLock busy={action.busy}>
            <Space orientation="vertical" className="full-width" size={12}>
              <ErrorNotice error={action.error} />
              {!!action.error && (
                <Button
                  onClick={() =>
                    modal.confirm({
                      title: "重新核对当前系列？",
                      content: "保留选择，使用当前关系重新核对后再提交。",
                      okText: "重新核对",
                      cancelText: "继续查看",
                      onOk: () =>
                        action.run(async () => {
                          const c = await prepareCommand({
                            ...dialog.command,
                            work_ids:
                              dialog.kind === "add"
                                ? dialog.targets.map((t) => t.id)
                                : dialog.command.work_ids,
                            target_series_id:
                              dialog.kind === "transfer"
                                ? dialog.targets[0]?.id
                                : undefined,
                          });
                          setDialog({ ...dialog, command: c });
                        }, ""),
                    })
                  }
                >
                  重新核对
                </Button>
              )}
              {(dialog.kind === "add" || dialog.kind === "transfer") && (
                <OrganizationPicker
                  kind={dialog.kind === "add" ? "works" : "series"}
                  library={series.library_id}
                  multiple={dialog.kind === "add"}
                  exclude={
                    dialog.kind === "add" ? series.work_ids : [series.id]
                  }
                  selected={dialog.targets}
                  onChange={(targets) => setDialog({ ...dialog, targets })}
                />
              )}
              {dialog.kind === "sort" && (
                <p>按当前自然排序规则重新排列整个系列？</p>
              )}
              {dialog.kind === "delete" && (
                <p>删除此系列？所有作品、版本和源文件均保留。</p>
              )}
              {dialog.kind === "remove" && (
                <p>
                  将“{dialog.member && originalTitle(dialog.member)}
                  ”移出系列？作品及所有版本均保留。
                </p>
              )}
            </Space>
          </FormLock>
        </Modal>
      )}
    </>
  );
}
