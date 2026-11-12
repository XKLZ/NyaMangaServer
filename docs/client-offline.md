# 原文件下载与离线阅读

客户端从作品查询选定具体 Edition，使用其内容版本和页序版本。原文件下载返回源 ZIP／CBZ 字节。受管离线阅读按清单保存逐页图片。两条路径均使用已登录账号的当前书库授权。设备通过 [客户端能力接口](client-foundation.md#实例书库和缓存) 读取 `downloads`、`offline_manifests`、`offline_sync` 和限制。完整字段见 [OpenAPI](../api/openapi.yaml)。

## 下载原文件并续传

所有路径相对于 `/api/v1/client`。

| 请求 | 用途 |
|---|---|
| `POST /editions/{id}/downloads` | 提交 `content_revision` 和 `idempotency_key`。202 表示正在准备，200 返回已存在记录或复用有效摘要 |
| `GET /downloads/{id}` | 获取 `preparing`、`ready`、`failed` 或 `abandoned`。就绪时有 `length` 与 `sha256`，失败提供稳定原因 |
| `HEAD /downloads/{id}/content` | 验证当前身份与访问权，读取长度、强 ETag 和附件文件名 |
| `GET /downloads/{id}/content` | 流式获取完整原文件，或使用单个字节范围 |
| `DELETE /downloads/{id}` | 放弃本人记录，取消进行中的准备 |

匿名准备请求正文。

```json
{"content_revision": 7, "idempotency_key": "fixture-download-001"}
```

示例续传请求，摘要使用匿名占位值。

```http
GET /api/v1/client/downloads/example-download/content
Authorization: Bearer <token>
Range: bytes=8388608-16777215
If-Range: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
```

接收 206 后，先核对 `Content-Range`、ETag 和分片长度，再写入对应偏移。收到完整 200 时从头保存该响应。不可满足的范围返回 416。只支持单段，建议 8 MiB，每段最多 16 MiB。有效起点的超长或多段请求返回 400 `invalid_download_range`。下载最终完成要求本地长度和整文件 SHA-256 都匹配，部分文件继续作为临时文件保存。

- 新准备首次顺序读取整个源归档以计算 SHA-256。准备全局单并发，最多 100 条待处理。中断的计算从头重新读取，同账号、世代、版本和源身份仍有效时复用已完成摘要。启动或浏览时没有全库摘要扫描。
- 每会话最多 1 路传输，全局 2 路。429 `download_session_limit` 或 503 `download_busy` 带 `Retry-After`。完整 GET 保持现有 HTTP 期限，大文件适合分段。传输使用小缓冲，每约 1 MiB 及末尾重新核对身份和源，不占图片解码位。
- 持有记录 ID 的请求仍需通过当前认证与授权。旧内容版本或世代返回 409，未就绪返回 409 `download_not_ready`。已开始传输若发现撤权或源变化会中止，客户端按不完整处理。相同文件身份、大小、修改时间的原地篡改可能绕过属性检查，最终以整文件摘要核对。下载不计阅读。

## 保存受管离线内容

1. 向 `POST /editions/{id}/offline-manifests` 发送 `{"content_revision":7,"parsing_revision":3}`。服务返回账号、实例、缓存世代、库、Edition、Work、页数、目录版本、图片表示版本、结构摘要和可独立更新的资料摘要。
2. 使用 `GET /offline-manifests/{id}` 读身份和状态，使用 `GET /offline-manifests/{id}/pages?page=1&page_size=24` 分页。列表从 1 开始，`page_index` 从 0 开始。按响应的 `image_url` 原样取页，发送 `X-Nya-Image-Purpose: prefetch`，离线取页并发为 1。该 URL 绑定清单 ID、内容和页序，服务校验当前表示版本。清单建立只读取安全目录，页图强 ETag 在实际取页时提供。
3. 本地按实例、世代、账号、Edition 和版本分区。每页先写临时文件，校验响应长度与 ETag 对应的 SHA-256，原子改名后登记该索引。失败页重取整页，重复索引只计一次。全部页核验通过才标记完整，部分保存显示通过页数。服务端响应成功只证明传输结果。
4. 普通缓存可按 LRU 淘汰。用户明确保存的下载不参与自动驱逐。更新先在独立版本目录准备并核验，成功后切换引用，空间不足保留旧的完整副本。删除下载目录时保留独立的待同步队列。

重连向 `POST /offline/check` 发送最多 100 个已选清单。匿名示例。

```json
{
  "instance_id": "example-instance",
  "cache_epoch": "example-epoch",
  "objects": [{"id": "example-manifest", "metadata_digest": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]
}
```

`unchanged` 可继续使用。 `metadata_changed` 更新标题、封面身份或系列资料，个人状态变化也无需重新取正文。 `content_changed` 或 `parsing_or_representation_changed` 使用独立新版本，旧页码需确认。 `source_unavailable` 表示源暂不可读。 `inaccessible` 对无权和不可见删除返回相同结果，无新标题和书库信息。 `cache_epoch_changed` 隔离旧版本与队列。搜索快照用于搜索分页，离线检查只覆盖请求给定的清单集合。

## 重连重放个人操作

`POST /offline/operations` 每批最多 100 项，`local_sequence` 按批内严格递增。每项独立提交，响应顺序与请求一致。写入范围为阅读开始、重新开始、进度、完成、取消完成、重置，Edition 收藏、隐藏，以及标签关注或取消关注。关注、收藏和隐藏提交明确的 `value`。阅读项提交内容和页序版本。开始与重新开始需要稳定 `offline_session_id`，后续阅读项可继续引用该会话。无心跳重放。

```json
{
  "instance_id": "example-instance",
  "cache_epoch": "example-epoch",
  "account_id": "example-account",
  "client_instance_id": "anonymous-install",
  "operations": [
    {
      "operation_id": "example-start",
      "local_sequence": 1,
      "object_id": "example-edition",
      "type": "start",
      "expected_revision": 0,
      "offline_session_id": "anonymous-session",
      "occurred_at": 1794500000,
      "body": {"content_revision": 7, "parsing_revision": 3}
    },
    {
      "operation_id": "example-progress",
      "local_sequence": 2,
      "object_id": "example-edition",
      "type": "progress",
      "previous_operation_id": "example-start",
      "offline_session_id": "anonymous-session",
      "body": {"content_revision": 7, "parsing_revision": 3, "page_index": 2}
    }
  ]
}
```

- `account_id` 绑定队列所属账号，服务器使用认证账号校验它。换账号返回 403。成功回执和业务修改同一事务提交，同 ID 同正文返回原结果。不同正文的项为 409 `client_idempotency_conflict`。已发且结果不明的项冻结正文和 ID，使用 `POST /offline/operation-status`，携带原队列身份和 `operation_ids`，查出回执后再处理依赖。
- 前置项必须属于同一队列、对象和 revision 域。只使用其成功回执的 revision，前置项之后存在其他编辑时仍冲突。 `blocked` 可在前置成功后用原正文重试，其他对象可继续提交。 `conflict` 可附当前状态，用户解决后用新 ID 和当前 revision 提交。未发送连续进度可合并，开始、完成、重置及重新开始保序。维护拒绝后保留队列。
- 可选 `occurred_at` 为 UTC Unix 秒，回执分别返回 `reported_at`、`event_at` 和 `received_at`。类型非法、溢出或超过接收时间 5 分钟的报告保留原值，使用接收时间并设置 `adjusted`。较早事件不倒退最近阅读时间，有效完成事件保存其发生时间。时间不参与冲突决策。普通开始保留完成，明确重新开始清除完成并回第 0 页。

断网时已完整保存的受管内容可本地阅读，暂停同步。凭证失效后暂停在线和重放，允许同账号重新登录并检查实例、世代、权限和版本。明确无权时停止受管展示、下载和重放并清理相应内容，只留最小待处置记录。换账号隔离。恢复或回退后隔离旧世代资源和队列。导出的原文件无法由服务端回收，服务也无法在设备断网期间即时通知撤权。

本协议验证范围为服务端及匿名模拟。真实客户端后台下载、本地持久化、断网界面和 NAS 性能需独立验收。
