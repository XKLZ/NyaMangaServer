import { resourceEvent } from "../shared/events";
import { useEffect, useId, useRef, useState } from "react";
import {
  Alert,
  Button,
  Input,
  Spin,
  Select,
  Tag as TagChip,
  Tooltip,
  Typography,
} from "antd";
import { PlusOutlined } from "@ant-design/icons";
import { metadata } from "../shared/metadata";
import {
  dictionaryLabel,
  selectedDictionaryLabel,
  mergeSuggestionPage,
  requestSuggestionPage,
  suggestionConditions,
  utf8QueryValid,
} from "./tagSuggestions";
import type { SuggestionPages } from "./tagSuggestions";
import type { Tag, TagName, TagSuggestion } from "../shared/metadata";
import {
  availableTags,
  mergeTags,
  namespaces,
  parseTags,
  tagKeyAction,
} from "./tagInput";
import { tagKey } from "./metadataForm";

type Props = {
  value: TagName[];
  known: Tag[];
  pending: Record<string, string>;
  disabled: boolean;
  ready: boolean;
  onChange: (tags: TagName[]) => void;
  onPending: (namespace: string, value: string) => void;
};
export function TagEditor(props: Props) {
  const [extra, setExtra] = useState<string[]>(() =>
    props.value.map((tag) => tag.namespace),
  );
  const groups = [
    ...namespaces.map(([namespace, label]) => ({ namespace, label })),
    ...[...new Set([...props.value.map((t) => t.namespace), ...extra])]
      .filter((ns) => !namespaces.some(([n]) => n === ns))
      .sort()
      .map((namespace) => ({ namespace, label: namespace })),
  ];
  const newNamespace = props.pending.__namespace ?? "";
  const [error, setError] = useState("");
  return (
    <div className="tag-editor">
      {groups
        .filter(
          ({ namespace }) =>
            extra.includes(namespace) ||
            props.value.some((t) => t.namespace === namespace) ||
            !!props.pending[namespace],
        )
        .map(({ namespace, label }) => (
          <TagGroup
            key={namespace}
            {...props}
            onChange={(next) => {
              setExtra((previous) => [
                ...new Set([
                  ...previous,
                  ...props.value.map((t) => t.namespace),
                  ...next.map((t) => t.namespace),
                ]),
              ]);
              props.onChange(next);
            }}
            namespace={namespace}
            label={label}
          />
        ))}
      <Select
        className="full-width"
        aria-label="添加标签分区"
        placeholder="添加标签分区"
        value={null}
        disabled={props.disabled}
        options={namespaces
          .filter(
            ([ns]) =>
              !extra.includes(ns) &&
              !props.value.some((t) => t.namespace === ns),
          )
          .map(([value, label]) => ({ value, label: `${label} · ${value}` }))}
        onChange={(ns: string) => setExtra((previous) => [...previous, ns])}
      />
      <div className="tag-add-namespace">
        <Input
          aria-label="自定义标签类型"
          placeholder="自定义 namespace"
          value={newNamespace}
          disabled={props.disabled}
          onChange={(e) => {
            props.onPending("__namespace", e.target.value);
          }}
        />
        <Button
          disabled={props.disabled || !newNamespace.trim()}
          onClick={() => {
            const next = newNamespace.trim().toLowerCase();
            if (!/^[a-z][a-z0-9_]{0,31}$/.test(next)) {
              setError("类型应为小写字母开头，最多32个字母、数字或下划线。");
              return;
            }
            setExtra((v) => [...new Set([...v, next])]);
            props.onPending("__namespace", "");
            setError("");
          }}
        >
          添加分区
        </Button>
      </div>
      {error && <Alert type="error" title={error} />}
    </div>
  );
}
function TagGroup({
  namespace,
  label,
  value,
  known,
  pending,
  disabled,
  ready,
  onChange,
  onPending,
}: Props & { namespace: string; label: string }) {
  const input = pending[namespace] ?? "";
  const selected = value.filter((t) => t.namespace === namespace);
  const [catalog, setCatalog] = useState<TagSuggestion[]>([]);
  const [results, setResults] = useState<SuggestionPages | null>(null);
  const resultsRef = useRef<SuggestionPages | null>(null);
  const conditionKey = JSON.stringify(
    suggestionConditions(namespace, input, value),
  );
  const [pageRequest, setPageRequest] = useState({
    key: "",
    page: 1,
    attempt: 0,
  });
  useEffect(() => {
    const changed = (e: Event) => {
      if (
        !["enhancements", "tag_follow"].includes(
          (e as CustomEvent<{ type?: string }>).detail?.type || "",
        )
      )
        return;
      resultsRef.current = null;
      setResults(null);
      setCatalog([]);
      setPageRequest((v) => ({ ...v, page: 1, attempt: v.attempt + 1 }));
    };
    window.addEventListener(resourceEvent, changed);
    return () => window.removeEventListener(resourceEvent, changed);
  }, []);
  const page = pageRequest.key === conditionKey ? pageRequest.page : 1;
  const validQuery = utf8QueryValid(input);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const composing = useRef(false);
  const listID = useId();
  const inputRef = useRef<import("antd").InputRef>(null);
  const candidates =
    results?.key === conditionKey
      ? availableTags(results.result.items, value)
      : [];
  useEffect(() => {
    if (open && active >= 0)
      document
        .getElementById(`${listID}-${active}`)
        ?.scrollIntoView({ block: "nearest" });
  }, [open, active, listID]);
  const translations = new Map(
    [...catalog, ...known].map((t) => [tagKey(t), t]),
  );
  useEffect(() => {
    if (!open || disabled || !ready || !validQuery) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      setLoading(true);
      const conditions = JSON.parse(conditionKey) as ReturnType<
        typeof suggestionConditions
      >;
      const snapshot =
        page > 1 && resultsRef.current?.key === conditionKey
          ? resultsRef.current.result.snapshot_revision
          : undefined;
      void requestSuggestionPage(
        {
          ...conditions,
          page,
          ...(snapshot ? { snapshot_revision: snapshot } : {}),
        },
        controller.signal,
        metadata.suggestions,
      )
        .then((result) => {
          if (!result || controller.signal.aborted) return;
          const merged = mergeSuggestionPage(
            resultsRef.current,
            conditionKey,
            result,
          );
          resultsRef.current = merged;
          setResults(merged);
          setActive(-1);
          setCatalog((previous) => [
            ...new Map(
              [...previous, ...result.items].map((tag) => [tagKey(tag), tag]),
            ).values(),
          ]);
          setError("");
        })
        .catch((error: unknown) => {
          if (!controller.signal.aborted)
            setError(
              error instanceof Error ? error.message : "标签联想加载失败",
            );
        })
        .finally(() => {
          if (!controller.signal.aborted) setLoading(false);
        });
    }, 200);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [
    open,
    disabled,
    ready,
    validQuery,
    conditionKey,
    page,
    pageRequest.attempt,
  ]);
  function add(tags: TagName[]) {
    if (disabled) return;
    try {
      onChange(mergeTags(value, tags));
      onPending(namespace, "");
      setActive(-1);
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "无法添加标签");
    }
  }
  function create(text = input) {
    try {
      add(parseTags(text, namespace));
    } catch (e) {
      setError(e instanceof Error ? e.message : "标签格式错误");
    }
  }
  return (
    <section className="tag-group" aria-label={`${label}标签`}>
      <div className="tag-group-heading">
        <strong>{label}</strong>
        <Typography.Text type="secondary">
          {namespace} · {selected.length}
        </Typography.Text>
      </div>
      <div className="tag-chips">
        {selected.map((t) => {
          const translated = translations.get(tagKey(t));
          return (
            <Tooltip
              key={tagKey(t)}
              title={`${t.namespace}:${t.name} · ${dictionaryLabel(translated?.dictionary_match)}`}
            >
              <TagChip
                closable={!disabled}
                onClose={(e) => {
                  e.preventDefault();
                  onChange(value.filter((v) => tagKey(v) !== tagKey(t)));
                }}
              >
                {translated?.translation &&
                translated.translation !== t.name ? (
                  <>
                    {translated.translation}
                    <span className="tag-original">{t.name}</span>
                  </>
                ) : (
                  t.name
                )}
                {selectedDictionaryLabel(translated?.dictionary_match) && (
                  <span className="tag-custom">
                    {selectedDictionaryLabel(translated?.dictionary_match)}
                  </span>
                )}
              </TagChip>
            </Tooltip>
          );
        })}
      </div>
      <div
        className="tag-combobox"
        onBlur={(e) => {
          if (!e.currentTarget.contains(e.relatedTarget)) {
            setOpen(false);
            setActive(-1);
          }
        }}
      >
        <div className="tag-input-row">
          <Input
            ref={inputRef}
            aria-label={`添加${label}标签`}
            role="combobox"
            aria-autocomplete="list"
            aria-expanded={open && !disabled}
            aria-controls={listID}
            aria-activedescendant={
              open && active >= 0 && candidates[active]
                ? `${listID}-${active}`
                : undefined
            }
            value={input}
            disabled={disabled}
            placeholder="搜索英文或中文，Enter 添加"
            onFocus={() => setOpen(true)}
            onChange={(e) => {
              onPending(namespace, e.target.value);
              setActive(-1);
              setOpen(true);
            }}
            onCompositionStart={() => {
              composing.current = true;
            }}
            onCompositionEnd={() => {
              composing.current = false;
            }}
            onKeyDown={(e) => {
              const action = tagKeyAction(
                e.key,
                composing.current ||
                  e.nativeEvent.isComposing ||
                  e.keyCode === 229,
                input,
                open,
                active,
                candidates.length,
              );
              if (action.action === "none") return;
              e.preventDefault();
              if (action.action === "navigate") {
                setOpen(true);
                setActive(action.index);
              }
              if (action.action === "close") {
                setOpen(false);
                setActive(-1);
              }
              if (action.action === "select") add([candidates[action.index]]);
              if (action.action === "create") create();
              if (action.action === "remove" && selected.length)
                onChange(
                  value.filter((t) => tagKey(t) !== tagKey(selected.at(-1)!)),
                );
            }}
            onPaste={(e) => {
              const text = e.clipboardData.getData("text");
              if (!/[\n\r,;，；\t]|[a-z][a-z0-9_]{0,31}:/i.test(text)) return;
              e.preventDefault();
              // Preserve any existing query instead of replacing it with a paste.
              if (input) {
                onPending(namespace, input + text);
                setError("请先添加或清除原有输入，再解析批量标签。");
                return;
              }
              try {
                const tags = parseTags(text, namespace);
                const next = mergeTags(value, tags);
                onChange(next);
                setError("");
                setActive(-1);
              } catch (err) {
                onPending(namespace, text);
                setError(err instanceof Error ? err.message : "标签格式错误");
              }
            }}
          />
          <Button
            aria-label={`添加${label}输入的标签`}
            icon={<PlusOutlined aria-hidden="true" />}
            disabled={disabled || !input.trim()}
            onClick={() => create()}
          />
        </div>
        {open && !disabled && (
          <div className="tag-suggestions">
            {!ready && (
              <Typography.Text type="warning">
                标签联想接口尚未就绪，请等待 API 升级。
              </Typography.Text>
            )}
            {!validQuery && (
              <Typography.Text type="warning">
                查询最多512 UTF-8字节，请缩短输入。
              </Typography.Text>
            )}
            {ready && validQuery && loading && <Spin size="small" />}
            <div role="listbox" id={listID} aria-label={`${label}标签候选`}>
              {candidates.map((t, i) => (
                <div
                  role="option"
                  id={`${listID}-${i}`}
                  key={tagKey(t)}
                  aria-selected={active === i}
                  className={active === i ? "tag-option active" : "tag-option"}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => {
                    add([t]);
                    inputRef.current?.focus();
                  }}
                >
                  <span>{t.translation || t.name}</span>
                  <Typography.Text type="secondary">
                    使用 {t.usage_count} 本 ·{" "}
                    {dictionaryLabel(t.dictionary_match)}
                  </Typography.Text>
                  {t.translation !== t.name && (
                    <Typography.Text type="secondary">{t.name}</Typography.Text>
                  )}
                </div>
              ))}
            </div>
            {ready && validQuery && !loading && !candidates.length && (
              <Typography.Text type="secondary">
                暂无候选，可输入英文标签自定义添加
              </Typography.Text>
            )}
            {ready && results?.key === conditionKey && (
              <>
                <Typography.Text type="secondary">
                  {results.result.dictionary_state === "ready"
                    ? "本地译库已就绪"
                    : results.result.dictionary_state === "disabled"
                      ? "译库未启用，仅查询本地标签"
                      : results.result.dictionary_state === "not_downloaded"
                        ? "尚未下载译库，仅查询本地标签"
                        : "译库暂不可用，仅查询本地标签"}
                </Typography.Text>
                {results.result.items.length < results.result.total && (
                  <Button
                    size="small"
                    loading={loading}
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() =>
                      setPageRequest((previous) => ({
                        key: conditionKey,
                        page: results.result.page + 1,
                        attempt: previous.attempt + 1,
                      }))
                    }
                  >
                    加载更多候选（{results.result.items.length}/
                    {results.result.total}）
                  </Button>
                )}
                {!!error && (
                  <Button
                    size="small"
                    onClick={() =>
                      setPageRequest((previous) => ({
                        key: conditionKey,
                        page: 1,
                        attempt: previous.attempt + 1,
                      }))
                    }
                  >
                    重新查询
                  </Button>
                )}
              </>
            )}
          </div>
        )}
      </div>
      {error && <Alert type="error" title={error} />}
    </section>
  );
}
