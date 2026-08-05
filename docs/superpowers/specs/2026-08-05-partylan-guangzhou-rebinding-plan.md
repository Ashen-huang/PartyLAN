# PartyLAN 广州节点完整改绑实施计划

目标：在不重建广州节点现有屏幕共享容器、不破坏网页端访问的前提下，增量部署独立语音 SFU，并将 PartyLAN 客户端的默认及两个已知旧配置迁移到 https://partylanapp.com。

设计依据：
docs/superpowers/specs/2026-08-05-partylan-guangzhou-rebinding-design.md

实施边界：本任务包含广州节点的增量部署、当前客户端源码修改、Windows 构建和验证；不修改旧节点，不推送远端 Git 分支，不创建 GitHub Release。任何服务端修改都先备份、先验证、后暴露；屏幕 SFU 5053 和 Screego 5060 不得被 down、recreate 或换镜像。

## Task 1：建立线上网页与屏幕服务基线

1. 使用 Playwright 打开 https://partylanapp.com，保存标题、页面快照、关键请求、控制台和静态资源加载结果。
2. 记录 DNS、证书主题/有效期、HTTP 80 跳转、/config、/api/rooms、/sfu-ws 的现状状态码或升级结果。
3. 通过 SSH 记录 hostname、时间、systemd 状态、监听端口、屏幕容器 ID、镜像、启动时间、compose/nginx/register 文件哈希和当前房间列表。
4. 确认 screego-register 的 EnvironmentFile 路径、运行环境文件权限和当前变量名，不输出现有密钥、密码、Authorization、SDP 或 ICE 内容。
5. 用与正式客户端相同的 Go gamecast.Client 对 https://partylanapp.com/config 做 TLS/HTTP 探测；若仍在握手阶段被重置，停止客户端迁移并单独诊断云侧路径。

门禁：Playwright 页面、网页 /config 和屏幕服务必须先达到可记录的基线；Go 探测失败不得被浏览器结果掩盖。

## Task 2：为客户端迁移建立红灯测试

文件：pkg/config/config.go、pkg/config/config_test.go、internal/gamecast/profile.go、internal/gamecast/profile_test.go、app.go；仅在能复用现有夹具时修改 app_test.go。

1. 将两个默认地址的断言改为 https://partylanapp.com。
2. 增加规范化旧地址识别测试，覆盖尾部斜杠、无 scheme、大小写主机名和非目标自定义地址。
3. 增加迁移测试：两个旧地址迁移并清除证书指纹；新地址、自定义地址、空值和非法值不误迁移。
4. 增加配置字段保留测试：display name、room history、音频/视频偏好和无关字段保持不变。
5. 增加重复启动幂等测试，确保迁移只发生一次且不反复清除新配置。
6. 增加保存失败测试，确认内存配置切换和错误记录行为明确。
7. 先运行目标测试，记录现有旧默认值导致的红灯。

## Task 3：实现客户端默认值、迁移和安全保存

1. 更新 pkg/config.DefaultConfig() 与 internal/gamecast.DefaultBaseURL 为 https://partylanapp.com。
2. 在 internal/gamecast 增加单一可测试的旧地址迁移辅助函数；比较前先调用现有 NormalizeBaseURL，只匹配 https://113.142.217.42:5050 和 https://85.137.240.159:5050。
3. 在 App.startup 的 config.Load 后、ensureGameCastJar 前调用迁移；迁移成功后清空 TrustedCertificateSHA256，保留其余配置。
4. 让配置写入使用临时文件、刷新并替换的安全保存路径，确保迁移写入失败不会产生半个 config.json；保持 Windows 与现有权限语义兼容。
5. 迁移失败时继续使用已迁移的内存配置并记录不含敏感字段的错误；不删除旧 cookie jar。
6. 运行 Task 2 目标测试，确认所有迁移分支转绿。

## Task 4：准备广州节点可回滚的增量部署包

1. 从当前仓库导出 go.mod、go.sum、server/voice-sfu/** 和已验证 Dockerfile 到带时间戳的临时目录；不把密钥、用户文件或本机配置打入包。
2. 生成构建上下文校验值，上传到广州节点独立临时目录，不覆盖 /root/screego。
3. 生成 Registration Control 语音变更的定向 patch；先对广州线上脚本做 dry-run，若上下文不匹配，改用人工审查的最小合并，禁止整份覆盖线上脚本。
4. 准备 nginx 两个 location 的最小 patch；保留广州域名、证书、80/443/5050 listen、当前静态资源版本和所有既有屏幕路由。
5. 准备 compose 只增 voice-sfu service 的 patch；保留线上当前 gamecast-sfu:udp-only-v1-20260731、TURN TCP 禁用配置和 host network。

## Task 5：构建并启动广州语音 SFU（不触碰屏幕容器）

1. 在广州节点备份 nginx site、compose、register 脚本、runtime env、systemd unit 和相关日志路径，记录备份哈希与权限。
2. 使用 openssl rand 或等价 CSPRNG 生成新的 GAMECAST_VOICE_SECRET；只追加或替换该变量，设置运行环境文件为仅 root 可读，绝不在终端回显。
3. 在独立构建目录执行 voice Docker build，记录 image ID/digest；构建失败不改变现有服务。
4. 运行 docker compose config，确认只新增 partylan-voice-sfu，然后执行目标服务启动；禁止 down、--force-recreate 或不带 service 名称的 up。
5. 验证 voice health、127.0.0.1:5054/TCP、0.0.0.0:5054/UDP、room limit=20 和无敏感日志；确认原 screego 容器 ID/启动时间未变化。

## Task 6：增量接入令牌控制面和 nginx 路由

1. 原子替换广州 register.py 的最小语音 patch，运行 py_compile 及已有 register/control 测试。
2. 重启 screego-register，立即验证 /config、/api/rooms、/api/history、注册/登录入口仍可用；控制面短暂不可用或测试失败时恢复备份，不继续改 nginx。
3. 验证无密钥回显、未授权 /api/voice/token 返回明确错误，合法 presence/auth 场景可签发 15 秒 HMAC token，刷新和过期错误符合协议。
4. 原子合并 nginx 两个语音 location，执行 nginx -t 后平滑 reload；不改屏幕 location、proxy header、TLS 文件或静态注入。
5. 验证公网 /api/voice、WSS /voice-ws、URL token 拒绝、Authorization header 接受，并确认网页首页和 /config 仍为基线结果。

## Task 7：外网语音与网页回归验收

1. 从广州节点外部启动至少两个 Pion voice client，完成同房间加入、服务端 offer/answer、ICE、双向 RTP、token refresh、断开和空房清理。
2. 若 5054/UDP 受云安全组阻断，保留网页/屏幕服务不变，记录具体端口证据并停止客户端发布；不得通过关闭 TLS 或改变屏幕端口规避。
3. 使用 Playwright 重跑首页、登录表单、/config、核心静态资源和控制台检查；保存迁移前后请求摘要和截图/快照。
4. 对比屏幕容器 ID、镜像、启动时间、nginx 证书、首页注入版本和屏幕 WebSocket route；任一变化超出计划范围即回滚并复验基线。

## Task 8：客户端完整自动验证和线上探测

```powershell
go test ./pkg/config ./internal/gamecast/...
go test .
go test ./...
go test -tags partylan_native_media ./...
$frontendTests = Get-ChildItem frontend/tests -Filter *.test.mjs | ForEach-Object FullName
node --test $frontendTests
npm.cmd --prefix frontend run build
git diff --check
```

1. 增加或运行一个默认跳过、由 PARTYLAN_LIVE_GAMECAST_URL 显式启用的 Go live probe，复用正式 TLS transport，验证新域名 /config、屏幕 WSS URL 和语音 WSS URL。
2. 运行前端与 Go 全量测试；失败时区分本次改动与既有环境/原生告警。
3. 将 pkg/version/version.go 从 0.5.4 增至 0.5.5，执行正式 native build，记录 PartyLAN.exe SHA256、Go build tags、CGO、opus.dll 和进程清理证据。
4. 在隔离临时 HOME/USERPROFILE 下启动新 EXE，验证默认配置；在备份的本机配置副本上验证两个旧地址迁移、旧 pin 清除、其他字段不变和重复启动幂等。

## Task 9：打包、交付门禁和回滚

1. 使用 Package-PartyLAN.ps1 生成 v0.5.5 portable zip；不覆盖既有 release 产物，审计 zip 白名单和 SHA256。
2. 只有 Task 1、5、6、7、8 全部通过后，才允许在本机运行迁移后的客户端；Go live probe 失败时保留旧客户端配置和旧 release。
3. 记录网页、服务端、外网 Pion、客户端自动化和构建证据，明确真实 Windows 麦克风/扬声器多端验收是否完成；不以 synthetic RTP 代替真实音频验收。
4. 任何回归按顺序恢复 nginx、register、compose/voice 容器；不得停止或重建屏幕 SFU，回滚后再次执行 Playwright 与屏幕路由基线。

## STOP 条件

- Go TLS/WSS 对新域名仍在握手阶段被重置。
- nginx -t、控制 API 回归或网页 Playwright 基线失败。
- compose 目标启动会重建现有屏幕容器，或线上配置 patch 上下文不匹配。
- 云安全组无法证明 5054/UDP 外网可达。
- 需要输出或提交真实密钥、账号密码、cookie 或原始媒体/信令日志。
- 真实客户端迁移需要强制覆盖非目标自定义服务器地址。

## Completion Criteria

- 广州 voice SFU、令牌控制面、WSS 和外网 UDP 媒体验证通过。
- partylanapp.com 网页访问、账号入口、房间 API、屏幕 SFU 和静态资源无回归。
- 新客户端默认使用广州域名，两个已知旧地址一次性迁移，其他配置保持不变。
- v0.5.5 native Windows 包完成构建、哈希和白名单审计。
- 工作区只包含本任务批准范围内的源码、测试、版本和文档变更；远端服务状态与回滚证据已记录。
