# Work／Edition／File结构与管理

日期：2026-10-17。当前代码与Mac开发实例均为 `foundation-v1`／schema7；由007原位升级，不清库，不修改001–006。网页适配、匿名集中验证及主Agent审查已完成；真实实例已正常停服、创建一致性私有快照、升级并恢复API8080与Vite5173。真实实例核验范围见文末，不以匿名流程通过代替用户漫画操作验收。

## 模型与资料归属

| 实体 | 保存内容 | 约束 |
|---|---|---|
| Series | 名称、作品成员与顺序 | 同书库，最多1000Work，Work最多归属一个Series |
| Work | 稳定作品ID、可选人工名称、首选Edition | 同书库1–100Edition，非空，首选必须属于自身 |
| Edition | 单文件管理身份、来源、人工覆盖、语言/属性、版本标签、封面与用户状态 | 每个Edition恰好一个File，不合并来源/人工劳动 |
| File | 相对路径、大小、时间、页数、摘要/算法、内容版本和状态 | EditionID作为关联键，严格一对一；源漫画只读 |

007保留EditionID=原书籍ID、WorkID=原逻辑条目ID，SeriesID和位置不变。原版本组成为Work，独立书籍成为单版本Work。原主版本成为首选；单版本Work首选唯一成员。多版本标签归属Edition，移归或拆分也保留。Work名称默认继承首选版本的有效标题，人工填写和恢复继承独立于Edition元数据编辑。

扫描新文件同事务建立Work／Edition／File；重扫不改人工组织。文件缺失仍保留File及管理资料。已有封面、选页身份和长期素材继续使用稳定ID，不因迁移重扫或重算。离线封面回退及内容页摘要策略保持。

## 管理操作

漫画管理仍按实际文件/Edition展示。打开“组织”面板可编辑版本标签、设首选、移归其他作品、拆为独立作品以及设置整个作品的系列。文件诊断和版本资料仍在同一个编辑面板，不要求用户依次管理四层。

作品管理列出单版本和多版本Work。名称可以填写或恢复继承；版本列表服务端分页，可加入一个版本、合并其他完整作品、选定版本拆分、拆为独立作品，以及显式收藏全部版本。加入版本从同书库检索漫画，保留其来源与人工资料；合并入口明确作用于源作品所有版本。漫画列表批量整理也按所选版本对应的完整作品操作，先提示范围。

- **移归版本**：可以在同书库跨Series移动，版本继承目标Work的Series。源Work仍有版本时保留位置；移空才删除源Work和位置。
- **拆分作品**：选择真子集新建Work，默认不继承Series。勾选“保留原系列”才紧接原Work排列。移走首选版本必须明确选择剩余首选。
- **合并作品**：明确目标，保留目标ID、名称设置与首选版本。同Series保留最早受影响位置；目标未属Series可继承唯一来源Series。不同Series拒绝直接合并。
- **2→1**：保留Work身份和Series位置，不自动解散。
- **回收站与永久清除**：回收保留组织；永久清除最后一个Edition才移除空Work。源漫画始终不会被删除。
- **系列**：加入/移出的是完整Work，支持分页、成员搜索、上下移动及显式标题自然排序。界面显示位置从1开始，HTTP移动参数从0开始。

关系修改统一预览、确认；确认提交服务器返回的规范命令，保留生成ID与空数组意图。目标检索不覆盖起始revision，SSE只提示变化；重新核对是明确用户动作。失败保留草稿，不自动接纳新组织归属。

旧 `/admin/version-groups/...` 链接跳转作品列表并显示模型迁移说明，旧组ID不会当成WorkID。`/books/{id}`、`/editions/{id}`、`/admin/editions/{id}` 按原ID进入现有漫画详情。列表条件和分页保存在URL，编辑关闭保留原列表上下文。

## 接口变化

| 接口 | 用途 |
|---|---|
| GET `/works`、`/works/{id}`、`/works/{id}/editions` | 作品查询、轻量摘要、分页版本及一致revision |
| PATCH `/works/{id}`；PUT `/works/{id}/preferred`、`/favorite` | 名称、首选、明确的全作品收藏 |
| POST `/works/{id}/merge`、`/split`、`/separate` | 合并、选择拆分、拆为独立作品 |
| POST `/editions/{id}/move`；PUT `/editions/{id}/version-labels` | 单版本移归与整数组标签替换 |
| GET `/work-organization/context`；POST `/work-organization/preview` | 相关上下文与统一影响预览 |
| `/series`及成员/排序/转移入口 | 正式使用work_ids；item_ids仅输入别名，混用400 |
| `/editions`及详情/元数据/图片/维护子入口 | 正式Edition接口，详情明确file子对象 |
| 旧 `/books`及对应子入口 | HTTP兼容适配，原ID和旧扁平文件字段保留 |
| 旧 `/version-groups...`、`/organization-context`、`/organization/preview` | 认证后JSON410 organization_model_changed，含替代说明 |

`version_labels:[]`明确清空；缺失/null拒绝。完整系列成员替换也要求显式数组，`[]`清空，缺失/null拒绝。正常业务非法输入沿用422；别名混用400、revision/关系冲突409。预览返回的空数组在确认命令中保留，避免误清空或无法回放。

普通搜索同时匹配Work名称和Edition有效/原文标题；`title:`、`title_original:`仍只匹配Edition字段。先匹配Edition再按 `none / works / series` 折叠，旧versions仅输入别名。旧保存搜索仍可读取，重新保存写works；首选被隐藏、回收或过滤排除时选稳定合格代表，不修改首选设置。

## 迁移、安全与验证

升级通过应用执行器，在实例锁内先创建私有一致性快照，再事务迁移外键和所有数据；失败整体回滚。旧版本组→Work映射写到 `data/upgrade-snapshots/work-edition-v7.json`，0600，仅ID/数量，不写私人标题或路径。prepared→complete标记支持提交与报告之间中断后恢复。不能外部运行SQL绕过应用身份、注册函数、锁和账本。

历史任务ID、检查点、执行代次、暂停状态及旧持久输入中的book_ids继续兼容，不自动恢复用户暂停的任务。源、人工覆盖、语言/属性、标签、素材、封面选择和个人状态逐ID保留。元数据任务的陈旧内容输入保护保持。

| 验证领域 | 证据及状态 |
|---|---|
| 第一增量迁移与存储 | 已通过schema6→7逐ID多领域保留、外键、失败回滚、重复启动、私有报告及扫描原子身份检查；001–006摘要未变 |
| 第二增量领域/API | 已通过移动/拆分/合并/2→1、容量100/1000、跨书库/系列、ID碰撞、并发、分页、普通Work名检索与title限定、隐藏代表、永久清除与paused任务检查 |
| 第三增量网页与综合检查 | 48条前端单元、格式/ESLint/类型/构建、OpenAPI、相关Go/vet/race、边界/链接及桌面手机Work定向流程通过；下方列明命令 |
| 真实开发实例 | 已核实身份及无活动任务，正常停服后原位升级；17 Edition／17 File／16 Work、2 Series，41项保留审计通过，API与Vite代理就绪 |
| 未验证/未实现 | Docker/Linux/NAS、真实TreeC转换、推荐整合、客户端/完整阅读、向量、完整导出备份恢复均未开展 |

日常仍用 `make dev-api` 与 `make dev-web`，访问 `http://localhost:5173/admin/`；Ctrl+C正常停止对应进程。两种配置共用`.runtime/dev`，不创建阶段专属路径，不运行reset-dev。重构不改密码、不自动扫描、联网、获取元数据或整理私人漫画。

### 2026-10-17集中验证

Go统一使用GOTOOLCHAIN=local及仓库.runtime/cache中的GOCACHE/GOPATH，TMPDIR为.runtime/tests/tmp/go，不读取真实库。

- `go test ./internal/organization ./internal/search ./internal/library ./internal/metadata ./internal/maintenance ./internal/httpapi ./internal/app -count=1`：通过。
- `go test ./internal/store -run 'Test(WorkOrganization|WorkConflicts|WorkMerge|WorkPurge|PausedMetadata|SearchFolding|SearchSeries|BoundedMatches|SeriesTransfer)' -count=1`：通过，不重复全部历史迁移/规模套件。
- `go vet ./cmd/... ./internal/... ./migrations/...`：通过。
- `go test -race ./internal/organization ./internal/library ./internal/metadata ./internal/maintenance ./internal/httpapi -count=1`及同一store定向正则的race：通过。
- `go test ./internal/httpapi -run 'Test(StaticAndHealth|WorkEditionHTTPRoutingAndProtection)$' -count=1`：补充深链接及空数组回放、缺失/null拒绝、别名混用检查通过。
- `npm --prefix web run test:unit`：48条通过；typecheck、lint、format:check及api:check通过。生产网页和Go server/fixtures构建通过；Vite保留既有组件公共chunk超过500kB提示，未新增依赖或扩大本轮视觉范围。
- `npm --prefix web run test:e2e -- --grep 'Work管理'`：匿名Chromium桌面/手机各一条完整短链通过（最终隔离根复核2/2，23.4秒），合并/标签/首选、Series加入/键盘移动/自然排序、首选拆分/显式替代/默认不继承、2→1、移归继承Series、旧入口、名称409草稿保留及重新核对、列表URL返回。无未预期HTTP或控制台错误。
- `node scripts/test/organization-smoke.mjs`：通过匿名实际进程停止/重启、会话、来源/人工值、Work/Series、保存搜索、快照过期及重扫保留；报告仅在`.runtime/tests/results/organization-smoke.json`。
- `python3 scripts/check/boundaries.py`、`python3 scripts/check/check-docs.py`：通过；Git diff检查由主Agent提交前执行。

浏览器暴露并修正两处实际集成缺陷：服务端缺少新Edition/Work深链接的SPA白名单；移归后显式刷新已删除源Work产生404。现在由更新后的Edition详情驱动成员查询，不向已删除对象发请求。标签输入使用Enter添加、Escape关闭下拉后确认，图标隐藏于辅助名称。

初始历史管理综合用例在新打磨界面遇到过时定位（标签关闭按钮/更多操作菜单）；已更新定位与Work接口，但本轮没有重复运行完整认证/元数据/维护浏览器套件。实际UI通过结论仅覆盖上列Work定向短链。TreeC只更新[转换契约](migration-compatibility.md)，没有执行转换器。

浏览器新短链使用第二个独立匿名根，避免与既有认证/管理用例书库互相污染。fixtures验收入口仍仅18081/18082、最多两个根，逐根验证完全相同的固定匿名样本，拒绝异物；正式server没有mock开关。

### 真实实例升级核验

主Agent只读核实8080服务的命令、配置与项目工作目录；旧库身份为nyamangaserver/foundation-v1、schema6，排队/运行任务及启用的周期扫描均为0。SIGTERM停止旧服务并确认端口释放后，生成私有逐行摘要审计，由新应用执行迁移及一致性快照，不外部执行升级SQL或复制活跃WAL。

升级结果为17个Edition、17条File、16个Work和2个Series；1个旧版本组映射已完成。账号1个、会话2条、来源关联2条、人工覆盖4条和历史任务7条保留；41项表/关系摘要比对通过，含文件字段、组织顺序、来源、人工值、属性、翻译、素材引用及暂停检查点。维护计划仅按迁移规则转换旧聚合CAS键，已过期计划仍拒绝。外键、一对一、首选归属和001–006迁移账本均通过。

应用生成的schema6一致性回退快照和完成状态映射报告权限为0600；完整审计及本次后台进程的启动/停止说明只保存在`.runtime/tests/results/work-edition-root/`，不进入Git。当前API直接就绪、Vite代理就绪、初始化状态和`/admin/`页面响应均已核验；没有自动登录、读取源漫画、扫描、联网或更改整理关系。真实漫画在新管理界面的实际操作留给用户手工确认。
