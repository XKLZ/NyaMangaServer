import { useRef, useState } from "react";
import {
  App as AntApp,
  Alert,
  Button,
  Checkbox,
  Input,
  Modal,
  Select,
  Space,
} from "antd";
import type { Book } from "../../shared/api";
import { organization } from "../../shared/organization";
import type {
  Command,
  EditionMember,
  OrgContext,
  Series,
  Work,
} from "../../shared/organization";
import {
  BookPicker,
  OperationPreview,
  OrganizationPicker,
  prepareCommand,
} from "../OrganizationControls";
import { ErrorNotice, useAction, useDirty, FormLock } from "../common";
import { cleanBookTitle, originalTitle } from "../books/bookTitle";

export type VersionAction =
  "edition_move" | "work_split" | "work_separate" | "work_merge" | "work_name";
const names: Record<VersionAction, string> = {
  edition_move: "加入版本",
  work_split: "拆分部分版本",
  work_separate: "移除该组",
  work_merge: "合并其他作品",
  work_name: "修改组名",
};
/** The context is captured when opening; refresh never advances an operation's CAS. */
export function VersionActionDialog({
  mode,
  group: initialGroup,
  base: initialBase,
  member,
  onClose,
  onDone,
}: {
  mode: VersionAction;
  group: Work;
  base: OrgContext;
  member?: EditionMember;
  onClose: () => void;
  onDone: () => void;
}) {
  const [base, setBase] = useState(initialBase);
  const group =
    base.works.find((w) => w.id === initialGroup.id) ?? initialGroup;
  const { modal } = AntApp.useApp();
  const [book, setBook] = useState<Book[]>([]),
    [targets, setTargets] = useState<(Work | Series)[]>([]);
  const [source, setSource] = useState<OrgContext>(),
    [remaining, setRemaining] = useState("");
  const editions = member ? [member.id] : [];
  const [keep, setKeep] = useState(mode === "work_separate"),
    [name, setName] = useState(group.name);
  const [inherit, setInherit] = useState(false),
    [command, setCommand] = useState<Command | null>(null);
  const [loadingSource, setLoadingSource] = useState(false);
  const selection = useRef(0),
    action = useAction();
  const dirty =
    !!book.length ||
    !!targets.length ||
    name !== initialGroup.name ||
    inherit ||
    keep !== (mode === "work_separate") ||
    JSON.stringify(editions) !== JSON.stringify(member ? [member.id] : []);
  useDirty(dirty || action.busy || !!command);
  function close() {
    if (action.busy || command) return;
    if (dirty)
      modal.confirm({
        title: "放弃未提交的修改？",
        okText: "放弃修改",
        cancelText: "继续编辑",
        onOk: onClose,
      });
    else onClose();
  }
  const sourceWork = source?.works.find((w) => w.id === book[0]?.work_id);
  const own = base.editions.filter((e) => e.work_id === group.id);
  const left =
    mode === "edition_move"
      ? (source?.editions.filter(
          (e) => e.work_id === sourceWork?.id && e.id !== book[0]?.id,
        ) ?? [])
      : own.filter((e) => !editions.includes(e.id));
  const needsPreferred =
    mode === "edition_move"
      ? !!sourceWork &&
        sourceWork.preferred_edition_id === book[0]?.id &&
        left.length > 0
      : mode === "work_split" && editions.includes(group.preferred_edition_id);
  const canSave =
    !loadingSource &&
    !action.busy &&
    !action.error &&
    !command &&
    (!needsPreferred || left.length === 1 || !!remaining) &&
    (mode === "edition_move"
      ? !!source && !!book[0] && book[0].work_id !== group.id
      : mode === "work_split"
        ? editions.length > 0 && left.length > 0
        : mode === "work_merge"
          ? targets.length > 0
          : mode === "work_name"
            ? inherit || !!name.trim()
            : true);
  async function submit() {
    await action.run(async () => {
      const c: Command = { action: mode, id: group.id, expected_revisions: {} };
      if (mode === "work_name") {
        c.name_op = inherit ? "inherit" : "set";
        c.name = name;
      }
      if (mode === "edition_move") {
        c.edition_id = book[0].id;
        c.target_work_id = group.id;
        delete c.id;
      }
      if (mode === "work_merge") c.work_ids = targets.map((t) => t.id);
      if (mode === "work_split") c.edition_ids = editions;
      if (mode === "work_split" || mode === "work_separate")
        c.keep_series = keep;
      if (needsPreferred)
        c.remaining_preferred_edition_id =
          left.length === 1 ? left[0].id : remaining;
      const frozen = {
        ...base,
        expected_revisions: {
          ...Object.fromEntries(
            targets.map((t) => [`work:${t.id}`, t.revision]),
          ),
          ...source?.expected_revisions,
          ...base.expected_revisions,
        },
      };
      const prepared = await prepareCommand(c, frozen);
      if (mode === "work_separate" || member) {
        // Generate canonical IDs using the same planner without displaying another confirmation.
        const result = await organization.preview(prepared);
        await organization.change(result.command);
        onDone();
      } else setCommand(prepared);
    }, "");
  }
  return (
    <>
      <Modal
        open
        centered
        width={mode === "edition_move" || mode === "work_merge" ? 820 : 600}
        styles={{
          body: {
            maxHeight: "min(640px, calc(100dvh - 220px))",
            overflowY: "auto",
          },
        }}
        title={`${member ? "移出版本" : names[mode]} · ${cleanBookTitle(originalTitle(group))}`}
        okText={
          mode === "work_separate"
            ? "确认移除"
            : member
              ? "确认拆分"
              : "查看整理影响"
        }
        cancelText="取消"
        confirmLoading={action.busy}
        okButtonProps={{ disabled: !canSave, danger: mode === "work_separate" }}
        closable={!action.busy && !command}
        mask={{ closable: !action.busy && !command }}
        keyboard={!action.busy && !command}
        onCancel={close}
        onOk={() => void submit()}
      >
        <FormLock busy={action.busy || !!command}>
          <Space orientation="vertical" className="full-width" size={12}>
            <ErrorNotice error={action.error} />
            {!!action.error && (
              <Button
                onClick={() =>
                  modal.confirm({
                    title: "重新核对当前关系？",
                    content:
                      "保留输入，并明确使用当前版本重新核对。请检查成员和系列影响后再提交。",
                    okText: "加载当前关系",
                    cancelText: "继续查看",
                    onOk: () =>
                      action.run(async () => {
                        const latest = await organization.context(
                          book.map((b) => b.id),
                          [group.id, ...targets.map((t) => t.id)],
                        );
                        setBase(latest);
                        if (book.length) setSource(latest);
                        setRemaining("");
                        action.setError(null);
                      }, ""),
                  })
                }
              >
                重新核对
              </Button>
            )}
            {mode === "work_name" && (
              <>
                <Input
                  aria-label="版本组名称"
                  value={name}
                  maxLength={512}
                  disabled={inherit || action.busy}
                  onChange={(e) => setName(e.target.value)}
                />
                <Checkbox
                  checked={inherit}
                  disabled={action.busy}
                  onChange={(e) => setInherit(e.target.checked)}
                >
                  恢复继承名称
                </Checkbox>
              </>
            )}
            {mode === "edition_move" && (
              <FormLock busy={action.busy || loadingSource}>
                <BookPicker
                  library={group.library_id}
                  selected={book}
                  onChange={(next) => {
                    const token = ++selection.current;
                    setBook(next);
                    setSource(undefined);
                    setRemaining("");
                    if (!next.length) return;
                    setLoadingSource(true);
                    void action
                      .run(async () => {
                        const value = await organization.context([next[0].id]);
                        if (selection.current === token) setSource(value);
                      }, "")
                      .finally(() => {
                        if (selection.current === token)
                          setLoadingSource(false);
                      });
                  }}
                />
                <Alert
                  type="warning"
                  title="所选版本将移入当前作品并继承其系列；源作品被移空时移除其组织位置。"
                />
              </FormLock>
            )}
            {mode === "work_merge" && (
              <OrganizationPicker
                kind="works"
                library={group.library_id}
                multiple
                selected={targets}
                onChange={setTargets}
                exclude={[group.id]}
              />
            )}
            {needsPreferred && left.length > 1 && (
              <Select
                aria-label="剩余作品首选版本"
                placeholder="选择剩余作品的新主版本"
                className="full-width"
                disabled={action.busy}
                value={remaining || undefined}
                onChange={setRemaining}
                options={left.map((e) => ({
                  value: e.id,
                  label: cleanBookTitle(originalTitle(e)),
                }))}
              />
            )}
            {(mode === "work_split" || mode === "work_separate") && (
              <>
                <Alert
                  type="warning"
                  title={
                    member
                      ? `将“${cleanBookTitle(originalTitle(member))}”移出为独立作品？`
                      : "将移除此组下的所有内容？"
                  }
                  description={
                    mode === "work_separate"
                      ? "所有作品默认继承系列。"
                      : "只改变组织关系，不删除漫画、元数据或源文件。新作品默认不继承系列。"
                  }
                />
                {group.series_id && (
                  <Checkbox
                    disabled={action.busy}
                    checked={keep}
                    onChange={(e) => setKeep(e.target.checked)}
                  >
                    新作品继承原系列，紧接原作品排列
                  </Checkbox>
                )}
              </>
            )}
          </Space>
        </FormLock>
      </Modal>
      {command && (
        <OperationPreview
          command={command}
          onCancel={() => setCommand(null)}
          onDone={onDone}
        />
      )}
    </>
  );
}
