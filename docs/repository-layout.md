# 仓库结构与职责

本文描述现行模块职责；可变的交付状态统一见[README](../README.md)。退役阶段通过Git追溯，保留的维护／评测记录见[专项索引](history/README.md)，日常启停见[维护说明](project-maintenance.md)。

| 位置 | 职责 |
|---|---|
| [cmd](../cmd/README.md) | server 入口；dev-reset 显式开发重置；dev-rehash 离线摘要重算；fixtures 匿名样本 |
| internal/app | 服务装配、启动、优雅停止 |
| internal/appdata | 数据锁、目录边界、重置及中断标记 |
| internal/config | 显式配置解析与校验 |
| internal/model | 无 HTTP／SQL 依赖的业务类型与错误 |
| internal/auth | 认证业务与其 Repository 接口 |
| internal/library | 书库规则、扫描编排、索引及图片服务 |
| internal/importflow | 导入批次、三阶段审核、持久子任务关联与恢复 |
| internal/tagimages | 全局标签图片原素材、安全重编码与独立缓存／清理保护 |
| internal/tagcatalog | 强制单库标签目录、双向原作／角色关系与人工决定／逐本否决 |
| internal/metadata | 元数据所有权、人工覆盖、来源选择和通用后台任务编排 |
| internal/organization | 同书库Work／Edition移归拆分合并、Series预览与事务变换 |
| internal/search | 有界语法解析、条件与保存搜索规则 |
| internal/maintenance | 显式维护计划、持久任务、停用/回收站/重新关联和清理编排 |
| internal/recommendation | 同作品／系列共用CPU工作器、离线特征、库级发现、完整方案与用户决策 |
| internal/userstate | 物理书籍收藏隐藏及独立版本 |
| internal/textkey | v1 Unicode规范化与自然排序键 |
| internal/provider | 固定站点访问、限速与边界、加密设置和纯文本译库 |
| internal/comic | 受限归档会话、自然页序、图片解码 |
| internal/imagecache | 封面分片、原子写入、容量与 LRU、重启恢复 |
| internal/store | SQLite 连接与迁移；auth、library、works／editions／files、jobs 分领域SQL |
| internal/httpapi | 认证边界、HTTP 输入输出与错误映射、独立响应类型 |
| internal/webassets | 被忽略的 dist 网页产物嵌入适配 |
| internal/testfixture | 匿名合成样本，只用于测试及 fixtures 命令 |
| web/src/app | 应用入口与页面装配 |
| web/src/features/auth | 初始化与登录；登录后的业务页面统一位于 admin |
| web/src/admin/books、libraries、imports、series、versions、tags、maintenance、settings | 按八个功能归组的页面、专属组件、流程和CSS |
| web/src/admin 根 | 跨功能编辑器、组织预览、当前书库、通用Hook及草稿保护 |
| web/src/shared | 集中 API 客户端、通用组件与样式 |
| web/tests/e2e | 桌面／手机浏览器测试 |
| scripts/dev、test、check | 稳定路径与开发配置、独立匿名进程验收、文档和依赖检查 |
| [api](../api/README.md) | OpenAPI 3.1 当前接口契约 |
| [migrations](../migrations/README.md) | 001_foundation.sql 至 027_scan_storage.sql 与嵌入 |
| [deploy](../deploy/README.md) | Docker／Compose；tests 下独立匿名容器验收脚本 |
| [testdata](../testdata/README.md) | 按需提交的匿名固定样本 |
| [experiments](../experiments/README.md) | 推荐算法冻结实验，不参与正式运行；匿名一致性测试仍引用其纯函数 |
| runtime/recommendations | 正式离线 Python 推荐工作器、固定模型／依赖声明与匿名测试，不导入实验命令 |
| docs | 现行约定、规划与历史阶段证据 |
| .runtime/dev、tests、cache、bin | 当前实例、独立匿名测试、共享工具／模型缓存、程序；Git忽略 |
| .runtime/history、maintenance | 旧实例完整私有归档、迁移进度／摘要与分支清单；不作为日常命令的数据源 |

服务只通过自身声明的仓储接口访问 SQL；HTTP 调用服务；业务类型不依赖 HTTP／SQL，规范化元数据 JSON 与数据库行分离。comic 返回自己的 Entry，由 library 转换为业务页。store 内部类型别名仅复用 model，不定义接口响应。正式服务不导入实验或测试辅助，make check 对实际 Go 依赖执行检查。

单个根 Go 模块和独立 web 工程保持不变；不加 backend 包装层、go.work、相邻路径依赖或额外状态库；管理台使用固定版本React Router浏览器路由。网页静态产物输出到 internal/webassets/dist，编译前由 make 构建，不作为源码提交。web/public 可按真实源静态资源需要建立，当前没有新增占位资源。

已退役编号阶段不再作为仓库目录或开发前置任务；原始材料由维护者本地归档，不在公开历史中。日常启动／构建／运行路径不依赖阶段目录，私人历史运行材料原地保留。日常检查不构建Docker，发布工作流另行显式执行。

元数据、搜索和组织模块已建立；完整阅读器与向量检索按实际需求演进，不预建空包。主库、长期人工素材和派生缓存的目标边界见 [后端数据架构](backend-data-architecture.md)；管理台新增人工封面assets；页面派生图尚未实施，推荐特征索引已独立管理。管理台验证状态见[阶段记录](admin-console-verification.md)。

结构化属性由model纯函数与store事务投影实现；完整词库属于同库独立派生表，未新建服务/外部引擎/运行目录。后端和web串行接入及schema5升级证据见[专项记录](properties-tag-suggestions.md)。

Work结构、兼容接口及升级证据见[专项说明](work-edition-model.md)。Edition/File真实拆表；旧books只留HTTP响应兼容，旧版本组领域和运行表已退役。

`cmd/recommendation-pilot`只准备固定隔离验证实例及白名单快照；`internal/recommendationpilot`负责有界输入与只读封面映射，`store`负责其原子导入。它们不属于正式TreeC转换器，详情见[同作品试用](work-recommendation-pilot.md)。

增强设置由internal/enhancements声明Repository与运行时接口，store负责配置CAS、影响指纹及事务门禁，app装配共享推荐／译库生命周期；HTTP只处理认证和契约。详见[增强设置](enhancements.md)。

标签关注继续归internal/tagcatalog领域服务，store处理个人CAS、账号世代和批量摘要；metadata沿原建议查询及事件入口，HTTP只从session赋予账号context。没有新增包装服务目录，见[标签关注](tag-follows.md)。

internal/galleryhighlight负责只读连接、协议边界、批量gid规范化和有界限流，Repository由store实现；HTTP保持管理会话与只读Bearer两条授权路径，app装配。没有额外后台索引、工作器或下载服务。见[本地画廊高亮](local-gallery-highlight.md)。

数据健康仍归internal/maintenance领域服务、internal/store持久化、internal/httpapi协议及app装配；cmd/health-diagnose是独立只读离线诊断入口，不走普通启动／迁移。详见[数据健康](data-health.md)。

整实例备份／恢复继续由internal/maintenance编排、store实现SQL与只读独立快照校验、httpapi负责认证契约。cmd/instance-restore使用同一实例锁及主库外日志，不走普通Store.Open；cmd/fixtures提供显式全新匿名恢复实例生成入口。024保留001–023，详细边界见[备份恢复](backup-restore.md)。

025／026沿既有auth、userstate、maintenance及store职责扩展账号／ACL、个人阅读、远端量值与受控离线补齐，不新建服务包装层或正式TreeC转换器，详见[数据兼容](data-compatibility.md)。
