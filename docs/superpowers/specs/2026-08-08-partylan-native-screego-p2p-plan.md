# PartyLAN 原生客户端 Screego P2P 实施计划

**状态：** 本地实施完成，待外部实机验收
**日期：** 2026-08-08
**设计依据：**
`docs/superpowers/specs/2026-08-08-partylan-native-screego-p2p-design.md`

**实施边界：** 在当前分支完成客户端源码、测试、构建和本地提交；不修改广州节点
网页资源、Screego 协议、Nginx、systemd、容器或生产开关，不推送远端。现有工作区中
未提交的 SFU 修复和 UDP-only 补丁必须保留，不得回退或混入不相关提交。

## Task 1：建立 Screego 协议与认证红灯测试

**新增：**

- `internal/gamecast/screegop2p/protocol.go`
- `internal/gamecast/screegop2p/protocol_test.go`
- `internal/gamecast/screegop2p/client_test.go`

先增加失败测试，覆盖：

1. `https/http` 基址分别转换为 `wss/ws` 的 `/stream`，保留基址路径并清除查询参数。
2. create/join、share/stopshare、host/client session、offer/answer、双方 ICE、ICE restart、
   endshare、room 和 Error 消息按 Screego 线上 JSON 合同编解码。
3. WebSocket 握手只通过请求头复用当前 Cookie jar，不在 URL、状态详情或错误中输出
   Cookie 值。
4. TLS 使用现有 `gamecast.TLSConfig` 指纹策略。
5. 首个合法消息必须是相同房间的 `room`；认证失败、房间不存在、未知首消息和连接关闭
   分别归一化为稳定错误码。
6. 写操作串行化，非法 JSON、上下文超时和主动关闭行为可预测。

确认测试因 `screegop2p` 客户端尚不存在而失败，再实现最小协议 DTO、URL helper、错误
类型和连接客户端，使测试转绿。

## Task 2：实现持久 Screego 房间连接

**修改或新增：**

- `internal/gamecast/screegop2p/client.go`
- `internal/gamecast/screegop2p/client_test.go`
- `app.go`
- `app_test.go`

先增加失败测试，覆盖：

1. `legacy-p2p` 房间 owner 发送 `create`、`joinIfExist=true`、`mode=turn`，member 发送
   `join`；两者都携带显示名。
2. 只有收到 room ack 才算加入成功；room 更新不重建 Presence。
3. 原生客户端创建的空闲房间保持 `/stream` 连接，因此网页可以在共享前加入。
4. 离开 GameCast 房间、注销、切换服务器和应用退出都会关闭房间连接。
5. 意外断开使用有限指数退避重新加入；主动关闭不重连；耗尽后只上报
   `screego_unavailable`。
6. `mediaMode=sfu` 不连接 `/stream`，继续保留现有按 live 阶段连接 `/sfu-ws` 的行为。

实现应用层 `gameCastP2PRoom` 生命周期。连接复用已有持久 Cookie jar和 GameCast Profile，
但 Screego room users 不写入 PartyLAN Presence。

## Task 3：实现逐观看者原生 P2P 发布

**新增或修改：**

- `internal/gamecast/screegop2p/publisher.go`
- `internal/gamecast/screegop2p/publisher_test.go`
- `app.go`
- `app_test.go`

测试先行覆盖：

1. `share` 后每个 `hostsession` 使用该会话下发的 ICE servers 创建独立 Pion
   PeerConnection，并发送带对应 sid 的 `hostoffer/hostice`。
2. `clientanswer/clientice` 只进入匹配会话；远端描述前的 ICE 按 sid 排队。
3. 两个观看者同时存在时 H.264/Opus 样本分别写入两条会话；关闭或失败一条不影响另一条。
4. 每条连接独立处理 PLI/FIR，并通过现有回调请求关键帧。
5. `icerestartrequest` 只重启对应会话且重复请求合并。
6. `endshare`、stopshare、房间关闭和本地停止幂等清理全部会话。
7. 没有观看者时 publisher 也能 ready，不等待 PeerConnection connected。

实现一个应用层发布接口，使现有 SFU `nativehost.Host` 和新 P2P Publisher 都能被原生
采集、视频编码、音频编码、自适应和停止逻辑调用。P2P 路径不得修改现有采集/编码合同。

## Task 4：实现客户端 P2P 观看信令桥

**新增或修改：**

- `internal/gamecast/screegop2p/viewer.go`
- `internal/gamecast/screegop2p/viewer_test.go`
- `frontend/src/media/gamecastReceiver.ts`
- `frontend/tests/gamecastReceiver.test.mjs`
- `app.go`
- `app_test.go`

测试先行覆盖：

1. `clientsession` 保存 sid、peer 和该会话临时 ICE servers。
2. 同 sid 的 `hostoffer/hostice` 转换为现有 Wails `gamecast:signal`；offer 携带
   `sessionId` 和 Screego 下发的 `iceServers`。
3. `GameCastReceiver` 用 offer 自带的 ICE servers 创建 PeerConnection，不回退为推断
   STUN；生成的 answer/client ICE 带当前 `sessionId`。
4. Go 层把 answer/ICE 分别映射为 `clientanswer/clientice`，拒绝缺失、旧或不匹配 sid。
5. 新 clientsession 替换旧会话时关闭旧接收器、清空 ICE 和统计基线。
6. endshare 和房间 sharing 结束关闭媒体但保留 Presence、语音和空闲房间连接。

实现 viewer bridge，保留客户端现有 WebView `GameCastReceiver`、画面展示和质量统计，
不嵌入 Screego 网页。

## Task 5：实现模式感知的租约和应用路由

**修改：**

- `internal/gamecast/roomstate.go`
- `internal/gamecast/client.go`
- `app.go`
- `app_test.go`
- `frontend/src/media/gamecastOrchestrator.ts`
- `frontend/tests/gamecastOrchestrator.test.mjs`
- `frontend/src/App.tsx`

测试先行覆盖：

1. `legacy-p2p` 和受支持的 SFU 房间都允许原生界面共享/观看；未知模式被拒绝。
2. share path 返回明确的 `native-p2p` 或 `native-sfu`，不依赖房间 origin。
3. P2P claim 使用 `mediaMode=legacy-p2p`、`sfu=false` 且不强制 starting，控制面返回 live。
4. SFU claim 继续使用 `mediaMode=sfu`、`sfu=true`、`phase=starting`，保持原状态机。
5. P2P 共享启动顺序为 room joined、claim、采集/编码、share；无观看者也进入 live。
6. P2P 正常停止发送 stopshare、关闭 publisher、停止媒体并释放租约；所有故障路径释放
   已取得租约。
7. viewer 只在当前共享者不是自己时显示；P2P/SFU 各自按其合同重连，禁止自动跨协议
   回退。
8. Presence 成员、唯一共享者和独立 `/voice-ws` 生命周期在两种媒体模式下保持一致。

实现模式感知请求构造、发布接口、信令传输接口和前端纯判定函数。生成 Wails bindings
只允许反映真实 Go DTO 变化，不保留无关生成噪声。

## Task 6：跨协议媒体集成测试

**新增或修改：**

- `internal/gamecast/screegop2p/integration_test.go`
- `frontend/tests/gamecastReceiver.test.mjs`
- 必要的 `app_test.go`

覆盖：

1. 测试 Screego WebSocket 服务发送 hostsession/clientsession 并验证完整消息顺序。
2. Pion 原生 publisher 与兼容浏览器接收 PeerConnection 协商 H.264；启用音频时协商 Opus。
3. 浏览器式 host offer 经 viewer bridge 到 GameCastReceiver，再返回 answer/ICE。
4. 一个 host、两个 viewer 的会话和 RTP 样本互不串线。
5. ICE 乱序、观看者中途加入/退出、单会话失败、ICE restart 和 WebSocket 重连。
6. TURN credential 只进入 PeerConnection 配置，断言日志和状态消息不含 credential、
   Cookie、SDP 或 candidate 原文。

若锁定的 Screego 线上协议不能在无服务端变更的情况下通过这些测试，立即触发设计中的
STOP 条件，不修改生产节点绕过。

## Task 7：完整仓库验证与实测包

依次执行：

```powershell
python -m unittest discover -s server -p "test_*.py"
node --test (Get-ChildItem server -Filter 'test_*.mjs' | Select-Object -ExpandProperty FullName)
node --test (Get-ChildItem frontend/tests -Filter '*.test.mjs' | Select-Object -ExpandProperty FullName)
go test ./internal/gamecast/screegop2p
go test ./...
go test -tags partylan_native_media ./...
npm.cmd run build --prefix frontend
powershell -ExecutionPolicy Bypass -File server/screego/verify-build.ps1
git diff --check
```

然后：

1. 运行正式 Wails/原生构建门禁，确认 `partylan_native_media`、`CGO_ENABLED=1` 和
   `opus.dll`。
2. 记录 `PartyLAN.exe` 路径、大小、时间和 SHA-256。
3. 扫描本次差异和测试输出，确认没有 Cookie、密码、Authorization、TURN credential、
   SDP、ICE candidate 或媒体数据。
4. 审计提交边界，不提交 `tmp`、构建目录或已有无关工作区改动。

## Task 8：外部实机门禁（自动实现后由用户执行）

1. 网页主机到客户端、客户端主机到网页、客户端互看、网页互看。
2. 局域网内、局域网外、一个观看者和多个观看者。
3. 原生分辨率 60 FPS、弱网自适应、断网恢复和长时间稳定性。
4. 窗口音频、全屏系统音频和 PartyLAN 语音频道排除。
5. 成员列表、唯一共享锁、观看者中途加入/退出、停止后再次共享。
6. SFU canary 房间仍可使用旧客户端路径，普通房间不连接 `/sfu-ws`。

Task 8 不能被自动测试替代；未取得实机结果前状态只能是“本地实施完成，待外部验收”。

## STOP 条件

- `/stream` 非浏览器连接需要修改广州 Screego 服务端协议。
- 登录 Cookie 无法通过标准 WebSocket 请求头安全复用。
- Pion H.264/Opus 无法与锁定 Chromium/Screego SDP 稳定协商。
- 多观看者需要新增服务器媒体转发或修改当前单编码器合同。
- 实现要求把 `/voice-ws` 音轨接入屏幕共享。
- 新失败测试与已确认设计冲突。
- 继续推进需要修改、重启或重新部署广州节点服务。

## 本地完成标准

- Task 1 至 Task 7 全部转绿并形成可审计的本地提交。
- `legacy-p2p` 房间中客户端直接使用 `/stream`，无需打开网页。
- `sfu` 房间仍使用 `/sfu-ws`，两种会话实现不共享连接状态。
- 原生 host 支持多观看者，单个观看者故障不终止其他会话。
- 原生 viewer 使用 Screego 临时 UDP TURN 配置并在客户端界面内播放。
- Presence、共享租约、独立语音、音频排除和回滚路径保持有效。
- 正式客户端实测包构建完成；Task 8 明确记录为外部验收。

## 实施结果（2026-08-08）

Task 1 至 Task 7 已完成，Task 8 保持为外部实机门禁。实现没有修改、重启或重新部署
广州节点，也没有推送远端。

- 协议客户端、持久房间连接和多观看者发布分别落在提交 `2c5c925`、`b9557e5`、
  `6b5d04d`。
- 原生观看桥、模式感知路由、双向媒体集成与重连共享重宣告落在提交 `646b569`。
- `legacy-p2p` 使用 Screego `/stream`，客户端发布为每个观看者建立独立 Pion 连接；客户端
  观看继续使用应用内 `GameCastReceiver`，不打开或嵌入 Screego 网页。
- `sfu` 房间继续使用原有 `/sfu-ws` 路径；未知媒体模式被拒绝，不进行跨协议自动回退。
- P2P claim 使用 `mediaMode=legacy-p2p`、`sfu=false` 且不发送 `starting`；P2P 停止不发送
  SFU stopping 转换，离开房间前仍会释放共享租约。
- 内存 Screego 信令集成测试已验证一个原生发布器同时向两个浏览器式观看端协商并发送
  H.264 与 Opus，目标包竞态测试通过。

验证结果：

- 服务器 Python：37 项通过。
- 服务器 Node：106 项通过。
- 客户端前端 Node：40 项通过，前端生产构建通过。
- 普通 Go 全仓与 `partylan_native_media` 标签全仓通过。
- `go test -race ./internal/gamecast/screegop2p` 通过。
- Screego 补丁校验、上游 UI 构建和 Go 测试通过。
- `git diff --check` 通过；Wails 生成文件只有空白噪声，已清理。

正式实测包：

- `build/bin/PartyLAN.exe`：21,352,960 字节，SHA-256
  `A7D754A4126157EB0B921CEFD4EB8F0E8B9F7EF790F663DAF3415C8D3A22766C`。
- 构建时间：`2026-08-08 14:39:08.508 +08:00`。
- 构建标签：`desktop,partylan_native_media,wv2runtime.download,production`，
  `CGO_ENABLED=1`。
- `build/bin/opus.dll`：731,936 字节，SHA-256
  `2134411EA3DAF238B9FA163917292E8C9CF80593822A338A7938B3685269AF1F`。
- 构建结束后没有 PartyLAN 残留进程。
