# 多账号、阅读状态与远端资料

## 025账号与授权基础

账号身份继续使用原accounts.id，原五列保持。原singleton列改为正整数历史槽位，仅1定位旧password-only登录的原管理员，不再约束账号数量。角色、启用、强制改密及权限世代归account_profiles，逐库viewer/editor归account_library_grants。新账号普通、无授权、强制改密；没有公开注册、邮件或删除接口。

迁移固定专用连接关闭外键，事务内重建父表并逐行保留身份，不删除子表；提交前foreign_key_check，结束时恢复并核验foreign_keys，失败返回可见错误。升级快照仍由既有应用一致性快照机制创建，失败回滚。全新初始化同样创建admin配置；恢复的旧会话原位保留，停用账号每次取会话会被拒绝。

| 接口 | 授权与语义 |
|---|---|
| POST /auth/login | username+password；username缺失或空只查原管理员，绝不遍历账号 |
| GET /auth/session | 本人身份、角色、must_change_password、acl_revision、CSRF和期限；不含密码摘要 |
| PUT /auth/password | 本人当前密码、新密码；成功清除强制改密并撤销本人会话及高亮连接，重新登录 |
| POST /auth/logout | 本人登出 |
| GET /accounts | 管理员；账号与完整逐库授权清单 |
| POST /accounts | 管理员；username/password，新账号强制改密，返回revision=1及空library_grants |
| PUT /accounts/{id} | 管理员；expected_revision、role、enabled、完整library_grants，可选new_password重置 |

强制改密期间仅能读本人session、改本人密码及登出。账号管理服务及事务重新检查操作者当前启用的管理员身份。最后启用管理员不可停用或降级。CAS冲突409/account_revision_conflict；最后管理员409/last_administrator；无权403/forbidden；强制改密403/password_change_required。账号不存在404/not_found，非法字段422/invalid_input。安全审计记录操作者、目标、动作、原／新角色和授权集合，不记录密码／摘要。

停用／重置使该账号auth_revision变化并撤销全部会话和高亮连接；普通角色／ACL调整仅更新acl_revision和管理revision，实时鉴权生效，不永久撤销仍有效高亮授权。重新授权不能自动继续任务。

匿名示例：管理员以POST /accounts创建`{"username":"reader","password":"anonymous password"}`；授予匿名库`{"expected_revision":1,"role":"user","enabled":true,"library_grants":{"anonymous-library":"viewer"}}`。初次登录后必须先改密。所有写请求仍要求同源、会话Cookie及X-CSRF-Token；账号ID从服务端会话context赋予，不能在业务JSON选择操作者。

本增量先将普通账号的非本人认证入口默认拒绝。后续完整领域／SQL授权增量按逐路由清单开放，当前基础通过不代表授权覆盖已完成。

025基础实际匿名验证（2026-11-03）：`go test ./internal/auth`通过（1.002s）；`go test ./internal/auth ./internal/store -run '^(TestAccounts|TestConcurrentSetup)' -count=1`通过（0.438s／0.408s），覆盖原账号／会话、24→25、失败回滚、外键恢复、重复打开、命名登录、强制改密、停用、CAS及最后管理员；同范围`-race`通过（16.773s／15.577s）。均设置GOTOOLCHAIN=local、GOPROXY=off、GOSUMDB=off及项目缓存／匿名TMPDIR。未执行真实实例或网页构建。

## 全接口与后台授权增量

逐路由源码清单见[服务端授权矩阵](authorization-route-matrix.md)。普通账号的库列表、Edition／File／Work／Series、计数分页、搜索／命中、导入／方案／任务、标签／图片及个人集合使用固定的关系范围。所有已认证业务读统一绑定服务端`:acl_actor`，在聚合、排序和LIMIT前生成固定CTE；不依赖SQL文字包含哪些表来选择授权。拒绝main限定绕过和与范围同名的查询CTE；保留实际任务表需要的隐式rowid。普通业务输入继续参数化。数据库身份、迁移和不可信备份独立快照的结构检查不套用业务ACL。

写连接封装不继承裸数据库接口；每个BeginTx／ExecContext及显式连接绑定本次服务端身份。TEMP触发器逐表默认拒绝，检查当前enabled／强制改密／admin或逐库editor；UPDATE同时检查旧、新归属。viewer本人收藏隐藏／关注／保存搜索按浏览权限或本人身份写入。write_epoch及扫描状态更新是精确字段过渡，不能借此改配置；删除父对象的事务内短期归属只用于合法子项清理，下一事务清除。连接替换时原连接上的TEMP身份／触发器被重新安装，不能因连接池换连接而放宽。账号管理／本人改密／新来源的受控内部目的标记只在对应已鉴权事务内使用，并在下一事务重置。

后台jobs及import_batches固定actor_id／actor_acl_revision；旧记录归原管理员且状态不变。导入子任务继承批次操作者；按Edition集合创建的元数据／推荐任务在创建前冻结单库范围，跨库或全局仅admin。库级任务可由拥有编辑权的账号管理。工作器先从持久记录恢复身份，再领取与IO；领取／恢复及发布事务重新检查原发起账号当前权限。撤权、停用或重置在同一账号事务中暂停有关活动任务、提升generation、释放旧资源并暂停未结束批次，保留检查点；重新授权不自动恢复。执行世代仍是原工作器最终发布门禁。

可见Edition允许读取关联的共享来源。更新先在同一事务检查完整未过滤的受影响库集合，缺任何编辑权返回403/shared_source_requires_admin，整次回滚并保留候选；新来源在已验证目标Edition的受控事务内创建，不要求尚不存在的关联来证明可见性。

SSE每批重新读取身份和实时ACL；事件持久库归属／个人owner，过滤在LIMIT前。删除／未知归属事件不会落回全局通知。标签联想的usage、零使用个人身份、人工关系及图片依赖均应用可见范围。高亮使用实时账号和库授权；普通ACL调整保留令牌，最终发布比较ACL世代，停用／重置永久撤销。

session.actor新增library_grants和capabilities（manage_accounts、global_maintenance、manage_global_tags、personal_state）；jobs响应新增actor_id、actor_acl_revision。能力信息供界面展示，服务端仍独立重新鉴权。全局凭据配置、维护、诊断、健康、整实例及标签人工写在HTTP和相应服务入口均限制管理员。

实际匿名验证：认证／025及授权定向Go通过（auth 0.325s、store 2.371s、HTTP 6.614s）；新增确定非空Work／Series方案的editor HTTP用例通过（1.663s），独立高亮及自降级有接任管理员场景通过。三项读写范围／撤权／换连接的窄race通过（45.811s）。最终五项HTTP授权用例通过（7.961s），包括确定方案及组织操作。受影响auth、model、library、metadata、userstate、search、organization、tagcatalog、tagimages、galleryhighlight、enhancements包通过；相关12包vet通过。完整导入使用匿名归档、模拟远端与固定特征工作器，无真实站点、模型或日常操作。没有以零方案的可选采纳分支替代确定方案验收；未运行无关模型／规模／部署或生产网页构建。

## 026阅读状态、远端资料及恢复兼容

后台补齐还包括持久周期扫描配置的scan_actor_id及增强transition的actor_id／actor_acl_revision；旧值绑定原管理员，新的显式配置／重试绑定操作者。周期扫描创建前、增强后台恢复前以及全部任务工作器均恢复并核验持久身份，不因后台context缺失获得管理员。撤权／停用／重置使相关transition失败、任务／批次暂停；新管理员显式重试产生新attempt，旧工作器不能覆盖。没有自动恢复被暂停的任务。

### 阅读协议与影响

账号仅来自session；Edition独立，旧books路由为相同ACL与个人语义的适配。GET不创建状态或会话，详情／图片GET不计阅读。Edition列表、详情及搜索页代表Edition一次批量附带reading_state，不逐卡查询。初始revision=0合法；expected_revision字段缺失或null拒绝，不能当0。所有时间均为服务器Unix秒，页索引内部及API均从0起。

| 接口 | 请求／结果 |
|---|---|
| GET /editions/{id}/reading-state | 本人状态、记录／当前总页数、记录／当前content_revision、stale、完成时间、last_read_at、次数、revision |
| POST /editions/{id}/reading-sessions | expected_revision、content_revision、idempotency_key；显式start仅首次计1，返回持久session id、started_at、state |
| PUT /editions/{id}/reading-state | expected_revision、content_revision、idempotency_key、action；可选page_index、session_id、accept_content_change |

session幂等键限定账号+Edition；相同键及content_revision重连返回同一会话及当前状态，不再计次，expected_revision可沿用首次值；同键不同内容版本409。确实重新开始使用新键。PUT持久保存请求摘要和结果，相同键／相同规范请求重试返回原结果；同键不同请求409。账号／Edition／可选session归属始终核验，重启不丢幂等记录。

progress写明确页码，可倒退，不取max；末页不自动完成。complete显式完成，之后重读／翻页不隐式取消完成；uncomplete显式取消完成，保留页码、次数和last_read_at。reset清页码及完成／完成时间，保留次数及last_read_at。heartbeat只更新last_read_at，不加次数。总页数取服务器本地File，未知时可complete／reset／uncomplete但不能写页码；远端页数不能充当本地总页数。

内容版本变更后GET保留旧页码、完成与记录总页数并标stale。写入必须带当前content_revision，并明确accept_content_change=true；**确认先清除旧页码／完成状态，再执行指定动作**，次数与last_read_at保留，progress会设置请求中明确给出的新页码。confirm_content只做这一明确重置确认；这不表示把旧进度套到新文件。客户端须展示影响并由用户选择，不能静默确认。

CAS、内容及幂等冲突返回409，带error.code及current_state；稳定码分别为reading_revision_conflict、reading_content_changed、reading_content_confirmation_required、reading_idempotency_conflict。未知本地页数写页码为422/reading_page_count_unknown。前端保留草稿、显示当前状态并让用户处理冲突，不能自动提升expected_revision重试。所有个人写受维护屏障及instance.write_epoch覆盖；永久清除Edition通过FK级联清除三类阅读记录。

匿名完整请求示例：

```json
{"expected_revision":0,"content_revision":1,"idempotency_key":"reader-session-001"}
```

以上POST成功后，使用返回state.revision与session id提交：

```json
{"expected_revision":1,"content_revision":1,"idempotency_key":"reader-progress-001","action":"progress","page_index":0,"session_id":"SERVER_SESSION_ID"}
```

### 远端字段与受控离线补齐

Metadata、来源、候选及详情新增nullable remote_page_count／remote_size_bytes。EH只解析已验证gid的API filecount／filesize精确非负整数（数值或十进制字符串）；缺失为null，拒绝负数、小数、单位字符串、越界或gid不符。不开放人工覆盖。File.page_count／size、File内容版本与匹配状态保持独立。

规范来源保存远端字段，阅读／列表详情从来源补充显示；MatchingMetadataJSON继续使用026前的投影字节形状，排除remote_*。仅远端量值变化的刷新不重建投影、不提升Edition.revision；离线补齐也不改投影。已有来源在补齐前后及正常仅量值刷新后，匿名测试验证推荐Pool指纹、discovery token、Edition revision及原投影字节完全相同，健康投影复查正常。健康比较与修复使用同一序列化口径。

管理员POST /maintenance/remote-file-info-backfill必须明确dry_run及expected_revisions，最多1000来源。先用`{"dry_run":true,"expected_revisions":{"SOURCE_ID":0}}`读取可验证raw的预览／revision，再显式`{"dry_run":false,"expected_revisions":{"SOURCE_ID":REVISION}}`执行。执行要求正revision；任何CAS冲突整次回滚。只补缺失的remote_*，不改raw、其他规范值、人工覆盖、File或匹配状态；已有值与raw矛盾返回raw_conflict且保持原值。raw缺失／无字段、无效或提供方不支持均逐项明示，不联网、不从本地推断。重复执行无新增更新。

### 备份、健康及验证边界

整实例包全量保留多账号、角色／逐库ACL、强制改密、全部个人状态、阅读会话／幂等请求、后台actor与远端字段；同版本／同build／同账本策略不变，不自动将旧包迁入新结构。数据库初始化、身份检查和独立包检查按完整账号集合验证profile对应及有效管理员，原singleton=1只用作历史管理员定位，不再假设总账号数为1。

扩展匿名非空往返夹具含三账号、viewer／editor权限及强制改密账号、完成／进度／计次、阅读会话与幂等请求。完整逻辑行和素材保留，所有旧session清空、正常／强制改密登录均通过；后台hold保持，读取不写epoch。恢复后的本人阅读写入会提高write_epoch并拒绝旧激活回退，hold不冒充业务只读。健康检查保留账号集合／外键与阅读约束，既有来源补齐前后没有假投影损坏。

TreeC仍仅为[最终字段契约](migration-compatibility.md)及匿名目标字段保护测试，没有正式转换器或通用迁移档案系统；没有访问TreeC资料或改动两项历史验证工具。

整实例长操作也持久保存发起账号；长传输延长deadline及后台备份／验证分离请求生命周期时显式携带Actor，成功发布仍核验当前管理员，撤权只允许受控失败结算／解除屏障，不以无身份context发布成功。账号管理、后台身份与阅读改动均沿同一后端范围收尾，前端没有派发或修改。

### 最终匿名收尾记录（2026-11-03）

书库永久清除在原事务内先移除逐库授权、提高受影响账号的管理revision与acl_revision，记录library_purged安全审计及本人account_permissions事件；不撤销账号会话或其他书库授权。回收／恢复不改授权。匿名回归覆盖viewer／editor授权、回收／恢复／永久清除、末段SQL失败整体回滚、foreign_key_check、旧分页快照冲突及仅本人可见的权限事件。没有扩展维护业务。

以下命令均使用GOTOOLCHAIN=local、GOPROXY=off、GOSUMDB=off，以及仓库.runtime/cache下Go缓存和.runtime/tests/tmp匿名临时目录；未读取日常实例。

- `go test ./internal/store -skip 'Scale|Performance' -count=1`通过（78.994s），`go test ./internal/httpapi -skip 'Scale|Performance' -count=1`通过（43.371s）。这是收尾前的完整非规模回归；后续后台Actor携带及书库授权清理只重跑直接影响的定向门禁，不把前次完整结果冒充最终全量重跑。
- 最终定向：`go test ./internal/store ./internal/model ./internal/provider ./internal/httpapi ./internal/maintenance ./internal/enhancements ./internal/library -run '^(TestReading|TestRemoteFileInfo|TestTreeC|TestCompatibilityUpgrade|TestAuthorizationPeriodic|TestAuthorizationLibraryPurge|TestEnhancement|TestInstanceFullNonemptyBusinessRoundtripLogicalIdentity|TestInstanceHTTPContractAuthenticationTransferAndRestoreSummary|TestInstanceHistoryRequestKeyFindsLostAndLateUploadResponses|TestInstanceUpload|TestLibraryValidation)' -count=1`通过，依次7.085s／0.016s／0.016s／3.326s／6.850s／0.596s／0.498s。覆盖阅读CAS／重启幂等／维护屏障／实际清除级联、远端精确整数及整批回滚、24→26保留与失败回滚、完整非空恢复往返、持久后台身份及上传故障。
- `go test -race ./internal/store -run '^(TestReadingConcurrentSessionRetryAndWriteEpoch|TestReadingCancelledWriteThenImmediateRetryAndDeviceRace|TestReadingSessionIdempotencySurvivesReopen|TestAuthorizationPeriodicAndEnhancementActorsPersistAcrossRevocation|TestAuthorizationLibraryPurgeClearsGrantsAtomically)$' -count=1`通过（72.114s）。补充分页／SSE断言后的书库清除单项通过（0.563s）。
- 长传输身份显式断言：`go test ./internal/httpapi -run '^TestInstanceBulkContextIsAuthenticatedFiniteAndClientCancellable$' -count=1`通过（0.380s）；随机匿名回环慢上传`TestInstanceAuthenticatedTransferOverridesOnlyFiniteRouteDeadline`通过（0.688s），没有连接8080／5173或外部网络。
- auth／model／store／httpapi／library／metadata／recommendation／maintenance／enhancements／galleryhighlight／tagcatalog／tagimages／userstate／provider／app共15包`go vet`通过；`python3 scripts/check/boundaries.py --source-only`通过。001–024逐文件与2fed5c0字节比较全部保持，web及两项历史推荐pilot源码无差异。路由清单更新为249个方法条目；文档本地链接387项／64文件通过。

初次非规模回归暴露旧迁移夹具把新增列误算为旧行变化、过时schema数值、严格OpenAPI内联详情缺reading_state以及旧SSE夹具无授权／使用其他账号游标；均按实际历史边界或新契约修正后通过。新增整批CAS回滚夹具初次使用重复provider/external_id触发唯一约束，改为两个独立合法来源后验证真正的后项CAS失败及前项回滚。初次边界脚本缺项目缓存环境被沙箱拒绝默认缓存访问，补齐相同离线环境后通过；匿名回环监听在沙箱外按原测试执行。这些失败不涉及日常业务资料。

### 2026-11-04 实际装配下的健康写屏障联动修复

主对话匿名app发现health_state=idle时普通账号修改本人密码仍返回503。根因是HTTP写准入调用了新增管理员鉴权的HealthService.State，把无权读取维护详情误判为维护中。现将内部准入分离为CheckWriteAllowed／HealthWriteAllowed，只读取数据库phase是否idle并返回允许或错误；不返回阶段、repair_id或其他全局详情，不清空／替换Actor，也不授予业务权限。文件IO准入复用该判断，仍在取得IO锁前后检查；既有SQL TEMP最终写屏障不变。State和所有维护HTTP详情仍需管理员，读取错误保持拒绝写入。

生产Run原有HTTP／Health／Instance装配提取为assembleHTTP，生产及匿名集成测试共用，启动／关闭工作器和监听流程不变。TestAppHealthAdmissionPreservesOrdinaryAccountPermissions使用这条实际装配、独立临时数据库和合成Edition；不启动工作器、监听、模型或联网。覆盖viewer和editor初次强制改密／重登、本人阅读会话／收藏／关注、editor库内编辑、viewer编辑拒绝、越库个人写拒绝、普通维护／整实例／增强设置详情拒绝；维护非idle时本人密码／阅读／收藏写与文件IO拒绝，直接仓储个人写仍命中maintenance_active最终SQL屏障。

回归证据：临时还原修复前HTTP准入后，同一集成测试的viewer及editor均在PUT /auth/password复现503/maintenance_active（本应200）；测试后立即恢复修复源码。新测试初轮修正了已有接口返回200而非201的预期，以及两类匿名账号共用默认IP触发认证限流的夹具问题，按独立匿名IP保留原限流规则，没有放宽产品接口。

定向命令均设GOTOOLCHAIN=local、GOPROXY=off、GOSUMDB=off及项目Go缓存／匿名TMPDIR：`go test ./internal/app ./internal/httpapi ./internal/maintenance ./internal/store -run '^(TestAppHealthAdmissionPreservesOrdinaryAccountPermissions|TestHealthMaintenanceAuthenticationAndReadRoutes|TestHealthBarrierRejectsUnleasedWrites|TestHealthIOBarrierPausesTasksAndRejectsOldPublish|TestHealthPermitPoisonRecoveryAndReopen|TestHealthOldCompletionCannotUnlockNewGeneration)$' -count=1`通过（2.305s／0.668s／0.934s／1.508s）；`go test -race ./internal/app ./internal/maintenance -run '^(TestAppHealthAdmissionPreservesOrdinaryAccountPermissions|TestHealthIOBarrierPausesTasksAndRejectsOldPublish)$' -count=1`通过（30.939s／25.816s）；上述四包go vet通过。

相邻门禁核查：任务控制、增强Tick及实例自动工作门禁使用仓储内部状态／允许值，没有通过管理员State详情接口；HTTP维护详情有路由及服务管理员保护。未据此扩展任务、增强或维护业务。001–026与1a9e149逐文件字节保持，前端既有改动保留，仅提交相关Go与本段文档。未操作.runtime/dev、8080／5173，未构建前端或执行真实健康检查／修复／业务任务；集成测试验证实际装配路径，不声称已更新日常进程。

## 前端：账号权限与个人阅读状态（2026-11-04）

网页沿用 React／Ant Design／原生 CSS 和明暗主题，集中使用会话返回的 actor、角色和逐库授权。首次改密在业务 provider、目录及 SSE 挂载前完成，只允许本人会话、改密和退出；旧版无 actor 的会话保留原管理员入口，但不探测尚未支持的账号接口。登录只向原管理员省略 username，其他账号按用户名登录，不遍历密码。

管理员在设置的“账号与安全”内创建账号、提交完整逐库授权、改变角色／启用状态或重置密码；本人的密码入口独立。账号编辑冻结起始 revision，409／最后管理员保护不自动换版本重试；停用、重置及本人降级先确认实际影响。密码不进入浏览器持久存储，关闭或提交后清空。查看者可浏览授权库、元数据及组织关系，并保留本人收藏、隐藏、关注、保存搜索和阅读状态；编辑者只在有编辑授权的库显示整理／导入／推荐等操作。普通用户只加载任务与本人安全设置，不轮询管理员维护、凭据或全局标签管理接口。没有授权时显示空状态，不回退到全库读取。

编辑者推荐入口使用真实只读 `/settings/work-recommendations` 返回的 work／series 开关、支持能力和资源状态；不伪造 `/settings/enhancements` 配置，也不把无法读取管理员设置解释为永久禁用推荐。共享来源拒绝保留网址、候选和起始版本，在采纳按钮旁说明需要管理员；错误摘要可聚焦，手机对比表仅在自身横向滚动。远端页数及大小作为只读来源统计显示中文名称和未知回退，不作为人工覆盖、列表列、排序或推荐输入。

账号权限事件或回到页面时复核会话。相同 actor／ACL 代次保留工作台 DOM、草稿、焦点、分页及滚动；暂时读取失败保留输入并禁用新增写入口，在详情／账号／阅读窗口内保留可达的重试。身份或 ACL 真正变化才取消旧读取、清除旧临时缓存及选择并重新建立业务范围。JSON、图片二进制、封面上传及 ZIP XHR 都在错误副作用前核对代次，迟到的401不能退出新账号；维护状态也随身份代次清理。

列表与详情直接使用批量附带的 reading_state，不逐卡读取，也不会因查看详情／封面而创建阅读会话。状态只按 completed 和 page_index 判断，UI页码在 API 零基索引上加一，未知总数不显示0。标记完成、取消完成、重置分别使用冻结 revision、content_revision 和同一幂等键；网络重试保留请求身份，409展示当前状态并要求重新核对。内容过期须明确接受新内容版本并清除旧页码／完成状态；重置和取消完成保留次数及最近阅读时间。只读元数据保留自定义 namespace，缺失来源标签按空集合展示。

### 匿名验收入口与边界

使用独立 `playwright.compatibility.config.mjs`，新建匿名三账号／三书库实例验证真实账号和阅读接口；推荐与来源场景另外使用匿名固定特征工作器和模拟远端，不运行真实模型或访问真实站点。首次发现普通用户写入误报503后停止前端测试，由后端 c0aeda3 修复健康写门禁，再重新构建匿名 server／fixtures 验证；没有用旧二进制或模拟授权响应掩盖问题。

### 前端实际交付证据

源码主体008bb5a、单库批量边界增量d52aed8，共40份相关源码文件。普通账号即使在多个库均为editor，批量编辑入口和已知资源请求仍限单库，跨库仅admin；该收尾以最终相关单元、Lint和类型验证，没有放宽后端规则。153项前端单元通过；最终相关账号／阅读／图片9项再次通过，包含实际API模块的JSON、图片二进制、封面及XHR迟到401隔离。完整TypeScript、ESLint、最终增量ESLint、Prettier、OpenAPI静态与文档链接及diff检查通过；浏览器测试脚本由Prettier和实际Playwright执行核验，不把ESLint排除项记为已Lint。

8项唯一匿名流程按桌面／手机完成：三账号三库（首次强制改密、只读元数据及未知namespace、本人收藏／关注／保存搜索、阅读CAS与过期接受、远端量值／未知值、无管理员端点轮询、实际SSE清除已撤授权详情）；管理员（创建、完整逐库授权、密码重置、停用、账号409、权限读取失败时保存禁用及重试、相同权限回到页面后草稿DOM与焦点保持）；编辑者（真实只读能力、查找暂停／恢复／采纳、授权库修改及共享来源403保留候选）；独立实例同一SPA换号（无重载登录，旧DOM／详情／个人状态不复用）。另外只补跑上述阅读流程2项，注入一次网络中断再走真实API，确认两次提交完整请求和幂等键完全相同，并比较管理员完整user_state始终不变；不重跑无变化模型、规模或后端套件。

实际日志在 `.runtime/tests/results/compatibility-ui-final-flow.log`（6项，1.8分钟）、`compatibility-ui-admin-final.log`（2项，36.1秒）和 `compatibility-ui-reading-retry-final.log`（2项补充，57.4秒）。单元及静态日志为同目录的 `compatibility-ui-unit-final.log`、`compatibility-ui-unit-incremental.log`、`compatibility-ui-type-final.log`、`compatibility-ui-lint-final.log`、`compatibility-ui-lint-incremental.log`、`compatibility-ui-lint-scope-final.log`、`compatibility-ui-format-final.log`、`compatibility-ui-api-final.log`、`compatibility-ui-docs-final.log`。早期503、429、标签大小写及过渡帧等失败／过程证据保留，不计入最终通过或最终视觉集。

54张最终截图在 `.runtime/tests/compatibility-ui/screenshots/`，最终集只取带1280／1440／390宽度前缀的PNG；已实际复核桌面深色阅读、浅色账号冲突，以及手机浅色草稿／账号冲突和深色来源拒绝／账号表格。来源错误紧邻确认按钮且可聚焦，账号错误在Modal滚动体内可见、授权输入和底部操作保留；表格只在局部滚动，整页无横向溢出。来源对比的远端字段使用中文名、“未知”和“来源统计 · 只读”，不进入人工覆盖。采用Refactoring UI及既有Web Design Guidelines检查要点，本轮未联网抓取指南。

后端提交44f91b9、2873587、1a9e149，实际app健康准入修复c0aeda3；前端008bb5a、d52aed8及测试／交付e987369。共享目录全程串行，两个专项对话交付后停止写入。主对话核对最终230份web文件、54张稳定截图、两份匿名程序及12份验证日志的SHA-256与清单完全一致，实际复核九张代表性桌面／手机截图，包括强制改密、无授权、阅读、管理员冲突、草稿及共享来源拒绝。未用截图或按钮隐藏代替服务端权限验收。

主对话独立复验24→26旧数据保留／失败回滚、书库永久清除授权事务和阅读HTTP契约，store／httpapi通过（1.267s／0.464s）；实际装配健康写准入单项通过（1.678s）。OpenAPI、001–024原字节和Git差异检查通过。范围更广的Go／race／vet及前端测试以此前对应源码阶段的实际记录为准，没有重复无变化模型或规模套件。

日常预检确认：没有排队、运行或暂停任务，没有当前待创建子任务、来源搜索待创建、推荐重验、增强切换、未结束整实例操作及周期扫描；健康状态idle，整实例无owner且background_hold=0。SQLite quick_check通过、外键问题为零。8080／5173已停止且端口未占用，本次不称为保留原Vite进程，也没有强停或取消任务。

在干净完整提交`e987369a05fdef7168899bce8fdded90cb44de54`上分别构建API和离线instance-restore，均使用相同构建身份，未执行生产网页构建。保留原程序后以标准命令启动API，由应用创建一份0600一致性schema24快照，再正常应用025／026。当前schema26／112表；105张旧业务表原列逻辑摘要、001–024账本、12454个配置／秘密／素材／缓存／索引文件摘要完全保持。原管理员ID、密码摘要、会话及个人资料未改；兼容profile为admin且不强制改密，另五张新增表为空。旧任务及批次绑定原管理员，原状态不变。write_epoch因迁移记账从0增加到59；单独核对单调性，不将其作为业务资料变更。新快照quick_check及schema核对通过。

恢复Vite5173后，8080及5173代理各自的存活／就绪／初始化状态返回200、未登录session及全局服务信息返回401，共10项直接回环JSON检查通过；管理台入口返回Vite页面。没有进行真实登录或业务写入。私有核对、文件清单、旧程序和启动记录保存在 `.runtime/maintenance/data-compatibility-20261104/`（目录0700、报告0600），未加入Git；没有复制活跃WAL数据库。

### 使用与未验证范围

- 标准入口为`http://127.0.0.1:5173/admin/`；原管理员登录名`admin`、密码不变。设置的“账号与安全”管理新账号及逐库查看／编辑授权；普通账号创建后无库权限，首次登录及管理员重置后必须修改密码。
- 漫画资料详情展示本人阅读状态，可明确标记完成、取消完成或重置。网页没有阅读器，不因查看资料、封面而自动计次；客户端以后通过阅读会话API显式计次。
- 本地页数／大小与来源画廊统计分别展示。历史来源未自动补齐，缺值显示未知；管理员离线补齐仍须明确调用预览与CAS执行接口，不因打开界面联网。
- 启停使用`make status-dev`、`make stop-dev`、`make dev-api`、`make dev-web`；现有程序已经构建，不需重新初始化或设密。源代码网页通过5173热更新，生产网页等待用户确认本批界面后集中构建。

没有创建真实普通账号、授权或阅读会话，没有改真实个人状态、补齐真实来源、运行TreeC转换、真实联网获取、模型、Docker或NAS。整实例包含多账号／授权／阅读的恢复验证使用匿名实例，未执行真实备份恢复。TreeC本轮只有字段契约与匿名保护测试，两项既有测试导入工具未改，正式转换及生产部署另行规划。全部提交仅本地保存，完成后停止本轮，不自动推进后续功能。
