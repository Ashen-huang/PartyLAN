# PartyLAN WebRTC 720p30 媒体面设计

## 1. 背景

当前 PartyLAN 已经能通过 Tailscale/tailscale-system/tsnet 完成房间加入与共享协商。最近的排查确认，房间加入失败主要来自 tailnet 自定义 DERP 策略中 `omitDefaultRegions=true`，改成 `false` 后加入与共享可以工作。

性能问题已经转移到媒体面。当前共享链路仍然是：

- 房间控制：`internal/room` 通过 TCP `9000` 传输房间消息。
- 视频数据：`StreamSender`/`StreamReceiver` 通过 TCP `9001` 传输自定义帧。
- 软件路径：RGBA 转 YUV 后用 zlib 做全帧/差分压缩。
- 原生路径：已有 DXGI/NVENC/Media Foundation 桥接雏形，但仍接在自定义 TCP 流上。
- 预览渲染：接收端 Go 解码后再 JPEG/base64 通过 Wails 事件推给前端，前端默认只按 `previewUIFPS=15` 渲染。
- 音频：UI 和配置已有“共享音频来源”入口，但 `windowsaudio` 原生桥当前仍返回 `audio process capture not implemented yet`。

这些现状说明，继续微调当前 TCP/zlib/JPEG 预览路径不能稳定达到默认 720p30。下一步应以 WebRTC 媒体面替换当前共享媒体面，不再把旧 `software-backend` 作为运行时 fallback。

## 2. 目标

- 默认软件体验必须以 720p30 为最低可接受基线。
- 默认媒体面改为 WebRTC，获得 RTP/RTCP、抖动缓冲、丢包恢复、关键帧请求、码率/拥塞反馈等实时媒体能力。
- 复用 PartyLAN 现有房间、成员、权限、诊断和 Tailscale 身份，不引入独立登录系统。
- 屏幕视频、共享应用音频、语音聊天必须是独立轨道，不能把整机声音作为默认共享音轨。
- 默认避免 Screego 式系统音频回音：不默认捕获完整系统输出，不捕获 PartyLAN 自己播放的语音，不把外部语音聊天混进共享音轨。
- WebRTC/native 能力不足时明确失败并显示原因，不静默回退到当前 `software-backend`。

## 3. 非目标

- 本阶段不实现完整 SFU。初始 WebRTC 仍是 host 对每个 viewer 建立一条 PeerConnection。
- 本阶段不把浏览器 `getDisplayMedia({ audio: true })` 作为捕获方案，因为它会复现整机音频不可控的问题。
- 本阶段不实现远程控制、键鼠输入或文件传输。
- 本阶段不安装虚拟音频驱动。优先实现 Windows 原生进程级 loopback；虚拟驱动只作为后续增强。
- 本阶段不保证 10 人跨公网同时 720p30。多人 fan-out 的上传带宽问题需要 SFU/中继媒体服务器阶段解决。

## 4. 方案取舍

### 4.1 方案 A：浏览器 WebRTC 捕获与播放

前端 WebView2 使用 `getDisplayMedia` 抓屏、抓系统音频，再用浏览器 `RTCPeerConnection` 推流。

优点：

- 前端渲染最简单，`<video>` 原生播放。
- WebRTC API 直接可用。

缺点：

- 会复现 Screego 的主要音频限制：系统音频容易混入语音聊天，无法可靠按进程排除。
- 捕获能力取决于 WebView2/Chromium 暴露的屏幕捕获策略。
- 原生 DXGI/NVENC、Windows 进程音频捕获无法成为主路径。

结论：不采用。

### 4.2 方案 B：Go/Pion 原生发送，前端 WebRTC 播放

Host 端 Go 后端使用原生 DXGI/NVENC 和原生音频捕获，将 H.264/Opus 写入 Pion WebRTC。Viewer 端 React/WebView2 使用 `RTCPeerConnection` 接收并用 `<video>`/`<audio>` 播放。

优点：

- Sender 不依赖浏览器捕获，因此可以做应用级音频分离。
- Viewer 用浏览器硬件解码和原生媒体渲染，绕开当前 JPEG/base64/Wails 事件瓶颈。
- 改动边界清楚：Go 负责采集/编码/信令，前端负责播放/统计/交互。

缺点：

- 在 `tailscale-system` 后端下最自然，因为 WebView2 可以直接走系统 Tailscale 网卡。
- 在嵌入式 `tsnet` 后端下，WebView2 看不到 tsnet 虚拟网络，需要额外的 Go UDP mux 或本地 WebRTC 转发桥。

结论：采用为第一阶段主方案。

### 4.3 方案 C：Go/Pion 双端收发，本地桥接到前端播放

Host 和 Viewer 都由 Go/Pion 建立远端 WebRTC。Viewer Go 后端接收 RTP 后，再通过本机 loopback WebRTC 或其他低拷贝通道交给 WebView2 播放。

优点：

- 可以完整支持嵌入式 `tsnet`，因为远端媒体走 Go 后端可控的 `tsnet.ListenPacket`。
- 不依赖 WebView2 直接访问 tailnet。

缺点：

- 实现复杂度明显更高。
- 需要本地二段媒体桥，调试和统计更复杂。

结论：作为第二阶段，用于补齐嵌入式 `tsnet` 的完整 WebRTC 支持。

## 5. 推荐架构

采用“PartyLAN 控制面 + WebRTC 媒体面”的结构。

```
Host Go backend
  DXGI capture / NVENC H.264
  Windows process audio / Opus
  Pion PeerConnection per viewer
          |
          | SDP / ICE via existing room control channel
          | RTP / RTCP media over tailnet or local network
          v
Viewer WebView2 frontend
  RTCPeerConnection
  <video> / <audio> playback
  getStats diagnostics
```

控制面继续由 `RoomImpl` 管理成员、房间生命周期和消息转发。媒体面新增 `internal/media`，由 `App` 持有 `MediaManager`。旧的 `StreamSender`/`StreamReceiver` 在迁移完成后移除；迁移期间可以短暂共存，但不能作为共享失败时的 fallback。

## 6. 信令设计

在 `internal/room/protocol.go` 中新增单一媒体信令消息：

- `MsgMediaSignal`

信令 payload 使用 envelope，避免为每种 WebRTC 消息新增一个 room message：

```json
{
  "sessionId": "share-<host>-<counter>",
  "fromPeerId": "node-a",
  "toPeerId": "node-b",
  "kind": "offer|answer|ice|close|renegotiate|error",
  "media": "screen",
  "payload": {}
}
```

流程序列：

1. Host 点击开始共享。
2. `MediaManager` 建立一个 share session，并为每个 viewer 创建 PeerConnection。
3. Host 创建 offer，通过 `MsgMediaSignal(kind=offer)` 发给目标 viewer。
4. Viewer 前端收到 Wails 事件 `media:signal`，设置 remote description，创建 answer。
5. Viewer 通过 Wails 方法 `SendMediaSignal` 把 answer/ICE 交回 Go 后端。
6. Go 后端通过 room 控制连接转发到 host。
7. 双方 ICE connected 后，Viewer UI 切到 WebRTC 播放状态。

后加入房间的 viewer 由 host 的现有 peer join 回调触发新的 offer。共享停止时发送 `close`，并释放 PeerConnection。

## 7. 网络与 ICE

第一阶段优先支持 `tailscale-system` 和普通 LAN，因为 WebView2 能通过系统网络栈访问 peer IP。

ICE 策略：

- 默认使用 host candidates，优先 tailnet IP 和 LAN IP。
- STUN/TURN 配置可选，用于非 tailnet 或特殊网络测试。
- 如果用户已部署 Screego/TURN，可以复用 TURN 地址，但 TURN 只解决连通性，不解决多人 fan-out。
- 不使用浏览器系统音频捕获，即使使用 TURN。

嵌入式 `tsnet` 的补齐策略：

- 扩展 `network.Network`，新增可选接口 `PacketNetwork`：

```go
type PacketNetwork interface {
    ListenPacket(ctx context.Context, network, addr string) (net.PacketConn, error)
}
```

- `NetworkedNetwork` 使用 `tsnet.Server.ListenPacket("udp4", "<tailnet-ip>:0")` 实现。
- `TailscaleSystemNetwork` 使用 `net.ListenConfig.ListenPacket` 实现。
- Go/Pion 使用该 PacketConn 作为 ICE UDP mux。
- Viewer WebView2 无法直接走嵌入式 tsnet 时，第二阶段增加 Go viewer 接收 + 本地 WebRTC bridge。

第一阶段如果当前 active backend 是嵌入式 `tsnet` 且没有系统可达 IP，则 WebRTC 标记为 unavailable，`StartSharing` 返回明确错误，并在诊断里说明需要 `tailscale-system` 或后续 `tsnet` UDP mux 支持。

## 8. 视频设计

视频编码主路径：

- 捕获：Windows DXGI Desktop Duplication。
- 编码：NVENC H.264 优先，后续再扩展 AMF/QSV/软件 H.264。
- WebRTC codec：H.264，packetization-mode=1。
- 默认目标：1280x720、30fps、起始 2500-4000 kbps。
- 关键帧：收到 RTCP PLI/FIR 时强制 IDR。
- 帧调度：按目标 fps 节流；编码或网络拥塞时丢弃过期帧，不堆积延迟。

旧软件 zlib delta 不能直接作为 WebRTC codec。没有 H.264 编码能力时：

- 不把 zlib 包装成 WebRTC 视频轨。
- 共享启动失败，UI 显示 `native H.264/WebRTC unavailable`。
- 不启动当前 `software-backend`。

Viewer 播放主路径：

- 第一阶段由 WebView2 `RTCPeerConnection` 接收 remote video track。
- 用 `<video>` 渲染，不再走 Go 解码到 JPEG/base64 的预览路径。
- 诊断数据来自 `RTCPeerConnection.getStats()`，包括 decoded fps、frames dropped、jitter、packet loss、bitrate。

## 9. 音频设计

音频轨道分三类：

- `share-app-audio`：共享应用音频，Opus。
- `voice-mic`：PartyLAN 内置语音聊天，Opus。
- `system-audio`：完整系统音频，仅作为显式高级选项。

默认策略：

- 默认不共享完整系统音频。
- 如果用户选择了应用音频来源，只捕获所选应用/进程树。
- 如果没有选择应用音频，默认共享视频但不带共享音频。
- PartyLAN 自己的播放进程必须默认排除，避免观众声音被 host 电脑播放后再次捕获。
- 外部语音软件需要在 UI 中可排除；常见进程名可以默认建议排除，但用户仍可调整。

Windows 实现方向：

- 将 `internal/native/windowsaudio` 从能力 stub 扩展为真实进程级 loopback 捕获。
- 使用进程 include/exclude 策略，而不是单纯按进程名保存。
- 配置中保留用户可读名称，同时保存 PID/source identity 的运行期映射。
- 共享应用音频编码为 Opus 后作为独立 WebRTC audio track。

回声策略：

- AEC 只用于麦克风语音，不作为共享音频回声问题的主要方案。
- 共享音频从源头排除 PartyLAN voice playback 和用户排除的外部语音进程。
- 如果用户强制启用完整系统音频，UI 必须提示可能包含语音聊天和系统提示音。

## 10. 多人观看策略

第一阶段采用 P2P fan-out：

- Host 为每个 viewer 建立一个 PeerConnection。
- Host 可以尽量复用同一份采集/编码结果写入多个 track，降低 CPU 压力。
- 网络上传仍随 viewer 数量线性增长。

这比当前每个 TCP sender 各自不稳定压缩更可控，但不能彻底解决 Screego 的多人观看瓶颈。后续需要：

- PartyLAN SFU/relay：host 只上传一份到 SFU，SFU 分发给 viewers。
- 或部署轻量 Pion SFU，与现有房间控制面集成。
- TURN 不是 SFU，不能单独解决 host 上传随人数增长的问题。

## 11. 前端改动

新增前端模块：

- `frontend/src/media/webrtcReceiver.ts`

职责：

- 创建 viewer 端 `RTCPeerConnection`。
- 接收 offer/ICE，返回 answer/ICE。
- 将 remote stream 绑定到 `ScreenPreview` 的 `<video>`。
- 周期性读取 `getStats()`，转换为现有诊断叠层字段。
- WebRTC 断开时触发 UI 状态和可重试错误提示。

`ScreenPreview` 改造：

- WebRTC 模式渲染 `<video>`，不显示 JPEG canvas。
- 诊断叠层使用 WebRTC stats。
- 当前 canvas JPEG 预览在 WebRTC 播放闭环完成后移除。

## 12. 后端改动

新增包：

- `internal/media`
- `internal/media/webrtc`

核心接口：

```go
type Manager interface {
    StartShare(ctx context.Context, cfg ShareConfig) error
    StopShare() error
    HandleSignal(signal room.MediaSignalPayload) error
    AttachViewer(peer room.PeerInfo) error
    DetachViewer(peerID string) error
    Stats() MediaStats
}
```

`App` 改动：

- `StartSharing` 只启动 WebRTC session。
- `StopSharing` 关闭 WebRTC session。
- 新增 Wails 方法 `SendMediaSignal(signal)`。
- 新增事件 `media:signal`、`media:state`、`media:stats`。

`RoomImpl` 改动：

- 新增 `SendMediaSignal(peerID, payload)`。
- 新增 `OnMediaSignal(fn)`.
- 不在 room 层理解 SDP 内容，只负责可靠转发。

## 13. 配置与开关

新增配置字段：

- `EnableSystemAudioShare`: bool，默认 `false`
- `AudioExcludeProcesses`: []string
- `IceServers`: []string，可选
- `TurnUsername` / `TurnCredential`，可选

默认值：

- `EnableSystemAudioShare=false`
- native H.264 或 WebRTC 网络条件不可用时，共享启动失败并给出诊断原因。

环境变量覆盖：

- `PARTYLAN_ICE_SERVERS=stun:...,turn:...`
- `PARTYLAN_TURN_USERNAME`
- `PARTYLAN_TURN_CREDENTIAL`

## 14. 错误处理

- Native encoder 不可用：共享启动失败，并在诊断里说明 `native H.264 encoder unavailable`。
- WebRTC ICE 失败：关闭该 viewer 的 PeerConnection，显示可重试状态，不影响房间控制连接。
- Viewer answer 超时：host 清理该 viewer session，peer 仍留在房间。
- 音频进程捕获不可用：视频仍可共享，音频页显示应用音频不可用。
- WebRTC 能力不足：返回明确错误，不静默回退。

## 15. 测试与验收

单元测试：

- `MediaSignalPayload` marshal/unmarshal。
- room 层 `MsgMediaSignal` 只发送到目标 peer。
- media manager session 生命周期。
- audio policy：默认不启用完整系统音频；PartyLAN 进程默认排除。
- capability selection：`auto` 在不同能力组合下选择正确 backend。

集成测试：

- loopback room 中能完成 offer/answer/ICE 状态机。
- `tailscale-system` 两机能进入 WebRTC connected。
- `tsnet` backend 在第一阶段不可用时给出明确 unavailable reason。

手工验收：

- 单 viewer LAN：720p30 连续 5 分钟，viewer decoded fps 稳定 >= 27fps。
- 单 viewer 跨 LAN/tailnet：720p30 连续 5 分钟，无长期音画卡死。
- 开启 PartyLAN 语音时，共享应用音频不包含观众自己的语音回放。
- 启用完整系统音频时，UI 有明确风险提示。
- 3 个 viewers：CPU 不因重复捕获/重复编码线性暴涨；上传带宽线性增长在诊断里可见。

## 16. 实施顺序

1. 增加 room 媒体信令，不改变旧共享行为。
2. 增加前端 viewer WebRTC receiver 和 `<video>` 渲染模式。
3. 增加 Go/Pion host sender，先发无音频 H.264 视频。
4. 接入 native encoder 的 IDR/PLI、码率和 WebRTC stats。
5. 增加 WebRTC 诊断状态和能力错误。
6. 实现 Windows 进程级应用音频捕获，并作为 Opus track 加入 WebRTC。
7. 补齐嵌入式 `tsnet` 的 PacketConn/UDP mux 支持。
8. 移除旧 `StreamSender`/`StreamReceiver` 共享路径。
9. 设计第二阶段 SFU/relay，解决多人跨网 fan-out。

## 17. 外部参考

- W3C Screen Capture: https://www.w3.org/TR/screen-capture/
- Chrome Screen Sharing Controls: https://developer.chrome.com/docs/web-platform/screen-sharing-controls/
- Microsoft Application Loopback Audio Sample: https://learn.microsoft.com/en-us/samples/microsoft/windows-classic-samples/applicationloopbackaudio-sample/
- Microsoft process loopback parameters: https://learn.microsoft.com/en-us/windows/win32/api/audioclientactivationparams/ns-audioclientactivationparams-audioclient_process_loopback_params
