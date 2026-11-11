/** §2.1/§3.6: compact account table, one editor and one impact confirmation. */
import { useState, useRef, useEffect } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Table,
  Tag,
} from "antd";
import { ApiError, request } from "../../shared/api";
import type { Library } from "../../shared/api";
import type { Actor } from "../../shared/actorAccess.mjs";
import { usePermissions } from "../../shared/permissions";
import { useRemote, ErrorNotice, useDirty } from "../common";
type Account = Omit<Actor, "capabilities"> & { revision: number };
type Draft = {
  original: Account | null;
  username: string;
  password: string;
  role: "admin" | "user";
  enabled: boolean;
  grants: Record<string, "viewer" | "editor">;
  blocked: boolean;
};
export function AccountsSettings() {
  const accounts = useRemote<{ items: Account[] }>("/accounts"),
    libraries = useRemote<{ items: Library[] }>("/libraries");
  const { actor: self, verified } = usePermissions();
  const [draft, setDraft] = useState<Draft | null>(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState<unknown>(null),
    [impact, setImpact] = useState(false);
  const errorRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (error) {
      errorRef.current?.focus({ preventScroll: true });
      errorRef.current?.scrollIntoView({ block: "nearest" });
    }
  }, [error]);
  const dirty =
    !!draft &&
    (!draft.original ||
      draft.password !== "" ||
      draft.role !== draft.original.role ||
      draft.enabled !== draft.original.enabled ||
      JSON.stringify(draft.grants) !==
        JSON.stringify(draft.original.library_grants || {}));
  useDirty(dirty);
  function open(account: Account | null) {
    setError(null);
    setImpact(false);
    setDraft({
      original: account,
      username: account?.username || "",
      password: "",
      role: account?.role || "user",
      enabled: account?.enabled ?? true,
      grants: { ...account?.library_grants },
      blocked: false,
    });
  }
  function close() {
    if (busy) return;
    setDraft(null);
    setImpact(false);
    setError(null);
  }
  async function save() {
    if (!verified || !draft || busy || draft.blocked) return;
    if (
      draft.original &&
      !impact &&
      ((!draft.enabled && draft.original.enabled) ||
        draft.password ||
        (draft.original.id === self?.id && draft.role !== "admin"))
    ) {
      setImpact(true);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      if (!draft.original)
        await request("/accounts", "POST", {
          username: draft.username,
          password: draft.password,
        });
      else
        await request(`/accounts/${draft.original.id}`, "PUT", {
          expected_revision: draft.original.revision,
          role: draft.role,
          enabled: draft.enabled,
          library_grants: draft.role === "admin" ? {} : draft.grants,
          ...(draft.password ? { new_password: draft.password } : {}),
        });
      closeAfterSave();
      accounts.reload();
      window.dispatchEvent(new Event("actor-session-refresh"));
    } catch (e) {
      setError(e);
      setImpact(false);
      if (e instanceof ApiError && e.status === 409)
        setDraft({ ...draft, password: "", blocked: true });
    } finally {
      setDraft((d) => (d ? { ...d, password: "" } : d));
      setBusy(false);
    }
  }
  function closeAfterSave() {
    setDraft(null);
    setImpact(false);
  }
  return (
    <section className="accounts-settings" aria-label="账号管理">
      <div className="settings-section-actions">
        <h2>账号管理</h2>
        <Space>
          <Button onClick={accounts.reload} disabled={busy}>
            刷新账号
          </Button>
          <Button type="primary" onClick={() => open(null)}>
            创建账号
          </Button>
        </Space>
      </div>
      <ErrorNotice error={accounts.error || libraries.error} />
      <Table<Account>
        size="small"
        rowKey="id"
        pagination={false}
        dataSource={accounts.data?.items || []}
        scroll={{ x: 480 }}
        columns={[
          { title: "账号", dataIndex: "username" },
          {
            title: "角色",
            render: (_, a) => (a.role === "admin" ? "管理员" : "普通用户"),
          },
          {
            title: "状态",
            render: (_, a) => (
              <Space wrap>
                <Tag>{a.enabled ? "启用" : "停用"}</Tag>
                {a.must_change_password && <Tag>需改密</Tag>}
              </Space>
            ),
          },
          {
            title: "操作",
            render: (_, a) => <Button onClick={() => open(a)}>编辑授权</Button>,
          },
        ]}
      />
      <Modal
        title={draft?.original ? `编辑账号 · ${draft.username}` : "创建账号"}
        open={!!draft}
        onCancel={() => {
          if (dirty) {
            setError(Error("存在未保存的输入，请使用放弃修改关闭。"));
          } else close();
        }}
        closable={!busy}
        keyboard={!busy}
        mask={{ closable: !busy }}
        destroyOnHidden
        footer={
          <Space>
            {!verified && (
              <Button
                disabled={false}
                onClick={() =>
                  window.dispatchEvent(new Event("actor-session-refresh"))
                }
              >
                重试权限读取
              </Button>
            )}
            <Button disabled={busy} onClick={close}>
              放弃修改
            </Button>
            <Button
              type="primary"
              loading={busy}
              disabled={
                !verified ||
                !dirty ||
                draft?.blocked ||
                (!draft?.original && (!draft?.username || !draft?.password))
              }
              onClick={() => void save()}
            >
              {impact ? "确认影响并保存" : "保存账号"}
            </Button>
          </Space>
        }
        styles={{ body: { maxHeight: "60dvh", overflow: "auto" } }}
      >
        {draft && (
          <Form layout="vertical" disabled={busy}>
            <Form.Item label="用户名">
              <Input
                aria-label="账号用户名"
                disabled={!!draft.original}
                autoComplete="off"
                value={draft.username}
                maxLength={64}
                onChange={(e) =>
                  setDraft({ ...draft, username: e.target.value })
                }
              />
            </Form.Item>
            <Form.Item
              label={draft.original ? "重置密码（留空不修改）" : "临时密码"}
              help="12–128字符；新建或重置后必须首次改密。"
            >
              <Input.Password
                aria-label={draft.original ? "重置账号密码" : "账号临时密码"}
                autoComplete="new-password"
                value={draft.password}
                onChange={(e) => {
                  setImpact(false);
                  setDraft({ ...draft, password: e.target.value });
                }}
                maxLength={128}
              />
            </Form.Item>
            {draft.original ? (
              <>
                <Form.Item label="角色">
                  <Select
                    aria-label="账号角色"
                    value={draft.role}
                    options={[
                      { value: "user", label: "普通用户" },
                      { value: "admin", label: "管理员（全部书库）" },
                    ]}
                    onChange={(role) => {
                      setImpact(false);
                      setDraft({ ...draft, role });
                    }}
                  />
                </Form.Item>
                <Checkbox
                  checked={draft.enabled}
                  onChange={(e) => {
                    setImpact(false);
                    setDraft({ ...draft, enabled: e.target.checked });
                  }}
                >
                  启用账号
                </Checkbox>
                <h3>逐库授权</h3>
                {draft.role === "admin" ? (
                  <p>
                    管理员拥有全部书库权限；逐库授权仅用于普通用户，不与管理员身份混用。
                  </p>
                ) : (
                  (libraries.data?.items || []).map((l) => (
                    <div className="account-grant" key={l.id}>
                      <span>{l.display_name}</span>
                      <Select<string>
                        aria-label={`${l.display_name}授权`}
                        value={draft.grants[l.id] || "none"}
                        options={[
                          { value: "none", label: "无权限" },
                          { value: "viewer", label: "查看" },
                          { value: "editor", label: "编辑" },
                        ]}
                        onChange={(value) => {
                          const grants = { ...draft.grants };
                          if (value === "none") delete grants[l.id];
                          else grants[l.id] = value as "viewer" | "editor";
                          setDraft({ ...draft, grants });
                        }}
                      />
                    </div>
                  ))
                )}
              </>
            ) : (
              <p>新账号默认为普通用户、无书库授权。创建后可编辑逐库权限。</p>
            )}
            {impact && (
              <Alert
                type="warning"
                showIcon
                title="确认账号影响"
                description={
                  draft.original?.id === self?.id && draft.role !== "admin"
                    ? "将移除本人管理员权限；最后一位管理员不能降级。"
                    : "停用或重置密码将撤销该账号全部会话和高亮授权，并暂停相关活动工作；重新启用或授权不会自动恢复任务。"
                }
              />
            )}
            <div
              ref={errorRef}
              tabIndex={-1}
              role="group"
              aria-label="账号保存结果"
            >
              <ErrorNotice error={error} />
              {draft.blocked && (
                <Alert
                  type="warning"
                  title="账号已变化或受最后管理员保护。输入保留，密码已清空；请关闭后刷新核对，不会自动采用新版本。"
                />
              )}
            </div>
          </Form>
        )}
      </Modal>
    </section>
  );
}
