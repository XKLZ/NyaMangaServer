# 第三方组件与材料

MIT仅适用于本项目自有代码，不替代第三方许可证。

## 源码与网页

[third_party/manifest.json](third_party/manifest.json)列出实际服务端二进制使用的Go模块和网页production依赖，附带各包许可／署名文本，保留上游原始空白，不对许可证做格式改写。开发工具不随服务端运行；版本来源为go.sum、package-lock.json及平台锁。

使用已安装的锁定依赖运行 `python3 scripts/release/collect-licenses.py` 可重新生成，命令不安装包或联网。包内缺少独立LICENSE的两项：

- `@ant-design/icons-svg@4.6.0`：来自npm元数据gitHead对应的[上游LICENSE](https://github.com/ant-design/ant-design-icons/blob/7f2516ac91226d2b41f93b35cb5197c8d94f7189/LICENSE)，原文保存在third_party/upstream。
- `is-mobile@5.0.0`：从该锁定npm包README的License段提取，保留原署名。

## Docker 运行环境

镜像包含Python、固定wheel和Debian运行库，它们不是MIT项目代码。镜像中的 `/usr/share/nyamangaserver/` 保存本项目及额外复制库的许可；系统其余版权说明在 `/usr/share/doc/`，Python包许可在 `/opt/runtime/lib/python3.12/site-packages/` 的dist-info内。

额外复制的libstdc++、libgcc_s、libgomp与zlib来自固定Go bookworm镜像。构建时同时复制Go标准库许可、CA证书包及各运行库copyright文件，并记录二进制包／源码包版本。GCC运行库包含GPL与GCC Runtime Library Exception，zlib遵循其自身许可。对应源码可通过[Debian源码档案](https://snapshot.debian.org/)按记录的源码包及版本取得；发行前需核对镜像中的具体版权材料，不把所有运行依赖统称为MIT。

## 可选模型、译库与内容

模型权重不包含在源码、镜像或备份中。模型来源、摘要与许可声明见[runtime/recommendations/models.lock.json](runtime/recommendations/models.lock.json)；SSCD与MiniLM各自遵循模型发布方许可。

中文标签译库由用户主动安装，遵循[EhTagTranslation/Database](https://github.com/EhTagTranslation/Database)的许可证及署名要求，不并入本项目MIT授权。此仓库不附带完整译库。

没有附带漫画、外部站点凭据或私人元数据。界面演示图片为项目匿名测试生成的简单色块与测试文字。
