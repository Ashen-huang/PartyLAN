# PartyLAN 房间语音与共享音频完全隔离设计

## 1. 背景与结论

PartyLAN 现有 `internal/voice` 仍是面向旧房间模型的单连接实验实现：默认使用
`audio.NewMockCapturer()`，没有真实麦克风采集和扬声器播放；`StartVoiceChat()` 又依赖
旧 `a.room`，因此默认 GameCast 云房间无法形成可用的多人语音。

现有 GameCast 共享音频已经可以随所选窗口自动绑定进程，但房间工具弹窗仍暴露
共享音频选择和实验性语音入口。整屏共享还会选择 `CaptureSystemMix`，这会把
PartyLAN 播放的远端语音重新送进共享音轨，无法满足语音聊天与共享音频完全隔离的
要求。

本设计采用已确认的方案 A：新增独立、受版本控制的 `voice-sfu`，客户端在加入默认
GameCast 云房间后自动建立一条双向语音 PeerConnection。语音 SFU 只转发 Opus RTP，
不解码、不混音；客户端负责真实麦克风采集、远端语音解码混音和扬声器播放。共享
音频继续由独立的共享会话按共享目标自动决定，任何情况下都不得引用语音轨或回退到
原始系统混音。

## 2. 范围

### 2.1 目标

- PartyLAN 客户端加入默认 GameCast 云房间后自动加入同房间语音频道。
- 每个客户端只建立一条双向语音 PeerConnection，支持每房间最多 20 名客户端。
- 入房后麦克风默认开启，远端语音默认播放。
- 麦克风不可用不能阻断入房或远端语音接收，并允许用户直接重试。
- 房间界面直接提供麦克风开关和语音频道静音开关。
- 语音频道静音只停止本机远端语音播放，不停止麦克风发送。
- 语音生命周期与共享生命周期完全独立，不共享屏幕时也能正常通话。
- 窗口共享音频继续自动跟随所选窗口。
- 麦克风和远端语音在软件媒体图中均不可能进入共享音轨。
- Presence 或语音令牌失效时及时断开语音连接。

### 2.2 非目标

- 不让网页端成员加入语音频道。
- 不覆盖旧 `LAN`、`Tailscale`、`tsnet` 房间及旧网络语音协议。
- 第一版不提供设备选择、单成员静音、单成员音量、总音量滑块、录音、转写、空间音频
  或服务端混音。
- 第一版不新增可见的语音成员列表或说话者动画。
- 不修改现有共享 SFU `5053` 的协议或发布者模型。
- 本设计阶段不推送远端、不部署、不修改或重启云服务器。

## 3. 不可破坏的隔离约束

以下约束是实现和验收的硬条件：

1. 麦克风采集器、语音 Opus 编码器和语音 RTP 轨只由 `VoiceSession` 持有。
2. 共享会话只接受由共享目标策略生成的 `ShareAudioSource`，不能接受语音轨、麦克风
   设备或任意系统混音轨。
3. 窗口共享只能使用所选窗口进程树的音频。
4. 整屏共享只能使用排除 PartyLAN 根进程及其 WebView2 子进程树的系统音频。
5. 分享 PartyLAN 自身窗口、目标进程未知或无法证明排除有效时，只发布视频。
6. 操作系统不支持进程树排除时，只发布视频，不能回退 `CaptureSystemMix`。
7. Web 兼容共享不能证明已排除 PartyLAN 语音播放时，只发布视频。
8. 语音开始、停止、静音、重连和设备恢复不得调用共享会话的启动、停止或重连入口；
   共享状态变化也不得重建语音 PeerConnection。

这些约束同时由类型边界、策略测试和 Windows 实机音频标记测试保证，不能只依赖 UI
隐藏选项。

## 4. 总体架构

```text
PartyLAN Client
  GameCast Presence
    join / heartbeat / leave
            |
            +---- POST /api/voice/token ---- Registration Control :5052
            |                                  presence validation + signed token
            |
  VoiceSession
    Windows microphone -> Opus -> outbound RTP
    inbound RTP -> Opus decoders -> client mixer -> Windows speaker
            |
            | one bidirectional PeerConnection
            v
  /voice-ws (TLS via Nginx)
            |
            v
  voice-sfu :5054/TCP
    token validation
    one room <= 20 client connections
    Opus RTP forwarding only
    serialized server-offer negotiation

  ShareSession (independent)
    selected window process audio
      or display audio excluding PartyLAN process tree
            |
            v
  existing /sfu-ws -> screen SFU :5053
```

`voice-sfu` 的信令和健康检查仅监听 `127.0.0.1:5054/TCP`，Nginx 通过 TLS 路径
`/voice-ws` 代理到 `/ws`。媒体使用独立、可配置的 ICE UDP mux，默认绑定
`0.0.0.0:5054/UDP` 并发布现有 GameCast 公网地址；现有 TURN 作为回退。信令端口和
媒体端口使用相同数字但不同传输协议，不与 `5053` 共享进程或会话状态。

## 5. 组件边界

### 5.1 客户端 `VoiceSession`

新增 `internal/gamecast/voice`，负责：

- 接收当前 GameCast `roomID`、`participantID` 和已登录会话客户端。
- 获取和续签语音令牌。
- 维护一条 WebSocket 和一条双向 PeerConnection。
- 协调麦克风、Opus 编码、远端轨解码、客户端混音和扬声器输出。
- 维护连接、麦克风和远端播放三个正交状态。
- 使用单调递增代际隔离旧 WebSocket、PeerConnection、设备和异步回调。
- 在离房、Presence 失效或应用退出时同步释放全部语音资源。

现有 `internal/voice` 不作为新 GameCast 语音的基础，因为其单 peer TCP 模型、mock
设备和旧房间依赖均与本设计不兼容。旧模块可以暂时保留给 legacy 路径，但默认
GameCast UI 和生命周期不再调用它；是否删除留到独立清理任务。

### 5.2 Windows 语音设备

新增独立的 Windows 语音设备适配层 `internal/native/windowsvoice`：

- 麦克风使用默认 Windows 捕获设备，以 48 kHz 单声道、20 ms 帧向语音编码器供数。
- 扬声器使用默认 Windows 渲染设备，播放客户端混合后的 48 kHz PCM。
- 设备接口可由测试 fake 替换，设备错误不能直接操作 PeerConnection 或共享会话。
- 麦克风关闭时停止采集并释放设备；重新开启时重新获取默认设备。
- 扬声器不可用时继续保持网络连接和麦克风发送，并在后台有上限地重开默认设备。

语音设备层与 `internal/native/windowsaudio.ProcessCapturer` 分离。后者继续只负责共享
音频的进程 loopback，避免将输入设备和共享音频路由合并成一个可误用的通用对象。

### 5.3 Opus 发送与播放

- 麦克风编码使用 Opus VOIP 配置：48 kHz、单声道、20 ms 帧、目标 24 kbit/s，启用
  DTX 和可用的带内 FEC。
- 每条远端 RTP 轨拥有独立的小型抖动缓冲和 Opus 解码器。
- 客户端把最多 19 条远端 PCM 轨按 20 ms 节拍混合、限幅后送入一个扬声器会话。
- 频道静音只在本地混音/渲染边界生效；连接和远端 RTP 接收保持活动，以便立即恢复。
- `voice-sfu` 不读取 Opus 负载，不生成 PCM，也不保存媒体包。

现有 libopus 动态加载层需要补充 VOIP 编码控制和解码能力；共享音频编码器与语音
编码器分别实例化，任何一方关闭都不能关闭另一方。

### 5.4 语音 SFU

新增受版本控制的服务目录 `server/voice-sfu`，包含 Go 源码、测试和可复现的容器构建
文件。服务职责仅包括：

- 验证短期签名令牌和令牌续签。
- 按 `roomID` 管理最多 20 个唯一 `participantID`。
- 为每个客户端维护一条 PeerConnection。
- 把每名客户端的 Opus RTP 转发给同房间其他客户端。
- 在成员加入、离开或重连时串行协商远端轨集合。
- 清理断开的 PeerConnection、轨道、转发循环和房间对象。
- 暴露仅供内网检查的 `/health` 和不含敏感信息的房间计数。

同一 `participantID` 的新有效连接替换旧连接，不重复占用名额。第 21 个不同客户端
收到 `voice_room_full`，但 Registration Presence 和普通入房状态不受影响。

## 6. 鉴权与信令协议

### 6.1 语音令牌

Registration Control 新增 `POST /api/voice/token`。请求包含当前 `room` 和
`participantId`，并沿用现有 GameCast 登录认证。签发前必须验证：

- Presence 中存在相同 `room`、`participantId` 和认证用户。
- `clientType` 必须为 `client`，网页成员一律拒绝。
- Presence 尚未过期。

令牌使用 HMAC-SHA256 签名，声明至少包含 `aud=voice-sfu`、`room`、`participantId`、
`clientType=client`、`iat`、`exp` 和随机 `jti`。令牌有效期为 15 秒；客户端每 5 秒
续签一次。签名密钥只通过部署环境变量提供给 Registration Control 和 `voice-sfu`，
不得写入仓库、客户端或日志。

首次 WebSocket 握手通过 `Authorization: Bearer <token>` 传递令牌，不把令牌放入 URL。
连接建立后客户端通过 `auth-refresh` 信令提交新令牌。服务端在当前令牌过期前未收到
有效续签时主动关闭该语音连接。

### 6.2 信令

信令消息只允许固定类型：

- 服务端到客户端：`offer`、`ice-candidate`、`state`、`error`。
- 客户端到服务端：`answer`、`ice-candidate`、`auth-refresh`。

服务端是唯一 offer 发起方。每个客户端按修订号串行处理 offer；上一修订未完成时，
成员变化只标记为待协商，不能并发创建第二个 offer。客户端仅回答当前代际和当前修订，
旧 answer、ICE 和回调直接丢弃，从协议上消除 glare 和跨连接串包。

初始 offer 为服务端接收一条麦克风轨并发送当前所有远端语音轨。客户端即使没有可用
麦克风，也能以仅接收方式完成 answer；后续重试成功后在预留发送器上替换麦克风轨，
不重建共享连接。

## 7. 房间生命周期与数据流

### 7.1 自动加入

1. 客户端完成 GameCast Presence `join`。
2. 房间 UI 立即进入可用状态，不等待语音结果。
3. 后台创建新 `VoiceSession`，初始设置为麦克风开启、远端播放开启。
4. 并行尝试获取默认麦克风和语音令牌。
5. 建立 `/voice-ws` 与语音 PeerConnection。
6. 麦克风可用时发布 Opus；不可用时保持仅接收并显示可重试状态。

主办者和普通客户端成员执行同一流程。屏幕是否正在共享不参与语音加入判断。

### 7.2 麦克风开关

- 默认入房时为开启。
- 关闭时立即停止采集、停止写入语音 RTP 并释放麦克风设备，但保留发送器和
  PeerConnection。
- 重新开启时重新获取默认麦克风；失败则进入 `unavailable`，远端接收继续。
- 麦克风状态在同一次房间会话的网络重连中保持；真正离房后清除，下次入房重新默认
  开启。

### 7.3 频道静音

- 默认关闭，即远端语音正常播放。
- 开启后仅把客户端语音混音输出静音；麦克风采集和发送保持不变。
- 频道静音状态在同一次房间会话的网络重连中保持；离房后重置为正常播放。

### 7.4 离房和退出

离开 GameCast 房间时先使 `VoiceSession` 代际失效并停止令牌续签、设备、WebSocket、
PeerConnection、解码器和混音器，再执行 Presence `leave`。应用退出使用同一清理入口。
任何旧回调都不能重新打开设备或修改新房间状态。

## 8. 共享音频自动策略

GameCast 共享请求不再接受用户音频选择作为安全依据。共享后端根据已验证的共享源
生成唯一允许的音频策略：

| 共享来源 | 允许的音频捕获 | 失败或不确定时 |
| --- | --- | --- |
| 普通窗口，目标 PID 已确认且不属于 PartyLAN 进程树 | `CaptureIncludeProcessTree(targetPID)` | 仅视频 |
| 整个显示器，PartyLAN 根 PID 已确认且支持排除 | `CaptureExcludeProcessTree(partylanPID)` | 仅视频 |
| PartyLAN 或 WebView2 自身窗口 | 无 | 仅视频 |
| 窗口 PID 未知或已失效 | 无 | 仅视频 |
| 不支持进程树 include/exclude | 无 | 仅视频 |
| 无法证明排除有效的 Web 兼容共享 | 无 | 仅视频 |

整屏路径必须移除当前 `CaptureSystemMix` 默认值。后端不得接受旧 UI 传入的 `system`
或任意进程覆盖来绕过上述表格；实际 `audioMode` 仍可上报 `process`、`exclude-process`
或 `off`，但只表示后端最终生效结果。

窗口共享时 PartyLAN 语音播放天然不属于目标进程树；整屏共享时 PartyLAN 和 WebView2
中的远端语音播放由排除进程树能力过滤。麦克风是另一个输入设备，并且从未进入
`ShareSession`，因此也不会进入共享 Opus 轨。

## 9. 房间界面与状态

### 9.1 房间语音控件

房间主操作区增加一个紧凑的 `RoomVoiceControls`，仅在默认 GameCast 房间内显示：

- 麦克风图标按钮：`Mic` / `MicOff`。
- 语音频道图标按钮：`Volume2` / `VolumeX`。
- 一个不改变布局尺寸的状态点，表示连接中、已连接、重连中或不可用。

使用 `lucide-react` 的标准图标和悬停提示，不手绘 SVG，不增加独立语音卡片。两个按钮
具有固定尺寸、键盘焦点、`aria-label`、禁用态和错误态。麦克风不可用时点击麦克风
按钮即执行重试。

### 9.2 状态模型

前后端传递一个正交快照，而不是用单个 `muted` 同时表示输入和输出：

- `connection`: `idle | connecting | connected | reconnecting | full | unauthorized | error`
- `microphone`: `starting | live | muted | unavailable`
- `playbackMuted`: boolean
- `participantCount`: 0 到 20
- `errorCode`: 稳定、可本地化的错误码，不携带 SDP、ICE 或令牌内容

语音状态通过新的房间语音事件更新。旧 `peer:voice-state` 和 `VoiceStateMuted` 的含义
不再用于 GameCast UI，避免把“麦克风关闭”和“频道静音”混为一谈。

### 9.3 共享弹窗清理

- `RoomToolsDialog` 移除 `audio` 和 `voice` 页签，直接显示共享源和画质选择。
- 移除共享音频模式、进程列表、麦克风/扬声器占位开关及语音音量滑块。
- `MemberList` 下方移除语音聊天入口。
- 开始共享请求不再从前端提交音频覆盖字段；声音由共享目标自动决定。
- 当前 `AudioPanel` 若无其他调用方则删除，不保留不可达的占位状态。

## 10. 异常恢复

### 10.1 网络与信令

- 语音连接失败不回滚 Presence，不影响文字、成员列表、观看或共享。
- WebSocket、ICE 或 PeerConnection 失败后只重建 `VoiceSession` 的网络部分。
- 重连采用带抖动的 1、2、4、8、15 秒退避，后续最长保持 15 秒间隔。
- 每次重连先使旧代际失效并完整关闭旧连接，任意时刻只有一个重连在途。
- 同房间重连保留麦克风开关和频道静音状态。
- 成功标准是 PeerConnection 已连接且媒体链路就绪，不是 WebSocket 拨号成功。

### 10.2 令牌与 Presence

- `401`、`403`、Presence 不存在或 `clientType` 不匹配时立即停止语音，不继续后台重试。
- 临时网络或服务端错误可以在当前令牌到期前重试续签；到期仍失败则关闭语音连接。
- 只有 Presence 再次成功建立后，才允许创建新的 `VoiceSession`。

### 10.3 设备

- 初始麦克风失败时保持仅接收，麦克风按钮显示不可用并可重试。
- 通话中麦克风被拔出时立即停止发送并转为不可用，不重建 PeerConnection。
- 扬声器故障时继续接收和发送，输出适配层按同一有上限退避重开默认设备。
- 设备错误不得调用共享音频的静音、停止或回退入口。

### 10.4 容量

- 第 21 个不同客户端被语音 SFU 拒绝后显示“频道已满”，但仍保留房间成员身份和其他
  功能。
- 满员状态按正常重连退避重新尝试；一旦有名额释放即可自动加入。
- 同一参与者的旧连接被新连接替换，不制造重复播放或虚假占位。

## 11. 安全与可观测性

允许记录：

- 服务启动版本、房间连接数和总 PeerConnection 数。
- 脱敏后的房间/参与者标识。
- 连接代际、信令修订、状态转换、稳定错误码和清理结果。
- 令牌签发/续签成功与失败计数，但不记录令牌值。

禁止记录：

- PCM、Opus、RTP 负载或任何语音内容。
- SDP 正文。
- ICE candidate 正文及其中的地址和端口明细。
- `Authorization` 头、初始令牌、续签令牌、签名密钥和完整 `jti`。

Nginx 访问日志格式不得加入 `Authorization` 头。`voice-sfu` 对信令 JSON 只解析固定字段，
限制消息大小、房间长度和每连接发送频率；不把原始消息写入日志。

## 12. 预计代码边界

主要新增：

- `internal/gamecast/voice`：客户端语音生命周期、信令和状态。
- `internal/native/windowsvoice`：默认麦克风和扬声器适配。
- `server/voice-sfu`：受版本控制的语音 SFU、测试、Dockerfile。
- Registration Control 的语音令牌签发与测试。
- `frontend/src/components/RoomVoiceControls.tsx` 及对应样式和状态映射。

主要调整：

- `app.go`：把语音启动/停止接入 GameCast Presence 生命周期，替换旧 GameCast 语音
  Wails 方法，并收紧共享音频策略。
- `internal/audio`：增加受测试覆盖的 Opus VOIP 编码控制和解码能力。
- `frontend/src/App.tsx`：自动语音状态、两个房间内控制和事件接线。
- `RoomToolsDialog.tsx`、`MemberList.tsx`、`AudioPanel.tsx`：移除旧入口和占位控制。
- `server/screego/deploy/nginx-screego.conf`：新增转发到 Registration Control 的
  `/api/voice` 路径，以及转发到语音 SFU 的 `/voice-ws` 路径。
- `server/screego/deploy/docker-compose.yml`：新增独立 `voice-sfu` 服务定义。

现有屏幕共享 SFU `5053`、网页语音能力和 legacy 房间协议不在修改范围内。

## 13. 测试与验收

### 13.1 自动测试

- `VoiceSession`：自动加入、默认状态、输入/输出开关、代际隔离、单航班重连、离房清理。
- 设备 fake：无麦克风时仅接收、设备拔出、重试成功、扬声器恢复。
- Opus：20 ms 编解码、DTX/FEC 配置、每轨解码器隔离、最多 19 轨混音和限幅。
- 令牌：Presence 用户绑定、仅 `client`、过期、篡改、错误 audience、续签和日志脱敏。
- SFU：服务端单 offer、修订串行、加入/离开转发、同参与者替换、20 人上限、第 21 人
  拒绝和零成员房间清理。
- 共享策略：窗口 include、整屏 exclude、自身窗口、未知 PID、能力缺失和 Web 兼容降级。
- 结构约束：共享会话 API 无法接收麦克风轨或 `VoiceSession` 音频源。
- UI：仅保留两个房间语音按钮；共享弹窗不存在音频/语音页签和覆盖字段；响应式布局
  不重叠。

### 13.2 Windows 实机验证

至少使用三台 PartyLAN 客户端或三个隔离的 Windows 音频环境验证：

1. 不共享屏幕时，三方可以互相通话。
2. 麦克风关闭后不再发送，频道静音后只停止本机远端播放。
3. 共享开始、停止和切换来源时语音不中断，语音重连时共享不中断。
4. 麦克风拒权或拔出不阻断远端接收和房间功能。
5. 分享普通窗口时，只能听到该窗口进程树音频。
6. 分享整屏时，能听到非 PartyLAN 系统声音，但听不到 PartyLAN/WebView2 播放的远端
   语音。
7. 分享 PartyLAN 自身或模拟不支持排除能力时，共享只包含视频。

### 13.3 完全隔离证据

测试向麦克风、远端语音和目标窗口分别注入可区分的数字音频标记。在接收端把语音
PeerConnection 的远端轨与共享 PeerConnection 的音频轨分别落到测试缓冲区：

- 共享音轨必须包含目标窗口标记。
- 共享音轨不得检测到麦克风标记或远端语音标记。
- 语音轨不得包含由共享会话再次混入的共享输出。
- 代码级所有权测试必须同时证明两条轨道没有共同的 capturer、encoder 或 track 实例。

只有结构证明和运行时标记测试同时通过，才可以宣称“共享音频与语音完全隔离”。

### 13.4 20 人容量验证

- 20 个模拟客户端同时连接并进行全双工 RTP 转发，持续运行 30 分钟。
- 期间执行成员加入、退出、网络断开和同参与者替换，不得出现重复轨、并发 offer 或
  非预期断线。
- 第 21 个不同客户端稳定收到 `voice_room_full`，普通 Presence 仍成功。
- 全部离开后房间、PeerConnection、转发循环和相关 goroutine 均被清理，不出现持续
  资源增长。

### 13.5 验证分层

完成报告必须分别列出：

- Go、Python、Node/前端自动测试结果。
- Windows 原生麦克风、扬声器和共享音频实机结果。
- 三客户端功能验收结果。
- 20 客户端模拟负载结果。
- 云端部署与真实公网房间结果。

自动测试通过不能替代 Windows 音频隔离或云端部署证据。

## 14. 部署与回滚边界

实施完成后先在本地构建并完成自动化与 Windows 实机验证。云端上线必须另行获得明确
授权，并沿用现有生产变更流程：备份 Registration Control、Nginx、compose 和环境
配置，构建带固定版本的 `voice-sfu` 镜像，先验证健康检查和令牌签发，再开放客户端。

上线前必须确认 `5054/UDP` 媒体端口、TURN 回退、语音签名密钥和 Nginx WebSocket
代理均可用。出现认证绕过、语音串房、共享音频泄漏、20 人资源失控或房间主流程回归
时，立即禁用 `/voice-ws` 和令牌签发入口并恢复上一版配置；现有屏幕共享 `5053` 应
继续独立运行。

## 15. 完成标准

- 默认 GameCast 房间入房即自动加入语音，离房即彻底退出语音。
- 无屏幕共享时可正常进行多人语音。
- 麦克风默认开启，频道默认播放，并可在房间界面独立切换。
- 麦克风不可用时仍能入房和收听，并可重试。
- 20 人语音容量和第 21 人非阻断拒绝行为均有自动化证据。
- 共享弹窗不再出现音频选择或语音入口，窗口声音仍自动跟随所选窗口。
- 结构测试和数字标记实测均证明语音内容不能进入共享音轨。
- 语音、共享和 Presence 的错误及恢复互不重置对方状态。
- 日志不包含语音数据、SDP、ICE 地址或令牌。
- 未经另行授权，不改变云服务器状态、不推送远端分支。
