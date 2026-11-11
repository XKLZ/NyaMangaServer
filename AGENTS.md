# NyaMangaServer Agent Guide

## 工作方式

- 默认中文。先检查 Git 状态、当前分支和差异，保留已有修改。
- 明确要求实施时完成必要理解、修改、定向验证及修复；讨论不自动构成修改授权。
- 先读 README 和相关源码、领域文档；复用现有实现，不做无关重构。
- 默认串行，不擅自建立工作树、派发Agent、提交、推送或发布；任务明确授权优先。
- Rex 可用时先读其技能、检查会话并复用项目终端；确认空闲、目录及退出码。不得用终端绕过权限或泄漏秘密。

## 项目入口

| 范围 | 文档 |
|---|---|
| 环境与结构 | [开发环境](docs/development-environment.md)、[模块职责](docs/repository-layout.md) |
| 数据与安全 | [领域规则](docs/domain-rules.md)、[数据架构](docs/backend-data-architecture.md)、[安全](docs/security.md)、[授权矩阵](docs/authorization-route-matrix.md) |
| 接口与迁移 | [OpenAPI](api/openapi.yaml)、[迁移](migrations/README.md)、[兼容性](docs/migration-compatibility.md) |
| 网页 | [前端](web/README.md)、[当前书库](docs/current-library-context.md)、[导航](docs/navigation-consolidation.md) |
| 维护与发布 | [维护](docs/project-maintenance.md)、[备份恢复](docs/backup-restore.md)、[发布](docs/releases/PROCESS.md) |

历史验证只代表当时版本，不授权操作实例或自动继续下一功能。

## 职责与依赖

- 单进程 Go 模块化单体，SQLite WAL 为业务数据源。cmd只装配入口；app负责生命周期；HTTP负责认证和DTO；领域声明仓储接口；store负责参数化SQL与事务；model不依赖HTTP/SQL。
- web使用TypeScript、React、Ant Design和原生CSS；API、事件、URL放shared，页面按功能归组。UI任务使用已有设计技能、组件、主题及草稿保护。
- 正式推荐在runtime/recommendations；正式服务不导入experiments或testfixture。匿名一致性测试可引用冻结实验纯函数。
- 保持一个根Go模块，不引入包装模块、微服务、Redis或相邻项目依赖。
- 工具及依赖精确锁定，Go使用GOTOOLCHAIN=local，前端npm ci；不浮动升级或自动下载模型。
- internal/webassets/dist是生成产物，不手改或提交。

## 数据和运行边界

- `.runtime/dev`可能有真实资料；自动测试只使用`.runtime/tests`的合成归档、模拟站点及固定特征。不得清空、重导入或将日常资料用于自动测试。
- `.runtime/cache`为工具／模型环境，`.runtime/bin`可能包含恢复配对程序。不要递归删除整个`.runtime`，也不要复用旧清理清单。
- 源漫画只读，通过允许根及相对路径访问；拒绝穿越和越界符号链接。相邻项目只作参考，不读取生产库／凭据，不改相邻代码或环境。
- 实际联网、扫描、推荐、整理、清理、备份恢复和部署需要当前任务授权。代码修改不自动授权启停或升级服务。
- 开发默认8080／5173；操作实例前核实身份与任务，使用标准status/start/stop命令，不强杀或并行占用同一数据目录。
- SQLite位于服务主机本地文件系统。通过应用一致性快照备份，不复制活跃WAL库，不用裸SQL绕过锁和迁移器。
- 主库保存素材ID、相对键和摘要；人工素材长期保留，缓存独立限额。文件原子落盘后提交引用，删除前复核引用、身份与活动拥有者。
- 私人标题、路径、账号、凭据、模型、数据库、备份与私有截图不进入Git。整实例备份包含秘密，不称为脱敏导出。
- Docker／VM／NAS、系统与网络不例行操作。交叉构建不代表目标平台实际通过。

## 必须保持的不变量

- Series → Work → Edition → File；Edition/File一对一。Work为1–100个同库Edition且唯一首选；Series最多1000个同库Work，位置从1开始。2→1保留Work身份，首选改变不移动系列。路径、标题、gid或摘要相同不等于同本书。
- 组织预览与提交共用规范变换，冻结revision和规范命令；撤销不覆盖后来无关人工编辑。
- 文件、来源、人工覆盖和有效投影分离。继承、明确清空、未知、false不可混淆；重扫不覆盖人工资料、组织或个人状态。
- 服务端在SQL计数和分页前限制授权范围；图片、详情、任务、SSE也检查权限。个人状态绑定账号，隐藏不是访问权限。
- 搜索先匹配Edition再折叠计数；阅读绑定账号＋Edition。解析变化保留旧进度并要求确认，不静默套用页序。
- 长任务持久身份、检查点和执行代次；用户暂停不自动恢复，旧工作器不可发布。联网授权独立于扫描，候选必须人工采纳，失败不等于零结果。
- 刷新、切库、SSE不覆盖草稿或提升冻结版本；晚到响应不串库。Cookie／内存CSRF，不把令牌放localStorage，不放宽Origin／CORS／CSP绕过错误。
- 已有迁移及checksum不改，结构演进新增迁移；保留真实构建身份、升级快照及失败回滚。server与instance-restore配对，不伪造旧commit适配包。
- TreeC转换属于独立工具，本项目只承接标准恢复包，不建特殊恢复分支或恢复旧待执行任务。

## 最小充分验证

先确定影响范围；通过后无新修改或疑点不重复运行无关套件。

- 文档：`python3 scripts/check/check-docs.py`、`git diff --check`。
- 源码静态：`make check-source`。前端单元：`make test-web`。
- Go：受影响包的test／必要race／vet；全包范围限定`./cmd/... ./internal/... ./migrations/...`。
- 工具：`make test-dev-tools`、`python3 deploy/tests/release-tools.py`。
- 推荐：固定已有环境的`make test-recommendation`，不做真实推理。
- 构建／smoke／e2e有副作用，按任务选择，不覆盖保留的配对程序。
- UI需匿名桌面／手机、明暗、键盘、焦点、草稿和实际截图；静态检查不代替浏览器结果。

直接运行Go时使用Makefile相同的GOPATH/GOCACHE和`.runtime/tests/tmp`。依赖缺失时报告，不自动联网补模型或以假产物绕过。

交付说明实际改动、验证、未验证项及原因；区分源码、构建、启动、业务、平台与长期运行证据。更新现行说明，不把临时任务日志堆进本文。
