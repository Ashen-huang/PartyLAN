# PartyLAN 网页端与客户端统一 SFU 实施计划

**状态：** 实施中
**日期：** 2026-08-06
**设计依据：**
`docs/superpowers/specs/2026-08-06-partylan-unified-sfu-cross-client-design.md`

**实施边界：** 仅在当前分支完成源码、测试、补丁、构建和本地提交；不上传、不部署、
不重启广州节点服务、不切换生产开关、不推送远端分支。Windows 真实设备、跨端实机和
广州生产回归分别保留为外部门禁，不能由自动测试替代。

## Task 1：纳管生产 SFU 网页客户端并建立基线

**新增或修改：**

- 新增：`server/screego/deploy/sfu-client.js`
- 新增：`server/test_sfu_client.mjs`
- 修改：`server/test_voice_deployment_assets.py`
- 修改：`server/screego/upstream.lock`

1. 从广州节点 `/root/screego/frontend/sfu-client.js` 下载维护副本，同时读取
   `/var/www/screego/sfu-client.js` 的 SHA-256。
2. 要求维护副本、公开副本和仓库新增文件三者摘要一致；记录原始生产摘要
   `74826211f05c512f0ac403984ded48778d16692eb1f5054c6a84db4324a22450`。
3. 先增加资产测试，证明 Nginx 引用的 `sfu-client.js` 已被版本控制、摘要有基线且
   脚本不依赖注入层包装后的 `RTCPeerConnection`。
4. 此任务只读取远端文件，不替换任何服务器文件。

## Task 2：建立统一控制面红灯测试

**修改：**

- `server/test_gamecast_control.py`
- `server/test_register_control_api.py`

测试先行覆盖：

1. Presence 返回 `mediaProtocolVersion=1` 和四项布尔能力：`sfuViewer`、`sfuHost`、
   `h264`、`opus`；旧参与者缺失能力时按协议版本 0 兼容记录。
2. 房间稳定返回 `mediaMode`、`mediaProtocolVersion` 和 `sharePhase`；`origin` 不再决定
   媒体模式。
3. 新统一房间的共享租约从 `starting` 开始，只允许
   `starting -> live -> stopping` 及同阶段续约；逆向或跳跃转换返回
   `share_phase_invalid`。
4. `starting`、`live`、`stopping` 都锁定第二个 claim；非所有者不能改阶段或释放。
5. `sharing=true` 覆盖三个有效阶段，`sfu=true` 仅在 `mediaMode=sfu` 且
   `sharePhase=live` 时成立。
6. Presence 或租约过期会清理共享；旧接口和旧字段仍可读取。
7. `GAMECAST_UNIFIED_SFU_ENABLED`、`GAMECAST_UNIFIED_SFU_CANARY_ROOMS` 和
   `GAMECAST_LEGACY_P2P_ALLOWED` 分别控制全量、房间灰度和旧 P2P 新租约门禁；默认值
   保持现网行为，不自动切换广州网页房间。
8. SFU `/sessions` 只能补充连接统计，不能把 `starting` 租约提前合并为 `live`。
9. `server/register.py` 与 `server/screego/deploy/register.py` 继续字节一致。

先运行：

```powershell
python -m unittest server.test_gamecast_control server.test_register_control_api
```

确认新增断言因当前缺少字段、状态机和开关而失败。

## Task 3：实现向后兼容的控制面模型

**修改：**

- `server/gamecast_control.py`
- `server/register.py`
- `server/screego/deploy/register.py`
- `server/screego/deploy/runtime.env.example`
- `server/screego/deploy.md`

1. 为 `ControlPlane` 注入统一 SFU 全量开关、规范化 canary 房间集合和旧 P2P 许可开关。
2. Presence 验证并保存协议版本和能力；不按用户名合并参与者。
3. 创建房间时一次性确定 `mediaMode`，有效共享期间不得改变。
4. claim 保存 `mediaMode=sfu` 和 `phase=starting`；heartbeat 原子校验合法转换；release
   允许捕获取消和故障从任意有效阶段清理。
5. 对缺失新字段的旧调用维持兼容映射；门禁关闭后拒绝新的 legacy-P2P claim，返回
   `media_protocol_unsupported`，但不终止已存在的旧共享。
6. 房间、租约和 Presence 输出统一字段，并让 `sfu` 完全由有效 live 租约派生。
7. 修改源副本后复制为部署副本，并以 SHA-256 证明字节一致。
8. 运行 Task 2 测试并确认转绿。

## Task 4：建立 Screego 统一模式补丁红灯测试

**新增或修改：**

- 新增：`server/screego/patches/0003-partylan-unified-sfu-shell.patch`
- 修改：`server/screego/verify-build.ps1`
- 补丁内新增：`ui/src/partylanMode.test.mjs`
- 补丁内修改：`ui/src/useRoom.ts`
- 补丁内修改：`ui/src/Room.tsx`

先在锁定的 Screego `f946cee83b61d9f47c2f1c2238fb31f4042b628a` 加测试，覆盖：

1. `window.__PARTYLAN_SFU_ONLY__ === true` 时不处理 `hostsession` 或 `clientsession`。
2. 统一模式仍调用 `getDisplayMedia` 供 PartyLAN SFU 使用，但不向 Screego `/stream`
   发送 `share`、`stopshare`，也不向 Screego P2P PeerConnection 添加屏幕轨。
3. 非统一房间保留原 Screego 行为，供房间级回滚使用。
4. 成员 Tooltip 内存在固定 `#partylan-presence-roster` 挂载点和可隐藏的旧名单容器。

先运行补丁内 Node 测试，确认当前上游仍建立 P2P 会话且缺少稳定挂载点。

## Task 5：实现可重复应用的 Screego 外壳补丁

1. 把统一模式判定收敛为无副作用 helper，`useRoom.ts` 的捕获、信令收发和轨道发布均
   通过同一判定。
2. `Room.tsx` 只增加稳定名单锚点和回滚容器，不修改登录、路由、主题或房间握手。
3. 生成单独的 `0003` 补丁，不改写既有 `0001`、`0002`。
4. 在新的干净锁定检出中依次应用三个补丁，运行补丁测试、Screego UI build、Go tests
   和 auth race tests。

## Task 6：建立网页统一 Presence/SFU 红灯测试

**新增或修改：**

- `server/test_web_control.mjs`
- `server/test_sfu_client.mjs`
- 新增：`server/test_web_unified_sfu_integration.mjs`
- 修改：`server/screego/deploy/inject.js`
- 修改：`server/web_control.js`

测试先行覆盖：

1. `mediaMode=sfu` 是网页 SFU 的唯一正常模式选择，不再依赖 `origin`、观众数或
   viewer-intent。
2. viewer 只在 `sharing=true`、`sharePhase=live`、`sfu=true`、协议兼容时连接；
   `idle/starting/stopping` 不创建屏幕 PeerConnection。
3. 网页 Presence join 携带协议版本和能力，立即刷新名单，5 秒续约、3 秒刷新。
4. 名单只渲染到 `#partylan-presence-roster`，按 `participantId` 去重，完整显示网页端、
   客户端、角色、共享者和当前成员；React 重绘后可重建内容。
5. claim 明确请求 `mediaMode=sfu`、`phase=starting`；SFU 媒体就绪后 heartbeat 为
   `live`；正常停止先 heartbeat 为 `stopping`，关闭媒体后 release。
6. 捕获取消、编码不支持、SFU 超时和页面离开都会释放租约。
7. `sfu-client.js` 使用注入前保存的原生 RTC 构造器；视频只接受 H.264 能力，音频优先
   Opus；任一必需编码缺失时在发布前抛出 `codec_unavailable`。
8. H.264/Opus codec preference 和 offer SDP 均有测试，不以能力列表代替最终 SDP。

## Task 7：实现网页统一 SFU 生命周期

1. 注入脚本在取得房间状态后设置 `window.__PARTYLAN_SFU_ONLY__`，仅对固定
   `mediaMode=sfu` 房间启用；离房时清除。
2. 删除 DOM 克隆和名称文本扫描，注入层完全拥有固定 Presence 锚点；统一模式隐藏旧
   Screego 名单，legacy 房间保留它。
3. 收敛网页共享和 viewer 生命周期到 `/sfu-ws`，移除正常流程中的自动 SFU/P2P
   混合判定；旧 viewer-intent 只保留兼容接口，不参与新版选择。
4. `sfu-client.js` 在创建 transceiver 后设置 H.264/Opus codec preferences，并暴露稳定
   的媒体就绪 Promise/状态；错误只返回稳定错误码，不记录 SDP、ICE 或令牌。
5. normal stop 严格执行 `stopping -> close tracks/PeerConnection -> release`。
6. 更新 Nginx 静态资源版本和仓库资产测试，但不修改或重载生产 Nginx。
7. 运行 Task 6、现有网页控制、音频隔离、自适应、恢复、统计和 WebSocket 测试。

## Task 8：建立原生客户端跨来源与阶段感知红灯测试

**新增或修改：**

- `frontend/tests/gamecastOrchestrator.test.mjs`
- `internal/gamecast/client_test.go`
- `app_test.go`

测试先行覆盖：

1. `origin=web`、`origin=client` 和缺失来源一律选择原生房间，不调用
   `BrowserOpenURL`。
2. 原生分享路径只依赖当前房间、原生能力和统一媒体合同，不再要求
   `origin=client`；不恢复 WebView 兼容采集回退。
3. 加入空闲房间只注册 Presence/语音并显示成员，不连接 `/sfu-ws`。
4. 房间进入 `live` 后才连接 viewer；进入 `stopping/idle` 时只关闭屏幕 SFU，Presence
   和 `/voice-ws` 保持。
5. 客户端 Presence 和共享请求正确编解码协议版本、能力、`mediaMode` 和 `sharePhase`。
6. 原生 claim 为 `starting`，媒体循环就绪后转 `live`；正常停止先转 `stopping`，关闭
   原生媒体和 SFU 后 release。
7. Presence 轮询对所有来源运行，且同账号网页标签与客户端进程保留两条来源标记记录。
8. viewer generation 继续拒绝旧连接迟到回调，重连只在 live 阶段发生。

## Task 9：实现客户端原生统一房间流程

**修改：**

- `internal/gamecast/roomstate.go`
- `internal/gamecast/client.go`
- `app.go`
- `frontend/src/types.ts`
- `frontend/src/media/gamecastOrchestrator.ts`
- `frontend/src/App.tsx`
- 必要的 `frontend/wailsjs/` 生成绑定

1. 扩展 Go/TypeScript DTO，保留旧 JSON 字段兼容。
2. 增加 Presence-only 的原生加入入口；创建不存在的个人房间时为 owner，加入任何现有
   来源房间时为独立 member。
3. 删除网页来源自动打开浏览器的加入分支；保留用户显式“网页打开”命令。
4. 所有原生房间每 3 秒读取 Presence；空闲时不建立 viewer WebSocket/PeerConnection。
5. 用纯判定函数控制 live 时连接、stopping/idle 时断开和有限重连。
6. 原生 host 在任意来源的 SFU 房间领取 starting 租约，Windows 捕获、H.264 编码、
   Opus 共享音轨和 ICE 就绪后更新 live。
7. 离房、正常停止和应用退出按
   `stopping -> close native media/SFU -> release -> leave Presence` 排序；故障路径允许
   直接释放但不得残留租约。
8. 不修改既有原生分辨率 60 FPS、自适应画质、窗口进程树音频、整屏排除 PartyLAN
   语音的实现。

## Task 10：完整仓库内验证

依次执行：

```powershell
python -m unittest discover -s server -p "test_*.py"
node --test (Get-ChildItem server -Filter 'test_*.mjs' | Select-Object -ExpandProperty FullName)
node --test (Get-ChildItem frontend/tests -Filter '*.test.mjs' | Select-Object -ExpandProperty FullName)
go test . ./internal/gamecast/...
go test ./...
go test -tags partylan_native_media ./...
npm.cmd run build --prefix frontend
powershell -ExecutionPolicy Bypass -File server/screego/verify-build.ps1 -SourceRoot <clean-patched-checkout>
git diff --check
```

然后：

1. 运行正式 Wails/原生构建门禁，记录 EXE 路径、大小、时间和 SHA-256。
2. 对 `server/register.py` 与部署副本做 SHA-256 和逐字节比较。
3. 扫描改动和测试日志，确认没有密码、Authorization、令牌、SDP、ICE candidate 或
   媒体内容。
4. 审计提交只包含计划内源码、测试、Screego 补丁和受版本控制部署资产。

## Task 11：外部验收门禁（本轮不执行）

1. 四种组合：网页到网页、网页到客户端、客户端到网页、客户端到客户端。
2. 双端成员 3 秒内互相可见，同账号显示两条来源记录。
3. 最终 selected codec/SDP 证明 H.264 + Opus，而不是只检查浏览器能力。
4. Windows 真实设备验证窗口音频、系统音频与 `/voice-ws` 房间语音完全隔离。
5. 弱网下降级与恢复、多观看者中途加入和退出、停止后再次共享。
6. 广州节点首页、登录、注册、建房、加入、历史、桌面和移动视口无回归。
7. 生产网络统计证明统一房间没有通过 Screego P2P 发送屏幕 RTP。

上述验收需要另行确认部署、重启、灰度开关和真实设备操作。

## STOP 条件

- 广州维护副本、公开副本和已知生产 `sfu-client.js` 摘要不一致。
- 当前 `/sfu-ws` 协议不能在不改服务器信令合同的情况下承载网页 H.264/Opus。
- Screego 补丁不能在锁定上游干净检出中重复应用或会改变登录、路由和房间握手。
- 需要在同一有效共享中混用 SFU/P2P，或让单个 viewer 单独回退 P2P 才能工作。
- 需要把 `/voice-ws` 语音轨接入屏幕 SFU，或恢复 `CaptureSystemMix` 才能共享音频。
- 新失败测试与已确认设计冲突，或需要未规划的屏幕 SFU 协议升级。
- 继续推进需要上传、部署、服务重启、生产开关、真实密钥或真实音频设备。

## 本地完成标准

- Task 1 至 Task 10 全部通过并形成分层本地提交。
- 新版网页端和客户端的正常屏幕媒体选择都只指向 `/sfu-ws`。
- Presence、共享阶段、协议能力和媒体模式在 Python、Go、TypeScript/Node 三端一致。
- 客户端加入网页来源房间不再自动打开浏览器，空闲房间不建立屏幕媒体连接。
- 网页成员锚点稳定显示客户端成员，Screego 统一房间不建立屏幕 P2P 会话。
- 自动验证、构建、真实设备和生产验收分别记录，未完成的外部门禁不宣称通过。
