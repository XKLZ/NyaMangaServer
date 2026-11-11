import { usePermissions } from "../../shared/permissions";
import { Empty } from "antd";
import { Button, Select, Space, Table } from "antd";
import { useLocation, useNavigate } from "react-router";
import type { Library, Listing } from "../../shared/api";
import { importStageNames } from "../../shared/imports";
import type { ImportBatch } from "../../shared/imports";
import { ImportOrganizationWorkspace } from "./ImportOrganizationWorkspace";
import { ImportMetadataWorkspace } from "./ImportMetadataWorkspace";
import { ImportFailureSummary } from "./ImportFailure";
import { useRetainedRemote } from "../useRetainedRemote";
import {
  ErrorNotice,
  RefreshButton,
  StateTag,
  useURLValue,
  query,
  time,
} from "../common";
import "../libraries/libraryImport.css";
import { LibraryScopeControl, useCurrentLibrary } from "../CurrentLibrary";
import { useImportBatchRefresh } from "./useImportBatchRefresh";
export function ImportsPage() {
  const scope = useCurrentLibrary();
  return usePermissions().edit(scope.id) ? (
    <EditableImportsPage />
  ) : (
    <Empty description="此书库仅供查看，导入审核需编辑权限。" />
  );
}
function EditableImportsPage() {
  const scope = useCurrentLibrary();
  const nav = useNavigate(),
    location = useLocation(),
    id = new URLSearchParams(location.search).get("batch") ?? "";
  const [batchesPage, setBatchesPage] = useURLValue("batches_page", 1),
    [state] = useURLValue("state", "");
  const library = scope.all ? "" : (scope.library?.id ?? "");
  const libraries = { data: { items: scope.libraries } };
  const batches = useRetainedRemote<Listing<ImportBatch>>(
    `/import-batches?${query({ library_id: library, state, page: batchesPage, page_size: 25 })}`,
    !id,
  );
  function filterBy(key: string, value: string) {
    const params = new URLSearchParams(location.search);
    if (value) params.set(key, value);
    else params.delete(key);
    params.delete("batches_page");
    void nav({ search: params.toString() }, { preventScrollReset: true });
  }
  const open = (batch: string) =>
    void nav(`/admin/imports?batch=${encodeURIComponent(batch)}`);
  if (id)
    return (
      <BatchWorkspace
        key={id}
        id={id}
        libraries={libraries.data?.items ?? []}
      />
    );
  return (
    <section className="import-history-page">
      <h1 className="sr-only">导入历史</h1>
      <div className="library-import-toolbar">
        <Space wrap>
          <LibraryScopeControl />
          <Select
            aria-label="历史状态筛选"
            value={state}
            className="import-history-filter"
            options={[
              { value: "", label: "全部状态" },
              ...Object.entries({
                processing: "处理中",
                review: "等待审核",
                paused: "已暂停",
                failed: "失败",
                completed: "已完成",
                cancelled: "已取消",
              }).map(([value, label]) => ({ value, label })),
            ]}
            onChange={(v) => {
              filterBy("state", v);
            }}
          />
        </Space>
        <Space>
          <RefreshButton reload={batches.reload} />
          <Button onClick={() => void nav("/admin/libraries")}>返回书库</Button>
        </Space>
      </div>
      <ErrorNotice error={batches.error} retry={batches.reload} />
      <Table<ImportBatch>
        rowKey="id"
        size="small"
        dataSource={batches.data?.items ?? []}
        loading={batches.loading}
        pagination={{
          current: batchesPage,
          pageSize: 25,
          total: batches.data?.total ?? 0,
          showSizeChanger: false,
          onChange: setBatchesPage,
        }}
        scroll={{ x: 760 }}
        columns={[
          {
            title: "书库",
            render: (_, b) =>
              libraries.data?.items.find((l) => l.id === b.library_id)
                ?.display_name ?? "书库名称暂不可用",
          },
          {
            title: "开始时间",
            render: (_, b) => (b.created_at ? time(b.created_at) : "—"),
          },
          { title: "当前阶段", render: (_, b) => importStageNames[b.stage] },
          { title: "状态", render: (_, b) => <StateTag state={b.state} /> },
          {
            title: "处理摘要",
            render: (_, b) => <ImportFailureSummary batch={b} />,
          },
          {
            title: "操作",
            render: (_, b) => (
              <Button onClick={() => open(b.id)}>
                {["completed", "cancelled"].includes(b.state)
                  ? "查看记录"
                  : "继续导入"}
              </Button>
            ),
          },
        ]}
      />
    </section>
  );
}
function BatchWorkspace({
  id,
  libraries,
}: {
  id: string;
  libraries: Library[];
}) {
  const batch = useRetainedRemote<ImportBatch>(`/import-batches/${id}`);
  useImportBatchRefresh(
    id,
    batch.data?.state,
    batch.reload,
    batch.data?.jobs?.map((j) => j.id).join(","),
  );
  if (!batch.data) return <ErrorNotice error={batch.error} />;
  return batch.data.stage === "metadata" ? (
    <ImportMetadataWorkspace
      batch={batch.data}
      library={
        libraries.find((l) => l.id === batch.data?.library_id)?.display_name ??
        "导入审核"
      }
      onRefresh={batch.reload}
    />
  ) : (
    <ImportOrganizationWorkspace
      key={batch.data.stage}
      id={id}
      batchData={batch.data}
      reloadParent={batch.reload}
      library={
        libraries.find((l) => l.id === batch.data?.library_id)?.display_name ??
        "导入审核"
      }
    />
  );
}
