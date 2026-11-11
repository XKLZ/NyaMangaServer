# 本地画廊高亮：只读协议、设置与浏览器脚本

## 开关与持久结构

GET/PATCH `/api/v1/settings/enhancements`新增固定feature `local_gallery_highlight`。新旧实例均默认false，使用原全局revision CAS，开关变化发原enhancements／global通知。该功能始终即时200、state=stable、effective_enabled与enabled一致、affected_tasks=0、impact_token为空、transition=null、resource_state=ready；ready只表示本功能无需模型等额外资源。开启或关闭不加载Python、不访问网络、不暂停任务、不冻结导入、不调用词库恢复、不创建transition，也不处理其它三个功能的pending／failed切换。pause／impact／retry参数用于本功能时400，不套用旧异步切换流。

```json
{"expected_revision":12,"feature":"local_gallery_highlight","enabled":true}
```

021新增gallery_highlight_settings与gallery_highlight_connections两表、来源关联及连接列表索引。旧enhancement_settings CHECK只容纳原三功能，并由transition外键引用；本批保留其定义和所有行，用独立单行表映射新feature，避免DROP／RENAME误连旧transition。001–020字节、checksum及账本保持，升级仍由应用创建私有一致性快照并事务迁移；匿名验证逐项比较所有旧表原列、账本和素材，不执行真实升级。关闭不删除连接；有效连接可在重新开启后继续使用，已经到期／撤销／改密失效的连接不会复活。

## 管理连接（原登录会话）

路径前缀`/api/v1/settings/gallery-highlight/connections`。只使用现有登录cookie；POST／DELETE沿原Origin和X-CSRF-Token门禁。账号只从session取得，创建事务再次验证该session仍存在且未过期；不接受account_id、library_id或其它身份字段。所有管理操作有3秒服务处理期限。

- GET：仅允许page和page_size，默认1／50，最大page_size=100、page=1000000。返回`{items,total,page,page_size}`，同一readtx中真实计数及分页，按created_at降序、id稳定排序，只当前账号。功能关闭时仍能查看和撤销。
- POST：必须先开启local_gallery_highlight；关闭时403 gallery_highlight_disabled，前端按“先开启，再创建连接”组织操作，不将其解释为令牌无效。严格JSON、最多4KiB。`{}`；不再接受name／expiry_days或任何其他字段（400），也不接受null。新授权expires_at=null长期有效，服务端用独立连接ID生成短编号name，不使用令牌片段。旧有限期限保留原值和失效语义。最多20条当前账号、当前auth_revision、未撤销且未到期的有效连接。
- DELETE `/{id}`：严格JSON、最多4KiB，`{"expected_revision":1}`。非负检查进一步要求1至9223372036854775806；独立revision CAS。撤销升世代并保留历史；准确当前revision重复撤销200不变，旧revision409。别人的id也404。所有写接口拒绝query参数。

创建201仅此一次返回秘密，匿名示例中的token占位符不可使用：

```json
{"protocol_version":2,"connection":{"id":"anonymous-connection-id","name":"授权-AbCdEf12","created_at":1800000000,"expires_at":null,"revoked_at":null,"revision":1,"state":"active"},"token":"<one-time-ngh1-token>"}
```

普通列表或撤销响应中的connection字段为：id、name、created_at、expires_at（null表示长期）、revoked_at（可null）、revision、state。时间为Unix秒。state依次判定revoked、expired、invalidated（account.auth_revision已变化）、active。没有token、摘要、account_id或auth_revision字段。库只存带用途分离的SHA256摘要，原令牌为256-bit安全随机值、前缀ngh1_；不与管理会话格式／摘要混用。连接有效期独立于创建时的管理会话；改密递增auth_revision后，该账号所有旧连接即失效，不影响别的账号，也无需扫描更新历史行。创建／复制结果不确定时，普通列表无法取回秘密，前端应提示明确撤销不确定连接，再由用户创建新连接，不自动重试创建或显示虚假的复制成功。

连接写入仅影响自身状态，不推进目录、来源、标签关注或元数据revision，也不触发下载、扫描或其他任务。

## 独立Bearer只读接口

仅下列两个精确路径接受`Authorization: Bearer <token>`，不用管理cookie，Cookie头存在时拒绝。不接受query-token、任何query参数、account_id或library_id。管理会话即使放Bearer中也401，高亮令牌放管理cookie或Bearer中都不能访问管理／图片／详情／标签等受保护接口。既有管理认证、CSRF与CORS不改变，不为EH／EX放行Access-Control-Allow-Origin，OPTIONS不启用外站预检。后台GM通过精确@connect授权访问，服务器不会向站点或用户提供的地址发起请求。

GET `/api/v1/gallery-highlight/status`不接受请求body；在有效令牌、功能开启时200：

```json
{"protocol_version":2,"provider":"ehentai","max_batch_size":200,"connection_expires_at":null}
```

POST `/api/v1/gallery-highlight/lookup`严格JSON、最多16KiB：

```json
{"provider":"ehentai","gallery_ids":["00123","123","456","789"]}
```

provider固定ehentai，EH／EX共用gid；只接收正十进制int64字符串，最多19位、不能是0、负数、分数、指数、URL或带站点token的对象。前端从页面提取gid，不提交画廊token。上限200按输入项数在去重前检查；去前导零，按首次出现顺序去重。响应200仅对应本请求：

```json
{"protocol_version":2,"provider":"ehentai","query_version":"<stable-sha256>","items":[{"gallery_id":"123","state":"collected"},{"gallery_id":"456","state":"needs_review"},{"gallery_id":"789","state":"none"}]}
```

| state | 短标记 | 精确判据 |
| --- | --- | --- |
| collected | 已收录 | 范围内至少一条正式来源关联，保存的file_state=available且matched_content_revision=content_revision |
| needs_review | 记录待检查 | 范围内存在正式来源关联，但没有上述正常记录 |
| none | 不标记 | 没有范围内正式来源关联 |

范围是所有启用且未回收的library、未回收的Edition，包含个人隐藏资料。只读edition_sources→external_metadata(provider=ehentai,external_id=gid)及files当前保存状态；同gid多Edition／多Work合法，聚合一次，任一正常记录优先。手填但未采纳URL、孤立external_metadata、source候选／预览／搜索、未确认导入候选均不算。未实时读源文件或检查挂载，因此标记只表示正式记录及保存索引状态；不能用它判定内容相同、版本身份或自动合并。没有模型、标题近似、封面查找或输出全库gid清单。

统计聚合、权限和来源读取在同一readtx；SQL由provider＋本次gid索引进入，再按metadata_id索引查关联，受200项和处理期限限制。查询不更新last_used、来源、目录、元数据、关注、推荐或其他业务revision。

query_version是协议、规范化请求顺序和实际聚合状态的稳定摘要，覆盖本批输出，不能盲用仅标题／有效标签变化才更新的世代。同gid来源换绑，即使有效标题／标签不变，只要本次状态变化摘要就变化；无关目录更新但响应不变则摘要不变。缓存到期后必须应用新请求的实际结果，不得以不相关版本相同跳过，也不把摘要当作全库快照或授权凭证。

## 生命周期、时限与错误

令牌在初次授权、SQL读取、结果冻结前重检；每次验证当前账号auth_revision、未撤销、未到期、功能开启。关闭／撤销／改密的提交与最后一次授权＋有界内存序列化互斥，未通过最终检查的在途结果不会发布；其后新请求必然拒绝。只在最后授权与冻结JSON的短阶段持读锁，来源查询、HTTP输出不持该锁或写事务。冻结是发布的线性化点；已授权并冻结的响应可能在撤销提交后才结束网络传输，不能追溯收回已经交给传输层的字节。

服务处理最多3秒，响应写入另设3秒deadline。Bearer查询全局最多4个、每连接最多2个并发；每连接每分钟60次请求（含status）、1000个规范化gid，状态查询计0个gid但计1次请求。限流窗口只在有界内存中保存，不写业务库；失效令牌不能占用连接窗口。返回429带Retry-After秒数，客户端等待并停止后台自动刷新，不立即循环重试。

| HTTP | code | 客户端处理 |
| --- | --- | --- |
| 400 | invalid_gallery_highlight／invalid_request | 修正字段、数量、gid、分页或JSON；严格拒绝未知字段和身份／范围注入 |
| 401 | gallery_highlight_unauthorized | 授权缺失／格式错误／到期／撤销／改密；停止重试，用户重新连接。管理认证前置仍可能返回unauthenticated |
| 403 | gallery_highlight_disabled | 有效令牌但功能关闭，停止重试；管理写还沿用origin_rejected／csrf_rejected |
| 404 | gallery_connection_not_found | 管理撤销目标不存在或不属于本人 |
| 409 | gallery_connection_revision_conflict | 保留旧状态，明确重新读取，不静默抬升CAS |
| 413／415 | request_too_large／unsupported_media_type | 遵循4KiB或16KiB及application/json限制 |
| 429 | gallery_highlight_rate_limited | 按Retry-After等待，受处理并发、请求数或gid数限制 |
| 429 | gallery_connection_limit | 已达20条有效连接，需明确撤销闲置连接；不自动创建替代 |
| 503 | gallery_highlight_unavailable | 存储或处理超时；保留未知／失败状态，不能渲染成“无标记”的成功 |

错误沿原Error信封，例如：

```json
{"error":{"code":"gallery_highlight_unauthorized","message":"连接授权无效、已撤销或已过期","request_id":"anonymous-request-id"}}
```

请求日志只含方法／状态／耗时／request_id，不含URL参数、Authorization、token、连接配置、私人标题路径或原始SQL异常；本功能存储失败不进入会记录底层错误的通用failure分支。管理连接路径在前置session查询遇到存储错误时也用脱敏503，其他旧管理路径的认证行为保持。接口均Cache-Control=no-store；应用不记录原令牌，代理和GM脚本也不得把授权或配置写入日志。

## 后端交付时的前端与GM交接（历史范围）

增强项展开后配置服务地址、连接列表、创建并复制一次性配置、撤销、下载脚本。先开启增强再允许创建连接；关闭时仍提供列表和撤销。脚本面向Chrome／Edge＋Tampermonkey，@connect仅目标主机、不能通配或内嵌token；用户由GM菜单粘贴配置，token只在GM存储。使用GM匿名无Cookie、禁重定向、主动超时请求。只提取当前页gid，200ms合并、2并发；60秒／1000项内存缓存，页面离开或功能停用停止刷新，401／403停止重试。每次真实刷新用新响应覆盖缓存，none清除旧标记，失败不当none。DOM只细边框＋短标记，不跳管理台、不拉全库，不自动扫描／关注／采纳。

最低TS字段（数值秒和revision在当前安全整数范围使用；CAS不得自行累加）：

```ts
type GalleryState = 'collected' | 'needs_review' | 'none';
interface GalleryConnection {
  id: string; name: string; created_at: number; expires_at: number | null;
  revoked_at: number | null; revision: number;
  state: 'active' | 'expired' | 'revoked' | 'invalidated';
}
interface GalleryConnections {
  items: GalleryConnection[]; total: number; page: number; page_size: number;
}
interface GalleryConnectionSecret {
  protocol_version: 2; connection: GalleryConnection; token: string;
}
interface HighlightConfig { protocol_version: 2; service_url: string; token: string }
interface HighlightStatus {
  protocol_version: 2; provider: 'ehentai'; max_batch_size: 200;
  connection_expires_at: number | null;
}
interface HighlightLookupInput { provider: 'ehentai'; gallery_ids: string[] }
interface HighlightLookup {
  protocol_version: 2; provider: 'ehentai'; query_version: string;
  items: Array<{ gallery_id: string; state: GalleryState }>;
}
```

service_url由前端从用户确认的服务地址构造配置，不从后端数据库回读秘密，也不让服务器测试或访问该地址。用户复制失败／创建结果不明时给出明确状态，不能伪造已连接。

## 匿名验证及未验证范围

源码实现提交：422477d（021／设置／只读连接与SQL／领域限流）和1fc61da（HTTP／受限授权／应用接线／OpenAPI）。本批使用合成资料、独立数据库和假Repository，既有工具缓存离线运行，实际通过：

- 新增11项顶层测试及并发子场景：store 5项、galleryhighlight 2项、httpapi 4项，初轮组合race与后续变更对应的增量race均通过。覆盖默认off、旧pending切换保持、一次性秘密／摘要、账号隔离及独立有效连接上限、30／90／365、过期／撤销CAS／改密、正式来源状态／多Work／隐藏／停用回收／未采纳URL与候选、gid规范化／排除范围注入、结果摘要和必要索引计划、Cookie／Bearer互斥／普通API拒绝、CORS保持、401／403／400／413／429／503与脱敏。
- 关闭、撤销、改密、到期四个竞争子场景，在SQL读取完成后阻塞结果，提交权限变化再放行，均未通过最终发布；随后新请求拒绝。慢HTTP输出阻塞时，撤销仍能独立完成。并发4全局／2每连接、60请求／1000gid分钟窗口及超时释放有假Repository用例，HTTP验证Retry-After。
- 20→21逐项比较91张旧非账本表的全部原列、001–020账本、合成素材文件字节、正式来源、关注墓碑／账号世代／私有事件、设置、暂停任务、人工决定及未完成transition，失败回滚、FK、两次打开通过；新旧实例默认关闭，无自动连接。最后加入非空正式来源和关注／私有事件的迁移夹具再定向验证通过。
- 定向旧迁移／连接策略／增强升级保留检查通过；新feature接口与旧增强持久生命周期race通过；最终管理session存储503及旧登录／Origin／CSRF对应增量race通过。旧LoginCannotRacePasswordChange、SessionExpiryAndLogout、HTTPAuthenticationLifecycle、OriginCSRFAndBoundaries四项受影响认证回归race通过。
- 受影响Go包、app与cmd/server的vet、依赖边界、Redocly OpenAPI、325条文档本地链接、gofmt、diff及变更范围检查通过。001–020、web、依赖、配置、推荐运行代码与冻结实验模块相对52f9d36保持。

没有读取或写入.runtime/dev、启停或探测真实8080／5173、创建真实token、访问EH／EX、运行真实模型／420／规模／部署套件；未重跑标签或设置前端流程，未构建生产网页、独立Go二进制或启动新服务。GM脚本、Chrome／Edge／Tampermonkey、设置UI／桌面手机、真实021升级及用户连接体验均未验收。后端停止写仓库，主对话独立审查后协调前端和升级，不自行派发或进入下一业务。


## 主对话审查与前端接入（2026-10-31）

主对话审查422477d／1fc61da／825a4cd：021没有重建旧增强表，旧三功能及pending切换保持；高亮Bearer与会话授权分离，最后只在有界内存冻结阶段持权限读锁，HTTP不持事务／锁。正式来源SQL范围、query_version及前置session存储失败脱敏与接口契约一致。独立运行规范化限流、20→21旧表保留及HTTP协议／隐私三个定向测试，galleryhighlight／store／httpapi均通过。001–020字节与52f9d36相同，web、依赖和推荐模块未修改；325条文档本地链接及diff检查通过。已有race／认证等证据不重复运行。

现由既有前端串行接入设置与独立GM脚本，后端及主对话停止并行写仓库。前端沿React SPA、Ant Design、原生CSS、既有主题和四区设置，使用Refactoring UI与web-design-guidelines，不引入Tailwind／新依赖或新的导航页。创建前须开启功能，关闭仍可查看／撤销连接和下载不含令牌的脚本；页面不能把授权active或资源ready冒充真实浏览器已连接。

## 前端与独立脚本交付（2026-10-31）

前端源码提交859899f，基于独立图片复验文档提交53efbbd继续串行交付；测试与本节证据随后单独提交。没有改写后端或图片组件历史。

本轮接入React／Ant Design四区设置，沿用现有主题与表单提交保护，没有新增导航、框架或依赖。增强项显示“本地画廊高亮”，默认关闭，资源ready显示“无需额外资源”；开关提交沿原全局revision，始终使用即时切换，不附加暂停任务或impact参数。旧API缺少新feature时显示升级提示、禁用开关和创建，不访问新增连接接口，不把未升级解释为已开启或已连接。

展开后先确认服务根地址，再填写名称及30／90／365天有效期（默认90天）创建连接。地址默认当前网页origin，可改为浏览器能够访问的HTTP(S)根origin；拒绝凭据、子路径、查询、片段与控制字符，非回环HTTP邻近提示明文传输风险。确认地址只做本地校验，不请求该地址。连接按服务端实际结果每页10条展示，active称“授权有效”，不宣称浏览器在线；功能关闭后仍可查看／撤销连接及下载脚本。

创建成功的一次性配置只保存在当前组件内存，不进入URL、历史、localStorage、sessionStorage、日志或脚本文件。用户明确点击复制，只有Clipboard写入成功才显示成功；失败保留可手动选择的配置文本。切页／离开或收起时保护未保存地址、名称与一次性配置；提交期间全局开关、编辑和关闭受锁。创建网络／5xx或响应不完整时不自动重试，明确要求刷新列表、撤销可能创建的连接后重建。撤销先确认影响并冻结连接revision；409或结果不明保留选择，必须重新读取、核对后明确提交，不静默抬升版本。

### 安装、连接与撤销

### 脚本边界与排查

脚本只识别当前DOM的标准列表／缩略／扩展卡片及详情画廊ID，不提交画廊token、URL、标题、封面或全库快照。动态变化固定200ms合并，批量最多200项、并发最多2；内存缓存最多1000项／60秒，客户端也限制每分钟60次（含status）及1000个gid。每次刷新校验完整响应并应用实际状态，即使query_version不变也不会跳过；none清除旧标记，失败保持未知而非“未收录”。换配置、换页、隐藏、pagehide均取消旧读取，旧回调不能重新绘制；回到前台仅重查过期项。

网络只走GM匿名、无Cookie请求到固定origin的两个精确Bearer接口，禁止重定向，独立10秒主动计时并中止，不回退fetch／JSONP、不放宽CORS或关闭TLS校验。429遵守Retry-After，不立即循环；临时连接失败采用15／30／60秒有界退避，协议不完整、重定向或不支持可中止GM能力时停止自动请求。徽标使用独立前景／背景配对，站点主题与系统主题不同时仍保持可读，原卡片背景不改。

“连接状态”只显示有限诊断，不含令牌或私人内容。缺少粘贴菜单先核对Tampermonkey及隔离上下文；origin不一致应重新下载对应脚本，不能手改秘密绕过校验。连接失败检查直达地址、证书、浏览器／GM主机及本地网络权限，勿把有效授权状态当作网络可达。管理页确认地址不发起连接测试；脚本不会自动更换地址或访问其它站点来修复环境。

### 本轮验证与证据

使用schema21的18081／18082新建匿名实例，5173仅向匿名浏览器提供无业务Cookie的源模块。匿名工具overlay只增加galleryhighlight装配；没有修改正式Go、SQL、OpenAPI、配置或依赖，也没有生产构建、日常服务重启、真实令牌或E站访问。

- 8项定向单元通过（高亮4项、既有增强4项）：origin／名称及代码注入边界、一次性配置信封、精确主机模板、即时切换／旧服务、沙箱不支持时零秘密读写。最终日志`gallery-highlight-unit-final.log`。
- 设置页桌面／手机2项通过（24.8秒）：默认off、旧服务不访问新增API、地址／有效期、真实匿名创建与Bearer200／管理401／关闭403、一次性配置与复制失败／成功、创建不确定不重试、提交全局锁、撤销CAS409显式重读、12条真实连接分页及20条上限错误展示。日志`gallery-highlight-settings-ui-final.log`。
- GM下载产物的4个独立匿名模拟场景通过：`gallery-highlight-gm-boundaries.log`中的桌面／手机“批量上限、缓存过期、并发与主动超时；不支持的沙箱拒绝秘密”两项保留为证；其中另一手机视觉用例曾因夹具缺少viewport返回980px而失败。只修复夹具UTF-8、viewport和响应Content-Type后，重跑受影响的桌面／手机“下载脚本的GM模拟”两项通过（7.4秒），最终日志`gallery-highlight-gm-ui-final.log`。未把前一日志的整轮记为全过，也没有重复无变化的边界用例。
- 模拟覆盖动态／重复／导航排除、同版本实际状态更新与none清除、未知／失败／401／403／429、隐藏取消、旧配置／页面响应隔离、主动超时及配额；所有EH／EX URL和子资源均拦截到匿名HTML，不发生真实站点网络访问。
- TypeScript、完整前端ESLint与格式检查通过，独立userscript另外通过Prettier。完整ESLint日志`gallery-highlight-lint.log`；最终`gallery-highlight-lint-final.log`为安装文案调整后的组件定向复查。最终类型／格式／独立脚本格式日志分别为`gallery-highlight-type-final.log`、`gallery-highlight-format-final.log`、`gallery-highlight-script-format.log`。测试文件由Prettier与实际运行验证，不计作ESLint覆盖。文档本地链接、diff及修改范围另在交付前核对。

以上日志均在被忽略的`.runtime/tests/results/`。最终13份源码／测试SHA256快照为`gallery-highlight-source.sha256`，下载校验清单为`gallery-highlight-artifacts-final.json`。4份设置页实际下载产物与当前模板按各自origin生成结果逐字节相同、均可独立语法解析且无令牌／通配授权；桌面匿名local产物SHA256为`9947632d231c0a524a293bedb89025f1b8852f5f10ee563b01f2fccde0cbb1b0`，GM模拟实际使用这份文件。下载目录`.runtime/tests/gallery-highlight-ui/downloads/`仅为匿名验证产物，用户应在升级后的设置页确认自己的origin后自行下载。

18张正常viewport截图在`.runtime/tests/gallery-highlight-ui/screenshots/final/`：设置／连接12张及交叉站点／系统主题GM6张，覆盖1280／1440桌面、390手机和明暗。故障／一次性配置遮罩证据单列在`evidence/`，不拿故障态或旧fullPage截图当正常布局；已按Refactoring UI和既有Web Design Guidelines要点复核层级、间距、主题、焦点与溢出，徽标文字配对测试对比度至少4.5:1。

主对话复核最终13份源码／测试摘要、4份设置页实际下载产物与当前模板的一致性，以及正常viewport的桌面／手机设置和交叉主题GM截图。正式Go及001–021未被前端修改，没有新增依赖；53efbbd图片复验记录独立保留。主对话此前三个后端定向复验与双方现有证据仍对应最终源码，未重复无变化套件。GM的DOM隔离前置检查、令牌不进站点页面、无Cookie／禁止重定向、输出文字对比度及失败不冒充未收录均已审查；真实扩展行为仍需用户手工验证。

升级前核实排队／运行／暂停任务、组织重验、待创建子任务、未完成来源预览及增强切换均为零。正常停止API，保留旧程序后，仅编译Go程序并嵌入已有网页产物，没有执行生产网页构建。由应用在实例排他锁内创建0600一致性schema20快照，再事务升级021并恢复API8080；运行构建为40d60e3，Vite5173的原进程保持。

迁移前后逐项核对91张旧业务表的原列与全部行摘要、001–020账本、12454个配置／秘密／素材／缓存／索引文件摘要均一致。新增两表使总表数92→94；高亮设置为关闭、revision=1，连接表为空。新快照的91张旧业务表与停服基线相同，quick_check及外键检查通过。没有改密、创建真实令牌、扫描、获取元数据、启用高亮或访问E站。

HTTP启动核验仅覆盖API及Vite代理的存活／就绪、初始化状态和未授权边界：管理设置与连接列表、Bearer状态和批量查询在无授权下均401，没有放宽CORS。此检查不代替业务测试；业务已由独立匿名实例覆盖。私有保留报告、快照核对及旧程序位于`.runtime/maintenance/gallery-highlight-20261031/`，不进入Git，升级进度为complete。

### 使用入口与运行命令

在日常Vite管理台进入“设置 → 增强功能 → 本地画廊高亮”，或打开 `http://127.0.0.1:5173/admin/settings?section=enhancements&focus=local_gallery_highlight`。按上节安装步骤启用功能、确认浏览器可达地址、下载相应脚本，再创建连接并复制一次性配置到Tampermonkey菜单。源码中的userscript文件是待绑定地址的模板，不应直接当作安装产物；匿名下载产物也不用于真实连接。

现有服务已经运行；标准命令为`make dev-api`、`make dev-web`，查看用`make status-dev`，正常停止用`make stop-dev`。不需要重新初始化或清库。

未验证项：真实Chrome／Edge＋Tampermonkey安装与隔离、浏览器局域网权限、NAS／反向代理／TLS及私人画廊连接；用户自行启用体验后再记录。未运行模型、历史评测、规模或部署套件，生产网页仍等待本批界面确认后集中构建一次。完成后停在本批，不自动进入健康修复或备份。

## 2026-11-01：022与协议2后端交接

新POST创建body严格为{}（4KiB、会话／Origin／CSRF保持），不再提供名称或期限选项；旧参数包括空name／expiry_days=null均400 invalid_request，body null返回400 invalid_gallery_highlight。响应secret／status／lookup的protocol_version均2；connection.expires_at、status.connection_expires_at为number|null，必有字段，null不等于未知或遥远日期。旧有效有限期记录继续返回真实number。20条有效限额包含长期记录；开关on才能创建，off仍可列出和撤销。身份、scope、限流和最终冻结授权门禁保持。

022只重建无入向外键的连接叶表，复制所有旧ID、name、摘要、期限、auth_revision、撤销和revision，不转换旧期限为长期；001–021保持。新name为“授权-”加独立连接ID前8字符组成的非敏感短编号，不能作为秘密或授权依据。前端应先适配协议2／空body／nullable，旧v1脚本拒绝2；下载配置的protocol_version也必须2，不能将ngh1_前缀误当作协议版本。

Docker材料容器端口统一9400、外部端口仅一条映射；宿主192.168.1.3、NYAMANGA_PORT=19400时，用户与GM服务地址及public_url同为http://192.168.1.3:19400，容器仍9400。开发8080／5173及internal/config默认不变，见[部署材料](../deploy/README.md)。本轮不执行Docker、部署smoke网络流程、生产网页构建、真实升级或连接操作。实际验证：store／galleryhighlight／httpapi定向race通过（TestGallery*、增强HTTP契约和迁移连接策略），包含新增21→22全表／001–021账本／合成素材字节、回滚／重开／FK、旧合法及过期／撤销／改密记录保持、新长期跨20年不伪到期、NULL＋旧有限期混合20上限、off／改密／撤销／并发最终freeze、协议2及旧参数400、scope／CORS／脱敏。三个包分别72.919s／1.019s／41.189s；正常定向检查亦通过。受影响包及app／cmd的vet、Redocly OpenAPI、依赖边界、332条文档本地链接、gofmt、diff及001–021／web／internal/config／依赖／推荐模块保持检查通过。部署静态2项通过，AST只解析两份smoke而未执行；没有Docker、网络、真实升级、生产启动、网页构建或协议2前端／扩展验收。


## 2026-11-01：协议2前端与授权码交付

从0dbf276串行实施，源码提交11c7aa6，测试与说明另作本地提交。仅修改web及本专项／导航说明，未改Go、001–022、OpenAPI、部署材料、依赖或内部默认端口。日常仍40d60e3／schema21／94表、Vite5173原进程保持；本轮仅在18081／18082独立匿名schema22验证，没有真实授权、真实E站请求或日常写入。

设置将名称和有效期输入移除，“服务地址”改“NyaMangaServer地址”，主操作为“生成授权码”，只POST `{}`。服务端短编号直接显示，浏览器授权列表只保留编号、创建时间、状态和撤销，不常驻到期列，不称在线。`expires_at`类型允许null，不伪造日期；旧期限行继续由服务端返回真实状态。利用已有`/settings/service`的database_schema核对协议2支持：未读取、读取失败或schema21时不生成，刷新授权列表同步重读；旧feature缺失门禁保留，旧授权列表和撤销仍可用。

一次性结果是单独的只读授权码，不显示多字段JSON。明确复制且成功才提示“授权码已复制”；失败保留手动选择。码只在组件内存，不进URL、历史、站点／本地存储或日志。生成不确定不重试，要求核对并撤销可能生成的授权后显式重新生成；全局提交锁、离开／收起草稿保护及撤销CAS409核对保持。授权码不绑定输入地址，展示期间仍可确认地址及下载脚本；地址有未确认修改时不能下载。

### 当前安装步骤

脚本版本2.0.0，配置／secret／status／lookup均按协议2。存储键沿用以识别旧配置；读取到v1时保留GM原值，不查询、不当成未收录，明确要求重新配置。新脚本遇到旧协议／缺字段／非法到期值时停止并提示“请更新脚本或服务”，不承诺连接旧API21。status的必有`connection_expires_at=null`表示长期，数字旧有限期必须是有效未来安全整数秒；长期缓存仍最多60秒。401／403停重试、429等待、独立10秒主动中止、匿名无Cookie／禁止重定向、200ms合并／2并发／200项及迟到隔离保持。启动GM.getValue也保存读取代次，成功重配或清除后，旧读取或旧失败不能覆盖当前配置与诊断。

### 本批验证和未验证范围

- 8项相关单元通过：origin安全边界、协议2原始授权码及新授权null期限、旧协议拒绝、无秘密的精确主机脚本、开关兼容及沙箱门禁。日志`gallery-highlight-v2-unit-final.log`。
- 设置页匿名桌面／390手机2项通过（22.5秒）：schema22、缺feature／schema21不生成、真实POST{}和服务端短编号／expires_at=null、单码复制失败与成功、创建不明不重试、提交锁、离开保护、独立Bearer200／管理401／总开关403、撤销真实CAS409、分页及20条有效授权错误。初轮发现授权列表刷新未同步服务协议检查，修复后通过；术语统一后只重跑受影响的这两项。最终日志`gallery-highlight-v2-settings-terms-final.log`，前次行为通过日志`gallery-highlight-v2-settings-ui-final.log`保留。
- 设置页实际下载的脚本运行GM匿名模拟，桌面／手机各4项，共8项通过（11.5秒）：布局与状态／网络边界、批量缓存并发主动超时、双prompt取消／错origin／非法码原子保护、null及数字有限期／缺字段／非法值／旧协议、旧v1存储零请求零写入，以及初始化GM读取迟到不覆盖重配或清除。日志`gallery-highlight-v2-gm.log`。全部EH／EX URL及子资源拦截为匿名HTML，没有真实站点访问。
- 前端完整ESLint／格式、独立脚本格式和TypeScript通过。日志`gallery-highlight-v2-{lint,format,script-format}.log`及最终`gallery-highlight-v2-type-final.log`；后续仅文案影响的组件／辅助函数定向ESLint为`gallery-highlight-v2-copy-lint.log`，格式为`gallery-highlight-v2-terms-format.log`，末次加载文案由`gallery-highlight-v2-last-copy-{lint,format}.log`复核。测试脚本由Prettier及实际执行验证，不计作ESLint覆盖。336条文档本地链接、diff及修改范围检查通过，没有重跑已通过后端、图片、模型、规模或部署套件。

上述日志在被忽略的`.runtime/tests/results/`。本轮正常viewport截图独立存放在`.runtime/tests/gallery-highlight-v2-ui/screenshots/final/`（18张，1280／1440及390、明暗、交叉主题），一次性授权码遮罩及故障状态分放`evidence/`；不覆盖v1证据。沿Refactoring UI及既有Web Design Guidelines要点复核，正常页没有名称／有效期输入或到期列，列表只展示非敏感自动编号。实际下载产物在同级`downloads/`，只绑定匿名origin，不供真实安装；最终9份源码／测试及4份产物摘要见本轮结果目录的`gallery-highlight-v2-source.sha256`和`gallery-highlight-v2-artifacts-final.json`。4份实际下载与当前模板逐字节一致，精确@connect、版本2.0.0、无秘密且可独立语法解析；桌面local产物SHA256为`7b01b18db5b6c28d44145ffa80639bbda6b3b933909552910c3d83d892994bca`。另核对上一轮实际v1下载的原摘要，并离线执行其原status校验函数，面对协议2与合法数字期限仍拒绝为protocol；该兼容校验记录在同一产物清单，不当作真实扩展验收。

主对话审查022连接叶表重建、NULL授权查询／20条有效限额／最终发布、严格空对象解码及协议2响应；001–021、开发配置默认、模型及依赖保持。独立复验新迁移保留、长期授权撤销和HTTP协议三个定向用例通过，两个部署静态用例通过；没有重复已通过race或模型套件。前端完成后核对最终9份源码／测试摘要、4份实际下载脚本与当前模板一致，只有精确主机授权且不含授权码；审阅正常桌面／手机设置截图，名称、期限及JSON配置已移除。最终前端提交没有改Go、SQL、OpenAPI、部署或依赖。

真实实例核实：排队／运行／暂停任务、版本及系列重验、待创建子任务、来源查询待执行和增强切换均为零；高亮关闭，授权表为空。正常停止本项目API，保留旧40d60e3程序；仅编译Go并嵌入已有网页产物，没有执行生产网页构建。应用创建0600一致性schema21升级快照，在实例排他锁内事务执行022后恢复API8080，运行构建bc43089，Vite5173原PID保持。

前后93张原业务表全部原列／行摘要、001–021账本与12454个配置／秘密／素材／缓存／索引文件摘要一致。总表数仍94，连接期限列确认为可空，高亮保持关闭、授权记录为零。新一致性快照的schema21、93张业务表、旧账本、quick_check及外键全部核对通过。没有创建授权、修改密码、扫描、下载、模型推理或真实画廊查询。

API和Vite代理各7项启动／未授权边界检查通过：存活、就绪、已初始化及管理增强／授权列表／高亮状态／批量查询的无授权拒绝，未放宽CORS。这不是业务验收替代，业务结果来自本轮独立匿名实例。私有报告、旧程序及进度保存在`.runtime/maintenance/gallery-highlight-v2-20261101/`，目录0700、记录0600，进度complete，不进入Git。

当前使用[上述安装步骤](#当前安装步骤)：启用后生成并复制单独授权码，确认地址下载协议2脚本，在“配置服务器”中输入完整地址及授权码。开发仍8080／5173；Docker示例容器9400，宿主19400时访问`http://192.168.1.3:19400`，只需一条映射且public_url相同。部署材料仅静态核对，未构建或启动容器。标准命令仍`make dev-api`／`make dev-web`、`make status-dev`和`make stop-dev`，当前服务已经运行，不需要初始化。

未验证：真实Chrome／Edge＋Tampermonkey隔离与局域网权限、NAS／Docker／代理／TLS及用户连接；待用户自行启用体验。生产网页仍等本批界面确认后集中一次构建，不自动进入健康修复、备份或其他模块。
