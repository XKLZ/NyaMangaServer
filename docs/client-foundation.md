# 客户端协议 1

当前源码提供设备认证、实例能力、作品浏览、版本化阅读入口和个人阅读状态。协议字段见 [OpenAPI](../api/openapi.yaml)，设备可用的方法见[授权矩阵](authorization-route-matrix.md)。本阶段支持在线逐页读取。下载、离线同步、图片多规格和设备阅读偏好同步暂未实现。

## 登录与设备生命周期

`GET /api/v1/client/server-info` 可匿名读取服务名、版本、协议版本、逻辑实例 ID 和是否初始化。使用 HTTPS 连接已配置的服务地址。初始化仍由管理网页完成。

发送 `POST /api/v1/auth/device-sessions`，以 JSON 提交账号密码和本地生成的客户端实例标识。客户端实例标识属于应用安装实例，可使用随机 UUID。设备名称仅用于显示。重复使用同一标识登录会生成独立设备会话。

```json
{
  "username": "sample-reader",
  "password": "example password",
  "device_name": "匿名设备",
  "client_instance_id": "25f98fd1-09ed-4cc3-a256-a87c4085768b"
}
```

201 示例，时间为 UTC Unix 秒，以下凭证仅为格式占位。

```json
{
  "device": {
    "id": "device-example",
    "client_instance_id": "25f98fd1-09ed-4cc3-a256-a87c4085768b",
    "device_name": "匿名设备",
    "created_at": 1800000000,
    "expires_at": 1802592000,
    "last_active_at": 1800000000,
    "revoked_at": null
  },
  "token": "nya_device_v1_<opaque>",
  "actor": {
    "id": "account-example",
    "username": "sample-reader",
    "role": "user",
    "enabled": true,
    "must_change_password": false,
    "acl_revision": 2,
    "library_grants": {"library-example": "viewer"},
    "capabilities": {
      "manage_accounts": false,
      "global_maintenance": false,
      "manage_global_tags": false,
      "personal_state": true
    }
  }
}
```

- 凭证只在成功登录时返回一次。使用平台安全存储。每次 API 和图片请求发 `Authorization: Bearer <token>`。凭证不进入 URL、日志和普通应用偏好。原生请求可省略 Origin，若发送则必须匹配服务配置。服务不提供新的 CORS 放行。
- 每账号最多同时保有 20 个有效设备。响应丢失后由用户决定是否重新登录。新登录可能新增一条记录，可通过设备列表找到并撤销。
- 使用服务端时钟计算有效期。剩余时间严格小于 7 天时，客户端可主动发送 `POST /auth/device-sessions/current/renew`（空正文），到期更新为当时起 30 天。提前续期返回现有到期时间。续期不轮换凭证，普通读取不延长期限。到期后重新登录。
- `GET /auth/session` 使用设备凭证时返回设备和账号，不返回 CSRF 或凭证。`GET /auth/device-sessions` 查看本账号设备，包括历史和撤销时间。`DELETE /auth/device-sessions/{id}` 返回 204，重复删除、未知 ID 和其他账号设备 ID 的响应一致。用另一个仍有效的本人会话重复撤销。`POST /auth/logout` 撤销当前设备。
- 本人改密、管理员重置、账号停用会撤销设备，恢复授权无法复活旧凭证。若 `must_change_password=true`，仅可读身份、改密或退出。改密使用既有 `PUT /auth/password`，成功后重新登录。
- 设备列表和撤销也接受管理网页的 Cookie，网页写入继续校验 Origin 与 CSRF。混用 Cookie 和 Bearer 返回 400。设备凭证和画廊高亮凭证各自使用独立用途的摘要。
- 最近活动最多每设备五分钟持久更新一次，维护期间跳过隐式活动写入。登录、续期、撤销和个人状态写入受维护屏障保护。设备写入计入恢复后的写入世代。

常见错误如下。

| HTTP | 错误码 | 处理 |
|---|---|---|
| 400 | `invalid_request`、`mixed_credentials`、`unexpected_credentials` | 修正字段或认证方式 |
| 401 | `invalid_credentials`、`unauthenticated` | 检查密码，或清除失效凭证并重新登录 |
| 403 | `password_change_required`、`forbidden`、`origin_rejected`、`csrf_rejected` | 按返回原因改密或检查账号、来源与网页会话 |
| 409 | `device_limit_reached` | 通过有效会话撤销本人旧设备 |
| 429 | `rate_limited` | 遵守 `Retry-After`，与网页入口共用来源限流 |
| 503 | `maintenance_active` | 等待维护完成后再提交写入 |

## 实例、书库和缓存

登录后读取 `GET /client/capabilities` 和 `GET /libraries`。前者提供账号、ACL revision、书库范围、全局维护状态、后台 hold、协议限制及各功能的 `supported`、`enabled`、`allowed`、`available` 和 `reason`。功能状态是当前账号和配置的概览，实际请求继续逐资源鉴权。书库接口提供设备用的 ID、名称、停用状态及根可用性。能力读取只检查配置和持久状态，不检查单个源文件或启动推荐运行时。

缓存按 `instance_id`、`cache_epoch` 和账号分区。图片再按 Edition ID、内容版本、页序版本和页索引分区，封面另使用 `cover_revision`。能力响应返回缓存世代。普通重启或地址变更保留实例 ID 和缓存世代。整实例激活继承来源逻辑实例 ID，回退继承保全的目标逻辑实例 ID，两条路径都刷新缓存世代并使旧设备凭证失效。客户端在重新登录后比较身份和世代，丢弃旧世代的资源缓存与分页快照。

## 查询作品

`POST /api/v1/client/works/search` 每次指定一个 `library_id`。沿用[搜索语法](search.md)，先匹配 Edition，再按 Work 折叠。列表页从 1 开始，默认每页 24，最大 100。默认按匹配版本的 `indexed_at desc` 排序，也可按题名或当前账号可见版本的 `last_read_at` 排序。无阅读时间的 Work 排在有时间的 Work 后。指定停用库可查询其资料和状态，源页面仍按停用处理。

```json
{
  "library_id": "library-example",
  "query": "title:sample",
  "scope": "all",
  "page": 1,
  "page_size": 24
}
```

响应以 `items`、`total`、`page`、`page_size`、`snapshot_revision` 分页。后续页保留同一条件和页大小，带回快照。条件、账号 ACL、目录或该库个人状态变化后，服务可返回 409 `search_snapshot_expired`，此时从第一页重新查。

- `all` 默认排除隐藏和回收。收藏范围 `favorites` 只匹配已收藏的可见版本。显式 `hidden` 查询本人隐藏的版本。
- `entry_edition_id` 是本次命中的版本。首选也命中时优先，其他命中按稳定 ID 选择。题名原文优先。版本摘要、封面和 `reading.edition_id` 都绑定入口版本。使用 `edition.user_state.revision` 调用 `PATCH /editions/{id}/user-state` 更新该版本的收藏或隐藏。
- `preferred_edition_id` 在首选超出当前可见范围时为 `null`。响应不返回该版本的题名或封面。显式隐藏查询只在隐藏集合内计算首选可见性。
- `resume_edition_id` 是同 Work 最近读过的未完成可见版本，按最近阅读时间降序、稳定 ID 升序选择。它可以与入口版本不同。 `scope: "continue"` 只保留存在该版本的 Work。读取完成属于 Edition，响应不推断 Work 的整体完成状态。

## 从作品打开到更新进度

1. 点击查询结果时带上命中的版本，调用 `GET /client/works/{id}/reading-context?edition_id=<entry_edition_id>`。显式版本必须属于作品且可访问。默认 `intent=open` 选择首选或稳定可见版本。继续阅读用 `intent=continue`，最近未完成版本即使暂不可读或进度过期也保持选中。
2. 检查返回的 `edition.readability`、`reading` 和两个确认标志。已知不可读时，可用 `readable_alternative_id` 提示用户主动选择。读取能力依据索引和根映射估计，实际图片请求仍校验源文件。上下文还返回版本列表、系列位置和允许浏览的前后 Work。
3. 跟随 `pages_url` 获取目录，逐页请求 `GET /client/editions/{id}/pages/{page_index}/image`。两个接口必带 `content_revision` 和 `parsing_revision`。客户端页索引从 0 开始，与 `reading.page_index` 一致。列表分页从 1 开始。旧管理网页目录及图片入口仍使用从 1 开始的页索引。
4. 打开目录和图片不计次。开始、进度、完成和重置复用 `POST /editions/{id}/reading-sessions` 与 `PUT /editions/{id}/reading-state`，带上当前版本、`expected_revision` 和幂等键。同一写入重试使用原键及原正文。之后 `GET /editions/{id}/reading-state` 可确认持久状态。

匿名目录示例。

```http
GET /api/v1/client/editions/edition-example/pages?content_revision=7&parsing_revision=3&page=1
Authorization: Bearer <token>
```

```json
{
  "items": [
    {"page_index": 0, "display_name": "1.png", "media_type": "image/png"},
    {"page_index": 1, "display_name": "2.png", "media_type": "image/png"}
  ],
  "total": 2,
  "content_revision": 7,
  "parsing_revision": 3,
  "directory_version": "natural-bmp-macos-v3",
  "page": 1,
  "page_size": 24
}
```

缺失或无效版本参数返回 400。授权外资源与合法范围外的页索引返回 404。内容或页序变化返回 409 `content_changed` 或 `parsing_changed`。重新取上下文并提示用户确认，已有进度由阅读状态 API 保留。书库停用、源文件或根不可用时，以返回的原因码处理。图片使用私有缓存及 ETag，条件请求仍先校验认证和版本。
