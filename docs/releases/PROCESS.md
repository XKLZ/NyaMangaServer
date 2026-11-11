# 发布流程

## 本地准备与公开边界

版本来源是 `internal/buildinfo/VERSION`；同步网页package、lock和OpenAPI后执行版本校验。相同干净提交构建server和instance-restore，不能修改manifest、标签或ldflags伪装其他提交。

首次公开使用独立无父提交的main。维护者本地的归档分支、旧标签及运行目录不进入远端；只推送明确的main和本次发布标签，禁止`push --all`、`--mirror`或批量推送旧tags。

准备工作包括许可证与notice、隐私审查、文档、安装工具、匿名验证及干净树导出。`public-tree.py --commit main`只检查该公开分支可达对象；自动检查不能代替人工复核。

本地准备与公开发布分别授权。维护者确认具体公开清单后，才创建远端、上传源码、发布镜像及Release。本版本已取得公开授权，实际产物仍须通过下面的验证门禁。

## GitHub 配置（公开确认后）

1. 再次确认`XKLZ/NyaMangaServer`不存在或归维护者所有，不覆盖已有项目。
2. 创建公开仓库，不自动生成README或许可证，只上传本地主分支；检查远端仅有新公开历史。
3. 启用私密安全报告；设置main保护及只读默认Actions权限。
4. 创建`release`环境，配置维护者审批；不要依赖环境名称本身提供审批保护。
5. 等待`Application checks`在实际GitHub运行通过，审查失败项，不能用本地通过替代。

## 构建与发布

`release.yml`仅手工触发，默认`publish=false`。检查job只读；镜像job在原生Linux AMD64构建并匿名验证，再导出同一个已测镜像及安装包。仅publish job获得contents/packages写权限，并受release环境控制。

检查依次运行完整普通Go测试、vet、网页静态与单元检查，以及`make ci-race`的串行并发回归集合；不将全包race超时改记为通过。实际容器和浏览器流程独立验证。

镜像验收需要root准备独立卷，但运行目录由runner预先建立，私有测试子目录与可导出的发行附件分开。首次GHCR包可能需要所有者在网页中改为公开；在此之前Release保持草稿。核实具体失败和包可见性后，可只重跑原运行失败的publish job：必须仍是同一提交、完整草稿附件摘要及相同镜像ID，续发不覆盖附件、镜像或已有公开版本。缺少服务端附件摘要、上传不完整或身份不一致时停止人工核对。

浏览器门禁使用`release-native.py --browser`创建全新匿名实例，并运行当前生产网页脚本：登录、七个深链接、完整漫画详情、四组视口／主题、键盘焦点和CSP，再进行标准备份恢复。历史`web/tests/e2e`包含专用夹具前置条件及旧页面断言，当前不能直接作为统一发行套件；保留源码，不将其失败记为通过。发布验证不启动Vite或接触日常实例。

- tag：`v0.7.0-beta.1`
- image：`ghcr.io/xklz/nyamangaserver:0.7.0-beta.1`
- 不使用latest，不覆写已存在版本。
- Release标记pre-release，包含安装包、离线镜像、SHA256SUMS、构建身份和发布说明；GitHub另生成源码归档。

首次GHCR包默认可能为private。发布脚本先创建草稿、上传同一镜像，再无认证pull验证公开性；未公开时停止。维护者检查包权限并改为public，复核同一镜像ID与资产摘要后再完成原草稿，不能盲目重跑或创建同名覆盖。

任何阶段失败均检查草稿、tag及镜像的实际存在状态。已经公开的有问题版本用新版本修正，不删除重发来掩盖历史。

## 交付核验

- GitHub公共main只含批准的可达历史，版本tag精确指向构建提交。
- GHCR包公开，无登录pull成功，容器revision/version与build-identity一致。
- 无登录下载Release附件并逐项验证SHA256。
- 保留当次平台、业务与界面验证边界。原型NAS实例或历史构建通过不能代表新镜像已验证。
- 发布不自动升级既有实例、不创建用户数据、不启动推荐或下载模型。

参考：[GitHub容器registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)、[Actions安全使用](https://docs.github.com/en/actions/reference/security/secure-use)。
