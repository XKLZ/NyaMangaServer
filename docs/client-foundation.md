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

## 在线图片和预加载

当前页默认使用 `X-Nya-Image-Purpose: display`。预加载请求使用 `prefetch`，其他值或重复的用途头返回 400 `invalid_image_purpose`。两种用途的图片字节和 ETag 一致。客户端先请求当前页，最多预取前 1 页和后 2 页，预取并发为 1。跳页、换版、离开和进入后台时取消无用预取，预取窗口留在当前 Edition 内。能力响应中的 `image_prefetch` 和 `limits` 提供这些限制。

| 边界 | 默认 |
|---|---|
| 在线图片请求 | 同一设备会话或网页会话 4 个，全局 16 个，含等待和发送 |
| 共享图片处理 | 最多 2 路，预取最多占 1 路。空闲处理位优先交给排队的显示请求 |
| 资源等待 | 最多 5 秒。会话满返回 429 `image_session_limit`，全局或处理资源不足返回 503 `image_busy`，均附 `Retry-After` |
| 图片内存 | 默认共享预算 512 MiB。热点编码字节最多 64 MiB 且不超过配置预算的 1/8 |
| 图片验证记录 | 最多 1024 条，闲置 5 分钟淘汰 |

首次取图校验安全路径、归档和条目、长度、CRC、格式、像素及解码预算。强 ETag 是最终响应字节的 SHA-256。用 `If-None-Match` 回送验证值，服务按 HTTP 弱比较语义处理标签列表。命中有效验证记录时，服务重检凭证、书库授权、内容版本、页序版本、缓存世代和源文件身份，再返回 304，无须重复解压或完整解码。验证记录和热点字节分别淘汰。旧验证值未命中时返回实际表示，若重新验证得到相同字节仍可按 HTTP 规则返回 304。文件属性检查无法发现所有保留相同文件身份、大小与修改时间的外部原地修改。

相同页面可合并处理。每个请求独立核验权限和取消，一个等待者离开时其他有效请求可以继续。全部取消后，可中断的读取和等待立即停止，正在运行的不可中断解码结束后丢弃结果。解码临时空间和响应字节分别记账。响应发送结束、失败或取消后释放引用。已开始的预取可运行至结束，显示请求优先于仍在排队的预取。保留原 HTTP 期限和 `Cache-Control: private, no-cache`，页面不写磁盘缓存。旧网页页图继续从 1 开始，共用图片验证、处理和在线限额。

## 阅读写入、继续与重新开始

1. 获取上下文、目录、封面、预取或导航到下一 Work 均不计次。客户端确认首张图片实际展示后，显式提交 `POST /editions/{id}/reading-sessions`。服务端以提交的阅读会话计次。
2. 同一设备对同一 Edition 串行写入。建议翻页合并 2 秒，持续翻页最长每 5 秒提交最新位置。停留时心跳至多每 60 秒一次，进入后台后停止持续心跳。离开、后台或换版前尝试提交当前待写位置，平时继续保存。
3. 提交中的正文和幂等键保持冻结。新页码只更新下一待提交值。结果不明时用原正文和原键重试，确认前暂停依赖新 revision 的写入。认证、授权或维护错误时停止自动写入并保留待提交位置。429 或 503 附带 `Retry-After` 时按该值等待。
4. 409 时保留本地位置和返回的 `current_state`，由用户明确选择。无需比较页码大小或设备时钟，也不自动更新 revision 后重试。设备在进入、回到前台、刷新和冲突时读取最新状态。

开始请求的 `intent` 默认 `start`。当前内容和页序下，普通开始、翻页和心跳保留已完成状态。用户明确选择「重新开始阅读」时使用 `intent: "restart"`，服务在同一事务创建新会话、将位置设为 0、清除完成及完成时间，并在原阅读次数上加 1。同键重试不会重复计次，同键更换意图返回 409。未知本地页数时返回 422 `reading_page_count_unknown`。内容或页序变化仍需使用现有确认字段，显式确认变化会清除过期位置和完成状态。

重新开始后，该 Edition 按最近未完成记录进入 `scope=continue`。手动取消完成保留位置，重置清除位置和完成，两者都保留阅读历史。首选版本变动时，继续阅读仍绑定最近未完成的允许版本。该版本离线或过期时显示原因和原进度。设备不接收 SSE。
