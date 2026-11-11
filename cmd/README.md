# 应用入口

server 只处理构建信息与启动失败，将装配交给 internal/app。dev-reset 显式重置当前仓库 .runtime/dev，拒绝正在使用的数据及路径重定向。fixtures 生成独立匿名样本，拒绝覆盖非生成器数据。cmd/treec-import 尚未实现。

通过根 Makefile 构建、启动及 reset-dev。整体导航见 [仓库结构](../docs/repository-layout.md)，执行约束见 [AGENTS.md](../AGENTS.md)。

`dev-rehash`为显式离线内容页摘要重算入口，仅装配调用internal/app，不启动服务或联网。开发实例使用`make rehash-dev`，先正常停服。可重复执行，私有报告与快照留在运行目录，见[约定](../docs/archive-manifest.md)。

`recommendation-pilot`仅保留固定隔离空目标准备／白名单导入，不是日常启动或正式TreeC转换入口。本轮不运行；工具与回退边界见[历史工具说明](../docs/history/development-tools.md)。

`instance-restore`是显式停服整实例恢复／状态／受控回退入口，与server共用构建标识和实例锁，支持空／旧／坏目标而不调用Store.Open；标准make build及Docker镜像都包含该命令。fixtures的`--recovery-instance`创建全新非空匿名实例，再由`--serve`装配真实恢复API。见[备份恢复](../docs/backup-restore.md)。
