> 本页保留初始管理台增量契约。schema7已用Work正式接口取代旧组织接口；旧版本组/context/preview返回410，Series成员使用work_ids、转移路径现在是源Series并传target_series_id。现行请求响应以[Work契约](work-edition-model.md)和[OpenAPI](../api/openapi.yaml)为准。books元数据/图片/维护兼容入口继续保留。

# 管理台增量契约

实施基线：`1c61abc`，foundation-v1/schema 3。本文是本阶段前后端的共同实施契约；验收状态另行记录。旧 API 保持兼容，所有路径均以 `/api/v1` 为前缀，认证、Origin 和 CSRF 沿用现有规则。

## 通用形状

- 分页：`page`（默认1）、`page_size`（默认50，最大100）；结果 `{items,total,page,page_size}`。
- 修改：`expected_revision` 或组织既有 `expected_revisions` 必填，缺失返回400，过期返回409 `revision_conflict`。
- 任务：长操作返回202 `{job_id}`，沿用 `/jobs/{id}` 及成员查询、控制；新任务类型 `maintenance`、`cover_rebuild`、`metadata_preview`。
- 错误沿用 `{error:{code,message,request_id,details?}}`；容量 `organization_capacity`、成员归属 `membership_conflict`、停用 `library_disabled`、回收站 `resource_trashed`、资源忙 `resource_busy`、预览过期 `preview_expired`、权限 `permission_denied`、磁盘满 `storage_full`。
- 所有新列表中的书籍沿用 book 响应，增加 `trashed_at`、`library_disabled`、`cover_revision` 等管理状态；书库增加 `disabled`、`trashed_at`。版本从响应读取，不由网页推测。

## 管理与维护

| 方法／路径 | 输入与输出 |
|---|---|
| POST `/admin/books/search` | 既有 SearchInput，默认 `collapse:none`，加 `visibility:active/trash/all`、`include_disabled:boolean`、`metadata_state:none/matched/needs_review`；返回分页物理书籍 `items`（附user_state及metadata状态/原文标题摘要）和 `snapshot_revision` |
| GET `/jobs` | 分页及 `type/state/library_id` 筛选；保留 items，增加total/page/page_size |
| PATCH `/libraries/{id}/status` | `{expected_revision,disabled}` → 更新后的书库 |
| GET `/maintenance/roots/{key}/entries` | `relative_path/page/page_size`，返回受限目录项 `{name,relative_path,kind}`，kind为directory/archive |
| POST `/maintenance/plans` | `{operation,book_ids?,library_id?,expected_revisions,options?}` → `{id,operation,expires_at,summary:object,affected:string[],protected:string[]}`；operation为trash/restore/purge/relink/rebind/cleanup |
| GET `/maintenance/plans/{id}` | 管理员只读既有完整公开计划；执行后含`job_id`和持久`summary.cleanup_result`，任务状态另读`GET /jobs/{id}`。过期／终态历史保留期间可读；不存在404。GET及POST均不返回内部Input、冻结文件清单或外部档案路径 |
| POST `/maintenance/plans/{id}/execute` | `{}` → 202 `{job_id}`；重复请求返回同一任务。执行时再次验证版本与资源边界 |
| GET `/maintenance/trash` | 分页及kind=book/library，返回回收站对象、当前版本与归属摘要 |
| GET `/maintenance/storage` | 分类别 `{categories:[{kind,bytes,files}],protected}`，不暴露宿主绝对路径 |

维护 options：relink `{root_key,relative_path,confirm_replacement}`；rebind `{root_key}`；cleanup `{categories:[covers,orphan_assets,tmp,terminal_jobs,snapshots]}`。expected_revisions以book:<id>和library:<id>为键。永久清除不删除源文件；扫描排除记录只经明确 `operation:reinstate` 删除。回收站默认保留业务关系。

## 封面与来源

| 方法／路径 | 输入与输出 |
|---|---|
| GET `/books/{id}/cover-selection` | `{mode:auto/page/asset,page_index?,asset_id?,revision,content_revision,state}` |
| PUT `/books/{id}/cover-selection` | `{expected_revision,mode,page_index?,content_revision?}` → 当前选择 |
| POST `/books/{id}/cover-assets` | 原始图片请求体，`Content-Type`和`X-Expected-Revision`，最多10MiB → 当前选择；JPEG/PNG/WebP |
| POST `/books/{id}/cover-rebuilds` | `{expected_revision}` →202 `{job_id}` |
| POST `/books/{id}/source-previews` | `{gallery_url,force_refresh?,expected_revision,selection_revision,content_revision}` →202 `{job_id}` |
| GET `/books/{id}/source-previews/{preview}` | `{id,state,expires_at,fields,source,current,shared_book_count,...}`，不返回远端原文 |
| POST `/books/{id}/source-previews/{preview}/confirm` | `{expected_revision,selection_revision,content_revision}` → 更新的元数据 |
| DELETE `/books/{id}/source` | `{expected_revision,selection_revision}` → 元数据，保留人工覆盖 |
| DELETE `/books/{id}/manual-source` | `{selection_revision}` → 元数据，仅清除人工URL |

metadata_preview任务成员成功结果包含 `preview_id`，预览保留30分钟；手动URL不能在预览采纳前写入已采纳来源。封面图片仍使用原认证接口，ETag来自实际图片内容。

## 组织

沿用 OrgCommand、OrgResult、`/organization/preview`。新增动作 `series_add`、`series_remove`、`series_move`，接受book_ids由服务端解析逻辑条目；移动使用 `item_id`、`position`（从0计）。相关聚合expected_revisions必须完整。

- GET `/series/{id}/members`、`/version-groups/{id}/members`：分页和query；返回轻量条目／成员，版本见聚合响应。新分页position从0计；VersionMember增加title；轻量聚合增加member_count。
- POST `/series/{id}/members`：series_add，继续拒绝隐式跨系列搬迁。
- POST `/series/{id}/transfers`：series_transfer；路径为目标系列，book_id解析当前逻辑条目（版本组整体），expected_revisions包含当前条目、版本组及两边加载的聚合。与 `/organization/preview` 共用变换，单事务移除旧位置、追加到目标末尾，保持连续顺序；拒绝同目标、未归属、跨库、超容量及版本冲突，空源系列保留。复用schema5组织表，无迁移。
- 系列成员分页补充代表书籍 `representative_book_id` 和 `representative_content_revision`，版本组采用主版本；OrgBook补充content_revision以请求本地封面。均为读模型字段，不持久化额外数据。
- DELETE `/series/{id}/members/{item}`：series_remove。
- POST `/series/{id}/members/{item}/move`：series_move。
- 原PUT完整有序成员更新、版本组POST/DELETE成员与主版本／标签接口保留。
- 系列与版本组列表支持query名称筛选；详情新增 `summary=true` 获取轻量摘要，不默认展开所有物理成员。

## 并行所有权与验收

后端Agent修改Go／迁移／Go测试；前端Agent修改web。OpenAPI、本文、公共文档、Makefile、Git和集成验证由主Agent负责。契约修订先通知双方，不独立猜测。构建产物和进程端口不能并行写入／占用。当前开发实例在匿名综合验收后才升级，不触发任何私人漫画操作。

保存搜索格式版本仍为1，新增可选管理条件include_disabled/metadata_state；打开时恢复这些已应用条件并从第一页查询。普通书籍搜索不扩大回收站范围。
