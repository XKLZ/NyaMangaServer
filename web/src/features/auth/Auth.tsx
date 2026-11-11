import { useState } from "react";
import type { FormEvent } from "react";
import { api, ApiError } from "../../shared/api";
import { useDirty } from "../../admin/common";
import type { Session } from "../../shared/api";
export function PasswordField({
  id,
  label,
  value,
  onChange,
  autoComplete = "current-password",
  disabled = false,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  autoComplete?: string;
  disabled?: boolean;
}) {
  const [visible, setVisible] = useState(false);
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <div className="password-wrap">
        <input
          id={id}
          name={id}
          type={visible ? "text" : "password"}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          autoComplete={autoComplete}
          required
          maxLength={512}
          disabled={disabled}
        />
        <button
          className="reveal"
          disabled={disabled}
          type="button"
          onClick={() => setVisible(!visible)}
          aria-label={`${visible ? "隐藏" : "显示"}${label}`}
          aria-pressed={visible}
        >
          {visible ? "隐藏" : "显示"}
        </button>
      </div>
    </div>
  );
}
export function AuthForm({
  setup,
  onSignedIn,
  onInitializedElsewhere,
}: {
  setup: boolean;
  onSignedIn: (session: Session) => void;
  onInitializedElsewhere: () => void;
}) {
  const [username, setUsername] = useState("admin");
  const [token, setToken] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent) {
    event.preventDefault();
    setError("");
    if (setup && password !== confirmation) {
      setError("两次输入的密码不一致");
      return;
    }
    setBusy(true);
    try {
      onSignedIn(
        await (setup
          ? api.setup(token, password)
          : api.login(password, username)),
      );
    } catch (e) {
      if (e instanceof ApiError && e.code === "already_initialized")
        onInitializedElsewhere();
      else setError(e instanceof Error ? e.message : "请求失败");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="auth-card" aria-labelledby="auth-title">
      <p className="eyebrow">{setup ? "从这里开始" : "欢迎回来"}</p>
      <h1 id="auth-title">{setup ? "初始化管理员" : "登录"}</h1>
      <p className="muted">
        {setup
          ? "设置管理员密码，开始管理书库和漫画资料。"
          : "使用已授权账号访问漫画资料。"}
      </p>
      <form onSubmit={submit}>
        {!setup && (
          <div className="field">
            <label htmlFor="username">用户名</label>
            <input
              id="username"
              name="username"
              autoComplete="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              disabled={busy}
              maxLength={64}
              required
            />
          </div>
        )}
        {setup && (
          <PasswordField
            disabled={busy}
            id="setup-token"
            label="一次性初始化凭据"
            value={token}
            onChange={setToken}
            autoComplete="off"
          />
        )}
        <PasswordField
          disabled={busy}
          id="password"
          label={setup ? "管理员密码" : "密码"}
          value={password}
          onChange={setPassword}
          autoComplete={setup ? "new-password" : "current-password"}
        />
        {setup && (
          <>
            <p className="field-hint">12–128 个字符，支持空格和中文。</p>
            <PasswordField
              disabled={busy}
              id="confirmation"
              label="确认密码"
              value={confirmation}
              onChange={setConfirmation}
              autoComplete="new-password"
            />
          </>
        )}
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <button className="primary" type="submit" disabled={busy}>
          {busy ? "正在处理…" : setup ? "创建管理员" : "登录"}
          <span aria-hidden="true">↗</span>
        </button>
      </form>
      <p className="card-foot">
        {setup
          ? "初始化凭据由部署者提供，完成后不再可用。"
          : "账号由管理员创建，没有公开注册。"}
      </p>
    </section>
  );
}
export function Security({
  onChanged,
  required = false,
}: {
  onChanged: () => void;
  required?: boolean;
}) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const releaseDirty = useDirty(!!current || !!next || !!confirmation);
  async function submit(event: FormEvent) {
    event.preventDefault();
    setError("");
    if (next !== confirmation) {
      setError("两次输入的新密码不一致");
      return;
    }
    setBusy(true);
    try {
      await api.password(current, next);
      setCurrent("");
      setNext("");
      setConfirmation("");
      releaseDirty();
      onChanged();
    } catch (e) {
      setError(e instanceof Error ? e.message : "请求失败");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="security-card">
      <h2>{required ? "首次登录，请修改密码" : "修改本人密码"}</h2>
      <p className="muted">
        修改后，本账号的所有会话与高亮授权将失效，需要重新登录。
      </p>
      <form onSubmit={submit}>
        <PasswordField
          disabled={busy}
          id="current-password"
          label="当前密码"
          value={current}
          onChange={setCurrent}
        />
        <PasswordField
          disabled={busy}
          id="new-password"
          label="新密码"
          value={next}
          onChange={setNext}
          autoComplete="new-password"
        />
        <p className="field-hint">12–128 个字符，密码中的空格会被保留。</p>
        <PasswordField
          disabled={busy}
          id="confirm-new"
          label="确认新密码"
          value={confirmation}
          onChange={setConfirmation}
          autoComplete="new-password"
        />
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <button className="primary" disabled={busy}>
          {busy ? "正在保存…" : "保存新密码"}
          <span aria-hidden="true">↗</span>
        </button>
      </form>
    </section>
  );
}
