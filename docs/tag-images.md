# 标签专属图片

本批从4643514串行实施，新增018迁移；001–017保持。图片全局归属实际parody／character标签，foreground主体和background背景各自可空、各自CAS，跨库共用。其他类别不支持图片。图片不来自漫画封面、联网搜索或推断，不影响人工译名、元数据、组织和关系决定；这些编辑的revision不随图片提升。

## 持久性与素材

复用assets保存ID、相对键、SHA256和大小；tag_images保存tag／role外键、素材ID、方向修正后的原尺寸、识别格式及独立单调revision。默认缺图revision=0；删除保留墓碑并增加revision，再上传须使用删除后的版本，避免ABA。018不回填图片、不修改旧表旧值或任务。原素材长期保存在`data/assets/tags/<SHA256前两位>/<素材ID>.<png|jpeg|webp|bmp>`；只在显式孤儿清理中重新核对引用后删除，替换／删除不立即销毁素材。

原子落盘必须先于事务引用；数据库失败保留旧引用，新文件成为受24小时宽限保护的孤儿。复用原assets引用检查并加入tag_images，外键阻止删除仍被引用的记录。网页没有原素材下载入口。

## 验证状态

## 完整HTTP契约

以下路径均带`/api/v1`前缀且要求现有登录Cookie。图片属于全局tag，不接收书库范围，不因书库停用而不可读；不接受宿主路径、URL、文件名、素材ID引用或multipart。实际tag必须已存在且namespace为parody或character；artist／group／content返回400。默认两role为空、revision=0。读取图片状态和卡片摘要不代表原素材或缓存一定可读，存储故障仍按下表返回。

| 方法与路径 | 请求 | 成功响应 |
|---|---|---|
| GET `/tag-catalog/{id}/images` | 无业务参数 | TagImages，固定foreground和background两项 |
| PUT `/tag-catalog/{id}/images/{role}` | 原始文件字节；Content-Type为image/jpeg、image/png、image/webp或image/bmp；`X-Expected-Revision`为该role当前版本；现有Origin及X-CSRF-Token | 200，更新后的TagImage |
| DELETE 同一role路径 | 无body；相同版本／Origin／CSRF头 | 200，asset_id=null、尺寸0、format空，revision加一 |
| GET `/tag-catalog/{id}/images/{role}/content?revision=N` | 必填单个正整数revision；可带If-None-Match | 200 image/png或实际字节ETag命中的304；不提供原文件 |

role严格为foreground或background。revision为int64非负值，写入必须小于MaxInt64；content要求正值。两张图片互不覆盖：先读到fg=2／bg=5，保存fg带2；随后保存bg仍带5。删除同样占用一个世代，即使删除空role也递增。陈旧版本409必须保留用户pending文件，重新读取状态并由用户决定重试，不自动提升expected版本。content先核对当前role版本，再判断ETag；旧URL始终409，不能用新图或304冒充旧图；当前空role的当前版本返回404。

响应TagImage固定字段：`role`、`revision`、`asset_id`（string或null）、`width`／`height`（方向修正后的原尺寸）、`format`（识别出的png/jpeg/webp/bmp，缺图为空）、`processing_version=1`。不暴露相对素材键或绝对路径。TagImages再含`tag_id`及固定两role。状态仅作编辑版本来源，不向前端提供可指定的素材路径。显示URL由上述固定路径、role和revision构造，不使用原素材ID拼接文件地址。

单次最大10MiB，以内容识别且要求声明MIME与实际格式一致，拒绝损坏、伪装、GIF／SVG等其他格式。与归档图片共用comic.Pool的两解码槽、100000000像素上限、256MiB总解码内存预约；预算不足会拒绝，不加载模型或调用外部图片服务。JPEG APP1、PNG eXIf和WebP EXIF的标准TIFF方向1–8在显示重编码时应用，私有原字节不变；原图带ICC／EXIF等附加资料不会直接进入显示图。主体PNG等比缩小至768×768以内，不放大、不裁切并保留alpha；背景PNG居中裁切到1024×512，可放大，不做模糊、自动配色或手动构图。响应Content-Type始终image/png，`X-Content-Type-Options: nosniff`、`Cache-Control: private, no-cache`，强ETag为实际PNG字节SHA256（带引号）。

| HTTP | 稳定错误code | 含义 |
|---|---|---|
| 400 | invalid_tag_image | 类别、role、revision或参数不合法 |
| 401／403 | 沿既有unauthenticated／Origin／CSRF错误 | 登录与请求验证失败 |
| 404 | tag_image_not_found | 标签不存在或当前role无图 |
| 409 | tag_image_revision_conflict | 写入或content版本陈旧 |
| 413 | tag_image_limit | 文件、像素或解码内存超限 |
| 415 | invalid_tag_image_type | MIME不符、格式不支持或解码损坏 |
| 503 | tag_image_storage_unavailable | 文件／缓存／数据库存储故障；不泄漏底层错误、路径或图片内容 |

写入后沿既有SSE `/events`的`event: change`投递JSON：`{"type":"tag_image","resource_id":"<tag_id>:foreground","revision":3,...}`。revision仅为该role的版本；另一role、metadata、translation、relations和organization的revision均不提升。前端只刷新对应图片摘要；有pending文件时提示冲突，不借SSE静默更新待提交版本。事件不含图片字节、路径或标签名称。

## 匿名请求／响应样例

以匿名原作tag `anonymous-parody`说明（实际使用服务返回的真实标签ID）：

```http
PUT /api/v1/tag-catalog/anonymous-parody/images/foreground
Content-Type: image/png
X-Expected-Revision: 0
Origin: http://localhost
X-CSRF-Token: <现有会话的CSRF>
Cookie: <现有登录Cookie>

<匿名透明PNG原始字节，禁止base64或JSON包装>
```

```json
{"role":"foreground","revision":1,"asset_id":"anonymous-asset","width":400,"height":800,"format":"png","processing_version":1}
```

接着GET `/tag-catalog/anonymous-parody/images`：

```json
{"tag_id":"anonymous-parody","foreground":{"role":"foreground","revision":1,"asset_id":"anonymous-asset","width":400,"height":800,"format":"png","processing_version":1},"background":{"role":"background","revision":0,"asset_id":null,"width":0,"height":0,"format":"","processing_version":1}}
```

主体显示GET `/tag-catalog/anonymous-parody/images/foreground/content?revision=1`返回384×768的安全PNG及实际ETag。DELETE同role带`X-Expected-Revision: 1`返回revision=2、asset_id=null；旧revision=1显示URL随后409，revision=2显示URL404，background仍为0。下一次上传主体必须带2。

## 批量摘要与快照

目录items、详情以及双向关系列表中所有CatalogTag对原作／角色增加可选`images: TagImages`，普通类别省略；即使无图也返回两role的0世代。目录及关系页在现有读事务中收集当前页ID并一次批量读取图片身份／版本，不逐卡查状态，不重新推断关系。旧字段与view=all／display、计数、筛选、排序保持。图片世代计入标签查询snapshot，图片上传／移除后旧分页快照返回原409 `tag_catalog_snapshot_expired`，包括全局图片在另一库修改；空图片表保持原快照算法结果。这个读取失效域不改变任何其他领域的编辑revision。

## 独立缓存、诊断与清理

可选宿主配置（不是网页设置），旧配置可继续省略：

```json
{"tag_image_cache":{"max_bytes":268435456,"max_files":10000}}
```

0或省略取默认256MiB／10000files，负值拒绝。目录为`data/cache/tag-images/v1/<缓存SHA256前两位>/<缓存SHA256>.png`。缓存键计算包含原素材digest、role、固定尺寸／contain或center-crop和处理版本v1；与漫画`cache/covers/v1`的额度和LRU实例完全独立。复用imagecache原子落盘、目录边界、LRU、mtime及重启恢复；缓存缺失从仍被引用的私有原素材校验长度和SHA256后重建。存储／额度失败返回503，不替换原图或解除引用。

存储诊断categories新增`tag_images`计入独立缓存；长期tag素材纳入既有`assets`总量。显式清理categories新增`tag_images`（只清派生缓存）；`orphan_assets`包含已失去引用的tag原素材，保留24小时宽限、pending文件及全部当前引用。AssetKeys和PruneAssets均保护cover_selections、recommendation_materials及tag_images。生产app只装配一个共享tagimages服务，HTTP读取／发布／删除和显式清理共用存储保护锁；清理取得锁后重新读取引用，逐文件核对size／mtime，并通过缓存自身锁条件删除，最后事务重查引用再删assets记录。这样旧预览、并发发布、在用原文件与缓存替换不会跨越引用复核和删除之间的窗口。所有原素材操作以受限os.Root和相对键执行，拒绝越界和符号链接；不会因清缓存删人工素材，也没有扩展原素材自动清理。

## 交接和验证界限

前端后续接入原作／角色图片卡片与局部编辑，明确“全库共享”，保留pending文件及每role expected revision；直接使用批量images避免N+1。主体完整显示、背景居中裁切，不新增自动找图／封面回填／手动构图控件。普通作者／社团／内容卡片只留译名或原名与Work数，12px padding、两行名，去掉156px min-height和prefix；目录与关系选择器移除零使用入口，不移入更多菜单，不再发送include_unused=true，旧URL规范化并回到第一页，但后端该兼容参数和既有人工关系继续保留。固定目录页脚、中间独立滚动及手机安全区由后续前端实施。本批web未改。原文标题优先、BookEditor直达和关系展示／编辑分离保持。


## 本批实际检查记录（2026-10-28）

- 35项顶层定向单元全部通过：comic、tagimages、store、httpapi、maintenance、config、imagecache和library。涵盖schema17→18的84张旧非账本表（含匿名账号／会话／来源／人工值／人工译名／组织／图片素材／暂停任务／关系决定）逐表值保持及001–017账本保持、外键／重复打开／失败回滚，角色CAS／ABA／版本耗尽边界，透明／方向／四格式／损坏／伪装／文件与像素／内存预算，原子IO／ENOSPC／权限／数据库提交故障注入，精确版本／ETag／Origin／CSRF／实际SSE回放、批量摘要／快照和旧标签接口回归，缓存重建／重启／配额，清理新引用／在用锁／指纹及旧封面回归。单元各包耗时0.05–6.327秒，匿名记录位于被忽略的`.runtime/tests/tag-images/unit.jsonl`。
- 17项核心顶层race全部通过（store64.928秒、httpapi39.721秒、tagimages27.809秒、maintenance13.618秒、comic2.509秒、config1.016秒、imagecache1.031秒）；补充PNG eXIf透明像素旋转、WebP VP8X+EXIF和TIFF方向1–8专项race1项通过（1.024秒）。共18项，记录为同目录`race.jsonl`和`exif-race.jsonl`。JPEG方向6也做了逐像素对照，不只核对宽高。
- 9项既有升级定向回归全部通过（1.900秒）：来源搜索、推荐任务引用、Work／Edition／File身份、完整导入／阶段决定、Series身份及推荐发现、管理维护升级的旧数据保持／回滚。仅把这些测试的“最新schema”断言17更新到18，以及连接策略测试的表数85更新到86；没有运行模型推理或修改推荐输入／算法。
- 涉及包`go vet`通过；确认已有`internal/webassets/dist`后单独`go build -o .runtime/tests/tag-images/server ./cmd/server`通过，未构建网页也未启动这个程序。
- OpenAPI lint、source-only Go依赖边界、287个Markdown本地链接与`git diff --check`通过。逐对象与4643514比对，旧API仅CatalogTag／详情新增可选images及清理categories枚举新增tag_images，另新增4个图片operation／2个schema；其余旧字段定义保持。001–017 SQL逐文件字节与基线一致；web、依赖、正式推荐运行代码和实验目录差异为空。

复现Go命令均从根目录使用离线固定工具链和匿名临时路径：

```sh
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
export GOPATH="$PWD/.runtime/cache/go" GOCACHE="$PWD/.runtime/cache/go-build"
export TMPDIR="$PWD/.runtime/tests/tmp"
go test ./internal/comic ./internal/tagimages ./internal/store ./internal/httpapi ./internal/maintenance ./internal/config ./internal/imagecache ./internal/library -run '^(TestTagImage|TestTagCatalog(Counts|Deterministic|Human|SourceRefresh|Dictionary|Relation|Overlapping|Works|HTTP)|TestCoverCommitFault|TestCoverAssetProtection|TestCacheLRU|TestShardingRestart|TestCleanupConditional|TestCleanupDoesNotRemove|TestCleanupFault|TestMigrationAndConnectionPolicy|TestExplicitConfiguration|TestCoverCacheConfiguration)' -count=1
go test -race ./internal/comic ./internal/tagimages ./internal/store ./internal/httpapi ./internal/maintenance ./internal/config ./internal/imagecache -run '^(TestTagImage|TestTagCatalog(RelationConcurrent|RelationDisplay|WorksReadTransaction|HTTP)|TestCleanupConditional|TestCleanupDoesNotRemove)' -count=1
go vet ./internal/comic ./internal/tagimages ./internal/store ./internal/httpapi ./internal/maintenance ./internal/config ./internal/app
```

## 主对话审查、实际升级与前端交接（2026-10-28）

主对话审查cd7507e／241ae07，独立运行迁移保留／回滚、双role CAS与素材保护、私有原素材和缓存重启、HTTP认证／ETag／摘要及清理并发共5项定向测试，涉及store／tagimages／httpapi／maintenance全部通过。复用已有网页dist单独构建Go API，没有生产网页构建。

升级前确认活动或暂停任务、版本／系列待重验、待创建导入子任务和来源搜索请求均为0，核实标准API和Vite进程身份。仅正常停止API，保留旧程序回退件；启动241ae07构建程序，由应用创建0600一致性快照并执行018。现schema18／86表，新tag_images为空，001–017账本保持。升级前后84张旧业务表原列逐行摘要和12444个配置／秘密／素材／缓存／索引文件摘要全部相同，Vite进程保持；quick_check为ok，外键错误0。新增的唯一升级快照确认为schema17，并核对全部84张旧业务表摘要与停服前基线一致。

后续由既有“前端”沿当前提交实施图片展示／局部编辑、普通标签紧凑卡片、移除零使用入口及固定分页；使用上文真实接口及独立role版本。源码／匿名API／实际启动与数据保留已分别核对，UI截图、真实图片体验、生产网页和部署尚未验收。生产网页继续等待用户界面确认，不由本次升级隐式构建。

## 前端接续：图片客户端（2026-10-28）

从465af4f继续串行实现。新增独立TagImages类型与读取／原始字节PUT／无body DELETE客户端；写入共用现有CSRF、同源会话、错误处理和writesPending，未绕开全局提交／切库保护。两个role各带自己的X-Expected-Revision，不使用翻译或关系版本。显示地址只由tag、role、正revision构造，asset_id仅判断有无图片。

本地选择先检查10 MiB上限和PNG／JPEG／WebP／BMP实际头，不信任File.type或扩展名；保留原File上传，同时用受限文件生成正确MIME的data URL预览。没有外部上传、blob依赖、裁剪控件或新依赖，服务端仍验证完整格式、像素及存储。120项前端单元通过，新增四种头识别、空／伪装类型与不支持／超限、无素材不请求、版本URL、raw PUT与无body DELETE的CSRF／写锁／409／网络／401释放；已有JSON请求解码保持。格式、ESLint和类型检查通过；目录布局hook的DOM滚动恢复使用scrollTo以符合React检查。当前记录不代表本批UI已经全部验收，实际截图和流程结果在后续增量补齐。

## 前端图片与目录界面：实际验收（2026-10-28）

图片客户端增量为8c7d29a。此后接入原作／角色卡片和详情：背景按中心cover、透明主体contain，名称与作品数位于独立文字区；无图只有主题底色。卡片使用目录／关系已批量提供的images，不逐项GET状态；只有进入图片编辑才读全局状态并冻结两个role版本。详情中的低强调“编辑图片”在同一个主要Modal内切换局部内容，明确“全库共享”。译名及关联作品视图继续保留挂载，原文标题、Work单列横卡、完整BookEditor直达及默认display／编辑all行为保持。

两个role分别选择、显示本地预览、保存或放弃；替换／移除不猜原文件名。删除原位说明“只移除标签图片，不影响漫画”，确认前无写入。保存期间使用全局写锁且禁用所有图片编辑与关闭。SSE或409保留File和原expected revision；重新选择文件仍属同一草稿，不自动重新定基，须显式读取当前图并采用该版本后才能重试。另一role保存或更新不提升本role草稿版本。图片状态、译名及关系CAS分离。未保存文件的返回、关闭、浏览器后退与切库均走统一草稿保护，丢弃或离开后释放本地引用；没有blob临时URL需要遗留清理。

作者／社团／内容目录卡片只显示名称与Work数，12px内边距、名称最多两行，没有图片区、prefix、版本数或技术占位状态。原作／角色使用独立图片区样式。目录和关系选择器移除零使用控件，普通请求不发送include_unused；旧URL只要存在该参数即删除并回第一页，规范化完成前禁止目录读取。后端兼容接口与历史身份仍保留，已有人工关系的零本条目继续在display／all出现。

目录高度从实际shell、通知与内容内边距测量，随visual viewport变化，不使用65vh。中部卡片区域独立滚动，底部总数和分页固定；空、加载、错误及少量结果不移动底栏，手机保留安全区。不同查询／页码分别保存滚动，关闭详情恢复入口焦点。像素复核修正了主体预览百分比高度造成的裁切、底栏贴边和焦点环空间；键盘测试还发现路由切换时书库边界短暂inert阻止焦点交接，现等读取边界恢复后再进入／返回图片编辑。

匿名夹具准备脚本仅在overlay内为cmd/fixtures装配真实tagcatalog、tagimages和共用实例的maintenance服务，显式固定GOPROXY／GOSUMDB关闭；正式Go装配、SQL、OpenAPI、配置和依赖未改。浏览器使用18081／18082全新匿名实例，5173仅提供无业务Cookie的源模块。测试额外限制图片策略为同源＋data，确认本地PNG预览仍能显示，没有放宽服务器策略。

本轮最终结果：

- 120项前端单元通过。新增raw PUT／无body DELETE、CSRF／全局锁／错误释放，四种图片头、错误／空MIME、不支持和10MiB上限、精确revision显示地址、旧零使用URL规范化以及图片子状态切库清理。格式、ESLint、TypeScript、文档链接与diff／范围检查通过。
- 图片与目录匿名桌面／手机两项通过（36.0秒）：真实双role上传及顺序独立CAS、原始字节与正确MIME／CSRF、预览取消、替换、删除确认、SSE保留文件、409显式核对、冲突后重新选文件不抬版本、跨库同图／停用可读、无素材不请求及无N+1状态读取；保存锁定、读取失败重试、非法／超限文件不提交、元数据／译名／关系版本不变。覆盖旧URL、无零使用控件、普通紧凑卡片、空／加载／错误页脚、560px高视口模拟、末项可见、键盘文件控件与进入／返回焦点、明暗和长标签。
- 原作／角色既有匿名桌面／手机两项通过（1.3分钟）：默认display、confirmed零本与excluded、人工译名、全局关系与逐版本例外两类ABA、编辑分离、分页／快照、旧深链接、关闭草稿、外库／停用及陈旧响应。选择器用本库实际使用的标签验证CAS，未恢复零使用入口；零本人工关系仍独立验收。
- Work及完整详情匿名桌面／手机两项通过（56.4秒）：五类单列、真实分页／去重、原文标题、回收首选的代表替代、漫画详情直达、内部版本按需读取、旧版本深链接、草稿／滚动／焦点、库隔离和响应代次保护。

30张实际截图在绝对路径`.runtime/maintenance/tag-images-ui/screenshots/`，涵盖1280／1440桌面与390手机明暗、紧凑目录、固定底栏、双角色编辑、合成透明主体／背景及详情。按Refactoring UI的层级、分组间距和图片规则与Web Design Guidelines的语义控件、可访问名称、焦点及溢出复核；截图为源模块和真实匿名API结果，没有用示意稿替代。格式识别单元包含四种头，浏览器图片使用匿名合成PNG；没有将浏览器结果扩大为所有格式或真实设备软键盘验收。

最终日志为`.runtime/tests/results/tag-images-{unit,format,lint,type}.log`、`tag-images-ui.log`、`tag-images-relations-ui.log`、`tag-images-work-ui.log`。在web目录可先运行`node tests/prepare-tag-fixture.mjs`，再分别串行执行：

```sh
NYAMANGA_E2E_VITE=http://127.0.0.1:5173 npm run test:e2e -- tests/e2e/tag-images.spec.mjs
NYAMANGA_E2E_VITE=http://127.0.0.1:5173 npm run test:e2e -- tests/e2e/tag-management.spec.mjs
NYAMANGA_E2E_VITE=http://127.0.0.1:5173 npm run test:e2e -- tests/e2e/tag-work-cards.spec.mjs
```

本批仅web及专项说明，另为现有维护清理选项补齐“标签图片缓存”label；未操作清理。没有读写.runtime/dev、启停8080／5173、自动上传真实图片、联网取图或查询TreeC；没有修改后端、迁移、推荐输入、依赖或冻结历史。仅重建匿名fixtures工具，不构建日常后端或生产网页，不运行Go业务／模型／规模／部署套件。真实图片体验、真机键盘及生产网页待用户确认后另行验收；本地提交，不推送，完成本阶段后停止扩展。

## 2026-10-29：主体卡片与详情直接操作

用户验收100f67f后，本轮调整取代上一节的叠加卡片、图片编辑子页、逐角色保存及删除确认交互；后端契约及两个role的独立CAS保持。

目录和关联卡片只使用批量images中的foreground，不请求background或逐卡状态。原作主体为2:1、角色为2:3，均完整contain；角色目录使用较窄统一卡片。透明素材不另加灰底，缺图留空。详情仍展示两种素材，但分别放在居中的独立图框中，手机换行；不是主体与背景合成图。

点击图框选择文件；取消系统选择器不写入，选定且通过已有格式／10MiB校验后自动上传。角落信息和删除均为与选择按钮平级的独立语义按钮；删除直接提交，鼠标悬停或键盘聚焦显示，触屏常显。信息支持hover／focus／click，展示全库共享、服务端返回的尺寸／格式、推荐比例和格式／大小限制；原素材字节数未知，不伪造。只有本次选择的File才显示其真实文件名及大小。

上传失败保留旧图和File，提供重试或取消；网络与5xx重试原版本。409读取当前图供核对，只有显式“核对后重试”才采用该role当前revision。重新选文件、另一role更新、SSE及列表刷新均不默默抬高起始版本。请求继续使用原raw字节接口、CSRF及全局提交锁；保存期间禁止关闭和其他编辑。文件读取在首个await前同步登记草稿，读取代次、取消和读取边界共同防止快速换图或离开后向旧标签迟提交。打开关系选择器时保留图片操作，但暂停隐藏图片读取；返回后可重试或取消中断文件。

本轮图片匿名桌面／手机两项通过（23.0秒）：选好即保存、正确MIME／原始字节／CSRF及独立revision、503原版本重试、真实409后显式核对、快速换图只提交末次、读取期间离开丢弃不迟写、直接键盘删除、触屏删除可见、跨库共享、漫画GET结果／译名／关系版本保持。更新截图等待Ant主题控件颜色稳定后截取，避免媒体切换中的中间帧。图片头及10MiB边界继续由已有纯函数测试覆盖，不把合成PNG浏览器结果扩大为全部格式或真实设备验收。

实际截图在被忽略的`.runtime/maintenance/tag-direct-ui/screenshots/`，包括1280／1440桌面、390手机及明暗、透明主体、独立背景和普通目录。日志为`.runtime/tests/results/tag-direct-images.log`；相关完整前端单元125项通过，后续最终静态和关系浏览验收续记[标签管理](tag-management.md)。图片显示只来源于匿名合成素材，没有联网取图或操作日常资料。未改后端／API／SQL／依赖，未读写.runtime/dev、启停8080／5173或运行生产网页构建；真实图片体验由用户在5173确认。

## 2026-10-29：悬停提示与信息精简

本增量仅调整上一节的图片操作展示。信息和删除图标统一到图框右下角，默认透明无底块；桌面悬停图框或键盘聚焦后显示，信息浮层打开期间也保持可见。触屏无hover时图标常显。整个选择区域在悬停或聚焦时显示轻微主题遮罩及“点击添加／点击更换”，禁用／提交时隐藏可点击提示。进度和成功状态放到左下角，避免与右下角操作相撞。

信息浮层有图只显示接口已有尺寸与格式，如“600 × 138 · PNG”；无图只显示“点击导入JPEG、PNG、WebP、BMP 格式图片 · 最多10 MiB”。移除全库共享、推荐比例及本次文件信息文案，但素材的全局作用域、独立role CAS、校验、失败File及重试均保持。信息／删除仍与选择按钮平级，不会冒泡打开文件选择器；信息支持hover／focus／click，Esc只关闭当前信息浮层。

复用既有匿名图片流程验证交互；延迟FileReader用例改为受控释放，明确在离开并卸载后才释放旧读取，不依赖固定毫秒等待。仅更改前端及本专项，没有修改API、依赖或任何保存规则，未读取日常实例、操作真实素材、联网、重启8080／5173或构建生产网页。

最终4项图片单元、2项匿名桌面／手机流程（38.1秒）、格式、ESLint、TypeScript、291条文档本地链接及diff检查通过。更新的流程覆盖默认隐藏与透明底色、右下角定位、悬停／聚焦添加与更换、浮层外移保持图标、Esc只关浮层、触屏常显、有图／无图单条文案、图标不打开选图器、禁用时无操作提示；原有即时保存、503／409、取消读取与独立CAS检查仍通过。未重跑无关组织／推荐／部署套件。

40张实际匿名截图在`.runtime/maintenance/tag-image-hover-ui/screenshots/`，包括1280／1440桌面及390手机、明暗、有图／无图和打开的信息浮层；浮层截图等待入场opacity=1后拍摄，未用透明过渡帧代替最终效果。沿用Refactoring UI及既有Web Design Guidelines检查要点，复核主题、层级、按钮独立语义、焦点和换行。日志为`.runtime/tests/results/tag-image-hover-{unit,ui,format,lint,type}.log`，最终源码定向ESLint另见`tag-image-hover-lint-final.log`。现有ESLint配置排除浏览器测试脚本，测试脚本由Prettier和上述Playwright实际执行验证，不计作已通过ESLint。真实素材与设备体验继续由用户在5173确认，本地提交不推送。

## 2026-10-30：当前工作区复核

本次请求的界面、匿名测试与记录已由5acf008完整提交，当前分支保留该实现，因此没有重复修改图片组件。保留工作区已有的设置功能改动，仅补充本次复核证据。

4项图片单元和2项匿名桌面／手机流程全部通过（38.6秒）；图片相关文件Prettier、组件ESLint、完整前端TypeScript、299条文档本地链接和diff检查通过。当前源模块重新验证右下角透明图标、桌面隐藏／悬停／聚焦与触屏常显、添加／更换遮罩、两种精简信息、图标不触发选图及即时保存／503／409保护。截图复用上述匿名目录并重新生成，复核桌面明暗和手机空图浮层；日志为`.runtime/tests/results/tag-image-hover-recheck-{unit,ui,format,type}.log`，组件ESLint直接执行通过。没有操作日常实例、用户浏览器或真实素材，没有重启8080／5173、修改接口或依赖、运行生产构建或推送。

## 2026-10-31：当前图片方案再次验收

用户再次明确本节悬停与信息精简方案。核对当前分支，5acf008已包含全部组件、样式和匿名测试，因此本次没有重复修改图片源码，也没有提交工作区已有的本地画廊高亮改动，仅补充独立复验记录。

4项图片单元、2项匿名桌面／手机流程（39.3秒）、图片相关Prettier、组件ESLint、完整前端TypeScript及文档本地链接检查通过。流程重新覆盖右下角透明图标、桌面闲置隐藏／悬停／聚焦显示、浮层打开保持可见、触屏常显、添加／更换提示、提交禁用提示、两种精简文案、图标不触发选图，以及即时保存、503原版本重试与409显式核对。40张截图在上述`tag-image-hover-ui/screenshots`目录重新生成，实际复核桌面浅色闲置、深色悬停与手机深色空图信息浮层。Web Design Guidelines沿用既有检查要点，本次未联网拉取规则。

日志为`.runtime/tests/results/tag-image-hover-20261031-{unit,ui,format,lint,type}.log`。浏览器只使用18081／18082新建匿名实例，5173仅提供无业务Cookie的源模块，未操作用户浏览器、真实素材或日常实例；没有启停8080／5173、改动API／依赖或构建生产网页。本次交付仅此专项记录，本地提交、不推送。
