# 标签管理后端契约

## 目录与统计

新增 tag-catalog，旧 tags／tag-suggestions 不变。category 为 parody／character／artist／group／content，content 包含其余所有命名空间（包括 language 和未知值）。缺省只列当前库非回收 Edition 实际使用标签；包含个人隐藏、缺失／损坏与明确停用库，拒绝回收库。译名优先人工、完整活动译库、旧远端投影、原文；沿用旧翻译写接口及 revision。

所有列表 SQL 筛选计数分页，Edition 计数独立于关系 join，Work 按真实 work_id 去重。snapshot 包含库、查询条件、目录、译名与关系世代，翻页失效返回409，不能通知到达后静默替换提交 revision。

## 关系规则

- 排除 original／origin 占位后，只有一个原作的 Edition 为其角色产生可重建证据；原始占位标签仍可浏览。
- 自动种子只来自当前启用、非回收库的非回收单原作 Edition；逐本否决使该书此对证据不作为自动种子，不排除其他书或改变原始标签。
- 全局人工 confirmed／excluded／auto 保留 revision 墓碑；auto 恢复不删除世代。全局排除优先。无共现也可合法确认，管理数量可为0，不增删 Edition 标签。
- 多原作 Edition 中角色在本书原作内仅一个可靠归属时才自动分配；多个可靠候选保留竞争状态，不按ID挑选。已可靠分配后，若仅剩一个原作与一个未知角色，且没有书外可靠归属、排除或逐本否决，才进行本书唯一剩余推断。
- 单原作证据与人工确认是仅有种子；任何多原作分配与唯一剩余推断不写回种子、不跨书连锁。角色允许多个原作，竞争时不强制一对一。逐本否决只约束自动分配，全局人工确认不被局部自动否决改成全局排除。
- 未归类包括竞争、矛盾、无可靠归属及被阻止的自动推断。关系分区可能重叠，分区之和不是唯一总数。

017 将原始证据、全局决定和逐本否决分表。有效标签变动在同事务维护当前 Edition 证据，涵盖来源采纳／共享更新、人工覆盖及解绑；回收／停用通过资格过滤即时失效，恢复重新可见，永久删除级联清理证据与该书否决。初次投影每批500与检查点同事务，失败停止启动，重启续做，未 ready 不服务。无源文件访问或联网。

后端接口与匿名验证见下文；不据此声明前端或真实升级已验收。

## HTTP 与前端交接

全部路径相对 `/api/v1`，沿用登录 Cookie、写 Origin／CSRF；所有读取强制 `library_id`，没有隐式全库。普通目录和关联漫画不读取归档、封面、联网或模型。仅新增接口，不改 `/tags`、`/tag-suggestions` 或旧搜索。

| 接口 | 用途 |
|---|---|
| GET /tag-catalog | category、namespace、q、sort、order、page、page_size、snapshot，五类目录 |
| GET /tag-catalog/{id} | 当前库统计、译名来源与人工翻译 revision、停用标记、未归类数 |
| GET /tag-catalog/{id}/editions | 默认物理 Edition 列表；返回真实 work_id、编辑版本及批量关系摘要 |
| GET /tag-catalog/{id}/relations | 原作→角色或角色→原作；分页返回双方标签摘要、全局决定及 revision、当前库各类计数 |
| PUT /parody-character-relations/{parody}/{character} | `{"expected_revision":0,"decision":"confirmed"}` 或 excluded，响应 scope=global |
| DELETE 同上 | `{"expected_revision":1}`，恢复 auto，返回新的 revision，不删除世代 |
| PUT /editions/{id}/character-suppressions/{parody}/{character} | `{"expected_revision":0}`，只否决此 Edition 的自动关系 |
| DELETE 同上 | `{"expected_revision":1}`，恢复本书自动，仍保留新 revision |

目录 category 缺省不限制；content 包含所有非 parody／character／artist／group 命名空间，不把 language 丢弃。`include_unused=true` 可管理已有标签身份的零使用条目（包括其他库使用的标签和人工关系）；不枚举没有身份的完整译库词条。原文与当前有效译文经 v1 Unicode 规范化匹配。名称排序使用译文显示名的自然排序键，usage 使用 Edition 数；同值按 namespace／name／id 稳定排序。

列表 page 缺省1、page_size缺省50且最多100；page>1必须带同一接口／同一条件返回的 snapshot。snapshot 不能跨目录、详情、关系或 Edition 查询复用；条件、库、目录、关系世代或译名版本变化返回409。关联 Edition 只支持 sort=name，q 匹配标题；关系支持名称／使用数排序和双方原文／译文搜索。

`/editions` 的 `partition`：

- all：含当前有效标签的非回收 Edition；带 related_tag_id 时按双方共现过滤，不声称已建立关系。
- related：已可靠归属或本书唯一剩余推断的 Edition。
- evidence：原始单原作证据明细，包含当前被全局排除或逐本否决的证据，不能据此当作已归属。
- inferred：仅本书唯一剩余推断。
- suppressed：本书仍有效的人工自动否决记录；标签已变化时仍能分页找回，并可 DELETE 恢复。
- competing：本书有多个可靠原作候选的 Edition。
- unclassified：标签对应的 Edition 中，仍有未可靠归属角色或没有该标签的有效关系。与部分已归属分区可能重叠，不能用关系行计数之和代替标签总数。

除 all／unclassified 外要求 `related_tag_id`，必须为反向命名空间。每项 `relations` 批量提供原作／角色 ID、名称、译名、state／state_label、decision／decision_revision、suppressed／suppression_revision。未知或仅有书外可靠归属的角色返回空 parody_id，表示没有可提交的原作目标，不能由前端猜一个。存在的候选／否决目标返回完整双方身份。

关系计数分别是原始 evidence_edition_count、实际可用 seed_edition_count、已归属 edition_count／去重 work_count、唯一剩余 inferred_edition_count、保留的 suppressed_edition_count 和 competing_edition_count。人工确认可在所有当前库计数均为0时显示。角色可有多个原作；某一书同时出现多个可靠候选时，该书仍为竞争状态，不因全局确认而任选一个。停用库的原始证据可浏览，但不充当自动种子；逐本写入沿用停用／回收／维护阻塞保护，全局人工决定不伪装成当前库操作。

人工翻译继续使用 `/tags/{id}/translations/zh-CN`；取 `translation_revision` 提交，不把关系 revision 或目录 snapshot 当作翻译 CAS。PUT 全局关系必须明确 confirmed／excluded，恢复自动用 DELETE；所有写入明确携带 expected_revision，缺失或 null 拒绝，旧版本返回409，不以重复 DELETE 冒充成功。SSE 新事件为 tag_relations／global，仅触发查询刷新，不允许更新草稿的冻结版本。

稳定错误：400 invalid_tag_catalog；404 not_found；409 tag_catalog_snapshot_expired、tag_relation_revision_conflict、library_disabled、resource_trashed、resource_busy；503 tag_projection_unavailable。权限沿用401未登录、403 Origin／CSRF。状态机器码与中文标签包括 single_parody（单原作证据）、evidence（本库可靠证据）、inferred（本书唯一剩余推断）、confirmed（人工确认）、excluded（全局排除）、suppressed（本书已否决自动推断）、competing_parodies（原作归属竞争）、known_elsewhere（可靠归属与本书矛盾）、unclassified（未归类）。

## 匿名响应摘录

以下摘录对应 `TestTagCatalogHTTPContractPermissionsAndCAS` 的匿名HTTP响应，省略 snapshot、请求ID和其他必填摘要字段，完整结构以 OpenAPI 为准；不是日常实例查询。

```json
{"items":[{"namespace":"parody","name":"alpha","translation":"alpha","translation_origin":"original","edition_count":1,"work_count":1}],"total":2,"page":1,"page_size":1}
```

```json
{"decision":"confirmed","revision":1,"scope":"global"}
```

恢复自动返回 decision=auto、revision=2；再以 expected_revision=0 提交得到：

```json
{"error":{"code":"tag_relation_revision_conflict","message":"标签关系已变化，请保留草稿并重新核对版本"}}
```

否决后 `/editions?partition=suppressed&related_tag_id=…` 返回 total=1，行内包含双方ID、suppression_revision=1、suppressed=true。恢复后 revision=2，旧0再次提交返回同一409。目录换页快照过期返回 tag_catalog_snapshot_expired；未完成回填返回 tag_projection_unavailable，而不是空列表。

## 实施与验证记录

- 契约基线：6320459；存储／服务：a3ad935。017新增四表，schema17共85表，001–016原字节和校验和保持。现有旧测试的“最新schema”字面断言同步17，未改推荐算法或执行推荐／模型套件。
- 定向匿名存储、服务、HTTP／OpenAPI测试已通过。覆盖五类／未知 namespace、同 Work 多版本去重、缺失／停用／回收、译文优先与词库零使用隔离、分页／翻译与关系快照、单原作与占位、多原作唯一剩余、跨库种子隔离、竞争、明确矛盾、不连锁、排除优先、无共现人工关系、逐本否决／标签变化后恢复、ABA和并发CAS。
- 真实事务链路覆盖人工标签、共享来源刷新、解绑、回收／恢复与永久清除；证据触发器失败使元数据／revision一并回滚。全局关系不改目录输入世代，既有任务／推荐不因关系决定被重置或触发。
- 匿名schema16→17验证迁移失败整体回滚；501个额外Edition跨越500批次，第二批失败阻止Open返回，重启按checkpoint续做。对原有80张非账本表逐表比较，含认证、来源、人工值及暂停任务，升级及回填后保持；外键校验、重复启动和一致性私有快照通过。
- 最终15项定向测试通过（新增标签测试10项及相关既有迁移／元数据回归5项），完整新HTTP读取／写入响应经OpenAPI验证，含旧翻译接口。定向race与Go vet通过，依赖检查覆盖新tagcatalog服务；280条本地链接、OpenAPI及diff检查通过。现有OpenAPI对象、001–016 SQL、web、正式依赖、Python运行代码和冻结实验均与821a5e9逐项核对未改。未重建现有嵌入网页资源。
- 未读写 `.runtime/dev`、未启停8080／5173；未运行生产网页构建、UI、真实升级、模型、规模或部署套件。真实升级、前端七项导航与交互仍由主对话串行安排，不能沿用本次后端测试宣称UI或真实数据已经验收。

## 主对话升级审查

先审查新接口的分区与全局影响文案，确认无活动、暂停或待执行工作，再由主对话正常停止API、保留Vite、使用应用数据锁和私有一致性快照升级。升级先提交schema17，再分批回填；回填异常时不能用旧schema16程序打开该库，优先修复后重启续做，回退必须由对应快照恢复。全部资料与人工值保留，不自动确认关系或修改真实标签。

当前规则为确定性 SQL 查询，没有新增模型依赖或全库逐对推断表。大规模真实库的读延迟、备份／导出恢复、前端与Linux部署本轮未验收；全局决定可能改变多个库的派生分区，单库数量不代表全库影响数量。未来源／数据语义变化需更新规则版本并设计新的回填，不能静默改017。

## 主对话审查、实际升级与前端接续

2026-10-27，后端 cf47430 交付且工作区干净。主对话审查017、事务回填、明确书库过滤、双向关系及写入CAS，并再次执行受影响store／tagcatalog／HTTP的TestTagCatalog定向测试，全部通过；未重跑推荐、规模或部署套件。

对原80张非账本表逐表核对旧列逻辑摘要、001–016迁移账本，以及12443个配置／秘密／素材／缓存／索引文件摘要，全部保持。新增全局决定与逐本否决均为零；quick_check和外键检查通过。快照权限为私有，Vite PID保持。新旧二进制与摘要、升级前后核对留在被忽略的`.runtime/maintenance/tag-management/`，不向Git写入私人文件名或数据。

开发启动管理器10秒等待先返回超时，但原API进程仍在执行启动，没有强停或重复启动；随后同一进程完成并提供HTTP。8080及5173代理的ready／auth status均为200，新目录未登录请求均为JSON401。仅验证就绪和认证边界，不将其冒充真实登录后的目录浏览、写入或UI验收。

接续交给既有“前端”：七项导航新增标签；原作与角色／作者与社团／内容标签三大类五小类；当前库统计、关联Edition、双向关系、未归类、全局确认／排除／恢复及逐本否决／恢复、人工译名和草稿保护均须接入真实API。特别注意分页返回字段为snapshot，不能使用其他查询的snapshot；全局决定revision、逐本suppression_revision和翻译translation_revision分别冻结，不互相替代。前端只用匿名实例验证，不自动操作真实资料，不重启服务或修改后端。

本记录只确认后端审查及实际升级；标签前端、真实库读取延迟和本提交的生产网页／Linux部署仍未验收。用户确认本批界面后再集中构建生产网页。

## 前端接续：目录与导航（第一增量）

2026-10-27，基于d9f5c3b串行接续；后端、升级及其历史证据不重写。本增量仅web与交互说明，无Go／SQL／OpenAPI／依赖／服务启停或真实资料操作。继续React／Ant Design／原生CSS、既有主题和8／16／24px分组，以标签身份和当前库去重计数为主，不增加一排统计卡。

七项导航新增“标签”，位于版本之后、维护分隔线之前。桌面顶部三大类及相应子切换，手机用五类紧凑选择；内容标签可筛选language和任意合法未知namespace。默认只当前库已使用，零使用身份需明确勾选，不调用完整词库联想伪造身份。列表搜索／排序／页码、当前标签和关联漫画页码写入URL。标签页始终单库，tag不作为书库归属依据；切库清除tag、关系、分区、Edition详情和各自快照。

目录、关系与关联Edition的分页各持独立snapshot；后续页只复用同一接口／同一条件快照。深链接到后续页时先读取该接口第一页取得快照，过期保留错误并要求显式重新载入，不拿详情snapshot翻页。所有读取有中断和身份隔离，未确认库时不挂载业务目录。

本批已接目录、统计、只读详情及Edition列表；人工译名／关系编辑在下一增量接入。110项前端单元、格式、ESLint和TypeScript通过（新增4项分类／URL／范围／真实目标校验）。本阶段浏览器验收尚未执行，不能将已完成后端或源码检查当作UI通过。

## 前端接续：译名、双向关系与逐本否决（第二增量）

2026-10-28，接续ecfc058；目录保持三大类／五小类，原作角色宽屏目录与详情两栏，窄屏全宽详情；普通标签用详情抽屉。响应式切换使用同一个已挂载编辑器，不丢译名草稿。关联漫画按25项分页，在工作台内打开既有漫画详情，先校验Edition属于当前库，不以全局标签身份自动跳库。打开／关闭漫画详情保留标签草稿，漫画自身未保存编辑仍受原保护。

接入全局人工译名及恢复继承、双向关系与全部分区、人工确认／排除／恢复自动、逐本否决／恢复。新增关系从反向真实目录选择，支持双语查询、分页和显式零使用；先取得该对关系的当前版本，再打开实际影响确认。占位标签不允许写关系；没有原作ID的未归类行不能猜目标；全局确认不被新建的局部否决覆盖。

译名translation_revision、人工关系revision、逐本suppression_revision分别冻结。SSE和手动刷新只更新读模型，不提升草稿版本；失败保留操作，冲突后必须读取、核对并显式采用当前版本。逐本核对只按当前标题与该对标签查找，找不到时保留错误，不遍历整库或猜0。关系写成功后在该列表刷新完成前禁止基于旧行连续修改。全局影响说明不将当前库的0数量宣称为全库无影响。

112项前端单元、格式、ESLint、TypeScript通过；定向匿名桌面／手机核心链路两项通过（52.4秒），包含双向跳转、两类独立分页、无共现零使用目标、全局ABA冲突显式核对、逐本恢复、SSE译名草稿、窄屏切换及漫画窗关闭保留草稿。新的边界验收与截图复核另记于后续增量。

匿名夹具原启动器尚未装配tagcatalog，直接重编译会返回404。本次仅在被忽略的`.runtime/tests/tag-fixture-adapter/`生成Go构建overlay，给cmd/fixtures装配与正式app相同的`WithTagCatalog(tagcatalog.New(db))`，构建独立`.runtime/bin/fixtures`；没有修改正式Go文件或服务语义、日常服务及数据。测试业务请求留在18081／18082，5173仅作为Vite源模块适配器。正式夹具装配仍由后端后续补齐，不能把当前overlay当作已提交的Go修改。

## 前端接续：边界、视觉与回归验收（第三增量）

最终标签匿名流程桌面／手机两项通过（1.2分钟）：五类与未知namespace、原文／译文检索、56项目录及关系／Edition独立分页、真实Work去重、占位只读与无可提交目标、竞争／唯一剩余／未归类、零使用无共现关系、全局与逐本ABA、译名保存／恢复、SSE草稿与响应式保持、漫画窗关闭及外库拒绝、快照过期后主动重载、切库取消和迟到响应隔离、停用库可浏览但不能逐本修改。用例检查了实际请求版本，未用自动重试提升CAS；键盘Enter关闭普通详情后焦点恢复目录链接，无页面横向溢出。

七项导航及原有维护／设置／退出／元数据草稿回归桌面手机两项通过（50.3秒）。112项前端单元、格式、ESLint、TypeScript、文档链接及diff检查通过。运行日志为被忽略的`.runtime/tests/results/tag-{2,3}-*.log`、`tag-ui-final.log`、`tag-navigation.log`；源码检查、UI与后端历史验收分别记录，没有以健康接口或旧截图代替本次UI。

Refactoring UI按层级、两栏布局、8／16／24px间距、名称换行、计数及操作顺序复核；Web Design Guidelines按语义控件、可访问名称、键盘焦点、草稿离开保护及明暗主题复核。1280／1440桌面、390手机的明暗及停用状态8张匿名截图位于`.runtime/maintenance/tag-management-ui/screenshots/`。修复了图标名称混入按钮读名、详情高度随长关系列表撑开、占位胶囊被grid拉伸，以及复核失败仍保留上次当前版本预览的问题。上述核心与边界流程在修正后的源模块重新通过。窄屏抽屉额外发现getContainer=false默认绝对定位会撑大滚动范围，现明确固定于视口；修正后手机完整流程再定向通过（39.0秒），包含停用状态390px宽度断言。

复现时在web中串行运行以下命令；每个浏览器命令启动自己的全新匿名实例，不能并行占用18081／18082，也不操作用户浏览器。5173仅提供源模块，不传递业务Cookie或请求日常API。

```sh
node tests/prepare-tag-fixture.mjs
NYAMANGA_E2E_VITE=http://127.0.0.1:5173 npm run test:e2e -- tests/e2e/tag-management.spec.mjs
NYAMANGA_E2E_VITE=http://127.0.0.1:5173 npm run test:e2e -- tests/e2e/navigation-consolidation.spec.mjs
```

本次未读取或修改.runtime/dev，未自动整理／翻译／确认真实标签、查询私人归档、联网获取译库或运行模型；不运行生产网页构建、Go业务／race、规模或部署套件。当前交付为源模块与匿名UI验收；生产网页、真实库性能及用户实际资料体验仍等待界面确认，不推送。

## 2026-10-28：作品优先读取（后端先行）

本轮基线 f099383，保持 schema17／85表和001–017 SQL。新增只读 `/tag-catalog/{id}/works`，不改旧目录、Edition诊断、原作／角色关系或任何写入语义。普通作者／社团／内容详情由 Work 卡片表达，也允许原作／角色使用该查询；不会把关系证据、推断或否决塞进普通 Work 响应。

核心读取先筛选当前库非回收且使用该标签的Edition，再按真实Work去重、标题匹配、排序、计数和分页；代表从同Work所有非回收成员中首选优先，否则按Edition ID稳定选择，首选无需包含该标签。隐藏／缺失／损坏及显式停用库可读，回收库不可读。显示名仍用既有人工名称／首选继承规则，封面替代不改变名称来源；查询不打开归档或生成封面。

Work 读取与测试增量覆盖去重、跨页、非首选命中、仅命中Edition题名搜索、稳定替代、范围／隔离、封面版本和并发改名下的同事务计数。独立snapshot沿用目录／组织／译名世代并额外纳入库内封面选择revision；旧接口snapshot不变。HTTP／OpenAPI接线与完整交接说明见下节。

已核对后续概览组合：`GET /works/{id}`提供Work原有名称及Series身份／位置，卡片另提供Series名称和封面摘要；`GET /works/{id}/editions`按服务端分页提供版本身份及编辑摘要。后者当前包含回收成员但没有trashed_at字段，前端不能据列表猜回收状态；用户选择具体版本后，使用已有Edition详情核对回收／停用状态，再进入原编辑器。本轮不修改这些旧响应，也不把首选版本资料扩展为Work共有资料。

### Work HTTP契约与类型

新增 `GET /api/v1/tag-catalog/{id}/works`，参数：必填 `library_id`，可选 `q`、`sort=name`、`order=asc|desc`、`page`、`page_size`、`snapshot`。默认1页／每页50、最多100；page>1必须带本接口同条件快照。旧 `/editions`、`/detail` 的snapshot不能复用。category、namespace、include_unused=true、related_tag_id以及非all的partition均拒绝为400，避免把关系诊断筛选悄悄变成普通作品查询。

仓储／服务返回 `model.TagWorkPage`：items、total、page、page_size、snapshot；total是匹配的去重Work数，不是Edition数。每项 `model.TagCatalogWork` 只有：

- id、library_id、name、revision：稳定Work身份及既有聚合版本；name按人工名称／首选继承，首选即使回收仍不改变名称来源。
- series：无归属时null，否则id、name、position、revision；position沿现有Series成员位置，1基。
- cover：复用WorkCoverSummary，含work_id、edition_id、content_revision、cover_revision；是数据库中的代表身份／版本摘要，不是图片存在或可解码的承诺。

普通响应不含版本数量、Edition状态、关系证据、推断或否决字段。标题查询仅匹配有效Work名称或实际使用本标签且非回收的Edition题名；其他非命中成员的标题不能被当作标签题名命中。排序用Work有效名称自然键，追加Work ID稳定排序，方向只影响名称排序。筛选／去重／计数／LIMIT／OFFSET都在SQL，计数、当前页和摘要来自同一只读事务；不先按Edition取一页再去重，不在Go或前端读取整库分组。

库、目录、组织、译名与既有关系世代沿用标签快照规则。因为封面选择更新本身只提升cover revision而不提升catalog_state，新接口额外在SQL聚合当前库cover revision后生成独立snapshot；永久删除通过既有目录世代失效；同库其他封面更新也可保守地使本页过期。这个补充仅影响新接口，不修改旧接口snapshot，查询不写入数据库。封面变化、改名、首选变化、标签变化、译名变化或条件变化后，旧页返回409 tag_catalog_snapshot_expired。

读取认证沿用Cookie登录边界，GET不要求写入Origin／CSRF；未登录401，非GET不提供业务处理。无效输入400 invalid_tag_catalog；标签／库不存在或库回收404 not_found；过期快照409 tag_catalog_snapshot_expired；投影未就绪503 tag_projection_unavailable。既有错误结构与中文提示不改。

匿名HTTP契约测试第一张卡片的响应示例（snapshot以说明占位，实际返回64位十六进制令牌）：

```json
{
  "items": [{
    "id": "i:book", "library_id": "lib", "name": "Chosen Work", "revision": 5,
    "series": {"id": "series", "name": "Anonymous Series", "position": 1, "revision": 4},
    "cover": {"work_id": "i:book", "edition_id": "book", "content_revision": 3, "cover_revision": 6}
  }],
  "total": 2, "page": 1, "page_size": 1, "snapshot": "<本接口返回的64位snapshot>"
}
```

请求为 `/api/v1/tag-catalog/{artist-author-id}/works?library_id=lib&page_size=1`；下一页原样保留条件并加 `page=2&snapshot=…`。只在非首选版使用该标签时仍返回Work，代表仍可为未含标签的首选版。首选回收后，同一匿名例子的代表变为a-spare／content_revision=9／cover_revision=12，名称和Work身份不变。该例没有创建或读取漫画文件、缓存或封面。

```json
{"error":{"code":"tag_catalog_snapshot_expired","message":"标签目录或关系已更新，请重新查询第一页","request_id":"<本次请求ID>"}}
```

前端卡片直接使用本响应的Series／cover摘要，不逐卡请求Work或Edition。点击后可读取 `/works/{id}` 并按需分页 `/works/{id}/editions?page=1&page_size=…`（成员题名筛选用旧参数query）。保留原编辑CAS，不将Work revision当成Edition编辑revision，也不将首选元数据当Work公共资料。成员列表含回收成员但不含回收标记；选择后读取已有Edition详情核对trashed_at／library_disabled及元数据revision。概览应保留新摘要中的封面代表，不因旧Work详情的preferred_edition_id仍指向回收版本就换回不可用代表。没有新增组织、编辑或阅读业务。

### 本轮后端验证与交接边界

Work读取增量为793ef7b，包含仓储／类型／服务及5项匿名测试。HTTP接线、OpenAPI、响应／错误及既有概览组合验证见本轮最终接口提交。最终11项定向回归通过（新增6项、既有目录／关系及输入／HTTP回归5项），Go vet、依赖边界、OpenAPI、282条本地链接和diff检查通过。Work核心与HTTP定向race通过。新增6项测试覆盖同Work多命中去重、跨页、非首选命中、Work名称与命中Edition题名、无关题名不命中、首选回收稳定替代、隐藏／缺失／损坏／停用／回收、库隔离、批量Series／封面版本、快照隔离和失效、无效输入／认证／只读方法及旧概览／成员分页复用。并发改名验证计数和页来自同一快照；定向race包含该并发读取及HTTP契约。

复现时在仓库根设置 `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOPATH="$PWD/.runtime/cache/go" GOCACHE="$PWD/.runtime/cache/go-build" TMPDIR="$PWD/.runtime/tests/tmp"`，执行：

```sh
go test ./internal/store ./internal/tagcatalog ./internal/httpapi -run '^TestTagCatalog(Works|InputBoundaries$|CountsTranslationsAndSnapshots$|DeterministicInferenceIsolationAndNoPropagation$|HumanDecisionsPlaceholdersAndCAS$|HTTPContractPermissionsAndCAS$)' -count=1
go test -race ./internal/store ./internal/tagcatalog -run '^TestTagCatalogWorks' -count=1
go test -race ./internal/httpapi -run '^TestTagCatalogWorksHTTPContractAndOverviewReuse$' -count=1
go vet ./internal/store ./internal/tagcatalog ./internal/httpapi ./internal/app
npm --prefix web run api:check
python3 scripts/check/check-docs.py
git diff --check
```

上述复现环境变量需要export至各命令；先创建 `.runtime/tests/tmp`。只编译现有Go和已有嵌入产物，不调用make综合构建。测试日志位于被忽略的 `.runtime/tests/tag-works/`。旧目录、译名／关系和CAS的定向回归一起检查；旧API对象及所有SQL与f099383对比不变。

本轮仍schema17／85表，没有新迁移、数据库回填、真实实例读取／升级或服务重启，没有web／依赖／推荐／模型改动。生产网页构建、UI、真实API更新、大规模延迟和部署未验收；主对话审查更新API后再串行交接前端。本轮后端交付不代表新的居中详情／卡片界面已经完成。

### 作品卡片API实际更新与前端接续

2026-10-28，主对话审查45fb731的SQL去重、同事务分页、非首选命中、封面替代和独立快照，复跑TestTagCatalogWorks的受影响store／tagcatalog／HTTP定向测试，均通过。确认没有排队／运行／暂停任务、待重验发现或待创建导入／来源查询子任务后，正常停止API，只构建Go服务并保留旧程序，复用现有嵌入产物后恢复8080；Vite5173保持原PID。没有执行迁移、回填或生产网页构建。

更新前后schema17／85表不变，84张非账本表的旧列逻辑摘要、全部迁移账本、12444个配置／秘密／素材／缓存／索引文件摘要和私有升级快照集合保持；quick_check与外键检查通过。私有核对与程序回退材料保存在被忽略的`.runtime/maintenance/tag-work-cards/`。8080和5173代理的ready及auth status返回JSON200，未登录新works接口返回JSON401；没有自动登录查询、编辑真实漫画或执行标签关系操作，这些就绪检查不能替代UI与实际业务验收。

前端从本交接提交接续：五类直接平铺、标签小卡片网格、所有标签详情居中；作者／社团／内容详情为紧凑身份栏、限宽译名图标操作及Work卡片网格，不出现Edition数量或关系诊断。Work概览及内部版本沿既有接口，使用新卡片的代表封面，不让首选回收造成封面回退。普通详情不读取关系列表；原作角色复杂关系仅保留原能力并适配居中容器，本轮不增加图片管理或重写关系规则。

本记录确认后端查询交付及日常API更新，新的卡片／居中界面仍待前端实现及定向匿名验证；用户确认本批界面后才集中构建生产网页，不引用旧截图作为本次效果验收。

## 2026-10-28：五类卡片与居中作品界面（前端第一增量）

本轮从1ed3267接续，按用户确认取代前轮“三大类再分组／列表／左右栏／抽屉”的设计。仍使用React、Ant Design和原生CSS、14／12px文字及8／16／24px分组，不增加依赖。先按Refactoring UI复核两张用户截图：P1层级混杂（§2.1）、输入过宽与纵向空白（§3.4／§3.6）、普通标签展示无关诊断（§2.5）；修正结构后再看匿名实际像素。

五类原作、角色、作者、社团、内容标签直接切换；目录为等宽小卡片，主名称、prefix胶囊和作品数，不展示Edition数。手机双列与紧凑五类选择，长名两行且可悬停／聚焦读全。保留双语查询、名称／原使用数排序、未知namespace、当前库、独立目录快照和显式零使用；原使用数排序仍为后端既有口径，不改称Work数量排序。

所有标签详情改为居中Modal。普通作者／社团／内容分为紧凑身份栏、限宽译名图标操作、关联作品三块；名称／prefix同行、作品数靠右，译名来源弱化，保存／恢复继承／放弃有tooltip和aria-label。保留translation_revision草稿与显式冲突核对。普通类别从组件挂载层就隔离关系和Edition查询，不以CSS隐藏“单原作证据／否决本书推断”。原作／角色原关系、全局决定与局部否决保留，适配同一居中入口；本轮不做独立图片管理或更改规则。

关联作品仅用新works接口白名单参数，24项分页和本接口snapshot，total直接是服务端真实Work数，不读取Edition页临时去重。Work小横卡只放封面与后端实际Work名称，名称不由前端清洗造名；封面直接使用响应摘要，读取失败用同尺寸占位。不额外逐卡查询首选或详情，不显示版本数、文件状态或关系诊断。

本增量接通只读Work概览及内部版本以便真实验收卡片入口。作品名称和Series来自既有Work与Series读取；封面保留新卡片摘要。内部版本显式展开后才读取旧成员接口，具体选择再读取Book确认当前库、Work、回收及停用，使用既有BookEditor自身编辑版本。标签、Work和版本编辑只显示一个主要内容Modal／遮罩，父级保持挂载，返回恢复草稿、滚动、页码和焦点；全局真实影响／离开草稿确认仍保留。完整保护和边界结果续记于后续验证增量。

114项前端单元、格式、ESLint、TypeScript通过；新增Work白名单与层级路由／切库清理测试。匿名Work卡片桌面／手机两项通过（25.2秒）：非首选命中、Work去重／跨页、缺封面、首选回收后的代表替代、普通详情零关系读取、版本按需读取、回收版本拒绝编辑、单内容遮罩、返回草稿／滚动／焦点及视口截图。原关系／CAS／迟到响应回归另行收尾，不沿用上轮截图或通过数。

## 五类居中界面：层级保护与最终验收

主体提交c7299db。后续针对Work层级补充边界：外库Work深链接在当前库内拒绝、不自动反推书库；源编辑移除入口卡片后，关闭详情退回搜索焦点，避免焦点落到已离开的DOM。正常返回仍优先恢复原Work／版本入口和父级滚动。URL保存tag／work／details=versions／book，以及各自w_q／w_page／v_q／v_page；切库清除全部子级身份与快照。Tag人工草稿可随父窗挂载保留，但Book自身草稿不被该例外忽略，浏览器返回、切库和关闭继续走既有保护。

Work列表和概览不共用Edition、关系、详情或目录snapshot。深链接没有已选卡片摘要时，只在当前标签内按Work名称有界查找一页摘要；未取得时保留封面占位，不用可能回收的preferred_edition_id替换代表，也不提前读取成员来猜封面。内部成员在显式视图中分页，选择后用Book真实revision检查回收／停用／Work身份；没有以Work revision代替编辑CAS。

最终实际验证：

- 新Work卡片完整桌面／手机流程两项通过（35.7秒），覆盖31个去重Work、真实总数与24项跨页、非首选命中、名称原样显示、隐藏与损坏成员计数、明确缺封面、首选回收后替代、Series、普通类别零关系／Edition诊断请求、内部版本按需读取、回收版本拒绝编辑、单个主要遮罩、父译名／滚动／页码／焦点保持、Book草稿浏览器返回保护、迟到Work响应、快照过期显式重载、外库Work拒绝、入口消失后的焦点回退及长名称。查询实际检查了参数白名单及服务器snapshot。
- 既有原作／角色定向桌面／手机两项通过（1.2分钟），保留双向归属、人工确认／排除／恢复、逐本否决／恢复、两类ABA、译名SSE草稿、分页、旧book深链接、关闭保护及快速切库陈旧响应隔离。居中Modal拥有焦点后，测试通过关闭／取消确认实际验证保护，没有强行点击被遮罩覆盖的背景书库控件，也没有删除旧业务断言来绕过回归。
- 本轮114项前端单元通过；收尾再定向检查标签8项。格式、ESLint、TypeScript、本地文档链接和git diff检查通过。未重跑无关导航／推荐／Go业务／race／模型／部署套件，不用这些历史通过代替本次界面验收。

实际匿名截图16张保存在被忽略的`.runtime/maintenance/tag-work-cards-ui/screenshots/`，涵盖目录、Work概览、1280／1440桌面和390手机、明暗、多卡片与过滤结果。弹窗使用当前视口截图，避免全页长目录让固定视口弹窗看似位于页面底部；没有隐藏真实状态或提示。Refactoring UI按层级、限宽输入、分组间距、封面等比、两行长名复核，Web Design Guidelines核对语义链接、图标读名、tooltip／键盘、单层焦点、无横向溢出及草稿保护；修正手机Select被block布局拆开箭头的问题。当前截图为本轮源码实际加载结果，不引用上轮左右栏截图。

日志位于`.runtime/tests/results/tag-cards-*.log`、`tag-work-cards-final.log`、`tag-work-boundaries.log`及`tag-relations-regression.log`。复现时先在web运行已有`node tests/prepare-tag-fixture.mjs`，再以`NYAMANGA_E2E_VITE=http://127.0.0.1:5173`串行运行`tag-work-cards.spec.mjs`和`tag-management.spec.mjs`两个独立匿名浏览器命令；每个命令自建18081／18082实例，业务请求和Cookie不转到日常API。

仅web及本文改变，没有Go／SQL／OpenAPI／依赖／推荐／模型／配置变更；不读写.runtime/dev、不启停8080／5173、不操作用户浏览器或私人资料、不推送。新的后端业务读取沿45fb731，不作前端假去重。生产网页构建、真实库延迟及真实资料体验尚未验证，继续等待用户界面确认；本轮不自动进入原作／角色关系或图片管理专项。

## 原文标题读取摘要与前端交接（2026-10-28）

从57d446a串行补充只读字段，不交换或重写title/name，不迁移数据。漫画显示统一取非空原文标题，再取原有人工Work名称／有效title，最后文件名；这只决定展示，搜索、排序、分页和输入指纹仍沿原契约。Series、书库和标签的名称／译名保持各自语义，不当作漫画题名。

| 读取位置 | 状态与原文来源 | 缺失回退／时态 |
|---|---|---|
| 漫画详情、管理搜索列表 | 已有metadata.fields.title_original；另补Book.title_original（含非管理搜索representative） | 当前Edition有效投影，包含人工覆盖；空值沿旧title／文件名 |
| Work列表／详情、Series作品成员、组织预览context.works | 已有Work.title_original | 实际首选Edition有效投影；人工Work.name保留 |
| Work版本成员、组织预览context.editions | 已有WorkEdition.title_original | 该Edition有效投影；不从Work或别的版本借原题 |
| 标签Work卡片 | 新增TagCatalogWork.title_original | 实际首选Edition有效投影，即使首选回收、封面已替换也不换原题来源；name不变 |
| 标签Edition诊断列表 | 新增TagCatalogEdition.title_original | 该Edition有效投影 |
| 搜索折叠匹配明细 | 新增EditionMatch.title_original | 该匹配Edition有效投影 |
| 导入成员（metadata／versions） | 新增ImportMember.title_original | 当前成员Edition有效投影；候选仍单独取candidate.fields.title_original |
| 导入成员（series） | 同一新增字段 | 当前Work首选Edition有效投影；保留旧title、work_name、series_name |
| 系列建议成员／待定作品 | 已有title_original | 原接口实时首选摘要，继续复用，不改冻结options或推荐输入 |
| 版本／Work建议成员 | 已有members[].title_original | 冻结plan_json；历史缺失用成员原有title，不能查询今天的原题补入 |
| 来源比较／导入候选 | 已有fields.title_original | 候选快照；未采纳候选不能当作当前正式原题 |
| 旧来源搜索候选Candidate、扫描／元数据任务Item | 历史缺失 | 原有候选title或relative_path文件名；Item.input是执行输入，不新增题名或改检查点 |
| 维护影响预览／维护任务计划 | 历史缺失 | summary.targets[].title、organization_effects[].title及原路径；冻结summary_json原样读取，不用今天资料补齐 |

新增字段均为string，缺失／人工empty返回空字符串。Book内部读取字段不进入Edition的直接JSON序列化（json:"-"），仅既有HTTP Book映射显式输出，避免改变内部快照编码。所有有效原题来自effective_editions.fields_json，绝不使用规范化search_documents.original_title作展示。新增内容在原有页级SQL／批量水合查询内选取，无逐卡详情请求；TagCatalogWork原有name／cover来源和匹配、折叠、计数、排序不变。导入成员虽然成员身份固定，既有标题摘要本来即实时JOIN；新增字段保持相同时态，不代表冻结时的题名。

匿名测试新增三项：首选／含标签／封面三种版本原题不同、人工Work名称、回收首选、分页与缺失／明确清空；导入实时人工原题和冻结候选分离、非管理搜索原始字符串；维护历史预览和缺原题的旧建议读取不被当前原题污染。复用Work／Series原题与旧标签／HTTP契约回归；未运行生产网页构建、UI或日常实例验收。此批源码不代表8080已更新。

## 关系展示视图（同批后端交接）

`GET /api/v1/tag-catalog/{id}/relations?library_id=lib&view=display&page_size=24`返回原TagRelationPage结构。`view=all`为缺省且与省略参数使用同一snapshot；它仍返回所有关系诊断。`view=display`只在SQL COUNT／LIMIT之前增加以下条件，count和成员仍在同一读事务：

- 全局decision=confirmed始终显示，包括当前库edition_count=0。
- 自动关系仅在当前库有效归属edition_count>0时显示；excluded、仅否决／历史而无有效归属的auto留在all。

响应中的decision、revision、state／state_label及全部证据／推断／否决／竞争计数不变；display不等于确认，也不产生新状态、事件或人工决定。unclassified_edition_count保留诊断口径，两方向（原作→角色、角色→原作）规则相同。前端普通关系展示用display，诊断管理用all；不得在分页返回后自行过滤或从别库推断关系。

例如当前库一项auto有效归属和一项全局confirmed零本时，display返回total=2，confirmed条目仍是decision=confirmed、edition_count=0；对它执行编辑仍使用原revision门禁。下一页完整保留view、id、library_id、q、sort、category等条件并带本次snapshot。改变任一快照条件或anchor需从第一页重新读取，旧snapshot返回409 `tag_catalog_snapshot_expired`，不静默重试提升草稿版本。

view仅属于tag-catalog关系GET。未知、空值、重复view返回400 `invalid_tag_catalog`；目录／详情／Edition／Work接口携带任何view均返回相同400。Work接口还拒绝显式partition或related_tag_id（含空值及partition=all）；请使用原白名单参数。未登录401、目标不存在404、投影未就绪503及写入旧revision的409 `tag_relation_revision_conflict`保持原契约。没有改变全局／逐本写入口、CSRF、人工empty或停用／回收门禁。

本批验证：标题三项新回归；关系display分页前total、confirmed零本、excluded／suppressed／auto历史零本、默认all兼容、字段逐项一致、双向／跨库、view／anchor／category／library／q／sort快照隔离；服务层view作用域；HTTP有效请求及响应OpenAPI校验、非法／重复参数、匿名认证和既有CSRF／CAS回归。Go定向单元和race、涉及包vet、OpenAPI lint、依赖边界source-only、文档本地链接和diff检查通过。仅使用匿名临时数据库及已有嵌入资产；未构建生产网页、未做本批UI／迁移／真实启动／部署验收，不读取或更新日常实例。

前端接续：复用上表的原题字段与历史回退，TagCatalogWork以首选原题优先但保持Work.name供回退／编辑；关系display与all分页／snapshot分别管理，切换视图清理旧页快照，陈旧响应隔离和草稿保护沿既有契约。后端完成后停在交接，不自动推进前端或更新API8080。

本批实际复现以根目录离线Go环境（GOTOOLCHAIN=local、GOPROXY=off、GOSUMDB=off及项目.runtime/cache/go、go-build、.runtime/tests/tmp）运行：`go test ./internal/store ./internal/httpapi ./internal/tagcatalog -run '^(TestTitleSummaries|TestTagCatalog|TestImportReview|TestSearchFolding|TestWorkRepresentativeTitles|TestSeriesProposalMembers)' -count=1`通过；相同范围加`-race`通过（store227.974s、httpapi37.384s、tagcatalog1.014s），来源原题／人工覆盖／清空补强用例另外单独race通过。`go vet ./internal/model ./internal/store ./internal/httpapi ./internal/tagcatalog`、`npm --prefix web run api:check`、`python3 scripts/check/boundaries.py --source-only`、`python3 scripts/check/check-docs.py`和`git diff --check`通过。OpenAPI逐对象与57d446a比对，仅五个schema的新增原题字段和两个标签读取operation发生变化；web、001–017迁移、依赖与正式推荐／实验目录差异为空。没有新增迁移或改变schema版本。

### 原文摘要与展示视图：实际API更新

2026-10-28，主对话审查c982273／40e940a后，再次执行TitleSummaries、关系Display／View、既有标签HTTP／Work契约的定向测试，store、tagcatalog和HTTP均通过。确认没有排队／运行／暂停任务、待重验发现或待创建导入／来源查询任务后，正常停止API，构建40e940a的Go服务并复用既有网页嵌入资源，恢复8080；没有生产网页构建、迁移或投影回填。

schema17／85表、84张非账本表逻辑摘要、全部迁移账本、12444个配置／秘密／素材／缓存／索引文件摘要及私有快照集合更新前后保持，quick_check与外键正常，Vite5173原PID保持。私有核对和旧程序保存在被忽略的`.runtime/maintenance/original-title-relations/`。8080与5173代理的ready／auth status为JSON200，匿名display请求为JSON401；不使用用户凭据，不自动读取或修改真实漫画资料，不能据这些就绪检查宣称UI已通过。

现在串行交既有前端：按本节矩阵全站使用有效原题，缺失回退但不污染冻结历史；标签和Work卡片移除重复Tooltip／title，图标功能提示保留；五类关联作品均为每行一张横向Work卡片，点击直达现有完整漫画资料，版本切换收在内部，不再展示空洞Work概览中转。原作／角色普通关联使用display并复用标签卡片，角色关联原作默认展示，编辑和具体Edition匹配例外收进二级管理；all／display分页快照隔离，原CAS与作用域保持。本段仅记录后端更新，新前端截图与交互验收待接续，不以旧界面记录代替。

## 2026-10-28：原文标题与详情直达（前端接续）

本轮基线a0a26e9，接续后端c982273／40e940a；本节取代前文Work概览中转及卡片重复悬浮全文的设计。仍为React／Ant Design／原生CSS和既有明暗主题，不新增依赖。按Refactoring UI先处理层级（§2.1）和单列阅读轴（§3.4），Web Design Guidelines复核语义链接、键盘焦点、图标说明和草稿保护。

首个增量统一展示函数originalTitle：有效原题优先，缺失时依次回退人工Work名称／既有名称／有效title／文件名。Book顶层明确空值不借用旧嵌套原题，只有旧响应缺失顶层字段才兼容metadata.fields；不改变数据库、搜索词、排序、推荐输入、提交键或执行检查点。元数据表单将title文案改为“其他标题”，原文在前，title／title_original键和清空／继承语义保持。

| 前端范围 | 本轮处理／保留边界 |
|---|---|
| 漫画列表、完整详情、封面无障碍名称 | 使用当前Book有效原题；列表继续原有括号清洗和两行限制 |
| Work与版本／系列成员、建议卡片、组织选择及影响预览 | 使用原接口原题；人工Work名称独立保存，系列名称不当漫画题名 |
| 标签Work横卡、内部版本、二级Edition诊断 | 使用本次读取原题；封面代表与题名来源独立 |
| 导入版本／系列建议与来源搜索候选身份 | 实时摘要或冻结候选各取自身原题，不互相借用；候选查询、指纹和选用版本不变 |
| 元数据审核的文件名双文本对比 | 保留已确认的“重合更多的候选标题”算法和双来源完整资料；它承担比较，不强制替换为统一身份标题 |
| 旧任务候选、任务Item路径、回收站、维护计划及缺原题的冻结建议 | 沿后端已有保存标题／路径，不逐条读取今天资料补齐历史；书库、系列、标签和文件条目仍展示各自名称 |

116项前端单元通过，包含顶层／旧嵌套原题、明确空值、人工名称、文件名、冻结旧摘要不变，以及独立路由／CAS意图。ESLint和TypeScript通过；涉及的旧表单测试定位同步为“其他标题”，不改请求字段。匿名UI正在按新的直达入口验证，当前源码检查不代表所有页面或生产产物已验收；后续结果续记于本节。

### 完整详情、只读关系与版本保护

第二增量将五类标签的关联作品统一为单列横卡，保留works真实total、24项分页及参数白名单；名称取原题摘要，不逐卡读取今日Book／Work。移除标签、Work及版本标题的重复Tooltip／原生title，保留图标功能、错误和帮助提示，完整原题可在漫画概览／元数据中查看。

点击Work卡片直接打开已有完整BookEditor：先读取Work并核对当前库，使用卡片cover.edition_id所指的代表；旧Work深链接在该标签内有界读取摘要。具体Book再核对库、Work、回收和停用，不能编辑外库或回收版本。首选回收后的卡片原题仍来自后端实际首选，封面及默认打开对象使用服务器有效替代，不能把两种身份混为一谈。删除空洞Work概览和独立内部版本中转窗；details=versions旧链接直接在完整资料内展开其他版本。版本列表点击才读取、25项分页；切换先保护当前Book草稿，再重新核对所选Book，仍使用该Book自身编辑revision。

原作／角色默认展示复用标签卡片：relations明确view=display，不挂载全局写按钮、证据表格或Edition诊断；confirmed零使用仍可显示，excluded不在前端分页后自行过滤。低强调“编辑角色归属”才挂载view=all和原有全局／逐版本编辑；两种视图各自维护查询、页码及快照，view不传到其他接口。作用文案改为“将此角色关联到该原作”“排除此关联”“恢复自动匹配”，单版本例外只在二级具体版本行提供。机器状态、全局作用域、逐版本CAS和翻译CAS保持。

父标签到关联标签的探索保留各自已挂载表单、条件、分页、正文滚动及焦点；返回使用保存的父标签路由，不依赖不确定的历史回退。隐藏父页暂停资源事件读取，切库／关闭仍经过草稿保护。Book内部版本开合不丢字段；切换Book、关闭、浏览器回退继续保护Book草稿。修正版本切换误记父页焦点的问题，只有首次进入详情时记录原作品卡片位置。SSE和主动刷新不提升任何草稿的起始提交版本；409仍需显式读取、核对并采用当前版本。

### 本轮实际验证

- 116项前端单元通过；最后另定向复查标签路由与意图。新增原题显式空值／旧嵌套兼容、旧冻结摘要不变、关系display与all路由清理、作品参数不携带view，以及单版本展示原题和核对查询原始title分离。
- 作品匿名桌面／手机两项通过（56.5秒）：漫画列表／版本管理／系列成员原题、31个Work真实去重／24项跨页、五类单列横卡、非首选命中、原题／人工名称／封面代表分离、缺封面、首选回收替代、完整详情直达、内部版本按需读取、旧details链接、单主要遮罩、Book草稿与返回父标签草稿／滚动／焦点、外库和回收拒绝、迟到读取及快照显式重载。
- 来源比较匿名桌面／手机两项通过（56.3秒）：长中日英标题／完整复制、候选完整资料、冻结选用版本与409、取消和旧响应、分页／草稿、桌面常驻本地对照及手机滚动、封面请求边界、只暂存不越过正式审核。没有用今日正式原题替换候选快照。
- 原作／角色匿名桌面／手机两项通过（1.3分钟）；包含默认display及服务端total、confirmed零本／excluded分离、只读关联标签、父标签草稿返回、二级编辑、两类ABA、翻译SSE冲突、独立分页、停用／外库、陈旧响应及键盘焦点。最后截图补拍前明确等待当前关系和Work页加载结束，避免把Skeleton当作最终界面。

36张匿名实际截图位于被忽略的`.runtime/maintenance/tag-direct-original-ui/screenshots/`，涵盖1280／1440桌面、390手机、明暗、默认原作／角色、单列作品、完整漫画窗、长原题和停用。Refactoring UI复核内容层级、8／16／24px间距、封面等比及单列轴；Web Design Guidelines核对原生语义链接／按钮、完整读名、图标提示、焦点恢复和无横向溢出。没有操作用户浏览器或使用私人标题／封面；日志为`.runtime/tests/results/original-{work,relation,source}-ui.log`及`original-{unit,format,lint,type}.log`。

复现继续先运行web中的`node tests/prepare-tag-fixture.mjs`，仅构建已有匿名fixtures工具；然后以`NYAMANGA_E2E_VITE=http://127.0.0.1:5173`分别串行运行`tag-work-cards.spec.mjs`、`tag-management.spec.mjs`和`import-source-search.spec.mjs`。独立18081／18082处理业务与Cookie，5173只提供源模块，不向日常API传递业务请求。

格式、ESLint、TypeScript、282条文档本地链接、diff及范围检查均通过；本批仅web及本文，Go／SQL／OpenAPI／依赖／模型／推荐／配置差异为空。不读写.runtime/dev、不启停8080／5173，不运行生产网页构建或无关业务／部署套件，不推送。当前是源码与匿名UI验收；真实资料体验和生产网页仍等待用户确认。

## 后续标签专属图片后端

从4643514新增018和原作／角色全局双role图片，旧目录／关系语义及人工决定保持，普通类别无图片。目录、详情、双向关系的CatalogTag批量增加images身份／版本，图片变化使旧标签分页snapshot失效，不提升其他编辑revision。完整请求、响应、匿名样例、缓存／清理保护和前端交接见[标签专属图片](tag-images.md)。本增量不修改web或日常实例；后续目录布局与零使用入口仅由既有前端串行实施。

## 2026-10-28：专属图片、紧凑卡片与固定目录页脚

本轮承接465af4f，原作／角色的主体＋背景使用真实全局图片及批量摘要，局部编辑留在同一详情窗内并保留独立role CAS和pending File；实现及实际匿名验收见[标签图片前端记录](tag-images.md)。上一轮原文标题、完整详情直达、内部版本、单列Work及关系展示／编辑分离保持。

目录普通作者／社团／内容改为12px紧凑卡片，仅名称和Work数；卡片无prefix或技术状态。图片类别另有统一2:1图片区，背景居中裁切，主体完整等比，文字单独放置。所有零使用入口从目录及关系选择器删除，旧include_unused URL先规范化到第一页才读取；后端兼容参数和已有关联不删，零本人工关系继续显示。目录中部独立滚动、页脚固定，保留查询、页码、滚动与返回焦点。仅为新增图片缓存补齐现有维护选项文案，不重做维护界面。

本轮120项单元与相关6项匿名桌面／手机流程通过，格式、Lint、类型、文档和diff检查通过；实际截图路径及范围见专项记录。未读写日常实例或启停服务，没有生产网页构建，真实体验继续由用户在5173检查。

## 2026-10-29：关联布局与唯一标签浏览轨迹

承接用户验收的100f67f，卡片与详情图片交互见[本轮标签图片记录](tag-images.md)。原作下的关联角色改为单行横向卡片：少量居中、溢出从首项开始，原生触控／触摸板滚动；可聚焦区域支持方向键及Home／End，不劫持滚轮。角色下的关联原作居中换行。两方向仍使用服务端display及每页24项、真实total和独立snapshot，不把本地图片或关系布局用于重新推断归属。

角色默认展示去掉关联搜索；旧d_q／d_page／d_sort／d_order／display_snapshot在任何隐藏筛选请求前清除。原作关联角色搜索和二级关系编辑筛选保留。横向位置按当前标签和关系页／查询保留，新页从头开始；返回已访问页面恢复位置。卡片保留等比主体、名称及Work数，不恢复零使用入口。

详情导航改为可序列化、同库、标签唯一的轨迹。目录首次打开push；关联跳转和返回箭头replace，不增加递归历史。A→B→A截回A，A→B→C可依次返回B／A；根标签没有返回箭头，X关闭到原目录。浏览器Back回到目录或真实前一页面，Forward及刷新读取当前历史项的轨迹；不恢复已经明确丢弃的草稿。目录category／查询／排序／页码沿最初入口保留，关联卡片不切换其目录类别。

旧parent_tag／tagReturn仅导入一个经同库核对且没有递归父节点的返回项；自指、循环、外库或外站返回值拒绝，只有URL的父引用作为根详情打开并清理。历史不会继续生成parent_tag。仍在轨迹内的表单保留译名／关系草稿、起始版本、条件、正文滚动和焦点；被截掉的表单及DOM引用释放。有草稿的子标签退出先走离开保护，取消不改变URL或轨迹。隐藏祖先的读取边界关闭，不继续请求或轮询；当前关系选择器保留必要读取。书库规范化仅重排同书库URL时保留详情state，真正切库仍清除。完整BookEditor、内部版本及独立冲突保护保持。

本轮实际验证：

- 最终125项前端单元、Prettier、ESLint和TypeScript通过。新增纯函数覆盖唯一轨迹截断、目录条件保留、同库校验、旧链接自指／循环／外库／外站拒绝、保留及丢弃草稿身份、角色隐藏筛选清理；图片测试补充网络／5xx与409重试分类。
- 8项匿名UI全部通过，分别独立新实例运行各spec：图片桌面／手机23.0秒；新增浏览轨迹桌面／手机29.5秒；已有关系编辑桌面／手机1.2分钟；Work及完整BookEditor桌面／手机1.0分钟。覆盖A→B→C和回访截断、草稿离开取消／确认、DOM缓存回收、Back／Forward／刷新、角色不发送隐藏q、关系分页与横向位置恢复、非当前祖先SSE不请求；原有全局关系／逐版本例外CAS、译名、分页／快照、停用／外库及内部版本切换继续通过。库范围断言只豁免契约明确全局的图片GET，其余目录及关系请求仍须带library_id。
- 源码与截图按现有React／Ant Design／原生CSS和主题tokens复核，使用Refactoring UI的分组、比例与留白规则，以及Web Design Guidelines的语义控件、可访问名称、焦点及溢出检查。没有为更新远端规范而联网。截图等待主题稳定，关联原作截图滚动到关系内容，不把隐藏内容或主题过渡帧作为验收。1280／1440桌面及390手机明暗实际截图在`.runtime/maintenance/tag-direct-ui/screenshots/`；缺图留空样例是匿名夹具的实际状态。
- 日志为`.runtime/tests/results/tag-direct-{unit-all,format,lint,type,images,relations,work,browsing-final}.log`。291条Markdown本地链接及git diff检查通过；提交范围只含web、两项标签专项和AGENTS当前工作状态。没有修改Go／SQL／OpenAPI／配置／依赖、推荐输入或冻结历史，没有重建fixtures或读取／修改日常实例、启动真实任务、启停服务、操作用户浏览器或生产构建。本次证明源码和匿名UI，真实素材体验、真机设备及生产网页仍待用户确认。

复现使用已有匿名fixtures工具，在web目录分别串行运行，每条都创建独立18081／18082实例；5173只提供无业务Cookie的源模块：

```sh
NYAMANGA_E2E_VITE=http://127.0.0.1:5173 npm run test:e2e -- tests/e2e/tag-images.spec.mjs
NYAMANGA_E2E_VITE=http://127.0.0.1:5173 npm run test:e2e -- tests/e2e/tag-browsing.spec.mjs
NYAMANGA_E2E_VITE=http://127.0.0.1:5173 npm run test:e2e -- tests/e2e/tag-management.spec.mjs
NYAMANGA_E2E_VITE=http://127.0.0.1:5173 npm run test:e2e -- tests/e2e/tag-work-cards.spec.mjs
```

## 标签关注后端增量（020，前端待接入）

目录、详情及关系两端摘要新增followed／follow_revision，当前账号独立CAS，不替代关系revision、翻译或图片role版本。目录followed_only=true在服务端SQL分页前过滤个人集合，明确library_id且可含本库零Work；其他详情／关联接口拒绝该参数。建议按本地实际tagID关注优先，旧GET tags与来源资料不附加个人状态。完整PUT／DELETE、快照隔离、私有SSE及前端星标边界见[标签关注](tag-follows.md)。本节仅后端源码交付，不能当作前端或真实升级验收。
