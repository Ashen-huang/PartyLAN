# partylan 网页端部署说明

本说明面向 partylan 网页端的生产发布。网页使用定制 Screego 服务承载房间与屏幕
共享，注册控制服务负责账号、房间状态、Presence 和质量遥测，语音服务为独立的
`voice-sfu` 容器。所有对外产品名称使用 partylan；`Screego`、`gamecast-sfu` 和
`GAMECAST_*` 仅表示既有内部实现或部署兼容名称。

生产发布必须使用新的不可变镜像标签，先验证、备份和灰度，再切换运行中的服务。
不要覆盖旧镜像，不要把密钥写入仓库、Shell 历史、日志或示例文件。

## 部署边界

- 网页 UI 与屏幕共享服务：Screego 上游源码加本仓库 `patches/` 补丁构建而成。
- 网页运行时资源：`deploy/` 中的 WebRTC、统计、共享音频、SFU 和 Nginx 配置输入。
- 控制服务：`server/register.py` 与 `deploy/register.py` 必须保持一致。
- 语音服务：独立的 `voice-sfu` 容器，仅在其镜像或配置变更时重建/重启。
- 生产 Compose：以实际生产服务器上的 Compose 文件为准；仓库
  `deploy/docker-compose.yml` 是受版本控制的期望配置。

当前兼容配置要求保留设备会话和无房主关闭行为：

```text
SCREEGO_SESSION_TIMEOUT_SECONDS=2592000
SCREEGO_CLOSE_ROOM_WHEN_OWNER_LEAVES=false
```

不要在未经迁移和验证的情况下更改上述值。

## 发布前检查与备份

1. 在本地干净上游工作树中应用补丁，并按 [README.md](README.md) 完成网页构建和
   回归测试。
2. 记录当前 `docker inspect screego`、活动镜像 ID、生产 Compose 文件哈希、Nginx
   配置哈希，以及对外和维护副本的静态脚本哈希。
3. 确认控制服务依赖可用：`python3 -c "import bcrypt"`。
4. 在 `/root/screego/backups/` 创建带时间戳的备份目录，备份当前 Compose 文件、
   注册控制服务、对外静态资源、维护副本和 `/etc/gamecast-sfu/users.htpasswd`。
5. 先运行 `nginx -t`。若需要更改 Nginx，只修改已审查的位置块、静态资源别名或
   `sub_filter` 注入项，并再次运行 `nginx -t` 后热重载。

生产 Nginx 通常还承担域名、TLS 和跳转等职责。仓库
`deploy/nginx-screego.conf` 是服务位置块模板，绝不能直接整份覆盖
`/etc/nginx/conf.d/screego.conf` 或其他生产主配置。

## 构建镜像

按 [README.md](README.md) 从锁定的 Screego 上游版本构建网页服务，并使用新标签：

```powershell
Push-Location $source
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
go build -o screego .
$screenTag = "gamecast-sfu:partylan-web-$(Get-Date -Format yyyyMMdd-HHmmss)"
docker build -t $screenTag .
Pop-Location
```

语音服务发生变更时，在仓库根目录构建独立的新标签：

```powershell
$voiceTag = "partylan-voice-sfu:$(Get-Date -Format yyyyMMdd-HHmmss)"
docker build -f server/voice-sfu/Dockerfile -t $voiceTag .
```

将经过验证的标签更新到生产 Compose 文件。不要复用或覆盖当前标签；保留旧标签，
以便在不重建镜像的情况下回滚。

## 运行时环境

生产服务器应将以下变量放在 `/etc/gamecast-sfu/runtime.env` 或等效的受保护环境
文件中。变量名模板见 `deploy/runtime.env.example`：

- `SCREEGO_EXTERNAL_IP`、`SCREEGO_SECRET`、`SCREEGO_USERS_DIR_HOST`
- `GAMECAST_VOICE_SECRET`
- `GAMECAST_UNIFIED_SFU_ENABLED`、`GAMECAST_UNIFIED_SFU_CANARY_ROOMS`、
  `GAMECAST_LEGACY_P2P_ALLOWED`
- `VOICE_SFU_PUBLIC_IP`、`VOICE_SFU_ICE_SERVERS_JSON`

`GAMECAST_SAFE_VIDEO_EGRESS_KBPS` 属于注册控制服务的环境变量，不属于 Screego
容器；未设置或设置为 `0` 时不启用主动的逐观众总出口带宽分配。

全量启用统一屏幕 SFU 前，应先以
`GAMECAST_UNIFIED_SFU_CANARY_ROOMS` 指定测试房间。不要在活跃共享期间切换一个
房间的媒体模式，也不要在旧 P2P 共享尚未结束时关闭新的旧协议声明。

## 切换与验证

在生产 Compose 文件所在目录执行。每次 Compose 操作都必须显式加载运行时环境：

```bash
docker compose --env-file /etc/gamecast-sfu/runtime.env config
docker compose --env-file /etc/gamecast-sfu/runtime.env up -d --force-recreate screego
```

只有在语音服务镜像或其配置发生变更时，才执行：

```bash
docker compose --env-file /etc/gamecast-sfu/runtime.env up -d --force-recreate voice-sfu
```

切换后至少验证：

1. `docker inspect screego` 显示目标镜像，容器处于健康运行状态。
2. 网页入口、登录/注册、房间创建和加入、共享、观看、刷新重连及移动端布局可用。
3. Nginx 的网页静态资源、控制接口、`/stream`、`/sfu-ws` 和所需的语音代理正常。
4. `http://127.0.0.1:5054/health` 只返回版本和聚合计数；防火墙允许 `5054/UDP`。
5. 控制服务能写入质量遥测，且 Nginx 和服务日志不包含认证头、密码、令牌、SDP、
   ICE 候选、原始信令或媒体内容。

对仅静态脚本或 Nginx 位置块的发布，不需要重建屏幕或语音容器；仍须备份目标文件、
`nginx -t`、原子替换并热重载 Nginx，再验证公开资源哈希和网页功能。

## 回滚

1. 使用本次备份恢复生产 Compose 中的上一不可变镜像标签，以及同批次的注册服务和
   静态资源。
2. 若恢复了 Nginx 相关文件，执行 `nginx -t` 并热重载；不要整份覆盖生产主 Nginx
   配置。
3. 在 Compose 文件所在目录执行：

   ```bash
   docker compose --env-file /etc/gamecast-sfu/runtime.env up -d --force-recreate screego
   ```

4. 仅在本次发布改动过语音服务时回滚并重建 `voice-sfu`；屏幕服务回滚不应中断无关
   的语音服务。
5. 除非凭据文件已损坏，否则不要恢复旧 `users.htpasswd`，以免丢失发布期间的合法
   注册。最后再次检查网页、控制服务和公开静态资源哈希。
