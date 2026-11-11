import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { GalleryHighlightSettings } from "./GalleryHighlightSettings";
import {
  Alert,
  App,
  Button,
  Descriptions,
  Modal,
  Skeleton,
  Space,
  Switch,
  Tag,
} from "antd";
import { DownOutlined, ReloadOutlined, UpOutlined } from "@ant-design/icons";
import { Link } from "react-router";
import { ApiError, subscribeWrites, writesPending } from "../../shared/api";
import { enhancementAPI, useEnhancements } from "../../shared/enhancements";
import type {
  EnhancementPatch,
  Enhancements,
  FeatureKey,
} from "../../shared/enhancementFlow";
import {
  enhancementError,
  featureNames,
  featureStatus,
  resourceLabel,
  togglePatch,
} from "../../shared/enhancementFlow";
import { metadata } from "../../shared/metadata";
import type { TranslationSettings } from "../../shared/metadata";
import { taskURL } from "../../shared/consoleRoutes.mjs";
import {
  ErrorNotice,
  useAction,
  ActionNotice,
  useDirty,
  time,
} from "../common";
import { useRetainedRemote } from "../useRetainedRemote";
type Intent = {
  patch: EnhancementPatch;
  affected: number;
  current?: Enhancements;
  error?: string;
  needsReview?: boolean;
};
const descriptions: Record<FeatureKey, string> = {
  work_recommendations: "查找同一作品的不同版本",
  series_recommendations: "查找作品之间的系列归属",
  chinese_dictionary: "提供标签中文译名和完整标签联想",
  local_gallery_highlight: "在EH／EX标记已收录及待检查画廊",
};
const keys = Object.keys(featureNames) as FeatureKey[];
export function EnhancementSettings({
  focus,
  navigate,
}: {
  focus: string;
  navigate: (path: string) => void;
}) {
  const shared = useEnhancements(),
    { message, modal } = App.useApp();
  const globalBusy = useSyncExternalStore(
    subscribeWrites,
    writesPending,
    writesPending,
  );
  const highlightDirty = useRef(false);
  const setHighlightDirty = useCallback((dirty: boolean) => {
    highlightDirty.current = dirty;
  }, []);
  function expand(key: FeatureKey) {
    const apply = () => setExpanded((v) => ({ ...v, [key]: !v[key] }));
    if (
      key === "local_gallery_highlight" &&
      expanded[key] &&
      highlightDirty.current
    )
      modal.confirm({
        title: "放弃尚未保存的高亮配置？",
        content: "未保存的地址和授权码会从本页移除。",
        okText: "放弃并收起",
        cancelText: "继续配置",
        onOk: apply,
      });
    else apply();
  }
  const [expanded, setExpanded] = useState<
    Partial<Record<FeatureKey, boolean>>
  >(() => (keys.includes(focus as FeatureKey) ? { [focus]: true } : {}));
  const [intent, setIntent] = useState<Intent | null>(null),
    [busy, setBusy] = useState(false);
  const submitted = useRef(false),
    mounted = useRef(false);
  const release = useDirty(!!intent);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const data = shared.data,
    reload = shared.reload;
  const ready = !!data;
  useEffect(() => reload(), [reload]);
  const pending =
    !!data &&
    keys.some((key) => data.features[key]?.transition?.state === "pending");
  useEffect(() => {
    if (!pending) return;
    const timer = setInterval(() => {
      if (document.visibilityState === "visible") reload();
    }, 1500);
    return () => clearInterval(timer);
  }, [pending, reload]);
  useEffect(() => {
    if (!ready || !keys.includes(focus as FeatureKey)) return;
    const target = document.getElementById(`settings-${focus}`);
    target?.scrollIntoView({ block: "start" });
    target?.focus({ preventScroll: true });
  }, [ready, focus]);
  function cancel() {
    if (submitted.current) return;
    setIntent(null);
    release();
  }
  async function submit(next: Intent) {
    if (submitted.current || writesPending()) return;
    submitted.current = true;
    setBusy(true);
    setIntent(next);
    try {
      const result = await enhancementAPI.change(next.patch);
      shared.publish(result);
      if (!mounted.current) return;
      setIntent(null);
      release();
      const f = result.features[next.patch.feature];
      if (!f) throw Error("服务未返回功能状态");
      if (f.state === "failed")
        void message.error(enhancementError(f.transition?.error_code || ""));
      else if (f.state !== "stable") void message.info("切换已提交，正在收尾");
      else
        void message.success(
          `${featureNames[next.patch.feature]}${f.enabled ? "已开启" : "已关闭"}`,
        );
    } catch (error) {
      let current: Enhancements | undefined;
      try {
        current = await enhancementAPI.read();
        shared.publish(current);
      } catch {
        shared.reload();
      }
      if (mounted.current)
        setIntent({
          ...next,
          current,
          error: enhancementError(error instanceof ApiError ? error.code : ""),
          needsReview: true,
        });
    } finally {
      submitted.current = false;
      if (mounted.current) setBusy(false);
    }
  }
  function toggle(feature: FeatureKey, enabled: boolean) {
    if (
      !data?.features[feature] ||
      shared.loading ||
      busy ||
      intent ||
      writesPending()
    )
      return;
    const next = {
      patch: togglePatch(data, feature, enabled),
      affected: data.features[feature]!.affected_tasks,
    };
    if (!enabled && next.affected > 0) setIntent(next);
    else void submit(next);
  }
  function retry(feature: FeatureKey) {
    if (feature === "local_gallery_highlight") return;
    const transition = data?.features[feature]?.transition;
    if (!data || !transition || busy || intent) return;
    void submit({
      patch: {
        expected_revision: data.revision,
        feature,
        retry_transition_id: transition.id,
      },
      affected: transition.affected_tasks,
    });
  }
  function adopt() {
    if (!intent?.current) return;
    const current = intent.current,
      feature = intent.patch.feature;
    if (intent.patch.enabled === undefined) {
      const transition = current.features[feature]?.transition;
      if (transition?.state !== "failed") return;
      setIntent({
        patch: {
          expected_revision: current.revision,
          feature,
          retry_transition_id: transition.id,
        },
        affected: transition.affected_tasks,
      });
    } else
      setIntent({
        patch: togglePatch(current, feature, intent.patch.enabled),
        affected: current.features[feature]?.affected_tasks || 0,
      });
  }
  const intentFeature =
    intent && (intent.current || data)?.features[intent.patch.feature];
  const retryableReview =
    !!intent?.current &&
    !!intentFeature &&
    (!intent.patch.retry_transition_id ||
      intentFeature?.transition?.state === "failed");
  return (
    <>
      <div className="settings-section-actions">
        <Button
          type="text"
          aria-label="刷新增强设置"
          icon={<ReloadOutlined />}
          disabled={busy || globalBusy}
          onClick={shared.reload}
        />
      </div>
      <ErrorNotice error={shared.error} retry={shared.reload} />
      {!data && !shared.error && <Skeleton active paragraph={{ rows: 6 }} />}
      {data &&
        keys.map((key) => {
          if (!data.features[key])
            return (
              <section
                className="enhancement-row"
                key={key}
                id={`settings-${key}`}
                tabIndex={-1}
                aria-label={featureNames[key]}
              >
                <div className="enhancement-heading">
                  <div>
                    <h2>{featureNames[key]}</h2>
                    <p>服务尚未提供此功能，请升级后刷新。</p>
                  </div>
                  <div className="enhancement-controls">
                    <Switch
                      aria-label={`${featureNames[key]}开关`}
                      checked={false}
                      disabled
                    />
                    <Button
                      type="text"
                      aria-label={`${expanded[key] ? "收起" : "展开"}${featureNames[key]}配置`}
                      aria-expanded={!!expanded[key]}
                      icon={expanded[key] ? <UpOutlined /> : <DownOutlined />}
                      onClick={() => expand(key)}
                    />
                  </div>
                </div>
                {expanded[key] && key === "local_gallery_highlight" && (
                  <div className="enhancement-details">
                    <GalleryHighlightSettings
                      supported={false}
                      enabled={false}
                      onDirtyChange={setHighlightDirty}
                      refreshFeature={shared.reload}
                    />
                  </div>
                )}
              </section>
            );
          const feature = data.features[key],
            transitioning =
              feature.state === "enabling" || feature.state === "disabling";
          return (
            <section
              className="enhancement-row"
              key={key}
              id={`settings-${key}`}
              tabIndex={-1}
              aria-label={featureNames[key]}
            >
              <div className="enhancement-heading">
                <div>
                  <h2>{featureNames[key]}</h2>
                  <p>{descriptions[key]}</p>
                  <Space size={8} wrap>
                    <Tag
                      color={
                        feature.state === "failed"
                          ? "error"
                          : feature.effective_enabled
                            ? "success"
                            : undefined
                      }
                    >
                      {featureStatus(feature)}
                    </Tag>
                    <span className="settings-muted">
                      {resourceLabel(feature.resource_state, key)}
                    </span>
                  </Space>
                </div>
                <div className="enhancement-controls">
                  <Switch
                    aria-label={`${featureNames[key]}开关`}
                    checked={feature.enabled}
                    loading={
                      (busy && intent?.patch.feature === key) || transitioning
                    }
                    disabled={
                      globalBusy ||
                      busy ||
                      shared.loading ||
                      !!intent ||
                      transitioning ||
                      feature.state === "failed"
                    }
                    onChange={(enabled) => toggle(key, enabled)}
                  />
                  <Button
                    type="text"
                    aria-label={`${expanded[key] ? "收起" : "展开"}${featureNames[key]}配置`}
                    aria-expanded={!!expanded[key]}
                    icon={expanded[key] ? <UpOutlined /> : <DownOutlined />}
                    onClick={() => expand(key)}
                  />
                </div>
              </div>
              {feature.state === "failed" && (
                <div className="enhancement-result">
                  <Alert
                    type="error"
                    showIcon
                    title={enhancementError(
                      feature.transition?.error_code || "",
                    )}
                    description={
                      <Space wrap>
                        <span>执行权限保持关闭。</span>
                        <Button
                          disabled={busy || globalBusy || !!intent}
                          onClick={() => retry(key)}
                        >
                          重试切换
                        </Button>
                      </Space>
                    }
                  />
                </div>
              )}
              {transitioning && (
                <p className="settings-muted enhancement-result">
                  正在处理切换收尾
                  {feature.transition?.affected_tasks
                    ? `，本次涉及 ${feature.transition.affected_tasks} 个任务`
                    : ""}
                  。
                </p>
              )}
              {expanded[key] && (
                <div className="enhancement-details">
                  {key === "local_gallery_highlight" ? (
                    <GalleryHighlightSettings
                      supported
                      enabled={feature.effective_enabled}
                      onDirtyChange={setHighlightDirty}
                      refreshFeature={shared.reload}
                    />
                  ) : key === "chinese_dictionary" ? (
                    <DictionarySettings
                      enabled={feature.effective_enabled}
                      navigate={navigate}
                    />
                  ) : (
                    <>
                      <p className="settings-muted">
                        关闭后保留现有分组与建议；开启不会自动恢复暂停任务。
                      </p>
                      <Link
                        to={`/admin/maintenance?section=tasks&type=${key === "work_recommendations" ? "work_recommendation" : "series_recommendation"}`}
                      >
                        查看相关任务
                      </Link>
                    </>
                  )}
                </div>
              )}
            </section>
          );
        })}
      <Modal
        open={!!intent && (!busy || !!intent.error)}
        title={
          intent?.needsReview
            ? "请重新核对增强设置"
            : intent?.patch.enabled === false && intent.affected > 0
              ? `关闭${featureNames[intent.patch.feature]}？`
              : "确认增强设置"
        }
        closable={!busy}
        keyboard={!busy}
        mask={{ closable: !busy }}
        onCancel={cancel}
        footer={
          <Space>
            <Button disabled={busy || globalBusy} onClick={cancel}>
              取消
            </Button>
            {intent?.needsReview ? (
              <Button
                type="primary"
                disabled={!retryableReview || busy || globalBusy}
                onClick={adopt}
              >
                采用当前状态重新核对
              </Button>
            ) : (
              <Button
                type="primary"
                loading={busy}
                onClick={() => intent && void submit(intent)}
              >
                {intent?.patch.enabled === false && intent.affected > 0
                  ? `确认暂停 ${intent.affected} 个任务并关闭`
                  : "确认提交"}
              </Button>
            )}
          </Space>
        }
      >
        {intent?.error && <Alert type="warning" title={intent.error} />}
        {intent && !intent.needsReview && (
          <p>
            {intent.patch.enabled === false && intent.affected > 0
              ? `将暂停 ${intent.affected} 个任务，保留当前进度。重新开启不会自动恢复任务。`
              : "按当前核对的状态提交；不会自动启动或恢复任务。"}
          </p>
        )}
        {intent?.needsReview && (
          <>
            <p>
              你的操作意图仍保留。
              {intent.patch.enabled === undefined
                ? "重试切换"
                : intent.patch.enabled
                  ? "开启"
                  : "关闭"}
              ：{featureNames[intent.patch.feature]}。
            </p>
            {intent.current && intentFeature && (
              <p>
                当前状态：{featureStatus(intentFeature)}；当前排队／运行任务：
                {intentFeature.affected_tasks} 个。请核对后重新确认。
              </p>
            )}
            <Button
              onClick={async () => {
                try {
                  const current = await enhancementAPI.read();
                  shared.publish(current);
                  setIntent((v) => (v ? { ...v, current } : null));
                } catch {
                  shared.reload();
                }
              }}
            >
              重新读取当前状态
            </Button>
          </>
        )}
      </Modal>
    </>
  );
}
function DictionarySettings({
  enabled,
  navigate,
}: {
  enabled: boolean;
  navigate: (path: string) => void;
}) {
  const globalBusy = useSyncExternalStore(
    subscribeWrites,
    writesPending,
    writesPending,
  );
  const state = useRetainedRemote<TranslationSettings>(
      "/settings/tag-translations",
    ),
    action = useAction();
  return (
    <>
      <ErrorNotice error={state.error} retry={state.reload} />
      <ActionNotice action={action} />
      <Descriptions
        size="small"
        column={1}
        items={[
          {
            key: "version",
            label: "译库版本",
            children: state.data
              ? state.data.active?.version || "尚未安装"
              : state.error
                ? "未知"
                : "正在读取…",
          },
          {
            key: "time",
            label: "启用时间",
            children: time(state.data?.active?.activated_at),
          },
        ]}
      />
      <Button
        type="primary"
        disabled={globalBusy || !enabled || !state.data}
        loading={action.busy}
        onClick={() =>
          void action.run(async () => {
            const job = await metadata.updateTranslations();
            navigate(taskURL(job.job_id));
          }, "")
        }
      >
        安装／更新中文译库
      </Button>
      <details>
        <summary>来源与使用许可</summary>
        <p>
          译文来自{" "}
          <a
            href="https://github.com/EhTagTranslation/Database"
            target="_blank"
            rel="noreferrer"
          >
            EhTagTranslation / Database
          </a>
          ，由全体编辑者共同贡献。文本采用{" "}
          <a
            href="https://github.com/EhTagTranslation/Database/blob/master/LICENSE.md"
            target="_blank"
            rel="noreferrer"
          >
            CC BY-NC-SA 3.0 及附加条款
          </a>
          ；请阅读
          <a
            href="https://github.com/EhTagTranslation/Database/wiki/开发指南"
            target="_blank"
            rel="noreferrer"
          >
            上游使用说明
          </a>
          （下游使用前需提交项目简介 Issue）。词库不随源码分发。
        </p>
      </details>
    </>
  );
}
