import { useState } from "react";
import { Alert, Button, Form, Input, Tooltip } from "antd";
import { SaveOutlined, UndoOutlined, CloseOutlined } from "@ant-design/icons";
import { tagCatalog } from "../../shared/tagCatalog";
import type { CatalogDetail } from "../../shared/tagCatalog";
import { ActionNotice, FormLock, useAction, useDirty } from "../common";
import { translationOrigins } from "./tagCatalogFlow";
type Draft = {
  value: string;
  original: string;
  expected: number;
  inherit: boolean;
};
export function TagTranslation({
  tag,
  library,
  onChanged,
}: {
  tag: CatalogDetail;
  library: string;
  onChanged: () => void;
}) {
  const [draft, setDraft] = useState<Draft | null>(null),
    [current, setCurrent] = useState<CatalogDetail | null>(null);
  const action = useAction();
  const dirty = !!draft && (draft.inherit || draft.value !== draft.original);
  const release = useDirty(dirty, { keepTagBook: true, tagID: tag.id });
  const latest =
    current ||
    (dirty && tag.translation_revision > draft!.expected ? tag : null);
  const frozen = () =>
    draft || {
      value: tag.translation,
      original: tag.translation,
      expected: tag.translation_revision,
      inherit: false,
    };
  async function save() {
    if (!draft || !dirty || latest || (!draft.inherit && !draft.value.trim()))
      return;
    await action.run(
      async () => {
        try {
          await tagCatalog.translate(
            tag.id,
            draft.expected,
            draft.inherit ? null : draft.value,
          );
          setDraft(null);
          setCurrent(null);
          release();
          onChanged();
        } catch (error) {
          try {
            setCurrent(await tagCatalog.detail(tag.id, library));
          } catch {
            /* Keep the save error and frozen draft if rereading also fails. */
          }
          throw error;
        }
      },
      draft?.inherit ? "已恢复继承译名" : "人工译名已保存",
    );
  }
  return (
    <section className="tag-translation" aria-label="人工译名">
      <div className="tag-section-heading">
        <h3>译名</h3>
        <span className="tag-original">
          全局 · {translationOrigins[tag.translation_origin] || "原文"}
        </span>
      </div>
      <FormLock busy={action.busy}>
        <ActionNotice action={action} />
        {latest && (
          <Alert
            type="warning"
            title="译名已变化，草稿保留"
            description={
              <>
                <p>
                  当前译名：{latest.translation || latest.name}（
                  {translationOrigins[latest.translation_origin] || "原文"}）
                </p>
                <Button
                  onClick={() => {
                    setDraft({
                      ...frozen(),
                      expected: latest.translation_revision,
                    });
                    setCurrent(null);
                    action.setError(null);
                  }}
                >
                  核对后保留草稿并采用此版本
                </Button>
              </>
            }
          />
        )}
        {draft?.inherit && (
          <Alert type="info" title="保存后恢复继承译库；没有译文时显示原文。" />
        )}
        <Form layout="vertical" onFinish={() => void save()}>
          <div className="tag-translation-row">
            <Form.Item
              className="tag-translation-input"
              help={
                draft && !draft.inherit && !draft.value.trim()
                  ? "请输入译名；恢复译库请使用恢复继承。"
                  : undefined
              }
              validateStatus={
                draft && !draft.inherit && !draft.value.trim()
                  ? "error"
                  : undefined
              }
            >
              <Input
                aria-label="标签中文译名"
                name="tag_translation"
                autoComplete="off"
                maxLength={512}
                value={draft?.value ?? tag.translation}
                disabled={!!draft?.inherit}
                onChange={(e) => {
                  const next = { ...frozen(), value: e.target.value };
                  setDraft(next.value === next.original ? null : next);
                  if (next.value === next.original) setCurrent(null);
                }}
              />
            </Form.Item>
            <div className="tag-translation-actions">
              <Tooltip title="保存译名" trigger={["hover", "focus"]}>
                <Button
                  aria-label="保存译名"
                  icon={<SaveOutlined aria-hidden />}
                  type="primary"
                  htmlType="submit"
                  loading={action.busy}
                  disabled={
                    !dirty ||
                    !!latest ||
                    (!draft?.inherit && !draft?.value.trim())
                  }
                />
              </Tooltip>
              <Tooltip title="恢复继承" trigger={["hover", "focus"]}>
                <Button
                  aria-label="恢复继承"
                  icon={<UndoOutlined aria-hidden />}
                  disabled={tag.translation_origin !== "manual"}
                  onClick={() => setDraft({ ...frozen(), inherit: true })}
                />
              </Tooltip>
              {draft && (
                <Tooltip title="放弃译名修改" trigger={["hover", "focus"]}>
                  <Button
                    aria-label="放弃译名修改"
                    icon={<CloseOutlined aria-hidden />}
                    onClick={() => {
                      setDraft(null);
                      setCurrent(null);
                      release();
                      action.setError(null);
                    }}
                  />
                </Tooltip>
              )}
            </div>
          </div>
        </Form>
      </FormLock>
    </section>
  );
}
