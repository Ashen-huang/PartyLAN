# PartyLAN GameCast 客户端功能完整化设计

> Scope: 在已经完成的 GameCast Viewer MVP 之上，补齐 host 共享、原生采集、语音聊天、应用音频、登录/注册/session、房间管理、生产级打包和端到端实机验收。

## 1. 背景

当前 PartyLAN 已经从旧的自研 P2P 屏幕共享 demo 转向 GameCast/Screego 原生客户端。第一阶段已经建立了新的主线：

- 默认模式是 `gamecast-client`。
- 客户端可以配置 GameCast 服务器。
- Go 后端可以探测服务器、读取 `/config`、读取 `/api/history`、读取 `/api/room-state`。
- 客户端可以固定信任私有服务器证书指纹。
- Go 后端可以连接 `/sfu-ws`。
- React/WebView2 可以作为 viewer 处理 SFU `offer`、发送 `answer`、转发 ICE，并播放远端画面。

这说明重构方向已经跑通了第一条关键链路：客户端不再依赖旧 PartyLAN 自定义房间协议，而是复用已经成功部署的 GameCast/Screego 服务端。

接下来的目标不是把旧功能原样搬回来，而是把旧功能重新接到 GameCast 协议边界上。

## 2. 总体原则

后续功能必须遵守这些原则：

- GameCast/Screego 服务端协议仍是事实标准。
- 网页端继续作为兼容入口和回退入口。
- 每个阶段都必须有可运行检查点。
- 不把旧 `internal/room`、`9000/9001/9002`、Tailscale/tsnet 路径重新变成默认主线。
- 不在客户端内部复制 Screego 的完整后端。
- 先完成 Web 兼容 host，再恢复原生 host。
- 屏幕共享、共享应用音频、语音聊天三条媒体能力要分层，不混成一个不可调试的大通道。
- 客户端能力增加时，必须保持网页 viewer 可观看客户端 host。

## 3. 推荐实施顺序

推荐顺序：

1. 登录、注册、session 和房间管理基础。
2. Web 兼容 host 屏幕共享。
3. 生产可用的 host 控制和房间状态。
4. 原生 DXGI/NVENC host capture。
5. 应用/进程级音频共享。
6. 语音聊天。
7. 生产级打包和端到端验收。

这个顺序的理由：

- 登录和房间管理是 host 的入口。
- Web 兼容 host 是验证服务端协议的最低风险方式。
- 原生采集必须建立在 host 协议已经稳定的前提下。
- 应用音频和语音都是音频媒体能力，但用途不同，不能一起做。
- 打包和验收应贯穿每个阶段，但最后需要单独作为发布门槛。

## 4. 非目标

本 spec 不做这些事：

- 不恢复旧 PartyLAN P2P 协议为默认路径。
- 不把 Tailscale/tsnet 作为客户端正常使用的前置条件。
- 不要求第一版 host 就使用原生 DXGI/NVENC。
- 不为了语音聊天破坏现有屏幕共享 SFU 协议。
- 不要求网页端同时具备所有原生客户端能力。
- 不在没有实机验收前声明生产可用。

## 5. 功能一：登录、注册和 Session 管理

### 5.1 目标

用户打开 PartyLAN 后，可以在客户端内完成服务器连接、登录、注册、session 复用和退出登录，不再跳到浏览器手动处理。

### 5.2 范围

客户端需要支持：

- 服务器 profile 管理。
- 证书信任状态显示。
- 登录状态显示。
- 用户名/密码登录。
- 注册账号。
- 退出登录。
- session cookie 持久化。
- session 失效检测。
- 登录成功后同步房间历史。

### 5.3 协议策略

优先使用服务端已有 JSON endpoint：

- `GET /config` 用于读取当前 session 状态。
- `POST /api/register` 用于注册。
- `GET /api/history` 和 `POST /api/history` 用于历史同步。

如果 Screego 登录没有稳定 JSON API，则采用两阶段策略：

1. 客户端内打开受控登录 WebView，复用 Screego 原登录表单，Go 后端从 WebView/cookie jar 接收 session cookie。
2. 后续在 GameCast 服务端补一个薄 JSON 登录 endpoint，让原生客户端不再依赖 DOM 表单。

第一阶段不抓取网页 DOM 作为登录成功依据，只以 `/config` 返回的 `loggedIn` 和 `user` 作为最终判断。

### 5.4 模块

新增或扩展：

- `internal/gamecast/session`
  - cookie jar 持久化
  - 登录状态探测
  - session 失效判断
- `internal/gamecast/auth`
  - register client
  - login adapter
  - logout adapter
- `frontend/src/gamecast/auth`
  - 登录表单
  - 注册表单
  - session 状态
  - 证书信任提示

### 5.5 验收

- 客户端首次打开能显示未登录状态。
- 注册新用户后可以登录。
- 关闭并重启客户端后 session 仍保留。
- session 失效后 UI 明确提示重新登录。
- 证书指纹变化时阻止登录和连接。

## 6. 功能二：自动创建房间和完整房间管理

### 6.1 目标

用户不需要手动去网页端创建或复制房间链接。客户端可以生成房间、加入房间、保存历史、重新进入历史房间，并显示房间当前共享状态。

### 6.2 房间模型

房间仍以 Screego/GameCast room id 为核心，不新增第二套房间 ID。

客户端房间状态由三部分组成：

- Screego session 当前用户。
- 本地历史记录。
- `/api/room-state` 中的 GameCast 活跃状态。

当用户点击“创建房间”时，客户端生成一个可读 room id，进入 host 准备态。房间在 host 连接 `/sfu-ws` 并发布 room-state 后成为活跃房间。

### 6.3 UI 能力

房间管理 UI 包括：

- 新建房间。
- 输入房间 ID 加入。
- 历史房间列表。
- 删除历史房间。
- 复制房间 ID。
- 复制网页观看链接。
- 显示房主、共享状态、音频模式、最后更新时间。
- 显示“网页备用入口”按钮。

### 6.4 模块

新增或扩展：

- `internal/gamecast/rooms`
  - room id 生成
  - history merge
  - room-state polling
  - room-state heartbeat 协调
- `frontend/src/gamecast/rooms`
  - 房间列表
  - 新建/加入表单
  - 房间状态标签
  - 分享链接操作

### 6.5 验收

- 客户端可以创建新的 room id。
- 创建后可以直接进入 host 准备态。
- viewer 可以从历史中重新加入房间。
- room-state 离线、在线、共享中三种状态显示清楚。
- 网页端打开复制的链接可以进入同一个房间。

## 7. 功能三：客户端主动作为 Host 发起屏幕共享

### 7.1 目标

客户端可以作为 host 发起屏幕共享，网页 viewer 和原生 viewer 都能观看。

### 7.2 第一版方案：Web 兼容 Host

第一版 host 使用 WebView2 中的 WebRTC 和屏幕捕获能力，尽量复刻 `sfu-client.js` 的 host 逻辑。

行为：

- 用户点击“开始共享”。
- 客户端选择屏幕/窗口。
- WebView2 调用 `getDisplayMedia` 获取 `MediaStream`。
- 前端创建 `RTCPeerConnection`。
- 以 `role=host` 连接 `/sfu-ws`。
- 发送 `{ type: "offer", sdp }`。
- 接收 `{ type: "answer", sdp }`。
- 转发 `{ type: "ice-candidate", ice }`。
- 发布 `/api/room-state` heartbeat。
- 停止共享时发布 inactive state 并关闭 PeerConnection。

### 7.3 画质设置

host 第一版支持：

- 自动。
- 720p。
- 1080p。
- 原始分辨率。
- 30fps。
- 45fps。
- 60fps。
- 清晰优先。
- 均衡。
- 流畅优先。

这些设置只映射到浏览器 capture constraints、sender parameters 和 simulcast encodings，不修改服务端协议。

### 7.4 兼容要求

必须保持：

- 网页 viewer 可以观看客户端 host。
- 原生 viewer 可以观看客户端 host。
- 网页 host 和原生 host 可以使用同一个 `/sfu-ws` 协议。

### 7.5 验收

- 客户端可以创建房间并开始共享。
- 网页 viewer 可以观看客户端共享。
- 原生 viewer 可以观看客户端共享。
- 停止共享后 viewer 端能收到停止状态。
- 主机断线后 room-state 在 TTL 后不再显示共享中。

## 8. 功能四：原生 DXGI/NVENC Host Capture

### 8.1 目标

在 Web 兼容 host 稳定后，恢复 Windows 原生采集和硬件编码，让客户端具备更高性能、更少浏览器限制的 host 能力。

### 8.2 推荐方案：Go Pion Native Host Adapter

推荐优先使用 Go 后端实现原生 host WebRTC adapter：

- 复用 `internal/native/windowsmedia` 的 DXGI/NVENC 能力。
- 复用 `internal/video` 的 capture/encoder 抽象。
- 使用 Pion 创建 host `PeerConnection`。
- 连接同一个 `/sfu-ws?role=host`。
- 向 SFU 发送标准 `offer`。
- 接收 `answer` 和 ICE。
- 发送 H.264 RTP track。
- PLI/FIR 到达时请求关键帧。

这样可以避免把原生帧桥接到 WebView2，再由 JS 重新编码。

### 8.3 备选方案

如果 Pion native host 与 GameCast SFU 协商不稳定，使用受控 helper：

- Electron helper：用 Chromium `desktopCapturer` 和 WebRTC sender。
- Rust/C++ helper：负责捕获和编码，Go 负责 UI 与控制。

备选方案只在 Pion native host 实机失败后启用。

### 8.4 协议约束

原生 host 不新增 SFU 协议。它必须发送与网页 host 等价的消息：

- `{ type: "offer", sdp }`
- `{ type: "ice-candidate", ice }`

它必须能处理：

- `{ type: "answer", sdp }`
- `{ type: "ice-candidate", ice }`
- `{ error }`

### 8.5 回退策略

原生 host 失败时，客户端必须允许用户回退到 Web 兼容 host。

回退条件：

- GPU 不支持。
- NVENC 初始化失败。
- 编码器持续失败。
- SFU 协商失败。
- 连续关键帧请求无法恢复画面。

### 8.6 验收

- 1080p30 原生 host 可以稳定共享 20 分钟。
- viewer 端可以看到连续画面和基础统计。
- 网页 viewer 与原生 viewer 均兼容。
- 原生 host 失败时 UI 能切换 Web host。
- 日志能区分 capture、encode、packetize、webrtc、sfu 五类问题。

## 9. 功能五：应用/进程级音频共享

### 9.1 目标

host 共享屏幕时，可以选择是否共享声音，并优先支持“只共享指定应用/进程音频”，避免把整个系统声音和语音通话一起发给观众。

### 9.2 音频模式

支持三种模式：

- 关闭音频。
- 窗口/应用音频。
- 系统音频。

第一优先级是应用/进程音频。系统音频作为可选能力，并在 UI 中提示回声风险。

### 9.3 Web 兼容 Host 路径

Web 兼容 host 中：

- 优先使用 `getDisplayMedia` 自带 audio track。
- 如果浏览器/WebView2 不支持特定应用音频，则显示能力受限。
- 不为了 Web host 强行注入旧 Windows process loopback。

### 9.4 原生 Host 路径

原生 host 中：

- 复用 `internal/native/windowsaudio` 的进程音频枚举。
- 复用 `internal/audio` 的 Opus 编码能力。
- 将音频作为独立 Opus track 加入 host PeerConnection。
- 音频 track 与屏幕视频 track 共享同一个 host 会话。
- 停止共享时同时停止音频捕获。

### 9.5 UI

音频 UI 包括：

- 音频模式选择。
- 活跃应用列表。
- 单选应用音频源。
- 音量电平。
- 静音开关。
- 回声风险提示。

### 9.6 验收

- host 可以关闭音频。
- host 可以选择一个正在发声的应用。
- viewer 能听到共享应用音频。
- host 停止共享后音频捕获释放。
- 应用退出或音频源消失时 UI 能提示并自动关闭音频 track。

## 10. 功能六：语音聊天

### 10.1 目标

房间成员可以通过客户端进行低延迟语音聊天。语音聊天是房间通信能力，不是屏幕共享音频的替代品。

### 10.2 与共享音频的边界

语音聊天：

- 来自麦克风。
- 面向房间成员交流。
- 支持静音、按键说话、输入输出设备选择。

共享应用音频：

- 来自被共享应用或系统。
- 面向观看内容。
- 随屏幕共享开始和停止。

两者必须在 UI、状态、媒体 track 和日志中分开。

### 10.3 服务端策略

语音需要服务端支持房间内多方音频转发。推荐新增一个小的 GameCast voice 协议扩展，而不是复用旧 PartyLAN 语音通道。

可接受的服务端形态：

- `/voice-ws?room=<room>` 独立语音信令。
- 或 `/sfu-ws?room=<room>&role=voice` 作为 SFU 的语音角色。

选择标准：

- 不破坏现有 screen share host/viewer。
- 可以单独关闭语音功能。
- 可以独立记录和诊断。

### 10.4 客户端能力

第一版语音支持：

- 加入语音。
- 离开语音。
- 麦克风静音。
- 按键说话。
- 输入设备选择。
- 输出设备选择。
- 本地音量。
- 远端成员音量。
- 基础 VAD 状态。

第一版不做：

- 录音。
- 变声。
- 空间音频。
- 复杂降噪。

### 10.5 模块

新增或扩展：

- `internal/gamecast/voice`
  - voice signaling
  - peer/session lifecycle
  - Opus track handling
- `internal/voice`
  - VAD 和状态机复用
  - 去掉对旧 room 协议的默认依赖
- `frontend/src/gamecast/voice`
  - 语音按钮
  - 设备选择
  - 成员语音状态

### 10.6 验收

- 两个原生客户端在同一房间可以语音通话。
- 屏幕共享开启和关闭不影响语音连接。
- 语音静音状态会同步到成员列表。
- 语音断线会自动重连或给出明确错误。
- 没有麦克风权限时 UI 给出可操作提示。

## 11. 功能七：生产级打包

### 11.1 目标

生成可以交给普通用户安装和运行的 Windows 客户端。

### 11.2 打包要求

打包产物包括：

- `PartyLAN.exe`
- NSIS installer。
- zip portable 包。
- `opus.dll`。
- 版本信息。
- 默认 `gamecast-client` 模式。
- 默认 GameCast 服务器 profile。
- release notes。
- SHA256 校验文件。

### 11.3 安装体验

安装版需要：

- 创建开始菜单快捷方式。
- 创建桌面快捷方式可选。
- 支持卸载。
- 保留用户配置。
- 升级安装不删除 session 和证书信任。

### 11.4 运行时要求

运行时需要：

- 首次启动显示服务器连接状态。
- 配置保存在用户目录。
- 日志写入 `%APPDATA%\PartyLAN\logs`。
- 崩溃或启动失败时保留诊断信息。
- 不要求用户安装 Node、Go、Wails 或 Tailscale。

### 11.5 验收

- 干净 Windows 设备上可以安装运行。
- 没有开发环境也能打开客户端。
- 重启电脑后客户端配置仍保留。
- 卸载后程序文件被移除。
- 升级安装后历史、session 和证书 pin 仍存在。

## 12. 功能八：端到端实机验收

### 12.1 目标

每个阶段完成后都必须用真实客户端、真实服务器、真实网页端做验收。仅通过单元测试和本机构建不能宣布功能完成。

### 12.2 环境矩阵

最低验收环境：

- Windows host 客户端。
- Windows viewer 客户端。
- 网页 viewer。
- 已部署 GameCast/Screego 服务器。
- IP + 私有证书模式。
- 公网可信证书模式。

### 12.3 核心场景

必须通过：

- 首次启动并信任私有证书。
- 注册账号。
- 登录。
- 创建房间。
- 客户端 host 开始共享。
- 原生客户端 viewer 观看。
- 网页 viewer 观看。
- host 停止共享。
- viewer 重新加入。
- 客户端重启后 session 保留。
- 证书变化时阻止连接。
- 网络断开后给出明确状态。

### 12.4 媒体场景

必须通过：

- Web host 720p30。
- Web host 1080p30。
- 原生 host 1080p30。
- host 共享应用音频。
- 两个客户端语音通话。
- 屏幕共享和语音同时开启。
- host 切换音频模式。
- viewer 全屏观看。

### 12.5 指标

验收记录以下指标：

- 首帧时间。
- 平均 decoded fps。
- 平均码率。
- 丢包。
- jitter。
- 断线恢复时间。
- CPU 使用率。
- GPU 编码状态。
- 内存增长。
- 日志错误数量。

### 12.6 验收结论

只有满足以下条件才可称为生产可用：

- 关键场景全部通过。
- 连续 30 分钟共享无崩溃。
- 网页 viewer 与原生 viewer 都可用。
- host 失败有回退路径。
- 日志能定位问题层级。
- 安装包可在干净 Windows 设备上运行。

## 13. 架构总览

完整化后的目标架构：

```text
PartyLAN Client
  React UI
    auth / rooms / host controls / viewer / voice / diagnostics
  Wails Go Backend
    gamecast profile
    cert pinning
    session jar
    room-state heartbeat
    sfu signaling
    native capture adapter
    process audio adapter
    voice adapter
  WebView2 WebRTC
    viewer playback
    web-compatible host
  Pion WebRTC
    native host capture
    native audio track
        |
        | HTTPS / WSS
        v
GameCast/Screego Server
  Screego room/session
  register/history/stats/room-state
  screen SFU
  voice relay or voice SFU extension
        |
        v
Web Viewer / Native Viewer
```

## 14. 模块规划

### 14.1 已有模块继续使用

- `internal/gamecast`
- `internal/gamecast/ws`
- `frontend/src/media/gamecastReceiver.ts`
- `pkg/config`
- `pkg/diaglog`
- `internal/native/windowsmedia`
- `internal/native/windowsaudio`
- `internal/video`
- `internal/audio`
- `internal/voice`

### 14.2 新增模块

- `internal/gamecast/session`
- `internal/gamecast/auth`
- `internal/gamecast/rooms`
- `internal/gamecast/host`
- `internal/gamecast/nativehost`
- `internal/gamecast/audio`
- `internal/gamecast/voice`
- `frontend/src/gamecast/auth`
- `frontend/src/gamecast/rooms`
- `frontend/src/gamecast/host`
- `frontend/src/gamecast/audio`
- `frontend/src/gamecast/voice`
- `frontend/src/media/gamecastHost.ts`

### 14.3 Legacy 模块处理

旧模块保留但不进入默认路径：

- `internal/room`
- `internal/network`
- 旧 `StreamSender`
- 旧 `StreamReceiver`
- 旧 `StartSharing` 的 P2P 房间路径

当 GameCast host 和 voice 均稳定后，再决定是否删除或归档这些模块。

## 15. 测试策略

### 15.1 单元测试

覆盖：

- session cookie 保存和恢复。
- certificate pinning。
- room id 生成。
- room-state heartbeat payload。
- host signaling message。
- WebRTC sender state machine。
- native host fallback decision。
- audio source selection。
- voice state machine。

### 15.2 集成测试

覆盖：

- fake GameCast HTTP server。
- fake `/sfu-ws` server。
- auth/session flow。
- room history merge。
- room-state TTL。
- host offer/answer/ICE 信令。
- voice signaling。

### 15.3 前端测试

覆盖：

- 登录表单状态。
- 房间创建/加入 UI。
- host 控制状态。
- 证书信任提示。
- 音频模式选择。
- 语音按钮状态。
- 错误提示。

### 15.4 手工实机验收

每个里程碑必须有手工验收记录，记录服务器、客户端版本、网络环境、结果和日志路径。

## 16. 风险和控制

### 16.1 WebView2 Host 风险

风险：WebView2 的 `getDisplayMedia` 行为可能受 Windows、WebView2 runtime 或权限策略限制。

控制：Web host 是协议验证阶段，不作为最终性能目标；失败时不阻塞 native host 研究。

### 16.2 Native Host 协商风险

风险：Pion native host 生成的 SDP 与 GameCast SFU 当前期望不完全一致。

控制：先做小型 fake SFU 和真实 SFU 协商测试，只在网页 viewer 兼容后继续性能优化。

### 16.3 音频回声风险

风险：共享系统音频和语音聊天同时开启会产生回声。

控制：默认推荐应用音频或关闭共享音频；系统音频显示明确警告。

### 16.4 语音协议风险

风险：语音能力需要服务端扩展，可能影响现有 screen SFU。

控制：语音使用独立 endpoint 或独立 role，并提供服务端开关。

### 16.5 打包依赖风险

风险：原生音频、NVENC、Opus、WebView2 runtime、NSIS 依赖可能导致不同机器行为不同。

控制：打包阶段建立干净 Windows 验收和依赖检查。

## 17. 里程碑和退出条件

### 17.1 M2 Auth + Rooms

完成条件：

- 客户端内登录/注册/session 可用。
- 房间创建、加入、历史、room-state 可用。

退出条件：

- 真实服务器上完成登录和房间进入。

### 17.2 M3 Web Host

完成条件：

- 客户端可以用 WebView2 发起共享。
- 网页 viewer 和原生 viewer 均可观看。

退出条件：

- 720p30 连续共享 20 分钟。

### 17.3 M4 Native Host

完成条件：

- 原生 DXGI/NVENC host 可用。
- 失败时可回退 Web host。

退出条件：

- 1080p30 连续共享 20 分钟，网页 viewer 兼容。

### 17.4 M5 Audio Share

完成条件：

- host 可以选择应用/进程音频并发送给 viewer。

退出条件：

- viewer 可听到目标应用音频，停止共享后资源释放。

### 17.5 M6 Voice

完成条件：

- 原生客户端之间可语音聊天。

退出条件：

- 屏幕共享和语音同时开启 20 分钟无崩溃。

### 17.6 M7 Production Package

完成条件：

- installer 和 portable zip 可用。
- 干净 Windows 设备可安装运行。

退出条件：

- 端到端验收矩阵通过。

## 18. 推荐下一步

下一步不应该直接做原生 DXGI/NVENC，也不应该先做语音。

推荐先写 M2 的实施计划：

- session/cookie jar。
- 登录/注册 UI。
- room id 生成。
- room history。
- room-state polling 和 heartbeat。
- 创建房间进入 host 准备态。

M2 完成后，再写 M3 Web Host 实施计划。
