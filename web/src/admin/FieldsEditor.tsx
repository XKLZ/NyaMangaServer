import { usePermissions } from "../shared/permissions";
import { useEffect, useReducer, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  Alert,
  Button,
  Dropdown,
  Input,
  InputNumber,
  AutoComplete,
  Select,
  Space,
  Table,
  Tooltip,
  Typography,
} from "antd";
import { MoreOutlined, SaveOutlined } from "@ant-design/icons";
import { ApiError, catalog } from "../shared/api";
import type { Book } from "../shared/api";
import { metadata } from "../shared/metadata";
import type { EditableField, TagName } from "../shared/metadata";
import { ActionNotice, FormLock, useAction } from "./common";
import {
  displayedValue,
  editorReducer,
  fieldLabels,
  isDirty,
  isPropertyField,
  propertyFields,
  effectiveValue,
  sameValue,
  submitDraft,
  valueText,
} from "./metadataForm";
import { TagEditor } from "./TagEditor";

export const categories = [
  ["Doujinshi", "同人志"],
  ["Manga", "漫画"],
  ["Artist CG", "画师CG"],
  ["Game CG", "游戏CG"],
  ["Western", "西方作品"],
  ["Non-H", "非成人"],
  ["Image Set", "图集"],
  ["Cosplay", "角色扮演"],
  ["Asian Porn", "亚洲影像"],
  ["Misc", "其他"],
];
export const languages = [
  ["chinese", "中文"],
  ["japanese", "日语"],
  ["english", "英语"],
  ["korean", "韩语"],
  ["french", "法语"],
  ["german", "德语"],
  ["spanish", "西班牙语"],
  ["russian", "俄语"],
  ["portuguese", "葡萄牙语"],
  ["italian", "意大利语"],
  ["thai", "泰语"],
  ["vietnamese", "越南语"],
  ["indonesian", "印尼语"],
  ["other", "其他"],
];
function propertyText(key: EditableField, value: unknown) {
  if (!isPropertyField(key)) return valueText(value);
  if (value == null) return "未知";
  const labels: Record<string, string> = {
    monochrome: "黑白",
    color: "彩色",
    uncensored: "无修正",
    mosaic: "马赛克修正",
    full: "完全修正",
  };
  return typeof value === "string"
    ? (labels[value] ?? value)
    : valueText(value);
}
function localDate(value: unknown) {
  if (typeof value !== "string" || !value || Number.isNaN(Date.parse(value)))
    return "";
  const date = new Date(value);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 19);
}
export function FieldsEditor({
  book,
  onSaved,
  dirty,
  onBusy,
  footer,
  submit = metadata.edit,
  reload = catalog.book,
  saveLabel = "保存修改",
  allowUnchanged = false,
  onRebased,
}: {
  book: Book;
  onSaved: (book: Book) => void;
  dirty: (value: boolean) => void;
  onBusy: (value: boolean) => void;
  footer: HTMLDivElement | null;
  submit?: typeof metadata.edit;
  reload?: (id: string) => Promise<Book>;
  saveLabel?: string;
  allowUnchanged?: boolean;
  onRebased?: (book: Book) => void;
}) {
  const verified = usePermissions().verified;
  const [state, dispatch] = useReducer(editorReducer, {
    base: book,
    draft: {},
    pending: {},
  });
  const [current, setCurrent] = useState<Book | null>(null);
  const [conflict, setConflict] = useState(false);
  const [tagReset, setTagReset] = useState(0);
  const action = useAction();
  const inflight = useRef(false);
  const changed = isDirty(state);
  useEffect(() => {
    dispatch({ type: "receive", book });
  }, [book]);
  useEffect(() => {
    dirty(changed);
    return () => dirty(false);
  }, [changed, dirty]);
  const details = state.base.metadata;
  if (!details)
    return <Alert type="warning" title="元数据详情暂不可用，请重新加载。" />;
  function change(key: EditableField, value: unknown) {
    dispatch({ type: "change", key, value });
    action.setNotice("");
  }
  function discard() {
    dispatch({ type: "discard", book });
    setCurrent(null);
    setConflict(false);
    setTagReset((v) => v + 1);
    action.setError(null);
    action.setNotice("");
  }
  async function save() {
    if (inflight.current || conflict) return;
    inflight.current = true;
    onBusy(true);
    try {
      await action.run(async () => {
        try {
          const updated =
            allowUnchanged && !changed
              ? await submit(state.base.id, state.base.revision, {})
              : await submitDraft(state, submit);
          dispatch({ type: "saved", book: updated });
          setCurrent(null);
          setConflict(false);
          onSaved(updated);
        } catch (error) {
          if (error instanceof ApiError && error.status === 409) {
            setConflict(true);
            // A failed conflict lookup must never replace the original save error.
            try {
              setCurrent(await reload(book.id));
            } catch {
              setCurrent(null);
            }
          }
          throw error;
        }
      }, "修改已保存");
    } finally {
      inflight.current = false;
      onBusy(false);
    }
  }
  function menu(key: EditableField) {
    return (
      <Dropdown
        trigger={["click"]}
        disabled={action.busy}
        menu={{
          items: [
            {
              key: "undo",
              label: "撤销本字段修改",
              disabled:
                !state.draft[key] &&
                !(key === "tags" && Object.values(state.pending).some(Boolean)),
            },
            {
              key: "restore",
              label: isPropertyField(key)
                ? "恢复自动识别"
                : details?.source
                  ? "恢复来源值"
                  : "恢复文件／默认值",
              disabled:
                !details?.overrides[key] &&
                !state.draft[key] &&
                !(
                  isPropertyField(key) &&
                  ["manual", "legacy_manual"].includes(
                    details?.properties?.[key].origin ?? "",
                  )
                ),
            },
          ],
          onClick: ({ key: operation }) => {
            dispatch({ type: operation === "undo" ? "undo" : "restore", key });
            if (key === "tags") setTagReset((v) => v + 1);
            action.setNotice("");
          },
        }}
      >
        <Button
          type="text"
          size="small"
          aria-label={`${fieldLabels[key]}菜单`}
          icon={<MoreOutlined aria-hidden="true" />}
        />
      </Dropdown>
    );
  }
  function field(key: Exclude<EditableField, "tags">) {
    const value = displayedValue(state, key);
    const id = `edit-${key}`;
    const labels = { id, "aria-label": fieldLabels[key] };
    let control;
    if (key === "languages") {
      control = (
        <Select
          {...labels}
          className="full-width"
          mode="tags"
          value={Array.isArray(value) ? (value as string[]) : []}
          options={languages.map(([value, label]) => ({ value, label }))}
          showSearch={{ optionFilterProp: "label" }}
          disabled={!details?.properties}
          placeholder="选择或输入语言；清空为未知"
          onChange={(values: string[]) =>
            change(key, [
              ...new Set(
                values.map((v) =>
                  v.normalize("NFKC").trim().replace(/\s+/g, " ").toLowerCase(),
                ),
              ),
            ])
          }
        />
      );
    } else if (isPropertyField(key)) {
      const options =
        key === "color_mode"
          ? [
              { value: "monochrome", label: "黑白" },
              { value: "color", label: "彩色" },
            ]
          : key === "censorship"
            ? [
                { value: "uncensored", label: "无修正" },
                { value: "mosaic", label: "马赛克修正" },
                { value: "full", label: "完全修正" },
              ]
            : [
                {
                  value: "true",
                  label: key === "translated" ? "已翻译" : "有",
                },
                {
                  value: "false",
                  label: key === "translated" ? "未翻译" : "无",
                },
              ];
      control = (
        <Select
          {...labels}
          className="full-width"
          disabled={!details?.properties}
          value={value == null ? "unknown" : String(value)}
          options={[{ value: "unknown", label: "未知" }, ...options]}
          onChange={(value) =>
            change(
              key,
              value === "unknown"
                ? null
                : ["translated", "extraneous_ads"].includes(key)
                  ? value === "true"
                  : value,
            )
          }
        />
      );
    } else if (key === "category") {
      const options = categories.map(([value, label]) => ({ value, label }));
      const text = String(value ?? "");
      if (text && !options.some((o) => o.value === text))
        options.push({ value: text, label: text });
      control = (
        <AutoComplete
          {...labels}
          className="full-width"
          allowClear
          value={text}
          options={options}
          filterOption={(input, option) =>
            `${option?.label} ${option?.value}`
              .toLowerCase()
              .includes(input.toLowerCase())
          }
          onChange={(v) => change(key, v)}
          placeholder="选择或输入自定义值"
        />
      );
    } else if (key === "rating")
      control = (
        <InputNumber
          {...labels}
          className="full-width"
          min={0}
          max={5}
          step={0.01}
          value={typeof value === "number" ? value : null}
          onChange={(v) => change(key, v)}
        />
      );
    else if (key === "description")
      control = (
        <Input.TextArea
          {...labels}
          value={String(value ?? "")}
          autoSize={{ minRows: 3, maxRows: 8 }}
          onChange={(e) => change(key, e.target.value)}
        />
      );
    else
      control = (
        <Input
          {...labels}
          type={key === "published_at" ? "datetime-local" : "text"}
          step={key === "published_at" ? 1 : undefined}
          value={
            key === "published_at" ? localDate(value) : String(value ?? "")
          }
          onChange={(e) =>
            change(
              key,
              key === "published_at" && e.target.value
                ? new Date(e.target.value).toISOString()
                : e.target.value,
            )
          }
        />
      );
    return (
      <div
        className={`metadata-field ${["title", "title_original", "description"].includes(key) ? "wide" : ""}`}
        key={key}
      >
        <div className="metadata-field-heading">
          <label htmlFor={id}>{fieldLabels[key]}</label>
          <Space size={4}>
            <Typography.Text type="secondary" className="small-text">
              {state.draft[key]
                ? "待保存"
                : details?.overrides[key]
                  ? "人工值"
                  : details?.source
                    ? "来源值"
                    : "文件／默认"}
            </Typography.Text>
            {menu(key)}
          </Space>
        </div>
        {control}
        {isPropertyField(key) && details?.properties && (
          <Typography.Text type="secondary" className="small-text">
            {details.properties[key].state === "conflict"
              ? "标记冲突"
              : details.properties[key].state === "unknown"
                ? "未知"
                : "已识别"}{" "}
            ·{" "}
            {
              {
                none: "无依据",
                tags: "标签",
                title: "标题",
                source: "来源",
                manual: "人工",
                legacy_manual: "原人工语言",
              }[details.properties[key].origin]
            }
            {details.properties[key].evidence.length > 0
              ? `：${details.properties[key].evidence.join("、")}`
              : ""}
            {["manual", "legacy_manual"].includes(
              details.properties[key].origin,
            ) &&
            !sameValue(
              key,
              details.properties[key].value,
              details.properties[key].automatic.value,
            )
              ? `；自动识别为 ${propertyText(key, details.properties[key].automatic.value)}，未同步原标签`
              : ""}
          </Typography.Text>
        )}
        {state.draft[key]?.op === "inherit" && (
          <Typography.Text type="secondary" className="small-text">
            保存后采用
            {isPropertyField(key)
              ? "自动识别"
              : details?.source
                ? "来源"
                : "文件／默认"}
            值：
            {propertyText(key, value)}
          </Typography.Text>
        )}
      </div>
    );
  }
  const saveBar = (
    <div className="metadata-save-bar">
      <Button
        type="primary"
        icon={<SaveOutlined aria-hidden="true" />}
        loading={action.busy}
        disabled={!verified || (!changed && !allowUnchanged) || conflict}
        onClick={() => void save()}
      >
        {saveLabel}
      </Button>
      <Tooltip title="更多编辑操作">
        <Dropdown
          trigger={["click"]}
          disabled={action.busy}
          menu={{
            items: [
              { key: "discard", label: "放弃全部修改", disabled: !changed },
            ],
            onClick: discard,
          }}
        >
          <Button
            aria-label="更多编辑操作"
            icon={<MoreOutlined aria-hidden="true" />}
          />
        </Dropdown>
      </Tooltip>
      {Object.values(state.pending).some(Boolean) && (
        <Typography.Text type="secondary" className="small-text">
          有尚未添加的标签文字，请先添加或清除。
        </Typography.Text>
      )}
    </div>
  );
  return (
    <FormLock busy={action.busy}>
      <div className="metadata-editor">
        <ActionNotice action={action} />
        {conflict && (
          <Alert
            type="warning"
            title="检测到其他编辑，草稿已保留"
            description={
              <Space orientation="vertical" className="full-width">
                {current ? (
                  <Table
                    size="small"
                    pagination={false}
                    rowKey="key"
                    dataSource={(Object.keys(state.draft).length
                      ? Object.keys(state.draft)
                      : [
                          "title",
                          "title_original",
                          "category",
                          "languages",
                          "tags",
                        ]
                    ).map((key) => ({
                      key,
                      label: fieldLabels[key as EditableField],
                      current: valueText(
                        effectiveValue(current, key as EditableField),
                      ),
                      draft: valueText(
                        displayedValue(state, key as EditableField),
                      ),
                    }))}
                    columns={[
                      { title: "字段", dataIndex: "label" },
                      { title: "当前值", dataIndex: "current" },
                      { title: "你的修改", dataIndex: "draft" },
                    ]}
                  />
                ) : (
                  <Typography.Text>
                    暂时无法读取当前值，请重新加载后核对。
                  </Typography.Text>
                )}
                <Button
                  onClick={() => {
                    if (!current) {
                      onBusy(true);
                      void action
                        .run(async () => {
                          setCurrent(await reload(book.id));
                        }, "已加载当前值，请核对差异。")
                        .finally(() => onBusy(false));
                      return;
                    }
                    dispatch({ type: "rebase", book: current });
                    (onRebased ?? onSaved)(current);
                    setCurrent(null);
                    setConflict(false);
                    action.setError(null);
                    action.setNotice("已保留草稿，请核对后再次保存。");
                  }}
                >
                  {current ? "核对后使用当前版本，保留草稿" : "重新加载当前值"}
                </Button>
              </Space>
            }
          />
        )}
        <section>
          <Typography.Title level={5}>基础信息</Typography.Title>
          <div className="metadata-grid">
            {(
              [
                "title_original",
                "title",
                "category",
                "rating",
                "published_at",
                "description",
              ] as const
            ).map(field)}
          </div>
        </section>
        <section>
          <Typography.Title level={5}>作品属性</Typography.Title>
          {!details.properties && (
            <Alert
              type="warning"
              title="结构化属性接口尚未就绪"
              description="等待 API 升级后重新加载。属性编辑暂不可用。"
            />
          )}
          <div className="metadata-grid">{propertyFields.map(field)}</div>
          {state.draft.tags && (
            <Typography.Text type="secondary">
              保存后按最终标签重新识别；当前自动识别依据来自已保存数据。
            </Typography.Text>
          )}
        </section>
        <section>
          <div className="metadata-section-heading">
            <Typography.Title level={5}>标签管理</Typography.Title>
            {menu("tags")}
          </div>
          {state.draft.tags?.op === "inherit" && (
            <Typography.Paragraph type="secondary">
              保存后采用来源标签；当前展示恢复后的预览。
            </Typography.Paragraph>
          )}
          <TagEditor
            key={tagReset}
            value={(displayedValue(state, "tags") as TagName[]) ?? []}
            known={details.tags}
            pending={state.pending}
            disabled={action.busy}
            ready={!!details.properties}
            onChange={(value) => change("tags", value)}
            onPending={(namespace, value) =>
              dispatch({ type: "pending", namespace, value })
            }
          />
        </section>
        {footer ? createPortal(saveBar, footer) : saveBar}
      </div>
    </FormLock>
  );
}
