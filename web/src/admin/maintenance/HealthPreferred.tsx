import { useEffect, useState } from "react";
import { Pagination, Radio, Space } from "antd";
import type { EditionMember, Members } from "../../shared/organization";
import { originalTitle } from "../books/bookTitle";
import { ErrorNotice } from "../common";
import { useHealthRead } from "./useHealthRead";
export function HealthPreferred({
  work,
  value,
  onChange,
  onRequired,
}: {
  work: string;
  value?: { id: string; title: string };
  onChange: (v: { id: string; title: string }) => void;
  onRequired: (v: boolean) => void;
}) {
  const [page, setPage] = useState(1);
  const data = useHealthRead<Members<EditionMember>>(
    `/works/${encodeURIComponent(work)}/editions?page=${page}&page_size=25`,
    true,
  );
  const count = data.data?.total;
  useEffect(() => {
    if (count !== undefined) onRequired(count > 1);
  }, [count, onRequired]);
  return (
    <div className="health-preferred">
      <strong>指定此作品的主版本</strong>
      <ErrorNotice error={data.error} retry={data.reload} />
      {count === 1 ? (
        <p>唯一成员：{originalTitle(data.data!.items[0])}，由服务端确定。</p>
      ) : (
        <>
          <p>
            {value ? `已选：${value.title}` : "必须明确选择本作品的一个版本"}
          </p>
          <Space orientation="vertical">
            <Radio.Group
              aria-label={`作品${work}主版本`}
              value={value?.id}
              onChange={(e) => {
                const item = data.data?.items.find(
                  (v) => v.id === e.target.value,
                );
                if (item) onChange({ id: item.id, title: originalTitle(item) });
              }}
              options={data.data?.items.map((v) => ({
                value: v.id,
                label: originalTitle(v),
              }))}
            />
          </Space>
          <Pagination
            size="small"
            current={page}
            pageSize={25}
            total={count || 0}
            showSizeChanger={false}
            onChange={setPage}
          />
        </>
      )}
    </div>
  );
}
