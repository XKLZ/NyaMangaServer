/** A secondary panel inside the actual Edition editor. Members remain lazy and
 * selecting one must pass the editor's draft guard and a fresh Book preflight. */
import { Empty, Input, Pagination, Spin, Tag } from "antd";
import { useLocation } from "react-router";
import type { Listing } from "../../shared/api";
import type { EditionMember, Work } from "../../shared/organization";
import { ErrorNotice } from "../common";
import { useRetainedRemote } from "../useRetainedRemote";
import { originalTitle } from "./bookTitle";
import { Cover } from "../versions/versionManagementViews";
export function BookVersions({
  work,
  current,
  busy,
  onSelect,
  onQuery,
}: {
  work: Work;
  current: string;
  busy: boolean;
  onSelect: (id: string) => void;
  onQuery: (v: Record<string, string | number | null>) => void;
}) {
  const q = new URLSearchParams(useLocation().search),
    page = Math.max(1, Number(q.get("v_page")) || 1),
    query = q.get("v_q") || "";
  const members = useRetainedRemote<Listing<EditionMember>>(
    `/works/${encodeURIComponent(work.id)}/editions?${new URLSearchParams({ query, page: String(page), page_size: "25" })}`,
  );
  return (
    <section className="book-versions" aria-label="其他版本">
      <div className="book-versions-tools">
        <h3>其他版本</h3>
        <Input.Search
          key={query}
          aria-label="搜索其他版本"
          placeholder="搜索版本题名…"
          defaultValue={query}
          disabled={busy}
          maxLength={512}
          allowClear
          onSearch={(v_q) => onQuery({ v_q })}
        />
      </div>
      <ErrorNotice error={members.error} retry={members.reload} />
      {members.loading && !members.data && <Spin />}
      <div className="book-version-list">
        {members.data?.items.map((member) => (
          <button
            key={member.id}
            type="button"
            className="book-version-option"
            data-version-id={member.id}
            aria-label={`切换到 ${originalTitle(member)}`}
            aria-pressed={member.id === current}
            disabled={busy || member.id === current}
            onClick={() => onSelect(member.id)}
          >
            <Cover
              id={member.id}
              content={member.content_revision}
              revision={member.cover_revision || 0}
              preview={false}
            />
            <span className="book-version-name">{originalTitle(member)}</span>
            {member.id === work.preferred_edition_id && (
              <Tag color="blue">主版本</Tag>
            )}
          </button>
        ))}
      </div>
      {members.data?.items.length === 0 && (
        <Empty description="没有匹配的版本" />
      )}
      <Pagination
        size="small"
        current={page}
        pageSize={25}
        total={members.data?.total || 0}
        showSizeChanger={false}
        disabled={busy}
        onChange={(v_page) => onQuery({ v_page })}
      />
    </section>
  );
}
