import { Descriptions, Space, Tag } from "antd";
import type { Book } from "../../shared/api";
import { namespaces } from "../tagInput";
export function MetadataReadOnly({ book }: { book: Book }) {
  const fields = book.metadata?.fields;
  const groups = [
    ...new Set(book.metadata?.tags?.map((tag) => tag.namespace) || []),
  ].sort((a, b) => {
    const rank = (name: string) => {
      const index = namespaces.findIndex(([key]) => key === name);
      return index < 0 ? namespaces.length : index;
    };
    return rank(a) - rank(b) || a.localeCompare(b);
  });
  return (
    <>
      <Descriptions
        size="small"
        column={{ xs: 1, sm: 2 }}
        items={Object.entries({
          title: "标题",
          title_original: "原文标题",
          category: "分类",
          language: "语言",
          rating: "评分",
          published_at: "发布时间",
          description: "描述",
        }).map(([key, label]) => ({
          key,
          label,
          children: String(fields?.[key as keyof typeof fields] ?? "未知"),
        }))}
      />
      {groups.map((name) => {
        const ns = namespaces.find(([key]) => key === name) || [name, name];
        const tags = book.metadata?.tags?.filter((t) => t.namespace === ns[0]);
        return tags?.length ? (
          <div key={ns[0]}>
            <h3>{ns[1]}</h3>
            <Space wrap>
              {tags.map((t) => (
                <Tag
                  key={t.id}
                  style={{
                    maxWidth: "100%",
                    whiteSpace: "normal",
                    overflowWrap: "anywhere",
                  }}
                >
                  {t.translation || t.name}
                </Tag>
              ))}
            </Space>
          </div>
        ) : null;
      })}
    </>
  );
}
