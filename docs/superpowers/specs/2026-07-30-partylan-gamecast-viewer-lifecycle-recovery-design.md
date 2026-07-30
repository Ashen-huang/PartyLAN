# PartyLAN GameCast 观看端生命周期恢复设计

## 1. 背景与结论

客户端开始共享后，观看端仍可能黑屏或显示“当前无人共享”。2026-07-29 的生产只读日志表明，这不是单一的 TURN 不通问题，而是观看端信令与媒体对象生命周期失配：

- 同一观看端会在数秒内建立多个 WebSocket/PeerConnection；旧 WebSocket 的消息仍能进入前端单例接收器，而回答通过 Go 层当前保存的另一个 WebSocket 发出。SFU 因而出现 `stable->SetRemote(answer)->stable` 的非法信令状态转换。
- 主播停止共享后，前端只清空画面和统计，没有关闭并置空 `GameCastReceiver`。此后遥测长时间显示 `receiverState=active`，但 `pairId` 为空且没有媒体。
- 观看者早于主播进入时，SFU 的等待窗口可能先到期。主播稍后开始共享后，旧实现没有可靠地把“无有效接收器但房间已共享”转换为一次新的、有上限的连接尝试。
- 已有日志也观察到 TURN 路径抖动和丢包，但它只能解释画质退化，不能解释上述持续黑屏和“无人共享”状态。

本轮采用客户端生命周期修复：Go 层用单调递增代际隔离 WebSocket，前端用令牌化、单航班、最多三次的观看端重连控制器管理 Receiver。无需修改或重启 SFU。

## 2. 目标

- 旧 WebSocket 的状态和消息在连接被替换后不能再影响当前前端状态。
- `SendGameCastSignal` 始终只向当前代际的 WebSocket 发送，不让旧 offer 的 answer 串到新连接。
- 一个观看端在任意时刻最多只有一个重连调用在途。
- WebSocket 拨号成功只表示信令可用，不能把重连次数清零；只有接收器实际收到远端媒体后才清零。
- 当前 WebSocket 关闭/报错或房间切换为 `sharing=false` 时，关闭并置空 Receiver，停止统计，清空媒体与统计，并使旧异步回调失效。
- 房间从未共享转为共享且当前没有有效 Receiver 时，触发恰好一次新的连接尝试；连续失败最多三次。
- 支持“观看者先进入、主播稍后开始共享”的时序。

## 3. 非目标

- 不修改 SFU、Screego、Nginx、TURN 或云服务器配置。
- 不调整码率、自适应画质、ICE restart 或 TURN 恢复策略。
- 不把丢包和抖动问题与本次完整黑屏问题混为同一修复。
- 不在本轮部署、重启生产服务或推送远端分支。
- 不增加无限重试、后台常驻轮询或第二套房间状态协议。

## 4. Go WebSocket 代际隔离

`App` 在现有互斥锁保护下保存 `gamecastWSGeneration`。每次开始替换或关闭 GameCast WebSocket 时先递增代际，再移除旧指针，最后关闭旧客户端。这样，旧客户端 `Close()` 或 read loop 延迟产生的回调在触达业务层之前已经失效。

创建客户端时，`OnMessage` 和 `OnStatus` 捕获创建时的代际。回调只有在该代际仍等于 `App` 当前代际时才允许调用 `emitGameCastSignal` 或 `emitGameCastStatus`。

新客户端在拨号前登记为当前客户端，以便服务器在拨号完成后立即发送 offer 时，前端产生的 answer 已有正确发送目标。拨号失败时，仅当该客户端及代际仍为当前值才清除指针。若连接期间已被更新操作取代，旧调用不得覆盖或关闭新客户端，并返回“连接已被取代”的本地错误供令牌化前端忽略。

`SendGameCastSignal` 继续从互斥锁下读取当前客户端；由于旧代际消息不会再向前端发布，因此该接口不会接收由旧 offer 产生的新 answer。

## 5. 前端观看端生命周期

### 5.1 生命周期控制器

在 `gamecastOrchestrator.ts` 增加一个小型 `GameCastViewerLifecycle`：

- `beginConnection()` 生成新令牌并使此前 Receiver/Promise 回调失效；用于用户主动连接。
- `beginReconnect()` 仅在没有重连在途时生成新令牌，否则返回空；用于自动恢复的单航班门控。
- `finishReconnect(token)` 只结束匹配令牌的在途状态；旧 Promise 完成不能释放新重连。
- `invalidate()` 生成新令牌并清除在途标记；用于退出房间、信令关闭以及停止共享。
- `isCurrent(token)` 供 Receiver 的流、状态、统计和 Promise 回调验证所有权。

控制器只负责所有权和单航班，不复制 React 房间状态或重连次数。

### 5.2 Receiver 所有权与清理

每个 `GameCastReceiver` 创建时捕获当前令牌。以下回调只有令牌仍当前且实例仍是 `gameCastReceiverRef.current` 时才可更新界面：

- `onRemoteStream`；
- `onStats`；
- `onStateChange`。

统一清理顺序为：先使令牌失效，再从 ref 取出并置空 Receiver，随后调用 `close()`，最后清空远端流、WebRTC 统计和对应的 `STREAM_STOPPED` 状态。先失效可以屏蔽 `close()` 同步产生的 `closed` 回调。

触发清理的条件：

- 当前 Go WebSocket 状态变为 `closed` 或 `error`；
- 当前观看房间状态变为 `sharing=false`；
- 用户离开、断开、切换为 Host 或开始兼容共享；
- 新的主动连接或自动重连取代旧连接。

### 5.3 重连次数与成功定义

`viewerReconnectAttempt` 表示当前共享周期内已经启动的自动重连次数：

- 定时器真正取得单航班令牌后才加一；
- WebSocket 拨号成功后保留该数值；
- 拨号失败后保留该数值，并在仍满足条件时进入下一次退避；
- 只有 `onRemoteStream` 收到至少一个媒体轨并确认当前令牌时才重置为零；
- `sharing=false` 开始新的共享周期，也将次数重置为零。

最大次数保持为三次，退避保持有界。`receiverState=connecting` 或控制器已有重连在途时不得再次启动。

## 6. 状态流

### 6.1 正常观看

```text
用户连接 -> 新令牌 -> Go 新 WS 代际 -> offer -> Receiver connecting
         -> WebRTC/track -> onRemoteStream -> media connected -> attempts=0
```

### 6.2 观看者先于主播

```text
观看者进入 -> WS 等待/超时 -> status closed -> 清理 Receiver
主播开始共享 -> room sharing false->true -> 无 Receiver
           -> 单航班 reconnect attempt 1 -> 新 WS/offer -> media connected
```

房间仍未共享时不消耗自动重连次数；共享转为 active 才允许发起恢复。

### 6.3 主播停止后再次共享

```text
sharing true->false -> 令牌失效 + Receiver close/null + 清媒体/统计 + attempts=0
sharing false->true -> receiverState idle -> 恰好一次 bounded reconnect
```

## 7. 错误处理与竞态边界

- 旧 Go 回调直接丢弃，不更新 UI，也不记录为当前连接错误。
- 旧前端 Receiver 或 Promise 回调直接丢弃，不清空新流、不改变新状态。
- 自动重连定时器触发前若房间退出、停止共享或令牌失效，调用被取消。
- 自动重连执行中若用户主动离开或切换角色，Promise 完成结果被令牌检查丢弃。
- 当前信令关闭会销毁 Receiver；下一次恢复必须创建新的 PeerConnection，不能复用旧 SDP 状态。
- 三次自动重连均失败时保留可审计错误状态，不进入无限循环。用户重新加入或新的 `sharing=false -> true` 周期可重新获得三次预算。

## 8. 测试策略

### 8.1 Go

- 当前代际回调可以通过，替换后的旧代际状态与消息被拒绝。
- 关闭流程先使代际失效，旧客户端同步发出的 `closed` 不再成为当前状态。
- 只有匹配代际和实例的客户端才能清除当前指针。

### 8.2 前端控制器

- 两次并发 `beginReconnect()` 只有第一次成功。
- 旧令牌的 Promise/Receiver 回调不能影响新令牌。
- WebSocket 完成只释放单航班，不代表媒体成功，也不重置 attempt。
- `invalidate()` 允许后续共享周期重新连接。
- `shouldReconnectGameCastViewer()` 在共享 active、Receiver 缺失时允许恢复，在 connecting、媒体已连接、已有兼容 Host 或达到三次时拒绝。
- 覆盖观看者先进入、信令超时、主播后开始共享的状态转换。

### 8.3 Receiver 与集成

- `close()` 关闭 PeerConnection、停止统计并忽略关闭后的异步 stats 结果。
- 实际远端轨回调作为媒体成功证据。
- 前端生产构建、全部 Node 测试、普通 Go 测试和原生媒体标签 Go 测试通过。

## 9. 实施范围

预计只修改：

- `app.go`；
- `app_test.go`；
- `frontend/src/App.tsx`；
- `frontend/src/media/gamecastOrchestrator.ts`；
- `frontend/src/media/gamecastReceiver.ts`；
- `frontend/tests/gamecastOrchestrator.test.mjs`；
- `frontend/tests/gamecastReceiver.test.mjs`。

若 Wails 绑定签名不变，不重新生成绑定。云端日志副本只作为只读证据，不纳入仓库。

## 10. 完成标准

- 回归测试能证明旧 WebSocket/Receiver 回调被隔离。
- 重连保持单航班、最多三次，并只在媒体到达后清零次数。
- 停止共享后 Receiver 已关闭且 ref 为空，流和统计均清空。
- 观看者先进入、主播后共享会触发一次有效恢复。
- 本地前端与 Go 验证通过，并生成可供 Windows 实机复测的客户端产物。
- 未改变云服务器状态，未推送远端。
