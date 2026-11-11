# 全局增强设置与服务信息

本批从5acf008串行实施，新增019，001–018保持。全局设置跨浏览器和书库，固定三个功能：work_recommendations（版本／同作品推荐）、series_recommendations（系列推荐）、chinese_dictionary（中文译库）。不涉及插件下载、额外更新检测、在线元数据总开关或模型／评分／命名算法变更。

## 初始化与持久生命周期

019创建enhancement_revision、enhancement_settings、enhancement_transitions和import_enhancements，源码schema19共90表。全新数据库两个推荐false、中文译库true；迁移前已有合法foundation迁移账本的实例，两个推荐保留true、译库true。判据是执行任何pending迁移之前读到的applied账本，绝不根据漫画／账号数量判断。新库一次执行001–019时applied仍为空，不能误判升级；019失败与其他pending迁移同事务回滚。旧批次没有import_enhancements行时沿原两阶段都需要的语义；不回填或改写旧完成批次。

全局revision作配置CAS。关闭先在同一事务重查影响指纹、写入enabled=false／disabling及持久transition，再仅暂停对应queued／running类型并提升generation；原paused保持。之后等待对应IO／租约结束才完成stable关闭。旧发布同时受job generation和事务feature gate约束。失败保持封锁并可按transition ID重试；attempt隔离旧完成回调。shutdown中断留pending，下次启动继续，不靠丢失记录的goroutine。

推荐共用原一个CPU worker。关闭一个功能只取消自己的执行／验证／后台重验租约，另一功能仍使用worker时不关闭进程。开启仅启用权限，不探测Python、不加载模型、不发现、不恢复任务；后台重验也保持未武装，直到用户明确启动／恢复任务或推进相应导入阶段。已有暂停任务只能任务中心明确resume。capabilities读取不再调用runner，配置存在只报configured_unverified，不能冒充ready。

## 导入与译库

新批次在创建事务冻结版本／系列启用标志；未启用阶段不创建childrequest／job，返回disabled_skipped／not_enabled_at_creation，metadata整批人工审核仍保留。中途关闭原本需要的阶段会暂停并报enhancement_disabled；打开不自恢复，可明确resume或skip_recommendation（仍需剩余人工审核）。

中文译库关闭屏蔽完整SQL索引和旧remote投影，保留本地频次、原始namespace:name、人工译名、原数据／JSON／索引。dictionary_state=disabled时dictionary_match为null而非false。读取快照加入增强设置世代，off/on也不能复用旧快照；不抬升metadata／override／translation／organization编辑CAS。重新开启异步离线恢复、复用已安装索引；未安装为not_downloaded，损坏或缺失为unavailable，需用户明确update才联网。恢复不清理旧索引或自动下载。

## HTTP与前端状态契约

所有路径均加`/api/v1`，沿用账号认证；PATCH要求Origin和X-CSRF-Token，JSON限1MiB，严格拒绝未知字段。旧诊断、推荐、任务、提供方与译库路由保留；受封锁的执行／采纳入口返回409 enhancement_disabled，历史查询、人工组织和无需模型的撤销仍可用。

GET `/settings/enhancements`返回全局revision及三个固定features。每项包含enabled（目标配置）、effective_enabled（enabled且stable）、state（stable／enabling／disabling／failed）、当前queued／running的affected_tasks及impact_token、resource_state和transition。transition为空或包含id、feature、attempt、state（pending／completed／failed）、target_enabled、error_code、原切换affected_tasks及Unix秒created_at／updated_at。当前任务数可能已经变为0，原切换数量仍保留在transition中。

资源状态独立于开关权限：推荐只报not_configured／configured_unverified／unavailable（自定义runner可为unverified），GET不启动Python，configured_unverified不能显示为模型已就绪；译库为disabled／restoring／ready／not_downloaded／unavailable。启用成功只表明权限已打开，缺资源时仍应提示配置或明确更新。GET `/settings/work-recommendations`同样不执行runner，available=false表示尚未执行可用性验证，新增work_enabled／series_enabled和resource_state；明确创建任务才做运行验证。

PATCH一次只改一项，必须携带最近GET的全局expected_revision。匿名请求示例（数字和token均需取实际GET结果）：

```json
{"expected_revision":1,"feature":"work_recommendations","enabled":true}
```

有queued／running任务时，先展示具体影响数量并获得用户明确暂停确认，然后提交同一读取结果的指纹；服务器在事务内重查ID、状态和generation。未确认或有新增／变动任务均409，整次不暂停任务：

```json
{"expected_revision":2,"feature":"work_recommendations","enabled":false,"confirm_pause":true,"impact_token":"<GET返回的指纹>"}
```

200表示当前请求已同步落定或无需变更，202表示切换已持久化且仍待对应IO／离线恢复。前端始终以返回的state和transition为准，202期间刷新同一资源；SSE的type=enhancements、resource_id=global、revision只通知重新读取，不能代替状态或提升未提交草稿版本。只在该组可见且有pending时轮询。切换中禁止重复操作；失败保持effective_enabled=false，展示脱敏error_code（drain_failed／offline_restore_unavailable）并允许明确重试：

```json
{"expected_revision":4,"feature":"work_recommendations","retry_transition_id":"<失败transition的id>"}
```

重试不得同时带enabled、confirm_pause或impact_token；同一transition的attempt递增，旧attempt不能完成新尝试。开启推荐不恢复已暂停任务；需要任务中心明确resume，或导入明确推进／resume阶段。关闭不会停止另一个功能占用的共享worker。

| HTTP | 错误码与处理 |
| --- | --- |
| 400 | invalid_request／invalid_enhancement：修正必填版本、功能或参数 |
| 401／403 | 沿用认证／Origin／CSRF校验，不能绕过 |
| 409 | enhancement_revision_conflict：重读版本；enhancement_impact_changed：重读并重新确认影响；enhancement_transition_conflict：重读切换身份／状态 |
| 413／415 | request_too_large／unsupported_media_type：遵循JSON大小与类型 |
| 503 | enhancement_storage_unavailable：重新读取持久状态，不假定本次提交已成功或失败 |

GET `/settings/service`返回真实Go进程version、commit、started_at（UTC RFC3339）、uptime_seconds、os、architecture、database_schema、database_baseline和state=ready。缺失构建值明确unknown；不取网页构建号、不推断发布版本、不联网检测更新，不暴露路径／秘密。数据库读取失败503 service_state_unavailable。四组设置与旧section映射见[导航交接](navigation-consolidation.md)。

## 验证边界

匿名专项覆盖：新库与合法旧账本默认值、18→19全旧表／账本／素材保留、故障回滚与重复打开；影响指纹变化、generation暂停、CAS、失败重试与旧attempt拒绝；共享worker按功能取消；读取零runner调用、开启不武装后台重验；完整与旧译库投影屏蔽、人工译名保留、off/on快照过期且编辑CAS不变；已安装SQL离线恢复且网络计数不增长；导入冻结、disabled_skipped、中途关闭暂停／明确跳过与metadata人工门禁；HTTP认证／CSRF／OpenAPI请求响应、服务字段、旧执行入口封锁与持久事件。

2026-10-30本批实际结果：0093181为持久模型／门禁／生命周期与导入译库实现，cc0cadc为HTTP／应用接线／契约。五包store、recommendation、metadata、enhancements、httpapi的上述定向测试使用`go test -race`通过；HTTP启动时间改为进程初始化时间后，HTTP增强与旧推荐契约再次race通过。迁移专项（MigrationAndConnectionPolicy、UpgradeSnapshotAndRollback及名称含Upgrade／Migration的store测试）通过；其中两处历史schema16断言更新为19，数据保留断言不减弱。受影响Go包vet、Go服务编译（复用已有嵌入网页，不运行生产网页构建）、依赖边界、Redocly OpenAPI、299条本地文档链接、diff与001–018／web／依赖／模型代码保持检查通过。未启动新二进制，未执行日常迁移或UI验收。

主对话独立审查门禁／共享工作器取消／导入冻结与译库屏蔽，运行store、recommendation、metadata、enhancements及httpapi五包的TestEnhancement定向测试全部通过；001–018、web、依赖及推荐运行／实验文件与5acf008保持。用户暂缓前端派发期间没有更新实例；用户明确继续后才进行以下升级。

确认queued／running／paused任务、版本／系列待重验、待创建导入子任务和来源搜索请求均为0，核实标准API和Vite身份后，仅正常停止API。复用已有嵌入网页单独构建a48a9f2程序，保留旧程序；启动时由应用创建0600一致性快照并执行019。当前schema19／90表，85张旧业务表的原列逐行摘要、001–018账本和12454个配置／秘密／素材／缓存／索引文件摘要保持，Vite原进程保持。新增唯一快照为schema18，85张旧业务表摘要与停服基线一致；当前数据库及快照quick_check均为ok，外键错误0。

迁移后的三个增强开关均为enabled=true、state=stable，符合本次已有实例保留可用的选择；没有enhancement_transitions或旧批次import_enhancements回填，没有自动扫描、推荐、下载、开关操作或历史批次重写。私有升级核对与旧程序位于被忽略的.runtime/maintenance/enhancements/。

8080及5173代理ready／auth-status为200，新settings/enhancements和settings/service未登录为401；仅核验启动与认证边界，没有登录真实账号或操作设置。推荐capabilities读取不再执行工作器，available=false表示运行尚未验证；现有旧前端按available禁用按钮的逻辑须在本次前端交接中一并适配，不把configured_unverified当成缺资源或实际模型已验证。

由既有“前端”继续四组设置及联动，旧translations链接进入增强功能的中文译库区域，人工译名回标签管理。真实开关／任务影响、设置UI截图与匿名桌面手机流程待前端单独验收；生产网页继续等待用户本批界面确认，不把启动／数据保持当作UI或真实模型运行通过。

## 前端接续一：全局客户端与执行权限（2026-10-30）

从a4789d6接续。新增类型化增强设置／服务信息客户端和账号级按需读取基础；多个可见入口共享同一增强状态，SSE只触发回读，迟到读取不覆盖较新的提交结果。没有消费者时停止读取，不在基础层轮询，不带书库参数。权限与资源状态分开判断：configured_unverified／unverified允许在有效权限和对应发现能力下由用户明确启动，available=false不再被当作缺模型或不支持；关闭／收尾／失败不允许执行。

新增纯函数冻结单功能PATCH的revision及影响token，区分当前任务数量和切换原始影响，不自动重试提交或恢复任务；导入下一阶段文案依据批次冻结标志，缺新字段保持旧两关语义。4项定向纯函数测试通过。设置四区、任务／导入／译库联动及匿名界面在后续增量记录；此提交不代表已构建生产网页或操作日常开关。

## 前端批次0：已验收设置收尾（2026-10-30）

37c491e是共享客户端增量；设置页面与联动此前仍在工作区，不能把它们算作图片提交5acf008的内容。本次核对全部30个已修改及8个新文件，均属于设置功能或其匿名夹具／兼容测试，保留已验收界面后按职责提交：e78f10b交付四区设置／持久开关／路由，3d9f8e3交付任务／推荐／导入／译库联动。仅补历史失败导入的执行门禁：不依赖错误码恰好为enhancement_disabled，版本／系列阶段的恢复与重试统一检查当前权限，关闭时给出增强设置入口；明确跳过和人工审核不受影响。

四区为概览、增强功能、元数据与网络、账号与安全；桌面单层分类栏加有限阅读宽度，手机分类选择器，没有恢复旧四页签或增设更新检测。概览读真实服务信息，诊断仅展开读取既有四个字段。增强功能区区分配置权限、资源状态及持久切换；关闭影响确认、202收尾、failed重试和三类409保留冻结意图，503回读实际状态，不乐观成功、不自动再提交。只在增强区可见且pending时轮询；账号级共享读取对旧响应做隔离，SSE回读不替代草稿版本。

译库版本、明确安装／更新和许可收纳于增强功能，配置展开前不读取；设置的人工译名表移除，标签编辑与已有人工译名保留。元数据与网络共页保存，站点Cookie与代理认证分组，秘密仍只写；各表单独立冻结版本，刷新、凭据成功和SSE不提升另一份草稿版本。账号安全沿用原会话、改密和离开保护。新路由与焦点映射见[导航实际交付](navigation-consolidation.md)。

执行入口不再以capabilities.available=false阻止已配置但尚未运行验证的推荐；仍要求对应发现能力、effective_enabled及有效资源配置，文字明确“运行时验证”。关闭只禁推荐执行／采纳／恢复／重试，手工整理和历史查询仍在。新导入使用服务端冻结的enhancement_stages显示跳过或暂停，开启不补回创建时关闭的阶段；所需阶段中途关闭后须明确恢复或跳过，剩余人工门禁继续保留。译库停用清理候选读取快照并回读显示，原始标签、人工译名和编辑CAS不变。

### 实际检查与证据对应

| 范围 | 本批实际结果 |
| --- | --- |
| 最终静态与单元 | 2026-10-30晚间最终源码130项前端单元通过，含4项增强纯函数及旧路由兼容；完整web格式、ESLint、TypeScript通过。日志为`.runtime/tests/results/enhancements-closeout-{unit,format,lint,type}.log`。 |
| 四区设置，桌面／手机2项 | 最终收尾流程通过（1.0分钟）：真实匿名API开关、请求未返回不改开关、刷新后持久状态、available=false可明确启动、取消关闭不写入、确认关闭暂停任务、开启不恢复、关闭仍可人工合并并打开加入版本、任务恢复门禁、双表单CAS／秘密不回显／离开保护与旧链接。日志`enhancements-closeout-settings-ui.log`。 |
| 导入及译库，桌面／手机2项 | 最终门禁补齐后通过（38.1秒）：创建时关闭两阶段、后来开启不补入、元数据审核后完成且无推荐任务、所需阶段停用暂停／明确恢复／明确跳过、剩余人工门禁、历史runtime失败关闭后两个重试入口禁用，以及译库关闭后保留人工译名和原始标签且仍可编辑。历史失败展示用匿名GET故障注入，不执行其重试。日志`enhancements-closeout-imports-ui.log`。 |
| 切换故障，桌面／手机2项 | 复用同日15:16的23.1秒通过记录`enhancements-transitions-ui.log`；其覆盖的设置／共享状态源码在该记录后未再修改。核对202可见轮询与隐藏停止、failed重试只带三字段、旧attempt／陈旧GET隔离、revision／impact／transition三类409、503持久回读及字典离线恢复失败；不假称真实磁盘故障。 |
| 导航兼容，桌面／手机2项 | 复用同日15:26的55.8秒通过记录`enhancements-navigation-ui.log`；设置分类、样式及导航源码自该记录后未改。覆盖按活动区读取、旧入口、凭据／改密草稿保护和账号退出；译库更新仅模拟用户明确点击的任务响应。 |

38张匿名截图位于`.runtime/maintenance/enhancements-ui/screenshots/`，包括1280／1440桌面及390手机明暗、四区、译库、暂停确认和失败状态。晚间最终流程重新生成设置截图；按Refactoring UI及已有Web Design Guidelines规则复核分类层级、凭据分组、移动换行、主题和焦点，不重新设计已验收页面。截图和网络请求均来自匿名实例；用户本批已验收设置界面，不代表以下未验证项通过。

最终静态／单元完成于当日22:56，设置流程22:58、导入流程22:59完成，之后只改文档和提交，没有再改被测web源码。本地文档304条链接、diff及范围检查通过，文档日志为`enhancements-closeout-docs.log`；最终41个变更web文件（含37c491e客户端）的SHA256清单位于被忽略的`.runtime/tests/results/enhancements-closeout-source.sha256`。旧import-workflow／import-round2／import-source-search／series-recommendations测试仅适配匿名新库推荐默认关闭，显式开启其原本需要的推荐；没有重跑这些无关整套流程，也没有改真实默认值。

用户本轮授权0＋1，本次只收尾0并停止写仓库；后端契约及标签关注UI由主对话后续串行安排。本批不预建020或关注／高亮／健康／备份模块，不自行派发，不推送。生产网页等标签关注完成并确认界面后统一构建。

## 第四项即时增强（021，后端交接）

local_gallery_highlight在新旧实例均默认false，沿原全局revision CAS读取／切换，始终stable且transition=null；resource_state=ready只说明无需额外资源，关闭时effective_enabled仍false。该项不加载模型／恢复词库／暂停任务／冻结导入／处理其他pending切换；连接独立保留。旧三项的持久切换、资源语义与历史保持。说明见[本地画廊高亮](local-gallery-highlight.md)，本批前端与真实021升级待主对话安排。
