# PartyLAN Guangzhou Full Rebinding Design

**Date:** 2026-08-05

## Goal

将 PartyLAN 客户端的默认 GameCast 服务端整体切换到广州节点
`https://partylanapp.com`，并在该节点补齐独立房间语音服务。迁移完成后，现有网页端、
屏幕共享、账号、房间、历史记录、证书和前端资源必须继续正常使用。

客户端升级采用一次性定向迁移：只将两个已知旧地址切换到新域名，其他用户手动配置的
服务端保持不变。

## Confirmed Scope

本次包含：

- 在广州节点部署现有 `voice-sfu`，使用独立的 `5054/TCP+UDP`。
- 为 Registration Control 补齐 `/api/voice/token` 和共享 HMAC 密钥。
- 为 nginx 增量增加 `/api/voice` 与 `/voice-ws` 路由。
- 将客户端默认 GameCast 地址改为 `https://partylanapp.com`。
- 一次性迁移 `https://113.142.217.42:5050` 与
  `https://85.137.240.159:5050` 两个已知旧地址。
- 清除被迁移地址对应的旧证书指纹，保留其他用户配置。
- 构建、测试并打包可区分版本的新 Windows 客户端。

本次不包含：

- 替换或重建现有屏幕 SFU 容器。
- 修改网页 UI、前端注入逻辑或静态资源。
- 更换域名、Let's Encrypt 证书、账号文件或持久化历史数据。
- 强制覆盖用户配置的其他自定义 GameCast 地址。
- 修改旧节点、推送远端 Git 分支或发布 GitHub Release。

## Verified Starting State

设计前只读检查确认：

- `partylanapp.com` 的 A 记录为 `8.134.126.54`。
- nginx、`screego-register`、`gamecast-sfu` 和 Screego 容器处于运行状态。
- nginx 当前提供 `80`、`443` 与 `5050/TCP`，屏幕 SFU 使用 `5053/TCP`，
  Screego TURN 使用 `3478/UDP`。
- 当前屏幕容器为 `gamecast-sfu:udp-only-v1-20260731`，必须原样保留。
- 广州节点当前没有 voice 容器、`5054` 监听、`/api/voice` 路由或
  `/voice-ws` 路由，Registration Control 进程也没有
  `GAMECAST_VOICE_SECRET`。
- Chrome 基线可正常打开 `https://partylanapp.com`，页面标题为 `GameCast`，
  登录表单可见，`GET /config` 返回 `200`，网页注入脚本为 `v6.22`。
- 当前非浏览器 TLS 探测在握手阶段被重置。原生 Go 客户端必须单独通过线上探测，
  不能用浏览器成功代替该验收。
- 本机 `~/.partylan/config.json` 当前使用
  `https://85.137.240.159:5050`，并保存了旧证书指纹。

## Selected Approach

采用增量 Docker sidecar 方案。`voice-sfu` 使用仓库内现有、已测试的独立服务实现，
通过 `docker compose up -d voice-sfu` 只启动语音容器。线上 compose 文件只增加
`voice-sfu` service，现有 `screego` service 的镜像、环境变量、命令和网络配置不变。

不整份覆盖仓库中的通用 nginx 模板。广州节点配置包含专用的 80 到 443 跳转、域名、
Let's Encrypt 证书和当前静态资源版本；实施时只向线上配置插入两个语音 location。

Registration Control 同样采用定向合并。只加入已审查的语音令牌导入、环境变量、路由和
处理函数，保留广州节点现有控制面逻辑。源文件先备份，替换采用同目录原子操作，随后仅
重启 `screego-register`。网页容器和屏幕 SFU 不重启。

## Server Architecture

```text
PartyLAN client
  POST /api/voice/token
        |
        v
Nginx :443
  /api/voice/* -> Registration Control 127.0.0.1:5052
  /voice-ws    -> voice-sfu 127.0.0.1:5054/ws
        |
        +---- Authorization: Bearer <short-lived token>
        |
        v
voice-sfu
  signaling/health: 127.0.0.1:5054/TCP
  WebRTC ICE mux:    0.0.0.0:5054/UDP
  public candidate:  8.134.126.54

Existing screen path remains unchanged:
  /sfu-ws -> gamecast-sfu :5053
  /       -> Screego :5060
```

语音与屏幕共享保持硬隔离。`voice-sfu` 不读取或保存 Opus 负载，不复用屏幕 SFU 房间，
不改变网页共享音频策略。房间人数上限保持 20。

## Secret And Network Handling

- 生成新的高熵 `GAMECAST_VOICE_SECRET`，只写入广州节点现有受限运行环境文件。
- 同一个密钥由 `screego-register` 与 `voice-sfu` 读取，不写入仓库、命令输出、日志或
  设计/验收文档。
- `VOICE_SFU_PUBLIC_IP` 设置为 `8.134.126.54`。
- `VOICE_SFU_LISTEN_ADDR` 保持 `127.0.0.1:5054`，信令只能经 nginx TLS 访问。
- `VOICE_SFU_UDP_ADDR` 保持 `0.0.0.0:5054`。主机防火墙与云安全组必须允许
  `5054/UDP`；是否可达最终由外网 Pion ICE 测试证明。
- WebSocket 令牌只允许放在 `Authorization` header，拒绝 URL token。
- 验证与日志检查不得输出 Authorization、令牌、SDP、ICE candidate 或密钥。

## Deployment Sequence

1. 记录网页基线、服务状态、监听端口、容器 ID/启动时间、配置哈希和当前房间状态。
2. 对 nginx site、compose、Registration Control、运行环境和 systemd unit 做带时间戳
   的权限保持备份；不打包账号密码明文或输出密钥。
3. 从当前受版本控制的源码构建不可变 voice 镜像，记录镜像 ID 与摘要。
4. 定向合并 runtime environment 和 compose 的 `voice-sfu` service，先运行
   `docker compose config`，再只启动 `voice-sfu`。
5. 在 loopback 上验证 voice health、`5054/TCP` 与 `5054/UDP` 监听；此时尚不公开
   nginx 语音路由。
6. 定向合并 Registration Control 语音令牌能力，进行 Python 语法/测试检查后原子替换，
   重启并验证现有 `/config`、房间、注册与历史接口仍返回预期结果。
7. 只向广州 nginx server block 增加 `/api/voice` 与 `/voice-ws`，运行 `nginx -t`，
   成功后平滑 reload。
8. 验证未授权请求被明确拒绝、短期令牌可签发和刷新、WSS 认证生效，并从服务器外部运行
   双 Pion 客户端完成 ICE、RTP 转发、断开清理测试。
9. 重跑网页浏览器基线并比较服务、容器、路由与静态资源；确认屏幕容器 ID 和启动时间
   未变化后，才进入客户端迁移。

任何步骤失败都停止后续步骤。服务端尚未达到全部门禁时，不启动带迁移逻辑的新客户端。

## Client Rebinding

客户端的两个默认地址来源必须同时更新：

- `pkg/config.DefaultConfig()` 中的 GameCast profile。
- `internal/gamecast.DefaultBaseURL`。

新增单一、可测试的旧地址迁移函数。配置加载后、session jar 初始化前执行迁移：

1. 对已保存地址做现有规则的规范化。
2. 仅在规范化结果精确等于以下任一地址时迁移：
   - `https://113.142.217.42:5050`
   - `https://85.137.240.159:5050`
3. 将地址设置为 `https://partylanapp.com`。
4. 清空 `TrustedCertificateSHA256`，因为旧 IP 证书指纹不能用于新域名。
5. 保留 display name、用户偏好、房间历史、音视频设置和其他配置字段。
6. 原子持久化迁移结果；持久化失败时记录错误，但当前进程仍使用已迁移的内存配置。

新域名使用公开受信任证书，不建立新的手工 certificate pin。现有 cookie jar 按 origin
隔离，旧地址的 session cookie 不会发送到新域名；用户可能需要在新域名重新登录，但旧
cookie 文件不做破坏性删除。

其他自定义地址、新域名本身、空配置和非法配置均不应被错误识别为旧地址。空配置继续回退
到新的默认域名；非法配置沿用现有安全回退行为。

## Native TLS Gate

浏览器访问成功不能证明 Wails Go 后端可以连接。客户端代码落地后，必须使用与正式客户端
相同的 `gamecast.Client`、TLS transport、系统代理规则和证书校验对
`https://partylanapp.com/config` 执行真实探测。

只有同时满足以下条件才可启动迁移后的客户端或生成交付包：

- DNS 解析到 `8.134.126.54`。
- Go TLS 握手成功且公开证书链验证通过。
- `/config` 返回有效 GameCast 配置。
- `/sfu-ws` 和 `/voice-ws` 的 WSS 升级到达正确后端。

若 Go TLS 仍被重置，先诊断云侧连接保护、网络路径或 TLS 兼容性；不得通过关闭证书校验、
固定 IP、回退 HTTP 或修改网页证书来绕过。

## Packaging

- 版本从现有 `0.5.4` 增加到新的补丁版本，避免覆盖已有 release 产物。
- 使用仓库现有原生媒体构建与 `Package-PartyLAN.ps1` 流程。
- 便携包继续包含 `PartyLAN.exe`、`opus.dll`、许可、启动脚本、release info 和 SHA256。
- 对 EXE 记录大小、SHA256、Go build tags 与 native media 依赖。
- 本任务不自动提交 release 二进制、不推送远端分支、不创建线上 Release。

## Verification

### Server And Web

- `nginx -t`、compose config 和相关 Python 测试通过。
- nginx、Screego、screen SFU、Registration Control 和 voice SFU 健康。
- 原屏幕容器 ID、镜像和启动时间保持不变。
- `80 -> 443` 跳转、Let's Encrypt 证书、`GET /`、`GET /config`、核心静态资源、
  注册/登录入口、房间 API、历史 API 与 `/sfu-ws` 保持正常。
- Playwright 复验页面标题、登录表单、关键网络请求、控制台和完整加载状态；不得新增功能性
  JavaScript 错误、资源失败或异常跳转。
- `/api/voice/token`、`/voice-ws`、voice health 和 `5054/UDP` 外网 ICE 可用。
- 双外部 Pion 客户端完成加入、协商、双向 RTP、令牌刷新、断开与资源清理。

### Client

- 配置单元测试覆盖两个旧地址迁移、新默认地址、证书 pin 清除、自定义地址保留、历史和
  偏好保留、重复加载幂等以及保存失败行为。
- `internal/gamecast` URL、WSS 与 STUN 派生测试通过。
- 运行相关 Go、native-media-tag Go、前端 Node、生产构建和打包检查。
- 新 EXE 首次启动后，本机配置精确变为 `https://partylanapp.com`，旧 pin 为空，其他字段
  未变化。
- 客户端服务器探测、登录状态提示、房间列表、屏幕 host/viewer 与语音连接没有地址混用。
- 自动测试不能替代真实 Windows 麦克风、扬声器和共享音频隔离验收；若本次环境无法提供
  多个真实音频端点，该限制必须单独报告。

## Rollback

回滚按暴露面从外向内执行：

1. 恢复 nginx 备份，`nginx -t` 后平滑 reload，立即撤销公开语音路由。
2. 恢复 Registration Control 脚本和运行环境，重启并复验原控制 API。
3. 停止并移除 voice 容器，恢复 compose 备份；不得对 `screego` service 执行 down、
   recreate 或 image replacement。
4. 保留失败日志时先脱敏；密钥不进入回滚包或报告。
5. 客户端线上门禁失败时不运行新 EXE，因此旧本机配置和旧 release 仍可继续使用。

回滚后重复网页 Playwright 基线与屏幕服务状态检查。只有网页、控制 API 和屏幕 SFU 均回到
基线才算回滚完成。

## Acceptance Criteria

任务完成必须同时满足：

- 广州节点 voice SFU、令牌、WSS 与外网 UDP 媒体路径可用。
- 新客户端默认连接 `https://partylanapp.com`，两个旧地址按规则一次性迁移。
- 其他自定义服务器和非服务器配置没有改变。
- 新客户端的 Go 原生 HTTPS/WSS 探测通过，不依赖跳过 TLS 校验。
- 网页端迁移后仍可正常访问，静态资源、账号入口、房间控制和屏幕共享路径无回归。
- 现有 Screego 屏幕容器未重建，用户、历史、证书和持久化数据未丢失。
- 自动化、浏览器、服务端、外网 Pion 和客户端构建证据分别记录，未完成的真实音频验收不被
  自动化结果替代。
