# 管理网页

固定版本 TypeScript／React／Vite、React Router、Ant Design 与原生 CSS，支持系统明暗主题及桌面／手机管理。七项主导航和范围见[导航契约](../docs/navigation-consolidation.md)与[当前书库](../docs/current-library-context.md)。

## 源码与产物

- `src/app`：应用装配、认证状态、布局与路由。
- `src/features/auth`：初始化和登录。
- `src/admin/imports`、`series`、`versions`：导入审核、系列与版本工作台，包含各自流程及CSS。
- `src/admin/books`、`libraries`、`maintenance`、`settings`：漫画、书库、任务／回收站／存储及设置页面。
- `src/admin` 根：跨功能共享编辑器、组织预览与批量整理、当前书库、通用Hook和草稿保护；不为旧目录增加转发文件。
- `src/shared`：集中 API 客户端、DTO、事件、URL 和通用样式。
- `tests/unit`：无需浏览器的匿名流程测试；`tests/e2e`：独立匿名实例的桌面／手机测试。

API 客户端使用 Cookie／内存 CSRF，不以 localStorage 保存认证令牌。生产静态产物写入被忽略的 `internal/webassets/dist`，由 Go 同源提供；Vite 开发代理保留 `/api` 和 Origin。`runtime/recommendations` 是服务端 Python 源码，与前端和私有 `.runtime` 数据目录不同。

## 检查与运行

在本目录运行 `npm run typecheck`、`npm run lint`、`npm run format:check`、`npm run test:unit`。常规构建为 `npm run build`，浏览器测试为 `npm run test:e2e`；后者使用已准备的匿名 fixtures 程序。根目录可运行 `make check-source test-web test-dev-tools`，均不构建生产网页。根命令见[README](../README.md)。

本轮整理不执行生产构建或重启现有服务。定向浏览器验证使用 `NYAMANGA_E2E_VITE=http://127.0.0.1:5173` 与既有 `vite-source.mjs`，页面源模块来自 Vite，API 始终由独立匿名18081／18082实例提供。实际结果与未验证项见[整理记录](../docs/project-structure-cleanup.md)。

## 固定依赖与CSP适配

正式版本和传递依赖以web/package.json及package-lock.json为准，使用npm ci。React Router只负责浏览器管理路由，不增加SSR或独立Node服务。

锁定的@rc-component/util 1.14.0和@rc-component/portal 2.2.1分别有临时滚动条测量／滚动锁定style缺少nonce；vite.config.mjs仅对固定模块各一个调用补传当前CSP nonce。版本或源码调用不匹配即停止，依赖升级必须复核；不修改node_modules，不放宽script-src或style-src-elem。源码检查不替代生产浏览器的CSP验证。
