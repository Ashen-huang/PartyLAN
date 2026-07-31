# PartyLAN 房间语音与共享音频完全隔离实施计划

**目标：** 实现默认 GameCast 云房间的客户端自动语音频道，并以代码所有权和 Windows
实机证据保证语音内容不能进入共享音频。

**设计依据：**
`docs/superpowers/specs/2026-07-30-partylan-room-voice-isolation-design.md`

**实施边界：** 只在当前分支本地实施、测试和构建；不推送、不部署、不修改或重启
云服务器。旧 `LAN / Tailscale / tsnet` 语音路径和网页语音不在范围内。

## Task 1：建立共享音频隔离策略红灯测试

**文件：**

- 修改：`app_test.go`
- 修改：`app.go`
- 修改：`internal/native/windowsaudio/capability.go`

1. 为纯策略函数增加测试：普通窗口只允许目标进程树音频。
2. 增加整屏共享测试：目标必须为
   `CaptureExcludeProcessTree(currentPartyLANPID)`，不能使用 `CaptureSystemMix`。
3. 增加 PartyLAN/WebView2 自身窗口、未知 PID、能力缺失时返回 `off` 的测试。
4. 增加旧请求传入 `system` 或其他进程时不能覆盖安全策略的测试。
5. 先运行目标测试，确认当前整屏 `CaptureSystemMix` 行为形成红灯。

## Task 2：实现共享音频封闭策略

**文件：**

- 修改：`app.go`
- 修改：`internal/gamecast/sharecatalog/*`
- 修改：相关 Go 测试

1. 把共享音频选择收敛为接收已验证共享源的纯函数。
2. 窗口源只生成 `CaptureIncludeProcessTree(targetPID)`。
3. 显示器源只生成 `CaptureExcludeProcessTree(os.Getpid())`。
4. 增加 PartyLAN 进程树识别，PartyLAN/WebView2、自身、未知和失效 PID 均返回 `off`。
5. 删除 GameCast 默认路径的 `CaptureSystemMix` 回退；启动排除失败时继续无音频共享。
6. 实际 room-state 只上报最终的 `process`、`exclude-process` 或 `off`。
7. 运行 Task 1 目标测试并确认转绿。

## Task 3：建立语音令牌控制面红灯测试

**文件：**

- 修改：`server/gamecast_control.py`
- 修改：`server/register.py`
- 修改：`server/test_gamecast_control.py`
- 修改：`server/test_register_control_api.py`
- 修改：`internal/gamecast/roomstate.go`
- 修改：`internal/gamecast/client.go`
- 修改：`internal/gamecast/client_test.go`

1. 增加当前认证用户、当前 Presence、`clientType=client` 才可签发的测试。
2. 增加网页成员、过期成员、错误参与者、错误房间和认证用户不匹配时拒绝的测试。
3. 增加 15 秒 HMAC-SHA256 令牌的篡改、过期、错误 audience 和字段边界测试。
4. 增加 `/api/voice/token` 的认证、限流、错误码和响应不泄漏密钥测试。
5. 增加 Go 客户端请求/解析语音令牌的 HTTP 测试。
6. 先运行目标 Python 和 Go 测试，确认缺少接口形成红灯。

## Task 4：实现短期语音令牌

**文件：**

- 修改：Task 3 文件
- 同步修改：`server/screego/deploy/register.py`

1. 在 `ControlPlane` 增加只读的客户端 Presence 授权入口。
2. 使用 Python/Go 标准库定义相同的 base64url JSON + HMAC-SHA256 紧凑令牌格式。
3. `POST /api/voice/token` 从环境读取 `GAMECAST_VOICE_SECRET`，签发 15 秒令牌。
4. 令牌只包含 `aud`、`room`、`participantId`、`clientType`、`iat`、`exp`、`jti`。
5. Go `gamecast.Client` 增加 `IssueVoiceToken`，沿用现有认证 cookie/session。
6. 同步生产副本并运行 Python/Go 目标测试。

## Task 5：建立 `voice-sfu` 红灯测试和服务骨架

**新增：**

- `server/voice-sfu/cmd/voice-sfu/main.go`
- `server/voice-sfu/internal/auth/*`
- `server/voice-sfu/internal/sfu/*`
- `server/voice-sfu/Dockerfile`
- 对应 Go 测试

测试先行覆盖：

1. 有效、篡改、过期、错误 audience 和非客户端令牌验证。
2. 同房间连接、跨房间隔离和同参与者新连接替换旧连接。
3. 20 个唯一客户端成功，第 21 个收到 `voice_room_full`，不创建 PeerConnection。
4. 服务端是唯一 offer 方；同一连接的协商修订串行，旧 answer/ICE 被拒绝。
5. 一名参与者的 Opus RTP 只转发给同房间其他参与者，不回送自身。
6. 离开后移除轨道并清理空房间；并发断开不泄漏转发循环。
7. `/health` 只返回版本、房间数和连接数，不返回参与者、SDP、ICE 或令牌。

## Task 6：实现独立语音 SFU

**文件：** Task 5 新增文件。

1. 使用根模块已有 Pion WebRTC 与 Gorilla WebSocket 依赖，不引用忽略目录 `tmp`。
2. 监听 `127.0.0.1:5054/TCP`，媒体使用可配置的 `5054/UDP` ICE UDP mux。
3. 握手只从 `Authorization: Bearer` 读取令牌，不接受 URL 令牌。
4. 房间表按 `room` 管理最多 20 个唯一 `participantId`。
5. 每个客户端只建立一条双向 PeerConnection；服务端串行发起 offer。
6. 每条远端 Opus track 使用 `TrackLocalStaticRTP` 转发给其他客户端，不解码、不混音。
7. 实现 `auth-refresh`，当前令牌到期仍未续签则关闭连接。
8. 所有信令日志只记录脱敏标识、修订和稳定错误码。
9. 运行 `server/voice-sfu` 全部测试，并执行 race 测试。

## Task 7：建立 Opus 解码和 Windows 语音设备测试

**文件：**

- 修改：`internal/audio/opus_encoder.go`
- 修改：`internal/audio/opus_encoder_windows.go`
- 新增：`internal/audio/opus_decoder*.go`
- 新增：`internal/native/windowsvoice/*`
- 新增或修改：对应 Go 测试

1. 增加 libopus VOIP、24 kbit/s、DTX、FEC 配置测试。
2. 增加 Opus 单声道 48 kHz、20 ms 解码、丢包恢复和关闭后拒绝测试。
3. 定义可替换的 `Microphone`、`Speaker` 接口和 fake，覆盖启动、停止、拔出和重开。
4. 增加最多 19 个远端流的 20 ms 混音、限幅、频道静音和资源清理测试。
5. 先运行测试，确认当前仅有编码器且没有真实设备/解码器形成红灯。

## Task 8：实现 Windows 麦克风、扬声器和客户端混音

**文件：** Task 7 文件。

1. 使用受维护的 miniaudio Go 绑定在 Windows WASAPI 后端打开默认捕获/渲染设备。
2. 麦克风输出 48 kHz 单声道 PCM16；关闭时真正停止并释放设备。
3. 扬声器接收混合后的 48 kHz PCM16；设备失败按有上限退避重开。
4. 扩展现有动态 libopus 包装，增加 VOIP 编码控制和独立 decoder 实例。
5. 每条远端轨使用独立抖动缓冲与 decoder，混合器只向一个扬声器会话输出。
6. 非 Windows/无 CGO 构建提供明确 unavailable stub，不伪装成功。
7. 运行普通、Windows 原生标签和 race 可用范围内的目标测试。

## Task 9：建立 `VoiceSession` 生命周期红灯测试

**新增或修改：**

- 新增：`internal/gamecast/voice/*`
- 修改：`app_test.go`
- 修改：`app.go`

测试先行覆盖：

1. Presence 成功后语音异步自动启动，语音失败不回滚入房。
2. 默认麦克风开启、默认远端播放；麦克风失败仍可仅接收。
3. 麦克风关闭只停止采集/发送，频道静音只停止远端播放。
4. 一条 WebSocket 和一条 PeerConnection；旧代际回调不能影响新会话。
5. 1、2、4、8、15 秒带抖动退避、单航班重连和同房间状态保持。
6. 令牌每 5 秒续签；401/403/Presence 失效立即停止，临时错误到期后停止。
7. 离房、换房、应用退出先停止语音并释放设备/网络，再离开 Presence。
8. 语音重连不调用共享入口，共享开始/停止不调用语音入口。

## Task 10：实现客户端 `VoiceSession` 和 App 接线

**文件：** Task 9 文件及 Wails 绑定。

1. 实现 WebSocket 固定消息协议、服务端 offer answer、trickle ICE 和 auth refresh。
2. 建立单调 generation、单航班重连和正交状态快照。
3. 使用预留音频 sender 支持无麦克风 answer 后重新附加轨道。
4. 接收最多 19 条远端 Opus track，交给客户端解码/混音，不交给共享媒体管理器。
5. `startGameCastPresence` 成功后异步启动；`LeaveGameCastRoom`、换房、shutdown 统一停止。
6. 替换 GameCast UI 使用的旧 `StartVoiceChat/StopVoiceChat/SetVoiceMuted` 语义，提供：
   - `GetRoomVoiceState`
   - `SetRoomVoiceMicrophoneEnabled`
   - `SetRoomVoicePlaybackMuted`
7. 通过事件发送完整房间语音快照。
8. 运行 Task 9 全部测试和 Wails 绑定一致性检查。

## Task 11：建立并实现房间语音 UI

**文件：**

- 新增：`frontend/src/components/RoomVoiceControls.tsx`
- 新增：`frontend/src/components/RoomVoiceControls.css`
- 修改：`frontend/src/App.tsx`
- 修改：`frontend/src/types.ts`
- 修改：`frontend/src/i18n.ts`
- 修改：`frontend/src/components/RoomToolsDialog.tsx`
- 修改：`frontend/src/components/MemberList.tsx`
- 删除或收敛：`frontend/src/components/AudioPanel.tsx` 及样式
- 修改：`frontend/package.json`、`frontend/package-lock.json`
- 新增或修改：`frontend/tests/*.test.mjs`

测试先行覆盖：

1. 默认 GameCast 房间渲染固定尺寸的麦克风和频道静音两个图标按钮。
2. 两个按钮分别调用输入和输出 API，状态互不覆盖。
3. 麦克风不可用时点击执行重试；连接中、重连、满员和错误状态可区分。
4. `RoomToolsDialog` 不再有 `audio/voice` 页签或音频覆盖字段。
5. `MemberList` 不再有语音入口，房间外不显示语音控件。
6. 添加 `lucide-react`，按钮具有 tooltip、`aria-label`、键盘焦点和稳定尺寸。
7. 运行全部前端 Node 测试和生产构建。

## Task 12：部署资产和安全测试

**文件：**

- 修改：`server/screego/deploy/nginx-screego.conf`
- 修改：`server/screego/deploy/docker-compose.yml`
- 修改：`server/screego/deploy.md` 或相应运维文档
- 修改：相关静态/配置测试

1. 新增 `/api/voice` 到 Registration Control `5052` 的代理。
2. 新增 `/voice-ws` 到 `127.0.0.1:5054/ws` 的 WebSocket 代理。
3. compose 增加固定镜像标签的独立 `voice-sfu`，不改屏幕 SFU `5053` 协议。
4. 环境示例只写变量名，不写实际 `GAMECAST_VOICE_SECRET`。
5. 自动审计 Nginx/服务日志格式不包含 Authorization、原始信令、SDP、ICE 或令牌。
6. 只验证仓库资产，不执行云端复制、重载、启动或重启。

## Task 13：完整自动验证与客户端产物

依次执行：

```powershell
python -m unittest discover -s server -p "test_*.py"
go test ./server/voice-sfu/...
go test -race ./server/voice-sfu/...
go test . ./internal/audio/... ./internal/native/windowsvoice/... ./internal/gamecast/...
go test ./...
go test -tags partylan_native_media ./...
node --test frontend/tests/*.test.mjs
npm.cmd run build --prefix frontend
git diff --check
```

然后：

1. 使用正式脚本构建 `gamecast-client` Windows EXE。
2. 记录 EXE 路径、大小、修改时间和 SHA-256。
3. 执行客户端启动冒烟，并确认进程清理。
4. 执行 20 个模拟客户端 30 分钟全双工/进出/重连负载测试。
5. 扫描测试日志，确认无语音数据、SDP、ICE 地址或令牌。

## Task 14：Windows 实机隔离验收

1. 至少三个隔离客户端验证不共享也可通话。
2. 验证麦克风关闭与频道静音互不影响。
3. 验证共享开始、停止、切换与语音连接互不重置。
4. 向麦克风、远端语音和目标窗口注入不同数字音频标记。
5. 分别提取语音和共享接收轨；共享轨只能包含目标窗口标记。
6. 整屏共享验证非 PartyLAN 声音存在、PartyLAN/WebView2 远端语音标记不存在。
7. 分享 PartyLAN 自身和模拟排除不支持时验证仅视频。
8. 若当前环境不足以建立三个隔离声卡/客户端，明确记录为未完成实机门槛，不以自动
   测试替代。

## STOP 条件

- 实现需要复用忽略目录 `tmp/gamecast-sfu-source` 才能成立。
- 语音 SFU 必须解码、混音或修改现有屏幕 SFU `5053` 协议才能工作。
- 共享整屏必须回退 `CaptureSystemMix` 才能发声。
- 无法通过当前 Presence 和认证用户安全签发客户端专用短期令牌。
- 失败测试与已确认设计冲突，或现有代码变化使服务端唯一 offer 模型不可实现。
- 自动验证发现语音轨可进入共享会话，或数字标记出现在共享轨中。
- 继续推进需要云端部署、服务重启、远端推送或真实密钥。

## 完成标准

- Task 1 至 Task 13 的自动验证、构建和本地产物审计通过。
- 有条件时完成 Task 14；否则将其作为明确的外部实机验收缺口保留。
- 20 人语音上限、客户端专用鉴权、独立生命周期和完全隔离均有测试证据。
- 本地提交只包含计划范围内的源码、测试、受版本控制部署资产和必要依赖。
- 不改变云服务器状态，不推送远端分支。
