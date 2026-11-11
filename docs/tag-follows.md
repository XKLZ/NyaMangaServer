# 标签关注：后端契约与交接

## 身份、所有权与迁移

关注属于当前认证account＋既有tag，跨书库共享，五类目录及未知namespace均支持。020只新增tag_follows、tag_follow_epochs，给events追加可空owner_account_id及索引；001–019原字节、校验和与原账本保留。已有事件保持ID、type、resource_id、revision、created_at，owner为NULL，仍为全局事件。既有账号模型和单账号约束不变，不新增凭据字段或多账号管理。

初始未关注为false／revision0；第一次关注升1、取消升2、再次关注升3，取消保留行作为墓碑。只有实际状态改变才更新本账号epoch并写事件，同目标且携带当前准确revision为200无变化；旧revision即使目标相同也409。不存在tag返回404，不根据完整词库词条自动INSERT tags。没有漫画引用、漫画移入回收站或库停用不会删除全局tag的个人关注。tag_follows的tag外键阻止在仍有关注墓碑时误删身份；本批不增加标签清除或修复业务。

写状态、单调epoch及私有事件在同一事务，失败一起回滚；int64上限不溢出，不通过sum(revision)构造快照。关注不修改标签身份、人工译名、图片、关系、Edition收藏、来源／有效tags、metadata／组织编辑CAS或推荐输入。读取关注使用服务端专用context，来源资料、导入冻结与历史元数据不附加当前账号状态。

## 写接口

路径均加`/api/v1`，要求现有登录cookie；PUT／DELETE要求Origin与X-CSRF-Token。路径`/tags/{id}/follow`，PUT关注、DELETE取消；仅接受JSON：

```json
{"expected_revision":0}
```

响应200：

```json
{"tag_id":"anonymous-local-tag-id","followed":true,"revision":1}
```

取消时使用最近读取的follow_revision（上例为1）：

```json
{"expected_revision":1}
```

响应为followed=false、revision=2。请求不接受account_id等未知字段或任何query参数；身份只从认证session取得并传入领域服务和Repository。整数必填且非null，范围0至9223372036854775806。maxint、负数、分数、溢出和非法JSON均400。已有记录或账号epoch耗尽时不能变更，返回409；不覆盖或回绕世代。

| HTTP | 稳定错误与处理 |
| --- | --- |
| 400 | invalid_tag_follow：版本／路径或查询参数非法；invalid_request：缺字段、未知字段或JSON解码失败 |
| 401／403 | 沿用unauthenticated、Origin／CSRF校验；不接受客户端选择账号 |
| 404 | not_found：不存在业务tag，不能关注仅词库候选 |
| 409 | tag_follow_revision_conflict：保持界面旧状态，明确重新读取和核对，不自动提升草稿CAS |
| 413／415 | request_too_large／unsupported_media_type：沿用1MiB与application/json限制 |
| 405 | method_not_allowed：只允许PUT、DELETE |
| 500 | internal_error：存储失败，重新读取持久结果，不假定成功 |

## 目录、详情、关系与建议

GET `/tag-catalog`、`/tag-catalog/{id}`及`/{id}/relations`内每个CatalogTag新增必有`followed:boolean`和`follow_revision:int64`。关系两端均补齐，目录最多100项、关系最多200个端点用单次有界批量查询补齐，避免每卡查询。images、display／all关系、名称／译名与原关系CAS保留。

仅目录接受`followed_only=true|false`，默认false；必须明确library_id。默认仍只当前库实际used标签；原include_unused兼容接口保留。true为个人关注集合，允许当前库零Work（包括只在其他库使用或所有漫画均已回收）的tag。category、namespace、q、sort、order均由SQL在计数和LIMIT之前执行，total真实；name／usage与稳定tie-breaker沿原规则。库停用仍可明确查看，回收或不存在库沿原404；关联漫画查询仍受明确库范围和回收语义约束，关注不会放宽范围。

```http
GET /api/v1/tag-catalog?library_id=anonymous-library&category=artist&followed_only=true&page_size=50
```

匿名响应示意（snapshot为不可解释的实际返回串）：

```json
{"items":[{"id":"anonymous-local-tag-id","namespace":"artist","name":"anonymous","translation":"anonymous","translation_origin":"original","translation_revision":0,"category":"artist","placeholder":false,"edition_count":0,"work_count":0,"followed":true,"follow_revision":1}],"total":1,"page":1,"page_size":50,"snapshot":"<opaque-snapshot>"}
```

followed_only出现在detail／works／editions／relations时，即使值为false也400 invalid_tag_catalog，不静默忽略。目录与建议拒绝account_id注入。普通与关注列表快照独立，绑定当前account和本账号epoch；本人关注变化或off/on的ABA使旧页409，别人的关注变化不使本账号旧页失效。全局catalog／metadata／relation／translation revision不因关注推进，其他原词库、译名、图片与目录变更的失效条件保持。

POST `/tag-suggestions`沿用JSON查询／排除／分页契约。新增followed与follow_revision，排序为已关注本地tag优先，然后原usage降序、local优先、normalized_name／namespace／name稳定键；归属按实际tagID关联，不根据译名或名称猜测。排除selected项（exclude）、q、namespace、count、LIMIT全在SQL，usage_count不被关注修改。仅词库候选tag_id=null、local_exists=false、followed=false、follow_revision=0，不能直接关注。译库关闭仍可用本地原文、人工译名及关注优先，dictionary_state=disabled、dictionary_match=null保持三态。旧GET `/tags`的排序和来源字段不增加个人状态、不改变原值。

## SSE私有范围和游标

关注状态变化事件与写入同事务，type为tag_follow、resource_id为tagID、revision为该关注revision。SSE仍使用event: change：

```text
id: 123
event: change
data: {"event_id":123,"type":"tag_follow","resource_id":"anonymous-local-tag-id","revision":1}

```

后端读取按`owner_account_id IS NULL OR owner_account_id=当前session账号`在SQL LIMIT前过滤；其他账号的事件不会进入序列化或发送层。owner不在model.Event或响应中，不采用浏览器过滤。旧全局事件所有账号可见；内部无账号的旧Events读取只返回全局事件，不能误泄私有事件。

24小时／10000条retention触发器保持。断档由同一只读事务内的整个保留日志low／high判断，不能因其他owner被过滤产生假gap。可见批次达到200条时游标在最后可见ID继续；少于200条（包括仅有别人私有事件的空批）安全推进内部游标至事务high，之后新全局事件仍能读到。初次无Last-Event-ID或真实断档仍resync_required，重连Last-Event-ID、心跳、周期会话撤销检查和关停沿原机制。数字事件ID允许间隔，不能推断缺号即丢失本账号通知。

SSE只通知重读；前端应刷新相关摘要或使旧页过期，不把事件revision用作其它tag的CAS，也不静默提升未提交草稿版本。当前HTTP流认证固定来自session，拒绝account_id query。

## 前端交接（后端交付时点）

五类卡片关注操作为独立semantic button；现有整卡Link需先拆交互边界，不能在Link内嵌button。已关注常显，未关注支持hover／focus及touch可点，点击不误开详情。详情、目录和关系摘要使用同一followed／follow_revision，不确认、busy防重复、失败保留原状态，409明确重读。工具栏全部／已关注写URL并回page1；保留分类／搜索／排序／当前库、固定页脚、原作2:1角色2:3、独立主体图片、无循环详情轨迹、原题优先及草稿／SSE冻结。不新增泛化零使用控件，不做通知订阅或自动推荐。

## 验证范围

仅匿名数据库、合成素材、固定词库及认证HTTP夹具，不扫描真实书库、不运行模型、不下载。两账号隔离夹具仅在测试连接暂时关闭CHECK以插入第二匿名账号，随后立即恢复；生产账号模型、SQL迁移和账户管理均未放宽。迁移逐项比较所有旧表的原有列（events原五列），另验旧owner全部NULL、001–019账本、匿名素材文件、设置、暂停任务和人工决定；覆盖失败事务回滚、FK和重复打开。

本批源码提交为24f0514（020／存储／查询）及73b596c（HTTP／SSE／OpenAPI）。实际验证通过：

- `go test -race ./internal/store ./internal/httpapi ./internal/metadata ./internal/tagcatalog -run '^(TestTagFollow|TestTagCatalog|TestTagImages|TestDictionary|TestRepeatedTranslation|TestSSEReplay|TestMetadataHTTP)' -count=1`：四包通过，覆盖本批与旧图片／关系／metadata来源／词库／认证SSE回归。
- 关注专项最终增量后再次race：store的五项关注／迁移测试和HTTP关注／两账号事件测试通过；最后HTTP身份注入检查及认证写入断言再次race通过。
- 新增`TestTagFollowPrivateEventRetention`与旧`TestEventsAndLookupRetention`一起race通过：真实插入10001个私有事件，检查只保留10000、真实gap、正常过滤无gap；时间移至24小时前，再写入全局事件只保留该新事件。
- 定向store迁移回归通过：MigrationAndConnectionPolicy、UpgradeSnapshotAndRollback、名称含Upgrade／Migration的既有测试及增强设置旧表保留。最新版本断言19→20，events保留比较明确限定原五列，其他旧列检查不减弱；020专项另验全部旧owner为NULL。设置、暂停任务、匿名人工决定和资产字节均保留。
- 受影响Go包及app／cmd/server的vet、依赖边界、Redocly OpenAPI、313条文档本地链接、gofmt、diff与变更范围检查通过。所有Go命令离线使用已有工具缓存；001–019、web、go.mod／go.sum、推荐运行代码／实验模块与9acf5c6基线无差异。


## 主对话审查与前端接入（2026-10-30）

主对话核对020、账号上下文、目录SQL筛选／快照、批量关注摘要、CAS和SSE所有者过滤；未发现需要阻止前端接入的问题。独立运行`go test ./internal/store ./internal/httpapi ./internal/metadata ./internal/tagcatalog -run '^TestTagFollow' -count=1`，store和HTTP通过；metadata／tagcatalog无此名称的测试，不能据此扩大覆盖结论。001–019与9acf5c6逐字节保持，web、依赖及推荐模块未改变；313条本地文档链接及diff检查通过。后端既有race／迁移证据见上节，不重复无关套件。

## 批次1前端：星标与个人目录（2026-10-30）

从677f862串行接续，源码增量cf18383，未重做批次0设置。五类目录、原作／角色关联卡片与详情标题使用同一关注控件；卡片外层改为普通容器，内部链接与星标按钮平级，点击星标不会打开详情。32px低强调星标使用现有Ant图标／主题：已关注常显，未关注桌面hover或键盘focus显示，触屏常显；正常Tooltip仅“关注／取消关注”，aria-label保留完整标签对象，没有恢复整卡名称悬浮文本。原有2:1／2:3独立主体图、紧凑文字及固定页脚保持。

目录增加全部／已关注，followed_only=true写URL，变更回第一页并保留分类、搜索、排序和书库。通用catalogInput不增加该字段，独立tagDirectoryInput仅供目录；路径生成再限制只有根目录可发送该参数，其他端点即使收到false也剥离。已关注集合由服务端跨库过滤、计数和分页，零作品如实显示，不恢复include_unused控件。联想仅补类型并在关注事件后作废读取快照，继续使用服务端顺序，不自行重排频次或给tag_id=null候选创建身份。

PUT／DELETE沿用原JSON请求、CSRF／Origin和全局提交锁，只发送冻结expected_revision。相同标签的按钮共享请求状态和已确认结果，不乐观改星、不重复提交；关注与取消保留墓碑版本。缺少follow字段或不安全revision时禁用写入，不猜false／0为已知状态；响应缺失、目标不符或版本未推进也不认定成功。失败保留原状态并显示重读入口，409不自动抬版本或重试；显式读取失败继续保留，成功读取后才可再次操作。普通后台摘要即使返回新版本，也不覆盖失败操作的冻结核对状态。

账号边界沿现有session卸载，工作台按书库重建并取消旧读取；关注本身仍为账号跨库共享。SSE tag_follow只触发相关摘要重新读取，不直接采用事件revision，目录和关系分页重建快照，作品／内页不因关注额外读取。译名与图片原组件继续保留挂载和自己的CAS／File。关注页取消后总数更新、空末页回退；提交前记录触发位置，刷新移除卡片后恢复相邻星标或范围控件焦点。详情不因条目从关注列表消失而关闭或丢弃草稿。

### 匿名验证与交接边界

本批先用web现有overlay重建独立fixtures程序，使用已存在网页dist和固定离线runner，匿名实例为schema20；没有修改正式Go或生产网页，也没有构建／启动日常API。关注浏览器流程全部通过18081／18082合成资料执行，5173只提供无日常Cookie的源模块。

- 21项标签定向单元通过：tag-follows、tag-catalog、tag-trail、tag-images。新增覆盖目录参数隔离（含false）、筛选回首页且保留其他条件、缺字段／空身份／不安全版本、PUT／DELETE墓碑CAS、严格响应确认及409不改原值；既有轨迹、草稿和图片客户端校验继续通过。没有重跑已完成的130项设置单元或后端模型套件。
- 关注桌面／手机流程覆盖五类卡片独立星标、无误开详情、未返回不改星与提交锁、正确CSRF与单字段body、个人集合51→50的末页回退／键盘焦点、跨库零作品、真实私有SSE回读、失败／读取失败／409显式核对、目录与关联／详情同步、取消关注不丢详情译名草稿、图片失败File保持、快速SPA切库隔离迟到响应，以及缺服务字段禁用写入。匿名截图位于`.runtime/tests/tag-follows-ui/screenshots/`，覆盖1280／1440桌面及390手机明暗、长标签、低强调星标和固定页脚。
- 卡片链接拆分后，既有图片匿名桌面／手机2项再次通过（38.7秒）：即时保存／取消选图、原始字节与CSRF、直接删除、503原版本重试、409显式核对及读取取消；不把合成PNG结果扩大为所有格式或真实设备验收。相关最终日志为`.runtime/tests/results/tag-follows-{unit,format,lint,type,images-ui}.log`，星标提示最终定向ESLint为`tag-follows-lint-final.log`。关注最终流程结果另记`tag-follows-ui-final.log`，早期调试日志不作为最终通过证据。

按Refactoring UI与已有Web Design Guidelines规则复核真实匿名截图，保留已验收密度、图片比例、语义按钮和焦点。未再次联网获取规范或资源，未修改依赖／算法／接口／迁移／配置。现有测试脚本不在ESLint范围，其格式和Playwright实际执行单独验收；不把截图、匿名测试或后端历史结果当作用户本批界面验收。

最终短Tooltip源码的关注桌面／手机2项通过（48.7秒），24张截图已重新生成；实际抽查1440浅色和390深色关注目录，提示仅“取消关注”，完整长标签名留在无障碍名称中，不再形成多行遮挡。相关单元、前端格式、ESLint、TypeScript及最终文档／diff检查通过，最后仅改提示文案的组件另做定向ESLint。批次0设置证据按未变源码复用，没有重跑其整套流程。

最终文档检查314条本地链接通过，日志为`.runtime/tests/results/tag-follows-docs.log`；14个变更web文件（含测试）的最终SHA256清单为`tag-follows-source.sha256`，与最终浏览器验证后的源码相同。可在web目录复现相关命令；两个spec应分别串行执行，各自创建全新匿名实例：

```sh
node tests/prepare-tag-fixture.mjs
node --test tests/unit/tag-*.test.mjs
npm run format:check
npm run lint
npm run typecheck
NYAMANGA_E2E_VITE=http://127.0.0.1:5173 npm run test:e2e -- tests/e2e/tag-follows.spec.mjs
NYAMANGA_E2E_VITE=http://127.0.0.1:5173 npm run test:e2e -- tests/e2e/tag-images.spec.mjs
```

审查前端cf18383／7d6ce13：卡片链接与关注按钮平级、正常提示仅动作文字、目录筛选未泄入详情／关系请求；CAS与原图／译名草稿互不覆盖。最终14个web源码及测试文件摘要与匿名验证清单一致。独立抽查1440浅色桌面和390深色手机截图，短提示、卡片密度及固定页脚符合本批范围；没有把匿名截图当作用户真实界面验收。前后端工作区均干净并停止写仓库。

2026-10-30曾尝试正常停API，但自动审批因账号额度无法完成，命令未执行、实例未变；2026-10-31恢复后重新核实身份、任务及数据，未沿用旧的运行前快照。升级前queued／running／paused任务、两类推荐待重验、待物化子任务、来源查询和增强切换均为0。正常停止API，私有保留旧程序，以7d6ce13离线构建的程序原子替换，再由应用启动自动创建一致性快照并执行020；未直接复制活跃WAL数据库。

实际核对结果：

- foundation-v1、schema19／90表升级为schema20／92表；001–019账本逐项相同，仅追加020。
- 89张旧业务表的原有列逐表数量与逻辑摘要相同，包含账号、会话、设置、Edition／File／Work／Series、来源、人工值、任务、历史决定及素材引用。events原五列保持，新owner全部NULL。
- 12454个配置、密钥、长期素材、缓存及索引文件内容摘要相同；未读取源漫画。Vite5173管理PID相同。
- 新增恰好一个schema19一致性升级快照，权限0600，逐表摘要与停服基线一致；升级后quick_check为ok、foreign_key_check无结果。新tag_follows／tag_follow_epochs均为空，未自动产生关注私有事件。
- API8080与5173代理的live／ready和初始化状态正常；未登录访问设置、目录和关注写入均401，没有使用真实凭据或发出真实关注。该检查仅证明启动和认证边界，业务验收仍以先前匿名测试为据。

私有审计、逐表／文件摘要、快照引用及旧程序保存在.runtime/maintenance/tag-follows-20261031/（目录0700、审计0600、旧程序0700），不进入Git。API日志仍在.runtime/dev/api.log，未输出原始凭据、标题或私有文件定位。升级中没有启动扫描、获取、推荐、下载、清理或其它维护。

使用已有账号访问http://127.0.0.1:5173/admin/tags，在五类卡片或详情点星标；“已关注”显示个人跨书库集合，数量始终是当前书库的作品数。取消不删除标签或资料。遇到冲突先明确重读关注状态，再决定是否操作；不会自动重试覆盖。标准命令为make dev-api、make dev-web、make status-dev，正常停止两个服务为make stop-dev；当前两服务已运行，无须重新初始化。

本次只额外编译API，沿用此前已存在的嵌入网页；当前新界面由5173提供。未执行生产网页构建、真实关注业务验收、模型／历史评测／规模／部署／NAS验证。界面确认后再统一生产构建一次，完成本批即停，不自动推进本地画廊高亮、健康修复或备份恢复。
