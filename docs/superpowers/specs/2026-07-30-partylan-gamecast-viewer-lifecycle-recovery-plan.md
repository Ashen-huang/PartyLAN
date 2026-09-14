# PartyLAN GameCast 观看端生命周期恢复实施计划

**目标：** 修复客户端共享后观看端持续黑屏或显示“当前无人共享”的连接生命周期问题。

**设计依据：** `docs/superpowers/specs/2026-07-30-partylan-gamecast-viewer-lifecycle-recovery-design.md`

## Task 1：建立 Go WebSocket 代际回归测试

**文件：**

- 修改：`app_test.go`
- 修改：`app.go`

1. 添加当前代际通过、旧代际拒绝的状态/消息回调测试。
2. 添加关闭或替换先使旧代际失效的测试。
3. 添加旧连接完成不能清除或覆盖新连接所有权的测试。
4. 先运行目标 Go 测试并确认旧实现不能满足断言。

## Task 2：实现 Go WebSocket 代际隔离

**文件：**

- 修改：`app.go`

1. 在 `App` 增加受 `mu` 保护的单调递增 WebSocket generation。
2. 将关闭、登记、失败清理和当前代际检查封装为小型辅助方法。
3. `newGameCastWSClient` 的消息与状态回调捕获 generation，并在发布前检查当前性。
4. Host/Viewer 连接在拨号前登记正确发送目标，失败或被取代时只清理自身。
5. 运行目标 Go 测试并确认通过。

## Task 3：建立前端生命周期与 Receiver 失败测试

**文件：**

- 修改：`frontend/tests/gamecastOrchestrator.test.mjs`
- 修改：`frontend/tests/gamecastReceiver.test.mjs`
- 修改：`frontend/src/media/gamecastOrchestrator.ts`
- 修改：`frontend/src/media/gamecastReceiver.ts`

1. 添加重连单航班测试：第二个并发 begin 被拒绝。
2. 添加令牌测试：invalidate 后旧 Promise/Receiver 完成无效。
3. 添加 attempt 语义测试：WebSocket 成功不重置，媒体到达才构成恢复成功。
4. 添加共享 active 且 Receiver 缺失时允许重连、connecting/connected/三次上限时拒绝的测试。
5. 添加“观看者先进入、信令关闭、主播稍后共享”的状态序列测试。
6. 添加 Receiver 关闭后在途 stats 结果不能继续上报的测试。
7. 先运行两组 Node 目标测试并确认失败原因对应旧行为。

## Task 4：接入前端令牌化清理和单航班重连

**文件：**

- 修改：`frontend/src/App.tsx`
- 修改：`frontend/src/media/gamecastOrchestrator.ts`
- 修改：`frontend/src/media/gamecastReceiver.ts`

1. 实现 `GameCastViewerLifecycle` 并在 App 中保存唯一实例。
2. 新建 Receiver 时捕获令牌，所有流、状态和统计回调检查令牌与实例所有权。
3. 统一执行“先 invalidate、后置空 ref、再 close、再清媒体/统计”的清理顺序。
4. 在当前 WebSocket 关闭/报错、房间 `sharing=false`、离开房间和角色切换时调用清理。
5. 自动重连取得单航班令牌后才增加 attempt；拨号成功只结束在途状态，不清零 attempt。
6. `onRemoteStream` 收到当前媒体后将 attempt 清零。
7. `sharing=false -> true` 且无 Receiver 时允许一次有界重连；保持最多三次。
8. 运行前端目标测试和生产构建。

## Task 5：回归、构建与产物审计

1. 运行全部前端 Node 测试。
2. 运行 `npm.cmd run build`。
3. 运行 `go test .` 与 `go test ./internal/gamecast/...`。
4. 运行 `go test ./...`。
5. 运行 `go test -tags partylan_native_media ./...`。
6. 运行 `git diff --check` 并审计仅有计划内文件变更。
7. 使用正式脚本构建 `gamecast-client` Windows 客户端；记录 EXE 路径、大小、时间和 SHA-256。
8. 不部署、不重启云端服务、不推送远端。

## 实机复测建议

1. 观看者先进入房间，等待超过 30 秒后主播开始共享；观看端应自动恢复出画面。
2. 主播连续停止并重新开始共享三次；每次观看端都应销毁旧 Receiver 并重新出画面。
3. 人为中断观看端信令后恢复网络；同一时刻只能看到一次重连，最多三次。
4. 确认 WebSocket 连通但媒体尚未到达时 attempt 不清零；首个远端媒体轨到达后才清零。
5. 检查新遥测中不再长期出现 `receiverState=active`、空 `pairId`、无媒体的僵尸状态。

## STOP 条件

- 无法用测试复现旧代际回调或 Receiver 僵尸状态。
- 代际隔离需要修改 SFU 协议或生产服务才能成立。
- 前端必须引入无限重试才能覆盖观看者先进入场景。
- 构建需要生产部署、服务重启或远端推送授权。

## 执行记录

- 已先建立红灯证据：Go 目标测试因缺少 WebSocket generation 门控而无法编译；Receiver 测试证明关闭后仍会接收迟到轨道并上报在途统计；观看者先进入场景证明 `status=connected + receiver=idle` 时旧判断不会恢复。
- Go 层已加入单调递增 generation，并以 generation 与 client 实例双重校验消息/状态回调；连接替换先失效、再关闭，旧连接完成不能清除新连接。
- 前端已加入令牌化 `GameCastViewerLifecycle`、单航班门控和有界三次恢复；WebSocket 拨号成功保留 attempt，只有当前 Receiver 收到远端媒体流后才清零。
- 房间 `sharing=false`、当前信令关闭/报错、离开房间和角色切换均会先使令牌失效，再关闭并置空 Receiver，同时清理流与统计；Receiver 自身也拒绝关闭后的迟到轨道和异步统计结果。
- 全部 34 项前端 Node 测试、前端生产构建、`go test ./...` 和 `go test -tags partylan_native_media ./...` 均通过。
- `go vet . ./internal/gamecast/...` 通过；`go vet ./...` 仍会报告未改动 Windows 原生音视频桥接中的既有 `unsafe.Pointer` 告警。
- 正式脚本已生成 `build/bin/PartyLAN.exe`，大小 20,383,232 字节，SHA-256 为 `A6B2EDFF9564B12FF54B191FF2386B4B0CD6356EBA9BC92732DBC83262C9D4C6`；原生媒体构建元数据校验及 `gamecast-client` 启动冒烟通过。
- 本轮未部署或重启云端服务，也未推送远端。真实双机的“观看者先进入、主播后共享”和连续停止/重开共享验收仍需使用该 EXE 执行。
