# PartyLAN GameCast 客户端重构设计

> Scope: 将 PartyLAN 从“自研 P2P 屏幕共享平台”收束为“GameCast/Screego 原生客户端”，复用已经验证成功的服务端协议，同时保留 PartyLAN 已经沉淀的桌面客户端资产。

## 1. 背景

PartyLAN 最初目标很完整，也很重：做一个 Windows 原生协作客户端，集成虚拟局域网、高性能屏幕共享、独立音频、语音聊天和私有中继能力。当前仓库已经包含：

- Wails + React 桌面 UI
- Go 后端
- Tailscale / tsnet 虚拟网络
- 自定义房间控制协议，涉及 `9000`、`9001`、`9002` 端口
- DXGI / NVENC 原生采集编码实验
- Pion WebRTC 媒体面
- Windows 进程级音频捕获实验
- 语音聊天脚手架
- DERP / TURN 诊断与启动脚本

这些工作不是没有价值。问题在于，它让 PartyLAN 同时承担了身份、组网、NAT 穿透、房间协议、采集、编码、媒体传输、中继选路、音频路由、播放、诊断、打包和 UI。这个复杂度已经超出了“把成功的网页端客户端化”的范围。

与此同时，已经部署成功的 GameCast/Screego 网页端证明了另一条更稳的路线：

- Screego 保留房间、会话、认证和基础后端能力。
- nginx 暴露 `/api/register`、`/api/history`、`/api/stats`、`/api/room-state`、`/sfu-ws` 和 Screego 后端。
- `inject.js` 与 `sfu-client.js` 已经能把网页端接入 GameCast SFU。
- 新服务器 `https://113.142.217.42:5050` 已迁移完成，并验证返回 `HTTP 200`。
- 服务端媒体链路已经足够稳定，可以作为原生客户端的协议基线。

关键结论：原生客户端方向没有错，错的是客户端过早替换了已经成功的服务端协议。

## 2. 现状诊断

PartyLAN 应被视为“旧架构失败 demo”，而不是“产品想法失败”。

从代码、设计文档和日志看，失败点集中在架构层：

- 早期自定义 TCP / zlib / JPEG 媒体路径只有 `0.x-3fps` 左右，达不到可用屏幕共享基线。
- 后续 WebRTC 方向是正确的，但仍绑定 PartyLAN 自己的房间协议、Tailscale 路径和 P2P 假设。
- 代码中已经明确限制：嵌入式 `tsnet` 后端不能直接支撑 WebView2 的 WebRTC 播放，因为 WebView2 看不到内嵌 tsnet 网络路径。
- 日志中存在大量 room probe / dial timeout，即使 DERP 诊断能看到 `cn1` 或 `sfo` relay。
- 进程级音频捕获不稳定，日志出现过 `ActivateAudioInterfaceAsync failed: 0x8000000e`。
- 项目同时负责控制面和媒体面，而 GameCast/Screego 服务端已经有一条可用控制面和 SFU 入口。

因此，继续修补 PartyLAN 原来的 P2P / tsnet / custom-room 架构，会持续和已经跑通的 GameCast/Screego 架构竞争。

## 3. 总体决策

PartyLAN 不再作为独立 P2P 屏幕共享栈继续推进。

PartyLAN 重构为 GameCast 原生客户端：

- 服务端协议成为事实标准。
- 现有 GameCast/Screego 部署继续承担房间、认证和媒体分发。
- PartyLAN 转为用户入口、桌面 UI、客户端状态管理、采集控制和后续原生能力承载。
- 原 PartyLAN 自定义房间协议和媒体协议从默认路径退休。

这是救援式重构，不是删除项目。保留有价值的桌面客户端资产，放弃和成功服务端协议冲突的部分。

## 4. 目标

- 用户打开桌面客户端即可使用，不再手动输入网页地址。
- 正常客户端体验中不出现浏览器证书警告页。
- 复用已经成功的 GameCast/Screego 服务端端点和 SFU 行为。
- 保留网页端作为备用入口。
- 优先复用 Wails + React 客户端壳。
- 复用现有 UI、配置、日志、诊断、打包、原生采集和音频实验资产。
- 先让屏幕观看和共享稳定，再恢复高级原生能力。
- 语音聊天、进程音频、快捷键、托盘、自动更新等私有化功能放到稳定连接之后。

## 5. 非目标

- 不继续把 PartyLAN 自定义 P2P 房间协议作为主线。
- 第一阶段不要求 Tailscale、tsnet、DERP 或 tailnet policy。
- 不继续维护 TCP `9000` / `9001` / `9002` 作为生产共享模型。
- 客户端没有接通 `/sfu-ws` 前，不新建 SFU。
- 不在 PartyLAN 内重写 Screego 的完整房间和认证系统。
- 第一阶段不以进程级音频捕获为阻塞条件。
- 不删除已工作的网页入口。

## 6. 方案对比

### 6.1 方案 A：继续修原 PartyLAN 架构

继续推进 Tailscale / tsnet、自定义房间协议、Pion WebRTC、原生编码和自建中继诊断。

优点：

- 保留最多现有代码。
- 保留“完整自研 LAN / tailnet 协作工具”的长期想象空间。
- Go / Pion 层控制力强。

缺点：

- 风险最高。
- 保留了造成 demo 失败的协议错位。
- 项目仍要同时承担组网、relay、信令、媒体和 UI。
- 无法直接吃到 GameCast/Screego 网页端已验证成功的成果。

结论：不作为主线继续。

### 6.2 方案 B：全新 Electron 客户端

抛弃 PartyLAN 代码，重新做 Electron 客户端并接入 GameCast/Screego。

优点：

- 干净。
- 和现有 `sfu-client.js`、WebRTC、浏览器媒体 API 最接近。
- Electron `desktopCapturer` 对屏幕/窗口选择更直接。

缺点：

- 丢掉 PartyLAN 已有 UI、日志、脚本、安装包和原生实验。
- 引入第二套应用技术栈。
- 运行体积更大。

结论：可作为备选，不作为第一推荐。

### 6.3 方案 C：救援 PartyLAN 为 GameCast 客户端

保留 PartyLAN 仓库和桌面客户端壳，替换默认连接路径，让它接入 GameCast/Screego 服务端。

优点：

- 复用已有 PartyLAN UI 与打包资产。
- 停止继续投资不稳定的自定义房间/媒体协议。
- 对齐已经成功的网页端部署。
- 后续仍能把原生采集、音频和语音能力接回来。
- 保留网页端 fallback。

缺点：

- 需要在 Go/Wails 与 GameCast Web 协议之间建立明确兼容层。
- 旧模块要冻结或迁出默认路径，否则会继续制造混乱。
- Wails/WebView2 在高级捕获能力上可能不如 Electron 直接。

结论：推荐采用。

## 7. 推荐架构

重构期间 PartyLAN 保留两种模式：

- `legacy-partylan`：当前自研 PartyLAN 栈，仅通过显式开发开关启用。
- `gamecast-client`：新的默认模式，连接 GameCast/Screego 服务端。

新架构：

```text
PartyLAN 桌面客户端
  React UI
  Wails Go 后端
  服务器配置
  登录 / 会话
  房间列表 / 房间控制
  证书信任
  诊断 / 日志
  原生采集实验能力
        |
        | HTTPS / WebSocket
        v
GameCast/Screego 服务端
  nginx :5050
  Screego backend :5060
  register/history/stats/room-state service :5052
  GameCast SFU WebSocket :5053 via /sfu-ws
        |
        v
网页观看端 / 原生客户端
```

客户端第一步要复现成功网页端已经做到的行为：

- 使用 Screego 会话和认证。
- 读写历史房间。
- 发布和读取 room state。
- 连接 `/sfu-ws`。
- 以 viewer 或 host 角色加入房间。
- 展示连接状态和媒体诊断。

只有这条基线稳定后，才让原生采集替代浏览器 `getDisplayMedia`。

## 8. 协议边界

GameCast 服务端协议是 PartyLAN 新主线必须遵守的边界。

初始端点：

- `GET /`
- `POST /api/register`
- `GET /api/history`
- `POST /api/history`
- `GET /api/stats`
- `POST /api/stats`
- `GET /api/room-state?room=<id>`
- `POST /api/room-state`
- `WebSocket /sfu-ws?room=<room>&role=<host|viewer>[&layer=<q|h|f>]`

客户端初始行为：

- server profile 保存 base URL、显示名和证书信任信息。
- 登录/会话尽量复用 Screego 现有 cookie/session。
- room state 使用与 `inject.js` 一致的语义：active、sharing、sfu、audio mode、user、ttl。
- `/sfu-ws` 消息格式先从 `sfu-client.js` 复制和整理，不先发明新格式。
- 客户端不得再创建第二套房间状态模型。

## 9. 证书与信任策略

原生客户端要消除浏览器安全警告页，但不能静默削弱 TLS。

支持三种信任模式：

- 公网域名模式：服务端使用正常可信证书，推荐后续接 Let's Encrypt。
- 私有服务器模式：首次连接时显示明确提示，用户确认后固定证书指纹。
- 开发模式：只允许在明显开发开关下放宽证书校验，并写入日志。

对当前 IP-only + 自签证书部署，私有服务器模式是合理过渡方案。客户端保存证书指纹后，如果证书变化，应阻止连接并提示用户重新确认。

## 10. 模块重构计划

### 10.1 保留

保留并复用：

- `frontend/src` 的布局、组件、样式和多语言模式。
- `pkg/config`，扩展 server profile 和证书信任配置。
- `pkg/diaglog` 日志系统。
- build / release 脚本。
- Wails 绑定基础。
- 房间控制、设置、状态栏、诊断面板、音频面板、成员列表等 UI 概念。
- 原生视频和音频包，作为实验能力保留，不作为第一阶段生产路径。

### 10.2 冻结

第一阶段冻结以下模块，不继续扩展：

- `internal/video`
- `internal/audio`
- `internal/native/windowsaudio`
- `internal/native/windowsmedia`
- `internal/voice`
- `internal/media`

这些模块等客户端可靠接通 GameCast 服务端协议后，再决定如何回归。

### 10.3 从默认路径退休

以下模块不再进入默认运行路径：

- `internal/network` 中的 Tailscale / tsnet / LAN 模式
- `internal/room` 自定义房间协议
- `StreamSender` / `StreamReceiver`
- `9000` / `9001` / `9002` 控制、探测和视频模型
- 以直连 P2P fan-out 作为产品主路径的设计

它们可以暂时保留在 legacy 模式和测试中，避免一次性删除导致回滚困难。

### 10.4 新增

新增模块建议：

- `internal/gamecast`
  - server profile
  - HTTP client
  - cookie jar / session handling
  - certificate pinning
  - endpoint models
- `internal/gamecast/ws`
  - `/sfu-ws` 连接
  - 消息编解码
  - 重连策略
- `internal/gamecast/roomstate`
  - room state 读写
  - TTL heartbeat
- `frontend/src/gamecast`
  - server profile、登录、房间和连接状态
- `frontend/src/media/gamecastReceiver.ts`
  - 以当前 `sfu-client.js` 语义实现 viewer 端 WebRTC 播放

## 11. 第一里程碑：原生 Viewer 壳

第一阶段不做 native host capture。

验收目标：

- 应用打开后是客户端入口，不是旧 PartyLAN 房间入口。
- 用户可以使用内置或手动配置的 GameCast 服务器地址。
- 用户可以一次性信任私有服务器证书。
- 用户可以登录或复用 session。
- 用户可以输入或选择房间。
- 客户端能以 viewer 身份连接 `/sfu-ws`。
- 远端视频能在桌面客户端中播放。
- 诊断显示 WebSocket 状态、WebRTC 状态、接收层级和基础接收统计。

viewer-first 是为了先验证服务端协议，不把原生采集复杂度过早带回来。

## 12. 第二里程碑：Web 兼容 Host

第二阶段实现 host，但仍以最小偏离成功网页端为原则。

可接受的第一版：

- 如果 Wails/WebView2 能支撑提示流程，则先使用 WebView2 内的 `getDisplayMedia`。
- 以 `role=host` 连接 `/sfu-ws`。
- 发布 `/api/room-state` 心跳。
- 使用与 `sfu-client.js` 相同的 simulcast / 单轨逻辑。
- 在 PartyLAN UI 中暴露画质预设。

这不是最终的原生采集目标，而是兼容性检查点。如果这一阶段不稳定，说明客户端还没有忠实复现成功网页端。

## 13. 第三里程碑：原生 Host Capture

viewer 和 web-compatible host 稳定后，再让原生采集能力回归。

原生 host capture 应满足：

- 尽量复用现有 DXGI / Windows capture 实验。
- 产出 WebRTC 可发送的视频轨。
- 不随意修改 `/sfu-ws` 服务端协议。
- 保持网页观看端兼容。
- 保留浏览器捕获作为 fallback。

这一阶段需要根据实际结果选择：

- 继续 Wails，并把原生帧桥接到 WebRTC sender。
- 在客户端内嵌受控 Chromium/WebRTC sender。
- 如果 Wails/WebView2 成为瓶颈，拆出 Electron 或 Rust helper 作为 host 采集端。

决策应在前两个里程碑完成后进行。

## 14. 第四里程碑：语音聊天

语音聊天应作为房间能力追加，而不是先回到旧 PartyLAN 语音通道。

建议模型：

- 屏幕共享媒体和语音媒体分开。
- 语音使用 WebRTC Opus audio track。
- 支持静音、按键说话、输入设备、输出设备、音量和基础 VAD。
- 不以进程级音频捕获作为前置条件。
- 先保证原生客户端之间可用，再考虑网页 fallback。

现有 `internal/voice` 和音频 UI 可以参考，但需要重新对齐 GameCast 服务端模型。

## 15. 迁移策略

重构按增量推进：

1. 新增 `gamecast-client` 模式，不删除 legacy 代码。
2. 开发默认入口切到 `gamecast-client`。
3. legacy PartyLAN 模式放到显式开关后面。
4. 完成 viewer-first 路径。
5. 完成 web-compatible host 路径。
6. 再决定原生采集留在 Wails 内，还是拆出 Electron/Rust helper。
7. GameCast client 成为默认可测路径后，再归档或删除旧 P2P 模块。

这样可以避免再次变成“大重写”，每一步都有可运行检查点。

## 16. 测试策略

单元测试：

- server profile 归一化
- 证书指纹保存和变更拒绝
- endpoint URL 构造
- room state payload 编解码
- `/sfu-ws` 消息解析
- 重连退避策略

集成测试：

- 连接本地 fake GameCast HTTP server
- room-state heartbeat 行为
- WebSocket 重连
- cookie/session 处理
- 测试证书下的 pinning 行为

手工验收：

- 使用内置服务器地址启动客户端。
- 首次连接时信任服务器证书。
- 登录。
- 作为 viewer 加入现有房间。
- 接收当前网页 host 的共享流。
- 从客户端启动 web-compatible host share。
- 网页 viewer 能观看客户端 host。
- 重启客户端后，服务器配置和信任状态仍保留。

## 17. 风险

- Wails/WebView2 可能不足以支撑足够好的 host 捕获体验。
- Screego 的认证/session 行为可能需要细致 cookie 处理。
- 当前服务端是 IP + 自签证书，证书 pinning 必须谨慎实现。
- 把 `sfu-client.js` 语义移出网页时，可能遇到 DOM、cookie、房间 URL 或注入脚本假设。
- legacy 模块保留太久会让开发者误判主线，因此默认模式和命名必须清晰。

## 18. 成功标准

重构成功的判断：

- PartyLAN 打开后是 GameCast 桌面客户端。
- 用户正常使用时不再手动输入 `https://113.142.217.42:5050`。
- 用户正常使用时不再看到浏览器证书警告页。
- viewer 模式可以稳定观看现有 GameCast 服务端房间。
- host 模式可以发布网页 viewer 能观看的共享流。
- 旧 PartyLAN 自定义 P2P 路径不再出现在默认用户旅程中。
- 后续语音聊天和原生采集可以建立在稳定服务端协议之上。

## 19. 推荐下一步

为第一里程碑“原生 Viewer 壳”写实施计划。

实施计划只覆盖：

- 新增 `gamecast-client` 模式。
- server profile 与证书信任。
- HTTP/session client。
- room state 读取。
- `/sfu-ws` viewer 连接。
- 当前 React UI 内的 WebRTC viewer 播放。

不要从原生采集、进程音频、语音聊天、Tailscale 或 legacy room networking 开始。
