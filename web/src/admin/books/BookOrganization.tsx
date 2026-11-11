import { originalTitle } from "../books/bookTitle";
import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Pagination,
  Select,
  Space,
  Tag,
  Typography,
} from "antd";
import {
  ArrowUpOutlined,
  ArrowDownOutlined,
  ExportOutlined,
} from "@ant-design/icons";
import type { Book } from "../../shared/api";
import { organization } from "../../shared/organization";
import type {
  Command,
  EditionMember,
  Members,
  OrgContext,
  Series,
  Work,
} from "../../shared/organization";
import { VersionLabelsInput } from "../VersionLabelsInput";
import {
  ActionNotice,
  ErrorNotice,
  FormLock,
  useAction,
  useRemote,
} from "../common";
import {
  currentMembership,
  memberPage,
  relationshipPageSize,
  seriesDestination,
  frozenRevisions,
} from "./bookOrganizationFlow";
import {
  OperationPreview,
  OrganizationPicker,
  prepareCommand,
} from "../OrganizationControls";
type Draft = { command: Command; base: OrgContext; targets: (Work | Series)[] };
export function BookOrganization({
  book,
  navigate,
  onChanged,
  onDirty,
  onBusy,
}: {
  book: Book;
  navigate: (path: string) => void;
  onChanged: () => void;
  onDirty: (dirty: boolean) => void;
  onBusy: (busy: boolean) => void;
}) {
  const { work, series, edition } = currentMembership(
    book.organization,
    book.id,
  );
  const [draft, setDraft] = useState<Draft | null>(null);
  const [command, setCommand] = useState<Command | null>(null);
  const [editionPage, setEditionPage] = useState(1);
  const [seriesPage, setSeriesPage] = useState<{
    key: string;
    page: number;
  } | null>(null);
  const key = `${work?.id}:${work?.position}:${series?.revision}`;
  const page =
    seriesPage?.key === key ? seriesPage.page : memberPage(work?.position ?? 1);
  const members = useRemote<Members<EditionMember>>(
    `/works/${work?.id}/editions?page=${editionPage}&page_size=8&revision=${work?.revision}`,
    undefined,
    "GET",
    !!work,
  );
  const seriesMembers = useRemote<Members<Work>>(
    `/series/${series?.id}/members?page=${page}&page_size=${relationshipPageSize}&revision=${series?.revision}`,
    undefined,
    "GET",
    !!series,
  );
  const action = useAction();
  useEffect(() => {
    onDirty(!!draft || !!command);
    return () => onDirty(false);
  }, [draft, command, onDirty]);
  useEffect(() => {
    onBusy(action.busy);
    return () => onBusy(false);
  }, [action.busy, onBusy]);
  function patch(value: Partial<Command>) {
    setDraft((d) => d && { ...d, command: { ...d.command, ...value } });
  }
  async function start(c: Partial<Command> & { action: string }) {
    await action.run(async () => {
      const fresh = await organization.context([book.id]);
      const base = {
        ...fresh,
        expected_revisions: book.organization
          ? frozenRevisions(book.organization, [fresh])
          : fresh.expected_revisions,
      };
      setDraft({
        command: { ...c, expected_revisions: base.expected_revisions },
        base,
        targets: [],
      });
    }, "");
  }
  async function propose() {
    if (!draft) return;
    await action.run(async () => {
      let c = draft.command;
      if (c.action === "series_choose") {
        const current = currentMembership(draft.base, book.id);
        c = {
          ...c,
          ...seriesDestination(
            current.series?.id,
            draft.targets[0]?.id ?? "",
            current.work!.id,
          ),
        };
      }
      if (c.action === "edition_move")
        c = { ...c, target_work_id: draft.targets[0]?.id };
      if (
        (c.action === "edition_move" || c.action === "series_choose") &&
        !draft.targets.length
      )
        throw new Error("请选择目标。");
      setCommand(await prepareCommand(c, draft.base));
    }, "");
  }
  const source = draft && currentMembership(draft.base, book.id).work;
  const remaining =
    draft?.base.editions.filter(
      (e) => e.work_id === source?.id && e.id !== book.id,
    ) ?? [];
  const changingMembership =
    draft && ["edition_move", "work_split"].includes(draft.command.action);
  const needsPreferred =
    changingMembership &&
    source?.preferred_edition_id === book.id &&
    remaining.length > 0;
  const blocked = !!draft || !!command || action.busy || !work;
  const shownMembers = members.data?.page === editionPage ? members.data : null;
  return (
    <Space orientation="vertical" size={16} className="full-width">
      <ActionNotice action={action} />
      <Card
        title="所属作品"
        extra={
          <Button
            icon={<ExportOutlined aria-hidden="true" />}
            disabled={!work}
            onClick={() => navigate(`/admin/works/${work?.id}`)}
          >
            管理作品
          </Button>
        }
      >
        {work ? (
          <Space orientation="vertical" className="full-width">
            <Typography.Title level={5}>{originalTitle(work)}</Typography.Title>
            <Space wrap>
              <Tag>{work.edition_count} 个版本</Tag>
              <Tag
                color={
                  work.preferred_edition_id === book.id ? "blue" : "default"
                }
              >
                {work.preferred_edition_id === book.id
                  ? "首选版本"
                  : "其他版本"}
              </Tag>
              {edition?.version_labels.map((v) => (
                <Tag key={v}>{v}</Tag>
              ))}
            </Space>
            <Typography.Text type="secondary">
              每个文件对应一个独立版本，资料、人工编辑与封面各自保留；作品只有一个版本时也保留身份。
            </Typography.Text>
            <Space wrap>
              <Button
                disabled={blocked}
                onClick={() =>
                  void start({
                    action: "edition_labels",
                    edition_id: book.id,
                    version_labels: edition?.version_labels ?? [],
                  })
                }
              >
                编辑版本标签
              </Button>
              <Button
                disabled={blocked || work.preferred_edition_id === book.id}
                onClick={() =>
                  void start({
                    action: "work_preferred",
                    id: work.id,
                    preferred_edition_id: book.id,
                  })
                }
              >
                设本书为首选版本
              </Button>
              <Button
                disabled={blocked}
                onClick={() =>
                  void start({ action: "edition_move", edition_id: book.id })
                }
              >
                移归其他作品
              </Button>
              <Button
                disabled={blocked || work.edition_count < 2}
                onClick={() =>
                  void start({
                    action: "work_split",
                    id: work.id,
                    edition_ids: [book.id],
                  })
                }
              >
                拆为独立作品
              </Button>
            </Space>
            <ErrorNotice error={members.error} retry={members.reload} />
            {shownMembers?.items.map((e) => (
              <Space key={e.id} wrap>
                <Button
                  type="link"
                  onClick={() => navigate(`/admin/books?book=${e.id}`)}
                >
                  {originalTitle(e)}
                </Button>
                {e.id === work.preferred_edition_id && (
                  <Tag color="blue">首选版本</Tag>
                )}
                {e.version_labels.map((v) => (
                  <Tag key={v}>{v}</Tag>
                ))}
              </Space>
            ))}
            <Pagination
              size="small"
              current={editionPage}
              pageSize={8}
              total={members.data?.total ?? work.edition_count}
              showSizeChanger={false}
              onChange={setEditionPage}
            />
          </Space>
        ) : (
          <Alert
            type="warning"
            title="作品信息暂不可用，请重新加载漫画详情。"
          />
        )}
      </Card>
      <Card
        title="所属系列"
        extra={
          series && (
            <Button onClick={() => navigate(`/admin/series/${series.id}`)}>
              管理系列
            </Button>
          )
        }
      >
        <Space orientation="vertical" className="full-width">
          <Typography.Text>
            {series
              ? `${series.name} · 第 ${work?.position} / ${series.member_count} 个作品`
              : "尚未加入系列"}
          </Typography.Text>
          <Typography.Text type="secondary">
            系列按作品排列，新增同作品版本自动继承系列，不增加系列成员。
          </Typography.Text>
          <Space wrap>
            <Button
              disabled={blocked}
              onClick={() => void start({ action: "series_choose" })}
            >
              {series ? "更换系列" : "选择系列"}
            </Button>
            {series && work && (
              <>
                <Button
                  disabled={blocked}
                  onClick={() =>
                    void start({
                      action: "series_remove",
                      id: series.id,
                      work_ids: [work.id],
                    })
                  }
                >
                  移出系列
                </Button>
                <Button
                  icon={<ArrowUpOutlined aria-hidden="true" />}
                  disabled={blocked || (work.position ?? 1) <= 1}
                  onClick={() =>
                    void start({
                      action: "series_move",
                      id: series.id,
                      work_ids: [work.id],
                      position: (work.position ?? 1) - 2,
                    })
                  }
                >
                  上移
                </Button>
                <Button
                  icon={<ArrowDownOutlined aria-hidden="true" />}
                  disabled={
                    blocked || (work.position ?? 1) >= series.member_count
                  }
                  onClick={() =>
                    void start({
                      action: "series_move",
                      id: series.id,
                      work_ids: [work.id],
                      position: work.position,
                    })
                  }
                >
                  下移
                </Button>
              </>
            )}
          </Space>
          <ErrorNotice
            error={seriesMembers.error}
            retry={seriesMembers.reload}
          />
          {series && seriesMembers.data?.page === page && (
            <>
              {seriesMembers.data.items.map((w) => (
                <div key={w.id}>
                  <Button
                    type="link"
                    onClick={() => navigate(`/admin/works/${w.id}`)}
                  >
                    {w.position}. {originalTitle(w)}
                  </Button>
                  {w.id === work?.id && <Tag>当前作品</Tag>}
                </div>
              ))}
              <Pagination
                size="small"
                current={page}
                pageSize={relationshipPageSize}
                total={seriesMembers.data.total}
                showSizeChanger={false}
                onChange={(p) => setSeriesPage({ key, page: p })}
              />
            </>
          )}
        </Space>
      </Card>
      {draft && (
        <Card title="编辑整理关系">
          <FormLock busy={action.busy}>
            <Space orientation="vertical" className="full-width">
              {draft.command.action === "edition_labels" && (
                <VersionLabelsInput
                  value={draft.command.version_labels ?? []}
                  onChange={(v) => patch({ version_labels: v })}
                  label="本书版本标签"
                />
              )}
              {["edition_move", "series_choose"].includes(
                draft.command.action,
              ) && (
                <OrganizationPicker
                  kind={
                    draft.command.action === "edition_move" ? "works" : "series"
                  }
                  library={book.library_id}
                  selected={draft.targets}
                  onChange={(targets) => setDraft({ ...draft, targets })}
                  exclude={[work?.id ?? "", series?.id ?? ""]}
                />
              )}
              {needsPreferred && (
                <label>
                  剩余作品的首选版本
                  <Select
                    className="full-width"
                    aria-label="剩余作品首选版本"
                    placeholder="必须明确选择"
                    value={draft.command.remaining_preferred_edition_id}
                    onChange={(v) =>
                      patch({ remaining_preferred_edition_id: v })
                    }
                    options={remaining.map((e) => ({
                      value: e.id,
                      label: originalTitle(e),
                    }))}
                  />
                </label>
              )}
              {draft.command.action === "work_split" && (
                <Checkbox
                  checked={draft.command.keep_series ?? false}
                  onChange={(e) => patch({ keep_series: e.target.checked })}
                >
                  保留原系列，新作品紧接原作品
                </Checkbox>
              )}
              <Alert
                type="info"
                title="确认前将展示真实关系影响。移归继承目标作品系列；拆分默认不继承系列，源文件不会改变。"
              />
              <Space>
                <Button
                  type="primary"
                  disabled={
                    !!needsPreferred &&
                    !draft.command.remaining_preferred_edition_id
                  }
                  onClick={() => void propose()}
                >
                  查看整理影响
                </Button>
                <Button onClick={() => setDraft(null)}>放弃整理修改</Button>
              </Space>
            </Space>
          </FormLock>
        </Card>
      )}
      {command && (
        <OperationPreview
          command={command}
          onCancel={() => setCommand(null)}
          onDone={() => {
            setCommand(null);
            setDraft(null);
            onChanged();
            action.setNotice("整理已保存");
          }}
        />
      )}
    </Space>
  );
}
