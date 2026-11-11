/** §2.1/§2.8/§3.6: one compact configuration column, one primary action.
 * A single generated code replaces the primary action; address only binds the script.
 * Existing React/Ant tokens and 8/16/24 rhythm; no new page or large cards. */
import { useLayoutEffect, useState, useSyncExternalStore } from "react";
import {
  Alert,
  Button,
  Form,
  Input,
  Modal,
  Pagination,
  Space,
  Tag,
} from "antd";
import { ApiError, subscribeWrites, writesPending } from "../../shared/api";
import { galleryHighlight } from "../../shared/galleryHighlight";
import {
  buildGalleryScript,
  connectionStateNames,
  connectionTime,
  insecureRemote,
  oneTimeAuthorization,
  serviceOrigin,
} from "../../shared/galleryHighlightFlow";
import type {
  GalleryConnection,
  GalleryConnections,
} from "../../shared/galleryHighlightFlow";
import type { ServiceInfo } from "../../shared/enhancementFlow";
import scriptTemplate from "../../../userscripts/gallery-highlight.user.js?raw";
import { useDirty } from "../common";
import { useRetainedRemote } from "../useRetainedRemote";
type Revoke = {
  baseline: GalleryConnection;
  current?: GalleryConnection;
  error?: string;
  needsReview?: boolean;
};
const fail = (error: unknown) =>
  error instanceof ApiError && error.code === "gallery_connection_limit"
    ? "已达20条有效授权，请先撤销不再使用的授权。"
    : error instanceof ApiError && error.code === "gallery_highlight_disabled"
      ? "请先开启本地画廊高亮，再生成授权码。"
      : "操作未完成，请核对当前授权列表。";
export function GalleryHighlightSettings({
  enabled,
  supported,
  onDirtyChange,
  refreshFeature,
}: {
  enabled: boolean;
  supported: boolean;
  onDirtyChange: (dirty: boolean) => void;
  refreshFeature: () => void;
}) {
  const [page, setPage] = useState(1),
    [address, setAddress] = useState(() => window.location.origin),
    [confirmed, setConfirmed] = useState(() => window.location.origin),
    [secret, setSecret] = useState<string | null>(null),
    [saved, setSaved] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [uncertain, setUncertain] = useState(false),
    [writing, setWriting] = useState(false),
    [reviewing, setReviewing] = useState(false),
    [revoke, setRevoke] = useState<Revoke | null>(null);
  const globalBusy = useSyncExternalStore(
      subscribeWrites,
      writesPending,
      writesPending,
    ),
    busy = writing || globalBusy;
  const state = useRetainedRemote<GalleryConnections>(
    galleryHighlight.path(page),
    supported,
  );
  const service = useRetainedRemote<ServiceInfo>(
    "/settings/service",
    supported,
  );
  const compatible =
    supported && !service.error && (service.data?.database_schema || 0) >= 22;
  function refresh() {
    state.reload();
    service.reload();
  }
  const dirty = address !== confirmed || (!!secret && !saved) || !!revoke;
  const release = useDirty(dirty);
  useLayoutEffect(() => {
    onDirtyChange(dirty);
    return () => onDirtyChange(false);
  }, [dirty, onDirtyChange]);
  function confirmAddress() {
    try {
      const origin = serviceOrigin(address);
      setAddress(origin);
      setConfirmed(origin);
      setError("");
    } catch (e) {
      setError((e as Error).message);
    }
  }
  async function create() {
    if (busy || !enabled || !compatible || secret || uncertain) return;
    try {
      if (serviceOrigin(address) !== confirmed || address !== confirmed)
        throw Error("请先确认服务地址");
    } catch (e) {
      setError((e as Error).message);
      return;
    }
    setWriting(true);
    setError("");
    setNotice("");
    try {
      const result = await galleryHighlight.create();
      const config = oneTimeAuthorization(result);
      setSecret(config);
      setSaved(false);
      setPage(1);
      state.reload();
    } catch (e) {
      const unknown =
        !(e instanceof ApiError) || e.status === 0 || e.status >= 500;
      setUncertain(unknown);
      setError(
        unknown
          ? "生成结果未确认。请刷新授权列表，明确撤销本次可能生成的授权后再重建；秘密无法从列表取回。"
          : fail(e),
      );
      if (e instanceof ApiError && e.code === "gallery_highlight_disabled")
        refreshFeature();
    } finally {
      setWriting(false);
    }
  }
  async function copy() {
    if (!secret || busy) return;
    try {
      if (!navigator.clipboard?.writeText) throw Error();
      await navigator.clipboard.writeText(secret);
      setSaved(true);
      setNotice("授权码已复制");
    } catch {
      setNotice("复制失败，请手动选择并复制下方授权码。");
    }
  }
  function download() {
    if (busy) return;
    try {
      if (address !== confirmed) throw Error("请先确认服务地址");
      const script = buildGalleryScript(scriptTemplate, confirmed),
        url = URL.createObjectURL(
          new Blob([script], { type: "text/javascript;charset=utf-8" }),
        ),
        link = document.createElement("a");
      link.href = url;
      link.download = "nyamanga-gallery-highlight.user.js";
      document.body.append(link);
      link.click();
      link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      setError("");
    } catch (e) {
      setError((e as Error).message);
    }
  }
  async function reread() {
    if (!revoke || busy || reviewing) return;
    setReviewing(true);
    try {
      const data = await galleryHighlight.list(page),
        current = data.items.find((v) => v.id === revoke.baseline.id);
      if (!current || current.revision < revoke.baseline.revision)
        throw Error();
      setRevoke({
        ...revoke,
        current,
        error: "已读取当前状态，请核对后明确撤销。",
        needsReview: true,
      });
      state.reload();
    } catch {
      setRevoke({
        ...revoke,
        current: undefined,
        error: "读取失败或授权已移出当前页，请重读或关闭后在列表重新选择。",
        needsReview: true,
      });
    } finally {
      setReviewing(false);
    }
  }
  async function remove() {
    if (!revoke || busy || reviewing || (revoke.needsReview && !revoke.current))
      return;
    setWriting(true);
    try {
      await galleryHighlight.revoke(revoke.current || revoke.baseline);
      setRevoke(null);
      state.reload();
      setNotice("授权已撤销");
    } catch (e) {
      setRevoke({
        ...revoke,
        current: undefined,
        needsReview: true,
        error:
          e instanceof ApiError &&
          e.code === "gallery_connection_revision_conflict"
            ? "授权版本已变化，原选择保留，请明确重新读取。"
            : "撤销结果未确认，请重新读取当前状态后再操作。",
      });
    } finally {
      setWriting(false);
    }
  }
  return (
    <section className="gallery-highlight-config" aria-label="本地画廊高亮配置">
      {!supported && (
        <Alert type="info" title="当前服务尚未提供此功能，请升级后刷新。" />
      )}
      {supported && !compatible && (
        <Alert
          type="info"
          title={
            service.error
              ? "无法核对服务协议，请刷新后重试。"
              : service.data
                ? "当前服务尚未支持协议2，请升级服务后刷新。"
                : "正在核对服务协议…"
          }
          action={
            <Button disabled={busy} onClick={refresh}>
              刷新
            </Button>
          }
        />
      )}
      {error && <Alert type="warning" showIcon title={error} />}
      <Form layout="vertical" onFinish={() => void create()}>
        <Form.Item
          label="NyaMangaServer地址"
          help={
            insecureRemote(confirmed)
              ? "远程HTTP会明文传输只读令牌，建议使用HTTPS。"
              : undefined
          }
        >
          <Space.Compact className="full-width">
            <Input
              aria-label="NyaMangaServer地址"
              value={address}
              disabled={busy}
              autoComplete="off"
              onChange={(e) => setAddress(e.target.value)}
            />
            <Button
              disabled={busy || address === confirmed}
              onClick={confirmAddress}
            >
              确认地址
            </Button>
          </Space.Compact>
        </Form.Item>
        {!secret && (
          <>
            <Button
              type="primary"
              htmlType="submit"
              loading={writing && !revoke}
              disabled={
                busy ||
                !compatible ||
                !enabled ||
                uncertain ||
                address !== confirmed
              }
            >
              生成授权码
            </Button>
            {!enabled && supported && (
              <p className="settings-muted">
                先开启本地画廊高亮，再生成授权码。
              </p>
            )}
          </>
        )}
      </Form>
      {uncertain && (
        <div className="enhancement-result">
          <Button disabled={busy} onClick={refresh}>
            刷新授权列表
          </Button>
          <Button
            type="text"
            disabled={busy}
            onClick={() => {
              setUncertain(false);
              setError("");
            }}
          >
            已核对并撤销，允许重新生成
          </Button>
        </div>
      )}
      {secret && (
        <div className="gallery-once" aria-label="一次性授权码">
          <h3>授权码（仅显示一次）</h3>
          <p>长期有效，撤销或改密后失效。请复制保存，列表无法再次取回。</p>
          <Input.TextArea
            aria-label="一次性授权码"
            autoComplete="off"
            spellCheck={false}
            readOnly
            value={secret}
            rows={2}
          />
          <Space wrap>
            <Button type="primary" disabled={busy} onClick={() => void copy()}>
              复制授权码
            </Button>
            <Button
              disabled={busy}
              onClick={() => {
                setSecret(null);
                setSaved(false);
                setNotice("");
                release();
              }}
            >
              已保存授权码，关闭
            </Button>
          </Space>
        </div>
      )}
      {notice && (
        <p role="status" className="enhancement-result">
          {notice}
        </p>
      )}
      <div className="gallery-install">
        <Button disabled={busy || address !== confirmed} onClick={download}>
          下载浏览器脚本
        </Button>
        <details>
          <summary>安装与配置</summary>
          <ol>
            <li>
              开启本地画廊高亮，生成并复制授权码；确认NyaMangaServer地址后下载脚本。
            </li>
            <li>
              在Chrome／Edge安装Tampermonkey，将下载的.user.js导入，或把完整内容粘贴到新建脚本后保存。
            </li>
            <li>
              启用Tampermonkey的Isolated Content
              Script上下文；脚本要求DOM隔离沙箱，其他或未知模式会拒绝读取配置。
            </li>
            <li>
              从脚本菜单选择“配置服务器”，分别输入完整NyaMangaServer地址与授权码。
            </li>
            <li>打开EH／EX画廊列表或详情；细边框和短标记表示本地正式记录。</li>
          </ol>
          <p>
            脚本只授权当前确认的服务主机。更换服务origin后请重新下载；不需要把令牌写进脚本。
          </p>
        </details>
      </div>
      {supported && (
        <section aria-label="浏览器授权列表" className="gallery-connections">
          <div className="gallery-connection-heading">
            <h3>浏览器授权</h3>
            <Button type="text" disabled={busy} onClick={refresh}>
              刷新授权列表
            </Button>
          </div>
          {!!state.error && (
            <Alert
              type="warning"
              title="授权列表读取失败，现有选择保持。"
              action={
                <Button disabled={busy} onClick={refresh}>
                  重试读取
                </Button>
              }
            />
          )}
          <ul>
            {state.data?.items.map((v) => (
              <li key={v.id}>
                <div>
                  <strong className="settings-value">{v.name}</strong>{" "}
                  <Tag>{connectionStateNames[v.state] || "未知状态"}</Tag>
                  <p className="settings-muted">
                    创建 {connectionTime(v.created_at)}
                  </p>
                </div>
                <Button
                  danger
                  type="text"
                  disabled={busy || v.state === "revoked"}
                  onClick={() => setRevoke({ baseline: { ...v } })}
                  aria-label={`撤销授权${v.name}`}
                >
                  撤销
                </Button>
              </li>
            ))}
          </ul>
          {!state.data && !state.error && <p role="status">正在读取授权…</p>}
          {state.data && (
            <div className="gallery-connection-pager">
              <span>{state.data.total}条授权</span>
              <Pagination
                size="small"
                current={page}
                pageSize={10}
                total={state.data.total}
                showSizeChanger={false}
                disabled={busy}
                onChange={setPage}
              />
            </div>
          )}
        </section>
      )}
      <Modal
        open={!!revoke}
        title={`撤销授权${revoke?.baseline.name || ""}？`}
        onCancel={() => !busy && !reviewing && setRevoke(null)}
        closable={!busy && !reviewing}
        keyboard={!busy && !reviewing}
        mask={{ closable: !busy && !reviewing }}
        footer={
          <Space>
            <Button
              disabled={busy || reviewing}
              onClick={() => setRevoke(null)}
            >
              取消
            </Button>
            {revoke?.needsReview && (
              <Button
                loading={reviewing}
                disabled={busy}
                onClick={() => void reread()}
              >
                重新读取授权状态
              </Button>
            )}
            <Button
              danger
              type="primary"
              loading={writing}
              disabled={
                busy || reviewing || (!!revoke?.needsReview && !revoke.current)
              }
              onClick={() => void remove()}
            >
              {revoke?.current ? "按已读取版本撤销" : "撤销此授权"}
            </Button>
          </Space>
        }
      >
        <p>此授权将失效，不影响漫画资料。</p>
        {revoke?.error && <Alert type="warning" title={revoke.error} />}
        {revoke?.current && (
          <p>当前：{connectionStateNames[revoke.current.state]}</p>
        )}
      </Modal>
    </section>
  );
}
