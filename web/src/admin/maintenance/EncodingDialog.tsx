import { useEffect, useRef, useState } from "react";
import { Alert, Button, Modal, Radio, Space, Tag } from "antd";
import { ApiError, request } from "../../shared/api";
import type { Book } from "../../shared/api";
import { encodingCommand, encodingNames } from "../../shared/scanFlow";
import type { Encoding, EncodingPreview } from "../../shared/scanFlow";
import { usePermissions } from "../../shared/permissions";
import { reason } from "../../shared/catalog";
import { ErrorNotice, useDirty, useRemote } from "../common";
import "./storage.css";
export function EncodingDialog({
  id,
  onClose,
  onChanged,
}: {
  id: string;
  onClose: () => void;
  onChanged: () => void;
}) {
  const preview = useRemote<EncodingPreview>(
    `/editions/${encodeURIComponent(id)}/encoding-preview`,
  );
  const book = useRemote<Book>(`/editions/${encodeURIComponent(id)}`);
  const permissions = usePermissions();
  const [draft, setDraft] = useState<{
    base: EncodingPreview;
    value: Encoding | "inherit";
  } | null>(null);
  const [busy, setBusy] = useState(false),
    [error, setError] = useState<unknown>(null),
    [blocked, setBlocked] = useState(false),
    [saved, setSaved] = useState<Book | null>(null);
  const lock = useRef(false);
  const conflictNotice = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (blocked) {
      conflictNotice.current?.focus({ preventScroll: true });
      conflictNotice.current?.scrollIntoView({ block: "nearest" });
    }
  }, [blocked]);
  const release = useDirty(!!draft);
  const shown = draft?.base || preview.data;
  const editable = !!book.data && permissions.edit(book.data.library_id);
  async function save() {
    if (!draft || !editable || blocked || lock.current) return;
    lock.current = true;
    setBusy(true);
    setError(null);
    try {
      const result = await request<Book>(
        `/editions/${encodeURIComponent(id)}/encoding`,
        "PUT",
        encodingCommand(draft.base, draft.value),
      );
      setSaved(result);
      setDraft(null);
      release();
      onChanged();
    } catch (e) {
      setError(e);
      if (e instanceof ApiError && e.status === 409) setBlocked(true);
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      centered
      width={900}
      className="encoding-modal"
      title="核对本文件页名编码"
      mask={{ closable: !busy && !draft }}
      closable={!busy && !draft}
      keyboard={!busy && !draft}
      onCancel={() => {
        if (!busy && !draft) onClose();
      }}
      footer={
        <Space wrap>
          <Button
            disabled={busy}
            onClick={() => {
              release();
              onClose();
            }}
          >
            {draft ? "放弃修改并返回" : "返回"}
          </Button>
          {blocked && (
            <Button
              disabled={busy}
              onClick={() => {
                setDraft(null);
                setBlocked(false);
                setError(null);
                preview.reload();
                book.reload();
              }}
            >
              重新读取并核对
            </Button>
          )}
          {!saved && (
            <Button
              type="primary"
              disabled={!draft || blocked || !editable || busy}
              loading={busy}
              onClick={() => void save()}
            >
              保存本文件编码
            </Button>
          )}
        </Space>
      }
    >
      <ErrorNotice
        error={blocked ? null : error || preview.error || book.error}
        retry={
          !draft
            ? () => {
                preview.reload();
                book.reload();
              }
            : undefined
        }
      />
      {blocked && (
        <div
          ref={conflictNotice}
          tabIndex={-1}
          role="group"
          aria-label="编码冲突"
        >
          <Alert
            type="warning"
            showIcon
            title="文件或解析依据已变化，原选择保留"
            description="核对后重新读取候选，再明确选择；不会自动提升提交版本。"
          />
        </div>
      )}
      {saved ? (
        <Alert
          type="success"
          showIcon
          title="本文件编码已保存"
          description={
            <p>
              文件内容版本未因编码设置改变。
              {saved.reading_state?.parsing_stale
                ? "页序已变化，旧阅读位置仍保留，需重新核对。"
                : "仅实际页序变化时才需核对旧阅读位置和人工页码封面。"}
              书库编码与其他漫画保持不变。
            </p>
          }
        />
      ) : (
        shown && (
          <>
            <p>
              当前设置：
              {book.data?.file.encoding_override
                ? encodingNames[book.data.file.encoding_override]
                : `继承书库（${encodingNames[shown.inherited_encoding] || "默认编码"}）`}
            </p>
            <p>选择能正确显示页名的编码。以下仅预览解析结果，不会自动修改。</p>
            <Radio.Group
              value={draft?.value}
              disabled={busy || blocked || !editable}
              className="encoding-options"
              onChange={(e) => setDraft({ base: shown, value: e.target.value })}
            >
              <Radio
                value="inherit"
                disabled={
                  !shown.candidates.some(
                    (v) => v.encoding === shown.inherited_encoding && v.valid,
                  )
                }
              >
                继承书库（
                {encodingNames[shown.inherited_encoding] || "默认编码"}）
              </Radio>
              <div className="encoding-candidates">
                {shown.candidates.map((candidate) => (
                  <section
                    className="encoding-candidate"
                    key={candidate.encoding}
                  >
                    <Radio
                      value={candidate.encoding}
                      disabled={!candidate.valid}
                    >
                      {encodingNames[candidate.encoding] || "其他编码"}
                    </Radio>
                    <Tag color={candidate.valid ? undefined : "warning"}>
                      {candidate.valid
                        ? `${candidate.page_count} 页`
                        : "无法解析"}
                    </Tag>
                    {candidate.valid ? (
                      <ol>
                        {candidate.sample.map((page) => (
                          <li key={page.page_index}>{page.display_name}</li>
                        ))}
                      </ol>
                    ) : (
                      <p>
                        {reason(
                          candidate.error_code || "invalid_archive_encoding",
                        )}
                      </p>
                    )}
                  </section>
                ))}
              </div>
            </Radio.Group>
          </>
        )
      )}
    </Modal>
  );
}
