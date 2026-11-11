import { originalTitle } from "./books/bookTitle";
import { useState } from "react";
import { Alert, Button, Drawer, Radio, Space, Tag } from "antd";
import type { Book } from "../shared/api";
import { organization } from "../shared/organization";
import type { Command, Series, Work } from "../shared/organization";
import { ActionNotice, FormLock, useAction, useDirty } from "./common";
import {
  OperationPreview,
  OrganizationPicker,
  prepareCommand,
} from "./OrganizationControls";

export function OrganizeSelection({
  books,
  onClose,
  onDone,
}: {
  books: Book[];
  onClose: () => void;
  onDone: () => void;
}) {
  const [mode, setMode] = useState("work_merge"),
    [targets, setTargets] = useState<(Work | Series)[]>([]),
    [command, setCommand] = useState<Command | null>(null);
  const action = useAction();
  useDirty(true);
  const library = books[0]?.library_id ?? "",
    same = books.every((b) => b.library_id === library);
  async function propose() {
    await action.run(async () => {
      if (!same || !targets.length)
        throw new Error("请选择同一书库和目标组织。");
      const context = await organization.context(books.map((b) => b.id));
      const workIDs = [
        ...new Set(
          books.map(
            (b) => context.editions.find((e) => e.id === b.id)!.work_id,
          ),
        ),
      ];
      const c: Command =
        mode === "work_merge"
          ? {
              action: mode,
              id: targets[0].id,
              work_ids: workIDs.filter((id) => id !== targets[0].id),
              expected_revisions: {},
            }
          : {
              action: mode,
              id: targets[0].id,
              work_ids: workIDs,
              expected_revisions: {},
            };
      setCommand(await prepareCommand(c, context));
    }, "");
  }
  return (
    <>
      <Drawer
        open
        title={`整理所选漫画 · ${books.length} 本`}
        size={600}
        onClose={() => !action.busy && onClose()}
      >
        <FormLock busy={action.busy}>
          <Space orientation="vertical" className="full-width" size={16}>
            <ActionNotice action={action} />
            {!same && <Alert type="error" title="作品与系列限定同一书库。" />}
            <Radio.Group
              value={mode}
              onChange={(e) => {
                setMode(e.target.value);
                setTargets([]);
              }}
              options={[
                { value: "work_merge", label: "合并到作品" },
                { value: "series_add", label: "加入系列" },
              ]}
            />
            <Alert
              type="warning"
              title="此处按所选版本对应的完整作品整理。合并将包含各作品的所有版本；仅移动一个版本请打开其组织面板。"
            />
            <div>
              {books.map((b) => (
                <Tag key={b.id}>{originalTitle(b)}</Tag>
              ))}
            </div>
            <OrganizationPicker
              key={mode}
              kind={mode === "work_merge" ? "works" : "series"}
              library={library}
              selected={targets}
              onChange={setTargets}
            />
            <Button
              type="primary"
              disabled={!same || !targets.length}
              onClick={() => void propose()}
            >
              查看整理影响
            </Button>
          </Space>
        </FormLock>
      </Drawer>
      {command && (
        <OperationPreview
          command={command}
          onCancel={() => setCommand(null)}
          onDone={() => {
            setCommand(null);
            onDone();
          }}
        />
      )}
    </>
  );
}
