# PartyLAN 原生共享启动稳定性实施计划

**目标：** 修复原生共享启动阶段视频交替停顿，以及无声启动后无法继续共享音频的问题。

**设计依据：** `docs/superpowers/specs/2026-07-13-partylan-native-share-startup-stability-design.md`

## Task 1：建立视频调度与关键帧门控测试

**文件：**

- 修改：`app_test.go`
- 修改：`app.go`

1. 为容量 1 的 latest-frame mailbox 添加覆盖旧帧、读取最新帧测试。
2. 为发送节奏辅助逻辑添加测试：首帧前不发送，首帧后无新帧时复用最近帧。
3. 为关键帧门控添加测试：首次请求通过，启动 1.5 秒窗口内合并，5 秒后采用 300 ms 门限。
4. 运行目标测试并确认因行为尚未实现而失败。

## Task 2：解耦原生视频采集与发送

**文件：**

- 修改：`app.go`
- 修改：`internal/gamecast/host/windowcapture/windowcapture_windows.go`
- 修改：相关测试文件

1. 将 `runGameCastVideoLoop` 拆为采集 worker 和固定 FPS 发送 loop。
2. worker 只保留最新有效帧；临时错误继续捕获，永久错误上报一次。
3. 发送 loop 在取得首帧后每 tick 编码最近帧，保持固定 H.264 样本时长。
4. 去除窗口 capturer 内部额外帧率 sleep，并将单次抓帧超时限制到一帧周期附近，防止 worker 长时间阻塞。
5. 接入关键帧门控并记录合并日志。
6. 运行视频目标测试与窗口捕获测试。

## Task 3：建立无声启动与准确 PID 测试

**文件：**

- 修改：`app_test.go`
- 修改：`frontend/tests/*`

1. 为音频调度辅助逻辑添加测试：没有 PCM 时生成一帧静音，真实 PCM 到达后立即替换静音。
2. 为音频目标解析添加测试：请求含 `audioProcessId` 时优先使用指定 PID；缺省时保留按进程名解析。
3. 为前端共享请求添加测试或纯函数测试，验证选中窗口的 `processId` 被传入原生共享请求。
4. 运行目标测试并确认失败原因对应缺失行为。

## Task 4：实现 Opus 静音保活与 PID 贯通

**文件：**

- 修改：`app.go`
- 修改：`frontend/src/App.tsx`
- 修改：`frontend/src/components/RoomControls.tsx`
- 修改：`frontend/src/components/RoomToolsDialog.tsx`
- 修改：Wails 生成绑定

1. 在 `GameCastHostShareRequest` 增加可选 `audioProcessId`。
2. 从选中的 `ShareSource.processId` 贯通到 `StartNativeGameCastSharing`。
3. `audioMode=process` 时优先使用指定 PID，不随机改选同名进程。
4. 将 Opus loop 改为 20 ms ticker：消费捕获 PCM；无 PCM 时编码全零 PCM。
5. 保留永久捕获、编码和写入错误的现有失败处理。
6. 更新 Wails 绑定并运行音频、前端目标测试。

## Task 5：全量验证和构建

1. 运行 `npm run build`。
2. 运行全部前端 Node 测试。
3. 运行 `go test ./...`。
4. 运行 `go test -tags partylan_native_media ./...`。
5. 运行 `scripts/Start-PartyLAN.ps1 -Action build -Mode gamecast-client -UseNativeMedia -UseNativeCaptureExperiment -UseNativeEncoderExperiment`。
6. 记录 `PartyLAN.exe` 时间、大小和 SHA256，提供窗口/全屏无声启动复测步骤。

## STOP 条件

- 测试无法复现已确认的固定节奏或 PID 问题。
- 现有底层 API 无法在取消时可靠退出，导致新采集 worker 泄漏。
- Opus 静音包无法让实际 SFU 在 viewer offer 前发现音轨，需要修改服务端协商协议。

## 执行记录

- 已完成原生视频采集与固定节奏发送解耦；容量 1 mailbox 只保留最新帧，临时无帧时复用最近帧。
- 已完成启动关键帧门控：启动阶段 1.5 秒合并窗口，稳定阶段 300 ms 合并窗口。
- 已完成 20 ms Opus 静音保活；真实 PCM 到达后在同一音频 loop 内连续接管。
- 已贯通 `audioProcessId`；默认窗口音频使用窗口进程 PID，用户明确选择其他音频源时使用所选活动源 PID。
- 前端生产构建、全部 26 项 Node 测试、普通 Go 全量测试和原生媒体标签 Go 全量测试均通过。
- 已使用正式脚本生成 `build/bin/PartyLAN.exe`，原生媒体元数据验证通过并复制 `opus.dll`。
