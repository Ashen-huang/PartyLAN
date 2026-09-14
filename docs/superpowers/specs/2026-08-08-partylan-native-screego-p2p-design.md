# PartyLAN 原生客户端 Screego P2P 适配设计

**状态：** 已确认
**日期：** 2026-08-08
**目标节点：** 广州节点 `partylanapp.com`

## 1. 背景

PartyLAN 当前已经统一房间控制面、Presence、共享租约和独立语音频道，但屏幕媒体的统一实现依赖 `/sfu-ws`。实机矩阵测试表明，现有屏幕 SFU 在分辨率、帧率、稳定性和断线恢复方面明显弱于 Screego 原生 UDP/P2P，并出现短暂断联和 `SFU 错误 3`。

广州节点已完成第一阶段回退：全量 SFU 关闭、SFU 房间灰度列表为空、旧 P2P 允许。网页端新房间因此重新使用 Screego `/stream`。当前 PartyLAN 原生客户端仍硬编码 `/sfu-ws`，不能在 `legacy-p2p` 房间中共享或观看。本设计补齐这一能力，而不是通过打开网页继续兼容。

## 2. 目标

1. PartyLAN 原生客户端直接加入 Screego `/stream`，与网页端共用同一房间和原生 UDP/P2P 媒体链路。
2. 客户端共享继续使用 Windows 原生采集、H.264 编码和 Opus 共享音频，不使用浏览器采集。
3. 浏览器共享可直接在 PartyLAN 客户端界面内观看，PartyLAN 客户端共享可直接在浏览器中观看。
4. 支持一个共享者和一个或多个观看者；每个观看者拥有独立的 WebRTC 会话。
5. `/api/presence` 继续是跨端成员列表的唯一事实来源，`/api/share/*` 继续负责唯一共享租约。
6. 房间语音继续使用独立 `/voice-ws`，不得混入屏幕共享音轨。
7. 现有 `/sfu-ws` 实现保留为显式房间级回退，不自动静默降级到 SFU。
8. 本阶段不修改广州节点网页默认链路，不删除屏幕 SFU、语音 SFU或现有网页资产。

## 3. 方案选择

采用“原生客户端直接实现 Screego P2P 协议”的方案。

不采用：

- 客户端嵌入或隐藏 Screego 网页。该方式仍由浏览器采集和浏览器页面承载媒体，不是原生客户端适配。
- 在服务器增加 Screego P2P 与 GameCast SFU 协议桥。该方式引入额外转发、协议状态和服务器带宽，不能保留原生 P2P 的质量与稳定性优势。
- 删除现有 SFU 代码。SFU 仍作为显式灰度和快速回滚路径保留。

## 4. 总体架构

```text
网页端 ───────────────┐
                      ├─ /api/presence ─ 成员与角色
PartyLAN 原生客户端 ─┘

网页端 ───────────────┐
                      ├─ /api/share/* ─ 唯一共享租约与状态
PartyLAN 原生客户端 ─┘

浏览器采集 ───────────┐
                      ├─ /stream ─ Screego 原生信令
Windows 原生采集 ─────┘             │
                                    ├─ WebRTC P2P ─ 网页观看端
                                    └─ WebRTC P2P ─ 客户端观看端

PartyLAN 原生客户端 ─ /voice-ws ─ 独立房间语音 SFU
```

新增 `internal/gamecast/screegop2p` 包，职责限定为：

- 建立带现有登录 Cookie 和证书信任策略的 `/stream` WebSocket。
- 编解码 Screego 房间、共享、会话、SDP、ICE 和恢复消息。
- 为原生共享端维护逐观看者 Pion PeerConnection。
- 为原生观看端把 Screego 信令转换为现有前端 `GameCastReceiver` 可消费的信号。

现有 `internal/gamecast/nativehost` 和 `internal/gamecast/ws` 保持 SFU 专用。应用层根据房间 `mediaMode` 选择传输，禁止在两个协议实现中混用会话对象。

## 5. Screego 协议合同

### 5.1 连接与房间

客户端连接 `<base-url>/stream`，HTTPS 转换为 WSS，HTTP 转换为 WS。连接必须复用当前 GameCast 登录 Cookie，并使用现有证书指纹校验。

房主发送：

```json
{
  "type": "create",
  "payload": {
    "id": "room-id",
    "joinIfExist": true,
    "closeOnOwnerLeave": true,
    "mode": "turn",
    "username": "display-name"
  }
}
```

观看者发送：

```json
{
  "type": "join",
  "payload": {
    "id": "room-id",
    "username": "display-name"
  }
}
```

只有收到首个 `room` 消息后，连接才进入 joined 状态。首消息不是 `room`、服务器 `Error`、房间不存在或认证失败都必须返回稳定错误，不得继续创建媒体会话。

### 5.2 共享和逐观看者会话

原生共享端完成采集与编码初始化后发送 `share`。Screego 为每个观看者下发独立 `hostsession`，其中包含 `id`、对端 `peer` 和临时 `iceServers`。

每个 `hostsession` 创建一个独立的 Pion PeerConnection：

1. 使用该会话下发的 ICE/TURN 配置。
2. 添加 H.264 视频轨和可选 Opus 音频轨。
3. 创建 offer，发送 `hostoffer`。
4. 将本地候选作为 `hostice` 发送。
5. 接收 `clientanswer` 和 `clientice`。
6. 连接失败或收到 `endshare` 时仅清理该会话。

同一个编码样本扇出到所有活动会话。一个观看者的背压、ICE 失败或退出不得停止其他观看者，也不得终止采集。

### 5.3 原生观看端信令转换

原生观看端收到 `clientsession` 后保存会话 ID、对端和该会话的 ICE/TURN 配置。收到同一会话的 `hostoffer` 时，向现有 Wails 前端发出：

```json
{
  "type": "offer",
  "sessionId": "screego-session-id",
  "sdp": {"type": "offer", "sdp": "..."},
  "iceServers": []
}
```

前端 `GameCastReceiver` 必须使用消息中的 `iceServers` 创建 RTCPeerConnection。前端生成的 `answer` 和 `ice-candidate` 必须带当前 `sessionId` 返回 Go 层，再分别映射为 `clientanswer` 和 `clientice`。

控制面一次只允许一个共享者，因此客户端同时只展示一个媒体会话。新会话替换旧会话前必须关闭旧 PeerConnection、清空 ICE 队列和统计基线。

## 6. 应用路由与状态

### 6.1 房间路由

- `mediaMode=legacy-p2p`：主机和观看端使用 `screegop2p`。
- `mediaMode=sfu` 且 `mediaProtocolVersion=1`：继续使用现有 SFU host/viewer。
- 未知模式或版本：阻止共享与观看，显示 `media_protocol_unsupported`。

前端 `selectGameCastSharePath`、`canStartGameCastSharing`、观看条件和重连条件必须显式识别两种受支持路径。路径选择只依赖服务端房间合同，不依赖 `origin`。

### 6.2 共享租约

P2P 共享的 claim 请求使用：

```json
{
  "mediaMode": "legacy-p2p",
  "sfu": false,
  "audioMode": "off|app|system|exclude-process"
}
```

旧 P2P 租约由控制面直接进入 `live`。应用层在以下顺序中操作：

1. 确认 Presence 和房间模式。
2. 连接并加入 Screego 房间。
3. 领取唯一共享租约。
4. 初始化原生采集、编码和共享音频。
5. 发送 Screego `share`。
6. 发布 `live` 状态并开始租约心跳。

停止顺序为：租约进入 stopping（若协议允许）、发送 `stopshare`、关闭全部 P2P 会话、停止采集、释放租约。取消、编码失败或信令失败必须释放已领取的租约。

P2P 共享在没有观看者时仍可进入 `live`；就绪条件是 Screego 房间已加入、共享信令已发送且本地采集已运行，不等待首个观看者连接。

### 6.3 Presence 与成员列表

Screego 的 `room.users` 仅用于媒体会话内部对端识别，不写入 PartyLAN 成员列表。网页和客户端继续通过 `/api/presence` 加入、心跳和离开，成员按 `participantId` 去重。

共享状态由控制面租约提供。Screego `room.users[].streaming` 只用于本地协议一致性检查，不能覆盖控制面中其他共享者的租约。

## 7. 媒体合同

### 7.1 视频

- 采集继续使用现有 DXGI/Windows Graphics Capture 选择逻辑。
- 默认继续为“原生”分辨率和 60 FPS，受硬件能力和用户设置约束。
- 编码继续使用现有 H.264 原生编码器。
- 所有观看者收到相同的编码层；本阶段不新增 SFU 式多层画质选择。
- 保留现有质量报告和原生编码器全局自适应，不允许单个低质量观看者直接停止整个共享。
- 每条 PeerConnection 独立处理 RTCP PLI/FIR，并请求共享编码器产生关键帧。

### 7.2 音频

- 窗口共享仅采集对应窗口/进程音频。
- 全屏共享采集系统音频。
- PartyLAN 语音频道相关进程继续从共享音频目标中排除。
- Opus 为 48 kHz 双声道；音频初始化失败时继续视频共享，并上报一次非阻塞提示。
- 同一 Opus 样本扇出到所有活动观看会话。

## 8. 生命周期与恢复

### 8.1 信令连接

- WebSocket 写操作串行化，读循环只接受合法 JSON 文本消息。
- 连接、房间确认和写操作均有超时。
- 主动关闭必须取消读循环、关闭所有会话并只发出一次 closed 状态。
- 意外断开采用有界指数退避；共享端重连后重新进入房间并重新发送 `share`，观看端重连后重新加入房间等待新会话。
- 达到最大重试次数后返回稳定的 `screego_unavailable`，不自动切换到 SFU。

### 8.2 ICE 与会话

- 远端描述设置前到达的 ICE 按会话排队。
- 未知、已关闭或过期的 session ID 消息被忽略并记录诊断，不影响其他会话。
- `icerestartrequest` 的 `restart` 只触发对应主机会话一次 ICE restart；重复请求合并。
- `endshare`、PeerConnection closed/failed 和房间中共享者消失均清理对应会话。
- 观看端连接失败可重建 Screego 房间连接；恢复期间不得制造重复 Presence 成员或重复共享租约。

## 9. 错误合同

对界面暴露稳定代码，详细底层错误只进入本地日志：

- `screego_auth_required`
- `screego_room_not_found`
- `screego_protocol_error`
- `screego_unavailable`
- `screego_session_failed`
- `media_protocol_unsupported`
- `share_locked`

任何错误都不得记录 Cookie、TURN credential、密码、完整 SDP 或完整 ICE 地址。遥测只记录房间的不可逆摘要、传输类型、候选类型、状态和耗时。

## 10. 测试策略

### 10.1 单元与协议测试

- `/stream` URL、Cookie 注入和 TLS 信任。
- create/join 首消息合同以及 Error 映射。
- Screego 所有使用消息的 JSON 编解码。
- hostsession/clientsession 生命周期和按 session ID 路由。
- ICE 先于 SDP、过期 ICE、重复 endshare 和有界重连。
- 多观看者会话互不影响、样本扇出和逐会话关键帧请求。
- P2P/SFU 房间路由及租约请求字段。

### 10.2 媒体集成测试

- Pion 原生主机到浏览器兼容接收端的 H.264/Opus 协商。
- 浏览器兼容主机到 PartyLAN `GameCastReceiver` 的 offer/answer/ICE 映射。
- Screego 下发 TURN UDP 凭据确实进入对应 PeerConnection。
- 单观看者、多观看者、观看者中途加入和退出。
- 主机或观看者 WebSocket 重连后会话恢复。

### 10.3 回归与实机验收

- Go 全仓测试、`partylan_native_media` 标签测试、前端测试和生产构建。
- Screego 补丁测试、上游 UI 构建和服务端 Go/race 测试。
- 网页主机到客户端观看、客户端主机到网页观看、客户端互看、网页互看。
- 局域网内、局域网外、一个观看者和多个观看者。
- 窗口/全屏视频、窗口/系统音频及语音频道排除。
- 退出房间、停止共享、断网恢复和重复加入。

## 11. 部署与回滚

本阶段首先生成新的 PartyLAN 客户端实测包。广州节点保持：

- `GAMECAST_UNIFIED_SFU_ENABLED=false`
- `GAMECAST_UNIFIED_SFU_CANARY_ROOMS=`
- `GAMECAST_LEGACY_P2P_ALLOWED=true`

服务器不需要新媒体服务。若实现需要修改 Screego 协议或网页资源，必须停止并单独确认，不得直接改生产节点。

回滚方式：

1. 客户端回滚到上一正式构建。
2. 如需临时恢复客户端共享，给指定测试房间重新加入 SFU canary。
3. 保留现有屏幕 SFU、静态资源和语音服务，不执行删除操作。

## 12. 实施停止条件

出现以下任一情况应停止实施并重新设计：

- Screego `/stream` 无法在不修改生产协议的情况下接收非浏览器客户端。
- Pion H.264/Opus SDP 无法与目标 Chromium/Screego 版本稳定协商。
- 登录 Cookie 无法安全复用，必须在 URL 或日志中暴露凭据。
- 多观看者需要服务器转发才能保持现有采集与编码合同。
- 实现要求把 PartyLAN 语音频道混入屏幕媒体或共享认证密钥。

## 13. 验收标准

1. `legacy-p2p` 房间中，PartyLAN 客户端可创建、加入、共享和观看，无需打开外部网页。
2. 网页与客户端成员在同一 Presence 列表中可见，且同一成员不重复。
3. 客户端共享可被网页和其他客户端观看；网页共享可被客户端观看。
4. 一个共享者面对多个观看者时，每个观看者拥有独立 P2P 会话；单个观看者失败不影响其他观看者。
5. 窗口共享、全屏共享及其音频范围符合既有合同，语音频道不进入共享音频。
6. 新链路使用 Screego 下发的 UDP TURN/ICE 配置，且遥测不泄露临时凭据。
7. `sfu` 房间仍可使用原有实现，P2P 失败不会静默切换 SFU。
8. 全部自动化门禁通过，并完成局域网内外、单/多观看者实机矩阵。
