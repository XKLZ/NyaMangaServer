import { useEffect, useRef, useState } from "react";
import { Button, Input, Modal, Pagination, Radio, Space, Table } from "antd";
import { tagCatalog } from "../../shared/tagCatalog";
import type { CatalogTag, CharacterRelation } from "../../shared/tagCatalog";
import { ErrorNotice } from "../common";
import { globalIntent } from "./tagCatalogFlow";
import type { TagIntent } from "./tagCatalogFlow";
import { tagName } from "./tagCatalogFlow";
import { TagReadError } from "./tagViews";
import { useTagPage } from "./useTagPage";
export function TagRelationPicker({
  anchor,
  library,
  onClose,
  onIntent,
}: {
  anchor: CatalogTag;
  library: string;
  onClose: () => void;
  onIntent: (intent: TagIntent) => void;
}) {
  const [draft, setDraft] = useState(""),
    [q, setQ] = useState(""),
    [page, setPage] = useState(1),
    [selected, setSelected] = useState<CatalogTag | null>(null),
    [decision, setDecision] = useState<"confirmed" | "excluded">("confirmed"),
    [busy, setBusy] = useState(false),
    [error, setError] = useState<unknown>(null);
  const abort = useRef<AbortController | null>(null);
  useEffect(() => () => abort.current?.abort(), []);
  const category = anchor.namespace === "parody" ? "character" : "parody";
  const list = useTagPage<CatalogTag>("/tag-catalog", {
    library_id: library,
    category,
    q,
    page,
    page_size: 50,
    sort: "name",
    order: "asc",
  });
  async function next() {
    if (!selected || busy) return;
    setBusy(true);
    setError(null);
    const c = new AbortController();
    abort.current = c;
    try {
      const rows = await tagCatalog.page<CharacterRelation>(
        `/tag-catalog/${encodeURIComponent(anchor.id)}/relations`,
        {
          library_id: library,
          view: "all",
          related_tag_id: selected.id,
          page: 1,
          page_size: 1,
        },
        c.signal,
      );
      if (!c.signal.aborted)
        onIntent(globalIntent(anchor, selected, rows.items[0], decision));
    } catch (error) {
      if (!c.signal.aborted) setError(error);
    } finally {
      if (!c.signal.aborted) setBusy(false);
    }
  }
  return (
    <Modal
      open
      title={`添加${category === "parody" ? "原作" : "角色"}关系 · ${tagName(anchor)}`}
      width={680}
      className="tag-picker"
      onCancel={() => !busy && onClose()}
      closable={!busy}
      keyboard={!busy}
      mask={{ closable: !busy }}
      footer={
        <Space>
          <Button disabled={busy} onClick={onClose}>
            取消
          </Button>
          <Button
            type="primary"
            disabled={!selected}
            loading={busy}
            onClick={() => void next()}
          >
            核对影响
          </Button>
        </Space>
      }
    >
      <Input.Search
        aria-label="搜索关系目标原文或译名"
        placeholder="搜索原文或中文译名…"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onSearch={(v) => {
          setQ(v);
          setPage(1);
        }}
        maxLength={512}
        disabled={busy}
      />
      <div className="tag-picker-tools">
        <Radio.Group
          disabled={busy}
          value={decision}
          onChange={(e) => setDecision(e.target.value)}
          options={[
            { label: "关联", value: "confirmed" },
            { label: "排除此关联", value: "excluded" },
          ]}
        />
      </div>
      <ErrorNotice error={error} />
      <TagReadError error={list.error} restart={list.restart} />
      <Table<CatalogTag>
        size="small"
        rowKey="id"
        pagination={false}
        loading={list.pending}
        dataSource={list.data?.items || []}
        rowSelection={{
          type: "radio",
          selectedRowKeys: selected ? [selected.id] : [],
          getCheckboxProps: (r) => ({ disabled: busy || r.placeholder }),
          onChange: (_, rows) => setSelected(rows[0]),
        }}
        columns={[
          {
            title: category === "parody" ? "原作" : "角色",
            render: (_, r) => (
              <div className="tag-identity">
                <strong>{tagName(r)}</strong>
                <span className="tag-original">
                  {r.namespace}:{r.name}
                  {r.placeholder ? " · 原创占位（只读）" : ""}
                </span>
              </div>
            ),
          },
          { title: "版本", dataIndex: "edition_count", width: 72 },
        ]}
      />
      <div className="tag-pager">
        <span>
          {selected ? `已选：${tagName(selected)}` : "请选择真实标签身份"}
        </span>
        <Pagination
          size="small"
          current={page}
          total={list.data?.total || 0}
          pageSize={50}
          showSizeChanger={false}
          disabled={busy}
          onChange={setPage}
        />
      </div>
    </Modal>
  );
}
