import { originalTitle } from "./books/bookTitle";
import { useState } from "react";
import {
  Alert,
  Button,
  Input,
  Modal,
  Space,
  Table,
  Tag,
  Typography,
} from "antd";
import { organization } from "../shared/organization";
import type {
  Command,
  OrgContext,
  OrgResult,
  Work,
  Series,
  EditionMember,
} from "../shared/organization";
import type { Listing } from "../shared/api";
import {
  ActionNotice,
  ErrorNotice,
  query,
  useAction,
  useDirty,
  useRemote,
} from "./common";
import { frozenRevisions } from "./books/bookOrganizationFlow";

export async function commandContext(c: Command) {
  const editions = [
    ...new Set([
      ...(c.edition_ids ?? []),
      ...(c.edition_id ? [c.edition_id] : []),
    ]),
  ];
  const works = [
    ...new Set([
      ...(c.work_ids ?? []),
      ...(c.target_work_id ? [c.target_work_id] : []),
      ...(c.id && c.action.startsWith("work_") ? [c.id] : []),
    ]),
  ];
  const series = [
    ...new Set([
      ...(c.id && c.action.startsWith("series_") && c.action !== "series_create"
        ? [c.id]
        : []),
      ...(c.target_series_id ? [c.target_series_id] : []),
    ]),
  ];
  return editions.length + works.length + series.length
    ? organization.context(editions, works, series)
    : ({
        works: [],
        editions: [],
        series: [],
        expected_revisions: {},
        snapshot_revision: "",
      } as OrgContext);
}
export async function prepareCommand(
  c: Command,
  base?: OrgContext,
): Promise<Command> {
  const context = await commandContext(c);
  const expected = base
    ? frozenRevisions(base, [context])
    : { ...context.expected_revisions };
  if (c.action === "work_favorite")
    for (const e of context.editions)
      expected[`state:${e.id}`] = e.user_state.revision;
  return {
    ...c,
    expected_revisions: expected,
    ...(c.action === "series_sort"
      ? {
          snapshot_revision:
            base?.snapshot_revision || context.snapshot_revision,
        }
      : {}),
  };
}

/** Server paging and stable selected objects keep off-page selections intact. */
export function OrganizationPicker({
  kind,
  library,
  selected,
  onChange,
  multiple = false,
  exclude = [],
}: {
  kind: "works" | "series";
  library: string;
  selected: (Work | Series)[];
  onChange: (value: (Work | Series)[]) => void;
  multiple?: boolean;
  exclude?: string[];
}) {
  const [page, setPage] = useState(1);
  const [filter, setFilter] = useState("");
  const result = useRemote<Listing<Work | Series>>(
    `/${kind}?${query({ library_id: library, query: filter, page, page_size: 25 })}`,
  );
  const items = result.data?.page === page ? result.data.items : [];
  return (
    <Space orientation="vertical" className="full-width">
      <Input.Search
        aria-label="查找目标组织"
        placeholder={kind === "works" ? "搜索作品名称" : "搜索系列名称"}
        onSearch={(v) => {
          setFilter(v);
          setPage(1);
        }}
      />
      <ErrorNotice error={result.error} retry={result.reload} />
      <Table<Work | Series>
        size="small"
        rowKey="id"
        loading={result.loading}
        dataSource={items}
        columns={[
          {
            title: kind === "works" ? "作品" : "系列",
            render: (_, value: Work | Series) =>
              kind === "works" ? originalTitle(value as Work) : value.name,
          },
          {
            title: "成员",
            render: (_, r) =>
              "edition_count" in r
                ? `${r.edition_count} 个版本`
                : `${r.member_count} 个作品`,
          },
        ]}
        rowSelection={{
          type: multiple ? "checkbox" : "radio",
          preserveSelectedRowKeys: true,
          selectedRowKeys: selected.map((v) => v.id),
          getCheckboxProps: (r) => ({
            disabled:
              exclude.includes(r.id) ||
              (multiple &&
                selected.length >= 1000 &&
                !selected.some((v) => v.id === r.id)),
          }),
          onChange: (ids) => {
            const byID = new Map([...selected, ...items].map((v) => [v.id, v]));
            const values = ids.flatMap((id) =>
              byID.has(String(id)) ? [byID.get(String(id))!] : [],
            );
            if (values.length <= 1000) onChange(values);
          },
        }}
        pagination={{
          current: page,
          pageSize: 25,
          total: result.data?.total ?? 0,
          showSizeChanger: false,
          onChange: setPage,
        }}
      />
      <Typography.Text type="secondary">
        已选择 {selected.length} 项
        {multiple ? "（最多1000项，达到上限后请先取消部分选择）" : ""}
        ；选择保留到其他页。
      </Typography.Text>
    </Space>
  );
}
export function EditionPicker({
  editions,
  selected,
  onChange,
  exclude = [],
}: {
  editions: EditionMember[];
  selected: string[];
  onChange: (ids: string[]) => void;
  exclude?: string[];
}) {
  return (
    <Table<EditionMember>
      size="small"
      rowKey="id"
      dataSource={editions}
      columns={[
        { title: "版本", render: (_, value) => originalTitle(value) },
        {
          title: "版本标签",
          render: (_, e) => e.version_labels.join(" · ") || "—",
        },
      ]}
      rowSelection={{
        selectedRowKeys: selected,
        preserveSelectedRowKeys: true,
        getCheckboxProps: (e) => ({ disabled: exclude.includes(e.id) }),
        onChange: (ids) => onChange(ids.map(String)),
      }}
      pagination={{ pageSize: 10, showSizeChanger: false }}
    />
  );
}
export function OperationPreview({
  command,
  onCancel,
  onDone,
}: {
  command: Command;
  onCancel: () => void;
  onDone: (r: OrgResult) => void;
}) {
  const [current, setCurrent] = useState(command);
  const preview = useRemote<OrgResult>(
    "/work-organization/preview",
    current,
    "POST",
  );
  const action = useAction();
  const release = useDirty(true);
  const result = preview.data;
  async function recheck() {
    await action.run(async () => {
      setCurrent(await prepareCommand(current));
      preview.reload();
    }, "");
  }
  return (
    <Modal
      open
      title="整理变更预览"
      width={720}
      okText="确认保存整理"
      cancelText="继续修改"
      confirmLoading={action.busy}
      onCancel={() => !action.busy && onCancel()}
      okButtonProps={{
        disabled:
          preview.loading || !result || !!preview.error || !!action.error,
      }}
      onOk={() =>
        void action.run(async () => {
          if (!result) return;
          const saved = await organization.change(result.command);
          release();
          onDone(saved);
        }, "")
      }
    >
      <Space orientation="vertical" className="full-width">
        <Alert
          type="info"
          showIcon
          title="核对作品、系列与顺序后保存。源文件及每个版本的人工资料保持不变。"
        />
        <ErrorNotice error={preview.error} />
        <ActionNotice action={action} />
        {Boolean(preview.error || action.error) && (
          <Button loading={action.busy} onClick={() => void recheck()}>
            核对当前版本并重新预览
          </Button>
        )}
        {result && !preview.loading && (
          <>
            <Typography.Title level={5}>作品与版本</Typography.Title>
            {result.context.works
              .filter((w) => w.edition_ids?.length)
              .map((w) => (
                <div key={w.id}>
                  <strong>{originalTitle(w)}</strong> · {w.edition_ids?.length}{" "}
                  个版本
                  <ul>
                    {w.edition_ids?.map((id) => (
                      <li key={id}>
                        {originalTitle(
                          result.context.editions.find((e) => e.id === id) ||
                            {},
                        )}{" "}
                        {id === w.preferred_edition_id && (
                          <Tag color="blue">首选版本</Tag>
                        )}
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            {result.effects.map((e, i) => (
              <Alert
                key={i}
                type={e.removed_work ? "warning" : "info"}
                title={
                  e.removed_work
                    ? "源作品将不再保留，其系列位置将被移除。"
                    : "版本关系变化"
                }
                description={
                  e.from_series_id !== e.to_series_id
                    ? "此版本将继承目标作品的系列归属；来源、人工编辑与版本标签保留。"
                    : "系列归属保持。"
                }
              />
            ))}
            <Typography.Title level={5}>系列与顺序</Typography.Title>
            {result.context.series.length ? (
              result.context.series.map((s) => (
                <div key={s.id}>
                  <strong>{s.name}</strong>
                  <ol>
                    {s.work_ids.map((id) => (
                      <li key={id}>
                        {(result.context.works.find((w) => w.id === id)
                          ? originalTitle(
                              result.context.works.find((w) => w.id === id)!,
                            )
                          : undefined) ??
                          s.work_names?.[id] ??
                          "作品"}
                      </li>
                    ))}
                  </ol>
                </div>
              ))
            ) : (
              <p>没有系列归属。</p>
            )}
          </>
        )}
      </Space>
    </Modal>
  );
}

export function BookPicker({
  library,
  selected,
  onChange,
}: {
  library: string;
  selected: import("../shared/api").Book[];
  onChange: (v: import("../shared/api").Book[]) => void;
}) {
  const [page, setPage] = useState(1),
    [filter, setFilter] = useState("");
  const result = useRemote<Listing<import("../shared/api").Book>>(
    "/admin/editions/search",
    {
      query: filter,
      library_id: library,
      page,
      page_size: 25,
      scope: "all",
      collapse: "none",
      sort: "indexed_at",
      order: "desc",
    },
    "POST",
  );
  return (
    <Space orientation="vertical" className="full-width">
      <Input.Search
        aria-label="查找可加入版本"
        placeholder="搜索漫画标题"
        onSearch={(v) => {
          setFilter(v);
          setPage(1);
        }}
      />
      <ErrorNotice error={result.error} />
      <Table<import("../shared/api").Book>
        size="small"
        rowKey="id"
        loading={result.loading}
        dataSource={result.data?.page === page ? result.data.items : []}
        columns={[
          { title: "版本", render: (_, value) => originalTitle(value) },
          { title: "源文件", dataIndex: "relative_path" },
        ]}
        rowSelection={{
          type: "radio",
          selectedRowKeys: selected.map((v) => v.id),
          preserveSelectedRowKeys: true,
          onChange: (ids) => {
            const byID = new Map(
              [...selected, ...(result.data?.items ?? [])].map((v) => [
                v.id,
                v,
              ]),
            );
            onChange(
              ids.flatMap((id) =>
                byID.has(String(id)) ? [byID.get(String(id))!] : [],
              ),
            );
          },
        }}
        pagination={{
          current: page,
          pageSize: 25,
          total: result.data?.total ?? 0,
          showSizeChanger: false,
          onChange: setPage,
        }}
      />
    </Space>
  );
}
