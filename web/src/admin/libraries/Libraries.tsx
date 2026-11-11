import { randomIdentity } from "../../shared/randomIdentity";
import { usePermissions, AdminOnly, EditOnly } from "../../shared/permissions";
import { taskURL } from "../../shared/consoleRoutes.mjs";
/** §2.1/§2.8: library identity and counts support the row's import action.
 * Existing Ant tokens and 8/12/16 spacing; maintenance stays in secondary menus. */
import { useRef, useState } from "react";
import {
  Alert,
  App as AntApp,
  Button,
  Card,
  Dropdown,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from "antd";
import {
  PlusOutlined,
  SettingOutlined,
  MoreOutlined,
  HistoryOutlined,
} from "@ant-design/icons";
import { reason } from "../../shared/catalog";
import { ApiError, catalog, request } from "../../shared/api";
import type { Library, LibraryInput, Root } from "../../shared/api";
import { importAPI } from "../../shared/imports";
import {
  RefreshButton,
  FormLock,
  ActionNotice,
  useAction,
  useRemote,
  ErrorNotice,
  useDirty,
} from "../common";
import { MaintenanceDialog } from "../maintenance/Maintenance";
import "./libraryImport.css";
export function LibrariesPage({
  navigate,
}: {
  navigate: (path: string) => void;
}) {
  const permissions = usePermissions();
  const libraries = useRemote<{ items: Library[] }>("/libraries"),
    roots = useRemote<{ items: Root[] }>(
      "/library-roots",
      undefined,
      "GET",
      permissions.admin,
    );
  const [editing, setEditing] = useState<Library | "new" | null>(null),
    [advanced, setAdvanced] = useState<Library | null>(null);
  const [maintenance, setMaintenance] = useState<{
    operation: string;
    library: Library;
  } | null>(null);
  const [importing, setImporting] = useState<string | null>(null),
    lock = useRef(false),
    keys = useRef(new Map<string, string>());
  const action = useAction();
  const blockReason = (l: Library) =>
    l.trashed_at
      ? "书库在回收站中，请先恢复"
      : l.disabled
        ? "书库已停用，请先启用"
        : l.error_code
          ? reason(l.error_code)
          : l.availability &&
              l.availability !== "available" &&
              l.availability !== "unknown"
            ? "漫画目录不可用，请检查目录状态"
            : "";
  async function start(l: Library) {
    if (lock.current) return;
    if (l.active_import) {
      navigate(
        `/admin/imports?batch=${encodeURIComponent(l.active_import.id)}`,
      );
      return;
    }
    if (blockReason(l)) return;
    lock.current = true;
    setImporting(l.id);
    await action.run(async () => {
      let key = keys.current.get(l.id);
      if (!key) {
        key = randomIdentity();
        keys.current.set(l.id, key);
      }
      try {
        const b = await importAPI.create(l.id, key);
        keys.current.delete(l.id);
        navigate(`/admin/imports?batch=${encodeURIComponent(b.id)}`);
      } catch (e) {
        if (e instanceof ApiError && e.status === 409) {
          const fresh = await request<{ items: Library[] }>("/libraries");
          libraries.setData(fresh);
          const current = fresh.items.find((v) => v.id === l.id)?.active_import;
          if (current) {
            keys.current.delete(l.id);
            navigate(`/admin/imports?batch=${encodeURIComponent(current.id)}`);
            return;
          }
        }
        throw e;
      }
    }, "");
    lock.current = false;
    setImporting(null);
  }
  function menuAction(key: string, l: Library) {
    if (key === "advanced") {
      setAdvanced(l);
      return;
    }
    if (key === "trash") {
      setMaintenance({ operation: "trash", library: l });
      return;
    }
    void action.run(
      async () => {
        await request(`/libraries/${l.id}/status`, "PATCH", {
          expected_revision: l.revision,
          disabled: !l.disabled,
        });
        libraries.reload();
      },
      l.disabled ? "书库已启用" : "书库已停用",
    );
  }
  return (
    <section className="library-import-page">
      <h1 className="sr-only">书库</h1>
      <div className="library-import-toolbar">
        {permissions.anyEditor && (
          <Button
            type="text"
            icon={<HistoryOutlined aria-hidden="true" />}
            onClick={() => navigate("/admin/imports")}
          >
            导入历史
          </Button>
        )}
        <Space>
          <RefreshButton reload={libraries.reload} />
          <AdminOnly>
            <Button
              icon={<PlusOutlined aria-hidden="true" />}
              onClick={() => setEditing("new")}
            >
              添加书库
            </Button>
          </AdminOnly>
        </Space>
      </div>
      <ActionNotice action={action} />
      <ErrorNotice error={libraries.error} retry={libraries.reload} />
      <Card className="table-card">
        <Table<Library>
          size="small"
          rowKey="id"
          loading={libraries.loading && !libraries.data}
          dataSource={libraries.data?.items ?? []}
          pagination={false}
          scroll={{ x: 760 }}
          columns={[
            {
              title: "书库",
              dataIndex: "display_name",
              render: (name: string, l) => (
                <div className="library-identity">
                  <strong>{name}</strong>
                  <span>
                    {roots.data?.items.find((r) => r.root_key === l.root_key)
                      ?.display_name ?? "漫画目录"}
                  </span>
                </div>
              ),
            },
            {
              title: "漫画数",
              dataIndex: "edition_count",
              align: "right",
              width: 88,
              render: (n: number | undefined) =>
                n === undefined ? "—" : n.toLocaleString("zh-CN"),
            },
            {
              title: "作品数",
              dataIndex: "work_count",
              align: "right",
              width: 88,
              render: (n: number | undefined) =>
                n === undefined ? "—" : n.toLocaleString("zh-CN"),
            },
            {
              title: "启用状态",
              width: 100,
              render: (_, l) =>
                l.trashed_at ? (
                  <Tag color="warning">回收站</Tag>
                ) : l.disabled ? (
                  <Tag>已停用</Tag>
                ) : (
                  <Tag color="success">已启用</Tag>
                ),
            },
            {
              title: "源文件状态",
              width: 132,
              render: (_, l) =>
                l.error_code ? (
                  <Tooltip title={reason(l.error_code)}>
                    <Tag color="error">目录异常</Tag>
                  </Tooltip>
                ) : l.availability === "available" ? (
                  <Tag color="success">可用</Tag>
                ) : (
                  <Tag>尚未确认</Tag>
                ),
            },
            {
              title: "操作",
              width: 224,
              render: (_, l) => (
                <Space size={8}>
                  <EditOnly library={l.id}>
                    <Tooltip
                      title={
                        l.active_import
                          ? "继续查看当前导入批次"
                          : blockReason(l) ||
                            "扫描文件并联网匹配新增或变化漫画的元数据"
                      }
                      trigger={["hover", "focus"]}
                    >
                      <span>
                        <Button
                          type="primary"
                          loading={importing === l.id}
                          disabled={
                            importing !== null ||
                            (!l.active_import && !!blockReason(l))
                          }
                          onClick={() => void start(l)}
                        >
                          {l.active_import ? "继续导入" : "导入漫画"}
                        </Button>
                      </span>
                    </Tooltip>
                  </EditOnly>
                  <AdminOnly>
                    <Tooltip title="书库设置" trigger={["hover", "focus"]}>
                      <Button
                        aria-label={`设置 ${l.display_name}`}
                        disabled={!!l.trashed_at}
                        icon={<SettingOutlined aria-hidden="true" />}
                        onClick={() => setEditing(l)}
                      />
                    </Tooltip>
                    <Dropdown
                      trigger={["click"]}
                      menu={{
                        items: [
                          {
                            key: "status",
                            label: l.disabled ? "启用书库" : "停用书库",
                            disabled: !!l.trashed_at,
                          },
                          {
                            key: "trash",
                            label: "移入回收站",
                            danger: true,
                            disabled: !!l.trashed_at,
                          },
                          { key: "advanced", label: "高级维护" },
                        ],
                        onClick: ({ key }) => menuAction(key, l),
                      }}
                    >
                      <Tooltip title="更多操作" trigger={["hover", "focus"]}>
                        <Button
                          aria-label={`更多操作 ${l.display_name}`}
                          icon={<MoreOutlined aria-hidden="true" />}
                          disabled={action.busy}
                        />
                      </Tooltip>
                    </Dropdown>
                  </AdminOnly>
                </Space>
              ),
            },
          ]}
        />
      </Card>
      {editing && (
        <LibraryEditor
          initial={editing === "new" ? undefined : editing}
          roots={roots.data?.items ?? []}
          used={(libraries.data?.items ?? []).map((l) => l.root_key)}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            libraries.reload();
          }}
        />
      )}
      <Modal
        open={!!advanced}
        title={advanced ? `${advanced.display_name} · 高级维护` : "高级维护"}
        footer={null}
        onCancel={() => setAdvanced(null)}
      >
        {advanced && (
          <Space orientation="vertical" className="full-width" size={16}>
            <Button
              disabled={advanced.disabled || !!advanced.trashed_at}
              onClick={() =>
                void action.run(async () => {
                  const j = await catalog.scan(advanced);
                  setAdvanced(null);
                  navigate(taskURL(j.job_id));
                }, "")
              }
            >
              仅更新文件索引
            </Button>
            <span>离线读取文件变化，不获取元数据；周期扫描也保持离线。</span>
            <Button
              disabled={advanced.disabled || !!advanced.trashed_at}
              onClick={() => {
                setMaintenance({ operation: "reinstate", library: advanced });
                setAdvanced(null);
              }}
            >
              恢复已排除文件
            </Button>
            <Button
              onClick={() => {
                setMaintenance({ operation: "rebind", library: advanced });
                setAdvanced(null);
              }}
            >
              重新关联漫画目录
            </Button>
          </Space>
        )}
      </Modal>
      {maintenance && (
        <MaintenanceDialog
          operation={maintenance.operation}
          library={maintenance.library}
          onClose={() => setMaintenance(null)}
          onDone={(job) => {
            setMaintenance(null);
            navigate(taskURL(job));
          }}
        />
      )}
    </section>
  );
}
function LibraryEditor({
  initial,
  roots,
  used,
  onClose,
  onSaved,
}: {
  initial?: Library;
  roots: Root[];
  used: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const available = roots.filter(
    (r) => !used.includes(r.root_key) || r.root_key === initial?.root_key,
  );
  const [root, setRoot] = useState(
    initial?.root_key ?? available[0]?.root_key ?? "",
  );
  const [name, setName] = useState(initial?.display_name ?? "");
  const [encoding, setEncoding] = useState(
    initial?.legacy_zip_encoding ?? "utf8",
  );
  const [enabled, setEnabled] = useState(
    initial?.scan_interval_minutes !== null &&
      initial?.scan_interval_minutes !== undefined,
  );
  const [interval, setInterval] = useState(
    initial?.scan_interval_minutes ?? 15,
  );
  const [dirty, setDirty] = useState(false);
  const action = useAction();
  const releaseDirty = useDirty(dirty);
  const { modal } = AntApp.useApp();
  function close() {
    if (dirty)
      modal.confirm({
        title: "放弃书库设置草稿？",
        okText: "放弃",
        cancelText: "继续编辑",
        onOk: onClose,
      });
    else onClose();
  }
  async function submit() {
    await action.run(async () => {
      const input: LibraryInput = {
        root_key: root,
        display_name: name,
        legacy_zip_encoding: encoding,
        scan_interval_minutes: enabled ? interval : null,
      };
      if (initial)
        await catalog.update(initial.id, {
          ...input,
          expected_revision: initial.revision,
        });
      else await catalog.create(input);
      setDirty(false);
      releaseDirty();
      onSaved();
    }, "书库设置已保存");
  }
  return (
    <FormLock busy={action.busy}>
      <Modal
        open
        title={initial ? "编辑书库设置" : "添加书库"}
        onCancel={close}
        footer={null}
      >
        <ActionNotice action={action} />
        <Form
          layout="vertical"
          onFinish={() => void submit()}
          onChange={() => setDirty(true)}
        >
          <Form.Item label="漫画目录">
            <Select
              aria-label="漫画目录"
              disabled={!!initial}
              value={root || undefined}
              options={available.map((r) => ({
                value: r.root_key,
                label: r.display_name,
              }))}
              onChange={(v) => {
                setRoot(v);
                setDirty(true);
              }}
            />
          </Form.Item>
          <Form.Item label="书库名称" required>
            <Input
              aria-label="书库名称"
              value={name}
              maxLength={128}
              onChange={(e) => {
                setName(e.target.value);
                setDirty(true);
              }}
            />
          </Form.Item>
          <Form.Item label="归档文件名编码">
            <Select
              aria-label="归档文件名编码"
              value={encoding}
              options={[
                { value: "utf8", label: "UTF-8" },
                { value: "gb18030", label: "GB18030（旧中文）" },
                { value: "shift_jis", label: "Shift-JIS（旧日文）" },
              ]}
              onChange={(v) => {
                setEncoding(v);
                setDirty(true);
              }}
            />
          </Form.Item>
          <Form.Item label="周期扫描">
            <Space>
              <Switch
                aria-label="启用周期扫描"
                checked={enabled}
                onChange={(v) => {
                  setEnabled(v);
                  setDirty(true);
                }}
              />
              <InputNumber
                aria-label="扫描间隔分钟"
                min={15}
                max={525600}
                disabled={!enabled}
                value={interval}
                onChange={(v) => {
                  setInterval(v ?? 15);
                  setDirty(true);
                }}
              />
              <span>分钟，最短 15 分钟</span>
            </Space>
          </Form.Item>
          {!available.length && (
            <Alert
              type="warning"
              title="没有可添加的允许目录，请先调整服务配置。"
            />
          )}
          <Space>
            <Button
              type="primary"
              htmlType="submit"
              loading={action.busy}
              disabled={!root || !name.trim()}
            >
              {initial ? "保存书库设置" : "添加书库"}
            </Button>
            <Button onClick={close}>取消</Button>
          </Space>
        </Form>
      </Modal>
    </FormLock>
  );
}
