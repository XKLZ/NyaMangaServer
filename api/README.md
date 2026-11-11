# 接口契约

[openapi.yaml](openapi.yaml) 是 OpenAPI 3.1 契约，使用合法 JSON 语法保存，记录已实现的认证、书库、通用任务、书籍／图片、人工覆盖、站点／译库设置、搜索／命中、收藏隐藏、Series／Work／Edition、组织预览、保存搜索、推荐、导入审核、来源查询及SSE接口。Redocly lint 和 Go HTTP 契约测试同步验证请求／响应。025／026增加多账号及逐库权限、个人阅读状态／持久阅读会话和受控远端量值补齐；完整阅读客户端仍是[功能待办](../docs/development-plan.md)。具体请求响应／CAS／稳定错误与匿名证据见[数据兼容](../docs/data-compatibility.md)，旧设计草案不作为可调用API。

整体职责见 [仓库结构](../docs/repository-layout.md)，当前阶段与执行边界见 [AGENTS.md](../AGENTS.md)。

schema7新增works/editions及work-organization接口，Series使用Work成员，搜索正式fold为works；旧books兼容，旧版本组/context/preview认证后返回JSON410。显式[]与缺失/null分开处理，预览规范命令可直接确认回放。见[当前模型](../docs/work-edition-model.md)。
