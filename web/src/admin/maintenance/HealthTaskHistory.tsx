import { Button, Pagination, Space } from "antd";
import { ApiError } from "../../shared/api";
import type { Job, Listing } from "../../shared/api";
import type { HealthCheck, HealthRepair } from "../../shared/healthFlow";
import { healthPhases } from "../../shared/healthFlow";
import { ErrorNotice, time } from "../common";
import { useHealthRead } from "./useHealthRead";
import { Link } from "react-router";
import { taskURL } from "../../shared/consoleRoutes.mjs";
export function HealthTaskHistory({
  page,
  id,
  update,
}: {
  page: number;
  id: string;
  update: (values: Record<string, string | number | null>) => void;
}) {
  const tasks = useHealthRead<Listing<Job>>(
    `/jobs?type=maintenance&page=${page}&page_size=10`,
    true,
  );
  const repair = useHealthRead<HealthRepair>(
    `/maintenance/health-repairs/${encodeURIComponent(id)}`,
    !!id,
  );
  const notRepair =
    repair.error instanceof ApiError && repair.error.status === 404;
  const check = useHealthRead<HealthCheck>(
    `/maintenance/health-checks/${encodeURIComponent(id)}`,
    !!id && notRepair,
  );
  const notCheck =
    check.error instanceof ApiError && check.error.status === 404;
  return (
    <section aria-label="维护任务历史">
      <p>包括检查、修复及其他维护任务。选择后按需读取报告，不改变原任务。</p>
      <ErrorNotice error={tasks.error} retry={tasks.reload} />
      {tasks.data?.items.map((v) => (
        <button
          key={v.id}
          data-task-id={v.id}
          className="health-history-item"
          onClick={() => update({ health_task: v.id })}
        >
          <strong>维护任务 · {healthPhases[v.state] || "状态待核对"}</strong>
          <span>{time(v.created_at)}</span>
        </button>
      ))}
      <Pagination
        size="small"
        simple
        current={page}
        pageSize={10}
        total={tasks.data?.total || 0}
        onChange={(p) => update({ health_task_page: p })}
      />
      {id && (
        <Space wrap>
          {repair.data ? (
            <Button onClick={() => update({ repair: repair.data!.id })}>
              查看修复与回退
            </Button>
          ) : check.data ? (
            <Button
              onClick={() =>
                update({ check: check.data!.id, finding_page: null })
              }
            >
              查看检查报告
            </Button>
          ) : notRepair && notCheck ? (
            <Link to={taskURL(id)}>查看其他维护任务</Link>
          ) : (
            <span>正在识别所选维护任务…</span>
          )}
          {!!check.error && !notCheck && (
            <ErrorNotice error={check.error} retry={check.reload} />
          )}
          {!!repair.error && !notRepair && (
            <ErrorNotice error={repair.error} retry={repair.reload} />
          )}
        </Space>
      )}
    </section>
  );
}
