import { useCallback, useEffect, useRef, useState } from "react";
import { App, Drawer, Spin, Alert } from "antd";
import { importAPI } from "../../shared/imports";
import type { ImportEditor, ImportMember } from "../../shared/imports";
import { FieldsEditor } from "../FieldsEditor";
import { ErrorNotice, useDirty } from "../common";

export function ImportMetadataEditor({
  batch,
  member,
  onClose,
  onSaved,
}: {
  batch: string;
  member: ImportMember;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [context, setContext] = useState<ImportEditor | null>(null),
    [error, setError] = useState<unknown>(null),
    [dirty, setDirty] = useState(false),
    [busy, setBusy] = useState(false),
    [footer, setFooter] = useState<HTMLDivElement | null>(null);
  const latest = useRef<ImportEditor | null>(null),
    { modal } = App.useApp();
  useDirty(dirty || busy);
  const load = useCallback(
    async (signal?: AbortSignal) => {
      const value = await importAPI.editor(batch, member.id, signal);
      latest.current = value;
      return { ...value.book, metadata: value.metadata };
    },
    [batch, member.id],
  );
  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal)
      .then(() => {
        if (!controller.signal.aborted) setContext(latest.current);
      })
      .catch((e) => {
        if (!controller.signal.aborted) setError(e);
      });
    return () => controller.abort();
  }, [load]);
  const close = () => {
    if (busy) return;
    if (dirty)
      modal.confirm({
        title: "放弃尚未保存的人工修改？",
        content: "该项尚未确认，返回审核列表后仍可继续处理。",
        okText: "放弃修改",
        cancelText: "继续编辑",
        onOk: onClose,
      });
    else onClose();
  };
  const candidate = !!context?.candidate_revision;
  return (
    <Drawer
      open
      placement="right"
      size={860}
      title={candidate ? "核对并修改候选资料" : "人工录入元数据"}
      onClose={close}
      keyboard={!busy}
      closable={!busy}
      mask={{ closable: !busy }}
      styles={{ body: { overscrollBehavior: "contain" } }}
      footer={<div ref={setFooter} className="import-editor-footer" />}
    >
      <div className="import-editor-file">{member.filename}</div>
      <ErrorNotice error={error} />
      {!context && !error ? (
        <Spin aria-label="加载人工编辑资料" />
      ) : (
        context && (
          <>
            {candidate && (
              <Alert
                type="info"
                showIcon
                title="保存会同时采纳完整候选；已有人工覆盖保留。"
              />
            )}
            <FieldsEditor
              book={{ ...context.book, metadata: context.metadata }}
              footer={footer}
              dirty={setDirty}
              onBusy={setBusy}
              allowUnchanged
              saveLabel={candidate ? "采纳并保存修改" : "保存并确认"}
              reload={() => load()}
              onRebased={() => {
                if (latest.current) setContext(latest.current);
              }}
              submit={async (_id, revision, fields) =>
                importAPI.manual(batch, member.id, context, revision, fields)
              }
              onSaved={() => onSaved()}
            />
          </>
        )
      )}
    </Drawer>
  );
}
