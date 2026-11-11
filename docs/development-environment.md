# 开发环境

## 固定工具链

Go 1.27.0，Node 24.19.0，npm 11.17.0。Go 使用 `GOTOOLCHAIN=local`，前端使用 `npm ci`。版本与平台锁是唯一依赖依据，不使用浮动 latest。

```sh
make install
make check-source
make build
make init-dev
make dev-api
make dev-web
make status-dev
make stop-dev
```

首次开发需要生产网页构建供 Go 嵌入。之后前端使用 Vite 热更新，后端修改后在确认任务空闲的情况下正常停止、构建并启动；不要覆盖需要保留的备份配对程序。

## 数据目录

| 目录 | 用途 |
|---|---|
| `.runtime/dev` | 开发实例与秘密；可能已有真实资料，不能当测试垃圾 |
| `.runtime/tests` | 独立合成样本与测试实例 |
| `.runtime/cache` | Go、npm、Python、模型等缓存／环境 |
| `.runtime/bin` | 构建程序；已有备份可能依赖对应版本 |
| `internal/webassets/dist` | 网页生成产物，不提交 |

API 默认8080，Vite5173。初始化不会覆盖现有配置。只有明确舍弃测试数据时才使用 `reset-dev`；升级不能用清库代替迁移。

## 检查选择

- 文档：`python3 scripts/check/check-docs.py`、`git diff --check`。
- 源码：`make check-source`（不构建生产网页）。
- 前端：`make test-web`，类型／Lint／格式包含在源码检查中。
- Go：按影响选择 `go test`、`go test -race`、`go vet`，包范围限定 `./cmd/... ./internal/... ./migrations/...`。
- 开发工具：`make test-dev-tools`。部署工具：`python3 deploy/tests/release-tools.py`。
- 推荐：`make test-recommendation` 仅使用已准备的固定环境和匿名特征，不下载模型。
- 完整构建／回归：`make build`、`make test`、`make e2e` 有构建或启动匿名服务的副作用，按需运行。

直接运行 Go 时沿用 Makefile 的 GOPATH/GOCACHE；临时文件放 `.runtime/tests/tmp`。不要从仓库根执行无界 `go test ./...` 遍历前端依赖。

发布版本以 `internal/buildinfo/VERSION` 为来源，`scripts/check/release-version.py` 验证 server、离线恢复、网页包及 OpenAPI 一致。源码检查通过不等于生产镜像或平台验收。
