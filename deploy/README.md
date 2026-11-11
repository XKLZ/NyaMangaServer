# Docker 安装与运行

本说明用于 Linux AMD64 测试版。首次公开镜像尚待发布；源码构建和匿名测试不代表远端仓库已经可用。

## 准备配置

安装包包含 `deploy/`、版本说明、许可证及校验文件。需要 Docker、Compose v2、宿主机 Python 3.9+。数据库必须存放在本地磁盘，不能使用 SMB／NFS 卷。

选择新的私有配置目录；父目录必须已存在。示例中的 `192.168.1.3`、`/srv/comics` 都应替换成自己的地址／漫画目录：

```sh
sudo python3 deploy/configure.py --directory /srv/nyamangaserver \
  --origin http://192.168.1.3:19400 --port 19400 --library /srv/comics
cd /srv/nyamangaserver
sudo docker compose pull
sudo docker compose up -d --wait
```

配置生成器只写新目录：`server.json`、随机 `setup-token`、Compose、`.env` 和安装收据。不会创建账号、数据库或启动 Docker。重复相同参数核验后保留原文件；目标已有不同材料或中断残留则停止，不覆盖秘密。

初始化挂载文件归 UID/GID 65532，权限0400，外部配置目录0700，由调用sudo的用户持有，便于进入目录执行命令。配置生成使用 sudo 只是为了设置这两个文件的属主；服务长期使用65532，不依赖777权限。Rootless Docker 的UID映射尚未验收，不建议直接套用此入口。

访问 `http://192.168.1.3:19400/admin/`。在宿主机本地读取配置目录的 `setup-token`，填写初始化页并设置管理员密码；令牌不要贴到日志、Issue或聊天。初始化后仍保留挂载文件，但令牌不再用于普通登录。

### 端口与 Origin

容器只有 `9400`，宿主机通过 `--port` 映射。`--origin` 是浏览器实际访问的 HTTP(S) origin，不是容器内地址；不可附带 `/admin/`。反向代理时使用外部 HTTPS origin。Origin／CSRF校验保持启用。

容器默认限制2CPU、2GiB内存且不额外使用Swap，日志每份10MiB、最多3份。调整资源前评估主机负载，基础服务验收不代表推荐模型峰值可在2GiB内运行。

### 数据与漫画目录

- `app-data`：SQLite、人工素材、缓存、索引及维护材料。
- `provider-secrets`：凭据文件和配套密钥。
- `models`：只读模型卷，默认空；不会自动下载模型。
- 漫画目录：明确只读挂载到 `/library/comics`，配置 root_key 为 `comics`。

未提供 `--library` 时没有漫画挂载。后续添加应同时修改 Compose 挂载及 `server.json` 的 `library_roots`，配置变动后正常重建本项目容器。不要对整个漫画目录批量改属主；确保65532可读／可遍历，或使用宿主机适当的ACL。

配置文件只读挂载，应用业务设置保存在数据库。手工修改后的安装收据不再匹配，配置生成器会拒绝覆盖，这是预期行为。

## 启停与升级

始终在同一安装目录运行，保留项目名与卷名：

```sh
sudo docker compose ps
sudo docker compose logs --tail 100 server
sudo docker compose stop server
sudo docker compose up -d --wait
```

升级前检查任务状态，创建并下载一致性备份，保存原镜像及对应构建身份；阅读目标版本兼容说明后修改精确镜像标签，再 `pull` 和 `up -d --wait`。不要使用 `down -v` 或全局 prune。第一次公开发布不提供自动升级。

## 离线镜像

下载同一 Release 的离线镜像及 `SHA256SUMS`，先核对校验和：

```sh
sha256sum -c SHA256SUMS --ignore-missing
sudo docker load -i nyamangaserver-0.7.0-beta.1-linux-amd64.tar.gz
# 配置中镜像标签与归档一致，无需联网 pull
sudo docker compose up -d --pull never --wait
```

安装包只是配置工具，不包含用户数据。仓库源归档、离线镜像和整实例备份是不同产物。

## 备份恢复

网页“维护中心 → 备份与恢复”提供完整流程。备份不加密、含认证资料，不包含源漫画和模型。恢复要求相同应用版本、完整构建身份及迁移账本，不能只修改包中的manifest来适配新镜像。

离线恢复时正常停止服务，使用**同一镜像和卷**：

```sh
sudo docker compose stop server
sudo docker compose run --rm --no-deps \
  -v /private/recovery-input:/recovery:ro \
  --entrypoint /instance-restore server \
  --config /run/config/server.json --archive /recovery/instance.zip --request /recovery/request.json
# 阅读预览后，同一命令追加 --prepare；正常重启才激活。
sudo docker compose up -d --pull never --wait
```

恢复映射选择容器内允许根的 `root_key`，不是NAS宿主路径。详见[恢复契约](../docs/backup-restore.md)。恢复后的后台暂停需要用户明确解除，不自动扫描或恢复所有任务。

## 源码构建与验证

[build-image.py](build-image.py) 只从干净完整Git提交导出源码，固定Go／Node／Python镜像与依赖。生成程序对使用同一真实提交身份：

```sh
python3 deploy/build-image.py --platform linux/amd64 \
  -t nyamangaserver:0.7.0-beta.1-local --output /private/output/image.tar
```

[compose.yaml](compose.yaml) 保留开发者本地构建入口；最终用户使用 [compose.release.yaml](compose.release.yaml) 的固定公共镜像。构建时可联网安装锁定依赖，启动不安装包或下载模型。模型按[运行说明](../runtime/recommendations/README.md)单独准备并只读挂载。

早期同平台匿名验证边界见[BETA.md](BETA.md)，本次公开候选状态见[发行说明](../docs/releases/v0.7.0-beta.1.md)。
