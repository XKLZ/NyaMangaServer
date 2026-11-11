import { useState } from "react";
import { Alert, Button, Form, Input, Select, Space, Tag } from "antd";
import { metadata } from "../../shared/metadata";
import type { ProviderSettings, SecretChange } from "../../shared/metadata";
import { ActionNotice, FormLock, useAction, useDirty } from "../common";
const secrets: Record<string, string> = {
  ipb_member_id: "Cookie：ipb_member_id",
  ipb_pass_hash: "Cookie：ipb_pass_hash",
  igneous: "Cookie：igneous",
  proxy_username: "代理用户名",
  proxy_password: "代理密码",
};
type ProviderDraft = {
  site: ProviderSettings["site"];
  proxy: string;
  expected: number;
};
export function ProviderEditor({
  initial,
  onSaved,
}: {
  initial: ProviderSettings;
  onSaved: (value: ProviderSettings) => void;
}) {
  const [draft, setDraft] = useState<ProviderDraft | null>(null);
  const [current, setCurrent] = useState<ProviderSettings | null>(null);
  const action = useAction();
  const releaseDirty = useDirty(!!draft);
  const site = draft?.site ?? initial.site;
  const proxy = draft?.proxy ?? initial.proxy_url;
  const latest =
    current ?? (draft && draft.expected !== initial.revision ? initial : null);
  function edit(change: Partial<ProviderDraft>) {
    setDraft((value) => ({
      ...(value ?? {
        site: initial.site,
        proxy: initial.proxy_url,
        expected: initial.revision,
      }),
      ...change,
    }));
  }
  async function save() {
    await action.run(async () => {
      try {
        const updated = await metadata.configure(
          draft?.expected ?? initial.revision,
          site,
          proxy,
        );
        setDraft(null);
        setCurrent(null);
        releaseDirty();
        onSaved(updated);
      } catch (error) {
        setCurrent(await metadata.settings());
        throw error;
      }
    }, "站点与网络已保存");
  }
  return (
    <FormLock busy={action.busy}>
      <section className="settings-group" id="settings-provider" tabIndex={-1}>
        <h2>站点与代理</h2>
        <ActionNotice action={action} />
        {latest && (
          <Alert
            type="warning"
            title="站点设置版本已变化，草稿保留"
            description={
              <>
                <p>
                  当前站点：{latest.site.toUpperCase()}；出口：
                  {latest.proxy_url || "直连"}
                </p>
                <Button
                  onClick={() => {
                    edit({ expected: latest.revision });
                    onSaved(latest);
                    setCurrent(null);
                    action.setError(null);
                  }}
                >
                  核对后按当前版本保存草稿
                </Button>
              </>
            }
          />
        )}
        <Form layout="vertical" onFinish={() => void save()}>
          <Form.Item label="默认检索站点">
            <Select
              aria-label="默认检索站点"
              value={site}
              options={[
                { value: "eh", label: "E-Hentai" },
                { value: "ex", label: "ExHentai" },
              ]}
              onChange={(site) => edit({ site })}
            />
          </Form.Item>
          <Form.Item label="代理出口">
            <Input
              aria-label="代理出口"
              placeholder="留空为直连；http://、https:// 或 socks5://"
              value={proxy}
              onChange={(e) => edit({ proxy: e.target.value })}
            />
          </Form.Item>
          <Space wrap className="form-actions">
            <Button
              type="primary"
              htmlType="submit"
              loading={action.busy}
              disabled={!draft}
            >
              保存站点与网络
            </Button>
            <Button
              loading={action.busy}
              onClick={() =>
                void action.run(async () => {
                  await metadata.test();
                }, "连接测试通过")
              }
            >
              测试已保存的连接
            </Button>
            {draft && (
              <Button
                onClick={() => {
                  setDraft(null);
                  setCurrent(null);
                  releaseDirty();
                  action.setError(null);
                }}
              >
                放弃网络草稿
              </Button>
            )}
          </Space>
        </Form>
      </section>
    </FormLock>
  );
}
export function SecretsEditor({
  initial,
  onSaved,
}: {
  initial: ProviderSettings;
  onSaved: (value: ProviderSettings) => void;
}) {
  const [changes, setChanges] = useState<Record<string, SecretChange>>({});
  const [expected, setExpected] = useState<number | null>(null);
  const [current, setCurrent] = useState<ProviderSettings | null>(null);
  const action = useAction();
  const dirty = Object.values(changes).some((value) => value.op !== "keep");
  const releaseDirty = useDirty(dirty);
  const latest =
    current ??
    (dirty && expected !== null && expected !== initial.revision
      ? initial
      : null);
  function change(key: string, value: SecretChange) {
    const next = { ...changes, [key]: value };
    setChanges(next);
    setExpected(
      Object.values(next).some((value) => value.op !== "keep")
        ? (expected ?? initial.revision)
        : null,
    );
  }
  async function save() {
    await action.run(async () => {
      try {
        const updated = await metadata.credentials(
          expected ?? initial.revision,
          changes,
        );
        setChanges({});
        setExpected(null);
        setCurrent(null);
        releaseDirty();
        onSaved(updated);
      } catch (error) {
        setCurrent(await metadata.settings());
        throw error;
      }
    }, "凭据已加密保存");
  }
  return (
    <FormLock busy={action.busy}>
      <section
        className="settings-group"
        id="settings-credentials"
        tabIndex={-1}
      >
        <h2>站点凭据</h2>
        <p className="settings-muted">凭据只写，不回显；保存成功后清空输入。</p>
        <ActionNotice action={action} />
        {latest && (
          <Alert
            type="warning"
            title="凭据设置版本已变化，输入内容仍保留"
            description={
              <>
                <p>
                  当前配置状态：
                  {Object.entries(secrets)
                    .map(
                      ([key, label]) =>
                        `${label} ${latest.configured[key] ? "已配置" : "未配置"}`,
                    )
                    .join("；")}
                </p>
                <Button
                  onClick={() => {
                    setExpected(latest.revision);
                    onSaved(latest);
                    setCurrent(null);
                    action.setError(null);
                  }}
                >
                  核对状态后更新凭据提交版本
                </Button>
              </>
            }
          />
        )}
        <Form layout="vertical" onFinish={() => void save()}>
          <div className="settings-credential-groups">
            {(["cookie", "proxy"] as const).map((group) => (
              <fieldset key={group}>
                <legend>
                  {group === "cookie" ? "站点 Cookie" : "代理认证"}
                </legend>
                {Object.entries(secrets)
                  .filter(
                    ([key]) => (group === "proxy") === key.startsWith("proxy_"),
                  )
                  .map(([key, label]) => (
                    <Form.Item
                      key={key}
                      label={
                        <Space>
                          {label}
                          <Tag
                            color={
                              initial.configured[key] ? "success" : undefined
                            }
                          >
                            {initial.configured[key] ? "已配置" : "未配置"}
                          </Tag>
                        </Space>
                      }
                    >
                      <Space.Compact className="full-width">
                        <Select
                          aria-label={`${label}操作`}
                          value={changes[key]?.op ?? "keep"}
                          options={[
                            { value: "keep", label: "保留" },
                            { value: "set", label: "替换" },
                            { value: "clear", label: "清除" },
                          ]}
                          onChange={(op) => change(key, { op })}
                        />
                        {changes[key]?.op === "set" && (
                          <Input.Password
                            aria-label={label}
                            autoComplete="off"
                            value={changes[key]?.value ?? ""}
                            onChange={(e) =>
                              change(key, { op: "set", value: e.target.value })
                            }
                          />
                        )}
                      </Space.Compact>
                    </Form.Item>
                  ))}
              </fieldset>
            ))}
          </div>
          <Space>
            <Button htmlType="submit" disabled={!dirty} loading={action.busy}>
              保存凭据
            </Button>
            {dirty && (
              <Button
                onClick={() => {
                  setChanges({});
                  setExpected(null);
                  setCurrent(null);
                  releaseDirty();
                  action.setError(null);
                }}
              >
                放弃凭据草稿
              </Button>
            )}
          </Space>
        </Form>
      </section>
    </FormLock>
  );
}
