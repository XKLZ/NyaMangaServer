import { cleanBookTitle, originalTitle } from "../books/bookTitle";
import { useState } from "react";
import { Alert, Button, Card, Select, Space } from "antd";
import type { Job, Listing } from "../../shared/api";
import type { SeriesPending } from "../../shared/seriesDiscovery";
import { ErrorNotice, query } from "../common";
import { Pager, useVersionRemote } from "../versions/versionManagementViews";
export function SeriesPendingPanel({
  library,
  job,
  refresh,
  onJob,
  onChoose,
}: {
  library: string;
  job: Job | undefined;
  refresh: number;
  onJob: (id: string) => void;
  onChoose: (id: string) => void;
}) {
  const [page, setPage] = useState(1),
    [jobPage, setJobPage] = useState(1);
  const jobs = useVersionRemote<Listing<Job>>(
    `/jobs?${query({ type: "series_recommendation", library_id: library, page: jobPage, page_size: 20, refresh })}`,
    undefined,
    "GET",
    !!library,
  );
  const pending = useVersionRemote<
    Listing<SeriesPending> & { unavailable_series_count: number }
  >(
    `/series-discovery-jobs/${encodeURIComponent(job?.id ?? "")}/pending?${query({ page, page_size: 10, refresh })}`,
    undefined,
    "GET",
    !!job,
  );
  return (
    <Space orientation="vertical" className="full-width">
      <Select
        className="full-width"
        aria-label="待核对发现任务"
        placeholder="选择发现任务"
        value={job?.id}
        options={[
          ...(job && !jobs.data?.items.some((j) => j.id === job.id)
            ? [{ value: job.id, label: `${job.id} · ${job.state}` }]
            : []),
          ...(jobs.data?.items ?? []).map((j) => ({
            value: j.id,
            label: `${j.id} · ${j.state}`,
          })),
        ]}
        onChange={(id) => {
          setPage(1);
          onJob(id);
        }}
      />
      <Pager data={jobs.data} onChange={setJobPage} />
      <ErrorNotice
        error={jobs.error || pending.error}
        retry={() => {
          jobs.reload();
          pending.reload();
        }}
      />
      {!!pending.data?.unavailable_series_count && (
        <Alert
          type="warning"
          title={`${pending.data.unavailable_series_count} 个系列资料不完整，原有关系保留`}
        />
      )}
      {pending.data?.items.map((p) => (
        <Card
          key={p.work_id}
          size="small"
          title={cleanBookTitle(originalTitle(p))}
        >
          <Space wrap>
            {p.options.map((o, i) => (
              <Button
                key={o.proposal_id}
                onClick={() => onChoose(o.proposal_id)}
              >
                备选{i + 1}：
                {o.kind === "join_series" ? "加入已有系列" : "组成系列"}（
                {o.target_work_ids.length}个目标作品）
              </Button>
            ))}
          </Space>
          {!p.options.length && <p>暂无可靠去向，不强行归组。</p>}
        </Card>
      ))}
      {!pending.loading && !pending.data?.items.length && (
        <p className="version-empty">
          {job ? "暂无待核对成员" : "请先选择发现任务"}
        </p>
      )}
      <Pager data={pending.data} onChange={setPage} />
    </Space>
  );
}
