# 服务端路由授权清单

本清单来自当前受保护路由策略，记录源码覆盖，不表示每行均单独执行过HTTP测试。未列入策略的新受保护路由默认仅管理员；数据库读范围在COUNT／分页前统一生效，写范围在同一事务内由TEMP触发器再次核验当前账号与授权。

管理网页的管理员 Cookie 可使用管理员接口。设备凭证在所有角色下受本文末尾的独立白名单限制。read为当前授权库内浏览，personal仅本人个人状态，editor为授权库编辑，admin为全局管理员，self为本人认证。POST只读搜索按read处理。全局标签图片／译名／人工关系写始终admin。

| 路径 | 方法 | 策略 |
|---|---|---|
| `/api/v1/accounts` | GET | admin |
| `/api/v1/accounts` | POST | admin |
| `/api/v1/accounts/{id}` | PUT | admin |
| `/api/v1/admin/books/search` | POST | read |
| `/api/v1/admin/editions/search` | POST | read |
| `/api/v1/auth/device-sessions` | GET | self |
| `/api/v1/auth/device-sessions` | POST | public login |
| `/api/v1/auth/device-sessions/current/renew` | POST | self，设备凭证 |
| `/api/v1/auth/device-sessions/{id}` | DELETE | self |
| `/api/v1/auth/login` | POST | self |
| `/api/v1/auth/logout` | POST | self |
| `/api/v1/auth/password` | PUT | self |
| `/api/v1/auth/session` | GET | self |
| `/api/v1/auth/setup` | POST | self |
| `/api/v1/auth/status` | GET | self |
| `/api/v1/books` | GET | read |
| `/api/v1/books/search` | POST | read |
| `/api/v1/books/{id}` | GET | read |
| `/api/v1/books/{id}/cover` | GET | read |
| `/api/v1/books/{id}/cover-assets` | POST | editor |
| `/api/v1/books/{id}/cover-rebuilds` | POST | editor |
| `/api/v1/books/{id}/cover-selection` | GET | read |
| `/api/v1/books/{id}/cover-selection` | PUT | editor |
| `/api/v1/books/{id}/manual-source` | DELETE | editor |
| `/api/v1/books/{id}/overrides` | PATCH | editor |
| `/api/v1/books/{id}/pages` | GET | read |
| `/api/v1/books/{id}/pages/{index}/image` | GET | read |
| `/api/v1/books/{id}/reading-sessions` | POST | personal |
| `/api/v1/books/{id}/reading-state` | GET | personal |
| `/api/v1/books/{id}/reading-state` | PUT | personal |
| `/api/v1/books/{id}/source` | DELETE | editor |
| `/api/v1/books/{id}/source-previews` | POST | editor |
| `/api/v1/books/{id}/source-previews/{preview}` | GET | read |
| `/api/v1/books/{id}/source-previews/{preview}/confirm` | POST | editor |
| `/api/v1/books/{id}/user-state` | PATCH | personal |
| `/api/v1/client/server-info` | GET | public |
| `/api/v1/client/capabilities` | GET | self |
| `/api/v1/client/works/search` | POST | read |
| `/api/v1/client/works/{id}/reading-context` | GET | read |
| `/api/v1/client/editions/{id}/pages` | GET | read |
| `/api/v1/client/editions/{id}/pages/{page_index}/image` | GET | read |
| `/api/v1/client/editions/{id}/downloads` | POST | personal |
| `/api/v1/client/downloads/{id}` | GET、DELETE | 本人记录与当前库授权 |
| `/api/v1/client/downloads/{id}/content` | HEAD、GET | 本人就绪记录与当前库授权 |
| `/api/v1/client/editions/{id}/offline-manifests` | POST | personal |
| `/api/v1/client/offline-manifests/{id}`、`/api/v1/client/offline-manifests/{id}/pages` | GET | 本人清单与当前库授权 |
| `/api/v1/client/offline/check` | POST | 当前账号所选清单，只读 |
| `/api/v1/client/offline/operations`、`/api/v1/client/offline/operation-status` | POST | 个人操作与本人回执 |
| `/api/v1/diagnostics` | GET | admin |
| `/api/v1/editions` | GET | read |
| `/api/v1/editions/search` | POST | read |
| `/api/v1/editions/{id}` | GET | read |
| `/api/v1/editions/{id}/character-suppressions/{parody}/{character}` | DELETE | admin |
| `/api/v1/editions/{id}/character-suppressions/{parody}/{character}` | PUT | admin |
| `/api/v1/editions/{id}/cover` | GET | read |
| `/api/v1/editions/{id}/cover-assets` | POST | editor |
| `/api/v1/editions/{id}/cover-rebuilds` | POST | editor |
| `/api/v1/editions/{id}/cover-selection` | GET | read |
| `/api/v1/editions/{id}/cover-selection` | PUT | editor |
| `/api/v1/editions/{id}/manual-source` | DELETE | editor |
| `/api/v1/editions/{id}/move` | POST | editor |
| `/api/v1/editions/{id}/overrides` | PATCH | editor |
| `/api/v1/editions/{id}/pages` | GET | read |
| `/api/v1/editions/{id}/pages/{index}/image` | GET | read |
| `/api/v1/editions/{id}/reading-sessions` | POST | personal |
| `/api/v1/editions/{id}/reading-state` | GET | personal |
| `/api/v1/editions/{id}/reading-state` | PUT | personal |
| `/api/v1/editions/{id}/source` | DELETE | editor |
| `/api/v1/editions/{id}/source-previews` | POST | editor |
| `/api/v1/editions/{id}/source-previews/{preview}` | GET | read |
| `/api/v1/editions/{id}/source-previews/{preview}/confirm` | POST | editor |
| `/api/v1/editions/{id}/user-state` | PATCH | personal |
| `/api/v1/editions/{id}/version-labels` | PUT | editor |
| `/api/v1/events` | GET | read |
| `/api/v1/gallery-highlight/lookup` | POST | admin |
| `/api/v1/gallery-highlight/status` | GET | admin |
| `/api/v1/health/live` | GET | admin |
| `/api/v1/health/ready` | GET | admin |
| `/api/v1/import-batches` | GET | read |
| `/api/v1/import-batches` | POST | editor |
| `/api/v1/import-batches/{batch}/series-proposals/{id}` | GET | read |
| `/api/v1/import-batches/{batch}/series-proposals/{id}/accept` | POST | editor |
| `/api/v1/import-batches/{batch}/series-proposals/{id}/dismiss` | POST | editor |
| `/api/v1/import-batches/{batch}/series-proposals/{id}/members` | GET | read |
| `/api/v1/import-batches/{batch}/series-proposals/{id}/resolve` | POST | editor |
| `/api/v1/import-batches/{batch}/series-proposals/{id}/restore` | POST | editor |
| `/api/v1/import-batches/{batch}/series-proposals/{id}/undo` | POST | editor |
| `/api/v1/import-batches/{batch}/work-proposals/{id}` | GET | read |
| `/api/v1/import-batches/{batch}/work-proposals/{id}/accept` | POST | editor |
| `/api/v1/import-batches/{batch}/work-proposals/{id}/dismiss` | POST | editor |
| `/api/v1/import-batches/{batch}/work-proposals/{id}/resolve` | POST | editor |
| `/api/v1/import-batches/{batch}/work-proposals/{id}/restore` | POST | editor |
| `/api/v1/import-batches/{batch}/work-proposals/{id}/undo` | POST | editor |
| `/api/v1/import-batches/{id}` | GET | read |
| `/api/v1/import-batches/{id}/control/{action}` | POST | editor |
| `/api/v1/import-batches/{id}/members` | GET | read |
| `/api/v1/import-batches/{id}/members/{edition}/candidate` | GET | read |
| `/api/v1/import-batches/{id}/members/{edition}/candidate-cover` | GET | read |
| `/api/v1/import-batches/{id}/members/{edition}/editor` | GET | read |
| `/api/v1/import-batches/{id}/members/{edition}/manual-review` | POST | editor |
| `/api/v1/import-batches/{id}/members/{edition}/refetch` | POST | editor |
| `/api/v1/import-batches/{id}/members/{edition}/source-searches` | POST | editor |
| `/api/v1/import-batches/{id}/members/{edition}/source-searches/{search}` | GET | read |
| `/api/v1/import-batches/{id}/members/{edition}/source-searches/{search}/control/{action}` | POST | editor |
| `/api/v1/import-batches/{id}/members/{edition}/source-searches/{search}/load-page` | POST | editor |
| `/api/v1/import-batches/{id}/members/{edition}/source-searches/{search}/results` | GET | read |
| `/api/v1/import-batches/{id}/members/{edition}/source-searches/{search}/results/{result}` | GET | read |
| `/api/v1/import-batches/{id}/members/{edition}/source-searches/{search}/results/{result}/cover` | GET | read |
| `/api/v1/import-batches/{id}/members/{edition}/source-searches/{search}/select` | POST | editor |
| `/api/v1/import-batches/{id}/organization-review` | GET | read |
| `/api/v1/import-batches/{id}/organization-review/complete` | POST | editor |
| `/api/v1/import-batches/{id}/retry-files` | POST | editor |
| `/api/v1/import-batches/{id}/review-selection` | GET | read |
| `/api/v1/import-batches/{id}/reviews` | POST | editor |
| `/api/v1/import-batches/{id}/series-proposals` | GET | read |
| `/api/v1/import-batches/{id}/work-proposals` | GET | read |
| `/api/v1/jobs` | GET | read |
| `/api/v1/jobs/{id}` | GET | read |
| `/api/v1/jobs/{id}/cancel` | POST | editor |
| `/api/v1/jobs/{id}/import-batch` | GET | read |
| `/api/v1/jobs/{id}/items` | GET | read |
| `/api/v1/jobs/{id}/pause` | POST | editor |
| `/api/v1/jobs/{id}/resume` | POST | editor |
| `/api/v1/jobs/{id}/retry` | POST | editor |
| `/api/v1/libraries` | GET | read |
| `/api/v1/libraries` | POST | admin |
| `/api/v1/libraries/{id}` | PATCH | admin |
| `/api/v1/libraries/{id}/scans` | POST | editor |
| `/api/v1/libraries/{id}/status` | PATCH | admin |
| `/api/v1/library-roots` | GET | admin |
| `/api/v1/maintenance/backups` | GET | admin |
| `/api/v1/maintenance/backups` | POST | admin |
| `/api/v1/maintenance/backups/{operation}` | GET | admin |
| `/api/v1/maintenance/backups/{operation}/cancel` | POST | admin |
| `/api/v1/maintenance/backups/{operation}/download` | GET | admin |
| `/api/v1/maintenance/health-checks` | GET | admin |
| `/api/v1/maintenance/health-checks` | POST | admin |
| `/api/v1/maintenance/health-checks/{check}` | GET | admin |
| `/api/v1/maintenance/health-checks/{check}/findings` | GET | admin |
| `/api/v1/maintenance/health-repair-plans` | POST | admin |
| `/api/v1/maintenance/health-repair-plans/{plan}` | GET | admin |
| `/api/v1/maintenance/health-repair-plans/{plan}/execute` | POST | admin |
| `/api/v1/maintenance/health-repairs/{repair}` | GET | admin |
| `/api/v1/maintenance/health-repairs/{repair}/rollback` | POST | admin |
| `/api/v1/maintenance/health-state` | GET | admin |
| `/api/v1/maintenance/health-state/recover` | POST | admin |
| `/api/v1/maintenance/instance-activation` | GET | admin |
| `/api/v1/maintenance/instance-state` | GET | admin |
| `/api/v1/maintenance/instance-state/release` | POST | admin |
| `/api/v1/maintenance/plans` | POST | admin |
| `/api/v1/maintenance/plans/{id}` | GET | admin |
| `/api/v1/maintenance/plans/{id}/execute` | POST | admin |
| `/api/v1/maintenance/remote-file-info-backfill` | POST | admin |
| `/api/v1/maintenance/restores` | GET | admin |
| `/api/v1/maintenance/restores` | POST | admin |
| `/api/v1/maintenance/restores/{operation}` | GET | admin |
| `/api/v1/maintenance/restores/{operation}/abandon` | POST | admin |
| `/api/v1/maintenance/restores/{operation}/confirm` | POST | admin |
| `/api/v1/maintenance/restores/{operation}/preview` | POST | admin |
| `/api/v1/maintenance/restores/{operation}/rollback` | POST | admin |
| `/api/v1/maintenance/restores/{operation}/validate` | POST | admin |
| `/api/v1/maintenance/roots/{key}/entries` | GET | admin |
| `/api/v1/maintenance/storage` | GET | admin |
| `/api/v1/maintenance/trash` | GET | admin |
| `/api/v1/metadata-jobs` | POST | editor |
| `/api/v1/organization-context` | GET | read |
| `/api/v1/organization/preview` | POST | read |
| `/api/v1/parody-character-relations/{parody}/{character}` | DELETE | admin |
| `/api/v1/parody-character-relations/{parody}/{character}` | PUT | admin |
| `/api/v1/saved-searches` | GET | personal |
| `/api/v1/saved-searches` | POST | personal |
| `/api/v1/saved-searches/{id}` | DELETE | personal |
| `/api/v1/saved-searches/{id}` | GET | personal |
| `/api/v1/saved-searches/{id}` | PATCH | personal |
| `/api/v1/search-results/{kind}/{id}/matches` | GET | read |
| `/api/v1/series` | GET | read |
| `/api/v1/series` | POST | editor |
| `/api/v1/series-discovery-jobs` | POST | editor |
| `/api/v1/series-discovery-jobs/{id}` | GET | read |
| `/api/v1/series-discovery-jobs/{id}/pending` | GET | read |
| `/api/v1/series-proposals` | GET | read |
| `/api/v1/series-proposals/{id}` | GET | read |
| `/api/v1/series-proposals/{id}/accept` | POST | editor |
| `/api/v1/series-proposals/{id}/dismiss` | POST | editor |
| `/api/v1/series-proposals/{id}/members` | GET | read |
| `/api/v1/series-proposals/{id}/resolve` | POST | editor |
| `/api/v1/series-proposals/{id}/restore` | POST | editor |
| `/api/v1/series-proposals/{id}/undo` | POST | editor |
| `/api/v1/series/{id}` | DELETE | editor |
| `/api/v1/series/{id}` | GET | read |
| `/api/v1/series/{id}` | PATCH | editor |
| `/api/v1/series/{id}/members` | GET | read |
| `/api/v1/series/{id}/members` | POST | editor |
| `/api/v1/series/{id}/members` | PUT | editor |
| `/api/v1/series/{id}/members/{item}` | DELETE | editor |
| `/api/v1/series/{id}/members/{item}/move` | POST | editor |
| `/api/v1/series/{id}/sort` | POST | editor |
| `/api/v1/series/{id}/transfers` | POST | editor |
| `/api/v1/settings/enhancements` | GET | admin |
| `/api/v1/settings/enhancements` | PATCH | admin |
| `/api/v1/settings/gallery-highlight/connections` | GET | admin |
| `/api/v1/settings/gallery-highlight/connections` | POST | admin |
| `/api/v1/settings/gallery-highlight/connections/{id}` | DELETE | admin |
| `/api/v1/settings/metadata-provider` | GET | admin |
| `/api/v1/settings/metadata-provider` | PATCH | admin |
| `/api/v1/settings/metadata-provider/test` | POST | admin |
| `/api/v1/settings/provider-credentials` | PUT | admin |
| `/api/v1/settings/service` | GET | admin |
| `/api/v1/settings/tag-translations` | GET | admin |
| `/api/v1/settings/work-recommendations` | GET | read |
| `/api/v1/tag-catalog` | GET | read |
| `/api/v1/tag-catalog/{id}` | GET | read |
| `/api/v1/tag-catalog/{id}/editions` | GET | read |
| `/api/v1/tag-catalog/{id}/images` | GET | read |
| `/api/v1/tag-catalog/{id}/images/{role}` | DELETE | admin |
| `/api/v1/tag-catalog/{id}/images/{role}` | GET | read |
| `/api/v1/tag-catalog/{id}/images/{role}` | PUT | admin |
| `/api/v1/tag-catalog/{id}/images/{role}/content` | GET | read |
| `/api/v1/tag-catalog/{id}/relations` | GET | read |
| `/api/v1/tag-catalog/{id}/works` | GET | read |
| `/api/v1/tag-suggestions` | POST | read |
| `/api/v1/tag-translation-jobs` | POST | admin |
| `/api/v1/tags` | GET | read |
| `/api/v1/tags/{id}/follow` | DELETE | personal |
| `/api/v1/tags/{id}/follow` | PUT | personal |
| `/api/v1/tags/{id}/translations/{locale}` | DELETE | admin |
| `/api/v1/tags/{id}/translations/{locale}` | PUT | admin |
| `/api/v1/version-groups` | GET | read |
| `/api/v1/version-groups/` | GET | read |
| `/api/v1/version-groups/{id}` | DELETE | admin |
| `/api/v1/version-groups/{id}` | GET | admin |
| `/api/v1/version-groups/{id}/favorite` | PUT | admin |
| `/api/v1/version-groups/{id}/members` | GET | admin |
| `/api/v1/version-groups/{id}/members` | POST | admin |
| `/api/v1/version-groups/{id}/members/{book}` | DELETE | admin |
| `/api/v1/version-groups/{id}/members/{book}` | PATCH | admin |
| `/api/v1/version-groups/{id}/primary` | PUT | admin |
| `/api/v1/work-discovery-jobs` | POST | editor |
| `/api/v1/work-discovery-jobs/{id}` | GET | read |
| `/api/v1/work-organization/context` | GET | read |
| `/api/v1/work-organization/preview` | POST | editor |
| `/api/v1/work-proposals` | GET | read |
| `/api/v1/work-proposals/{id}` | GET | read |
| `/api/v1/work-proposals/{id}/accept` | POST | editor |
| `/api/v1/work-proposals/{id}/alternatives` | GET | read |
| `/api/v1/work-proposals/{id}/dismiss` | POST | editor |
| `/api/v1/work-proposals/{id}/resolve` | POST | editor |
| `/api/v1/work-proposals/{id}/restore` | POST | editor |
| `/api/v1/work-proposals/{id}/undo` | POST | editor |
| `/api/v1/work-recommendation-jobs` | POST | editor |
| `/api/v1/work-recommendation-jobs/{id}/feedback-export` | GET | read |
| `/api/v1/work-recommendation-jobs/{id}/misses` | GET | read |
| `/api/v1/work-recommendation-jobs/{id}/misses` | POST | editor |
| `/api/v1/work-recommendations` | GET | read |
| `/api/v1/work-recommendations/{id}/accept` | POST | editor |
| `/api/v1/work-recommendations/{id}/feedback` | POST | editor |
| `/api/v1/work-recommendations/{id}/preview` | POST | editor |
| `/api/v1/works` | GET | read |
| `/api/v1/works/{id}` | GET | read |
| `/api/v1/works/{id}` | PATCH | editor |
| `/api/v1/works/{id}/editions` | GET | read |
| `/api/v1/works/{id}/favorite` | PUT | personal |
| `/api/v1/works/{id}/merge` | POST | editor |
| `/api/v1/works/{id}/preferred` | PUT | editor |
| `/api/v1/works/{id}/separate` | POST | editor |
| `/api/v1/works/{id}/split` | POST | editor |
| `/api/v1/works/{id}/version-labels` | POST | editor |

动态注册路径另有严格动作白名单：work／series-proposals的accept、dismiss、restore、undo；批次方案另支持resolve。批次control允许continue、pause、resume、cancel、retry、recheck、skip_recommendation；来源查询control允许pause、resume、cancel、retry。未知动作拒绝；具体领域状态与CAS校验继续执行。

只读高亮Bearer路由独立认证，不使用普通session替代；每次查询核验账号启用、强制改密状态及当前库授权，最终发布比较ACL世代防止撤权后冻结旧结果。管理入口仅admin。网页SSE每批重新读取Cookie会话与当前ACL，事件先按库／owner过滤再LIMIT；无法归属的已删除对象事件不回退为全局事件。

实际匿名证据包括viewer现有网页POST搜索／直接详情／单页与封面越库／本人状态／默认拒绝新路由；editor完整匿名导入与固定推荐工作器；有确定非空方案的Work和Series采纳／撤销，Series忽略／恢复，以及组织操作；仓储覆盖三账号三库、计数／嵌套CTE、禁止main绕过和同名影子、共享来源原子拒绝、新来源和标签、撤权陈旧工作器、连接替换、旧新归属、标签／图片／事件和高亮。具体命令与结果见[专项说明](data-compatibility.md)。


## 设备 Bearer 白名单

方法和匹配路由必须同时满足下表。账号角色和逐库 ACL 仍用于各次请求。设备数据库写入重检会话并限制个人状态和凭证操作。

| 路由（相对于 `/api/v1`） | 方法 |
|---|---|
| `/auth/session`、`/auth/device-sessions`、`/client/capabilities` | GET |
| `/auth/logout`、`/auth/device-sessions/current/renew` | POST |
| `/auth/password` | PUT |
| `/auth/device-sessions/{id}` | DELETE |
| `/libraries`、`/editions/{id}/cover` | GET |
| `/client/works/search` | POST |
| `/client/works/{id}/reading-context`、`/client/editions/{id}/pages`、`/client/editions/{id}/pages/{page_index}/image` | GET |
| `/editions/{id}/reading-state` | GET、PUT |
| `/editions/{id}/reading-sessions` | POST |
| `/editions/{id}/user-state` | PATCH |
| `/saved-searches` | GET、POST |
| `/saved-searches/{id}` | GET、PATCH、DELETE |
| `/tags`、`/tag-catalog`、`/tag-catalog/{id}/images/{role}/content` | GET |
| `/tags/{id}/follow` | PUT、DELETE |
| `/client/editions/{id}/downloads`、`/client/editions/{id}/offline-manifests` | POST |
| `/client/downloads/{id}` | GET、DELETE |
| `/client/downloads/{id}/content` | HEAD、GET |
| `/client/offline-manifests/{id}`、`/client/offline-manifests/{id}/pages` | GET |
| `/client/offline/check`、`/client/offline/operations`、`/client/offline/operation-status` | POST |

设备不接收 SSE。维护、任务、凭据、增强配置、元数据编辑、组织整理入口仅按网页授权矩阵开放。详见[客户端协议](client-foundation.md)。
