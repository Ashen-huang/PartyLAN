# PartyLAN 最小化窗口共享恢复实施计划

**状态：** 已实施并验证
**日期：** 2026-08-08
**设计依据：**
`docs/superpowers/specs/2026-08-08-partylan-minimized-window-share-restore-design.md`

**实施边界：** 只修改 Windows 原生窗口采集桥、客户端窗口共享接线与对应测试；不修改
网页端、Screego 协议、P2P/SFU 媒体链路、广州节点或已有未提交服务器文件，不推送远端。

## Task 1：建立最小化窗口恢复红灯测试

**修改：**

- `internal/native/windowsmedia/capability.go`
- `internal/gamecast/host/windowcapture/windowcapture_windows_test.go`
- `internal/gamecast/host/windowcapture/windowcapture_windows_cgo_test.go`

先增加失败测试，覆盖：

1. `WindowCaptureBridge` 暴露显式 `PrepareBounds`，与只读 `Bounds` 分离。
2. `Capturer.PrepareForShare` 使用准备接口返回尺寸，不回退到最小化外壳尺寸。
3. 窗口关闭保持 `ErrWindowClosed`；恢复超时返回明确的手动还原提示。
4. 正常窗口准备不触发恢复动作且立即返回现有尺寸。
5. 测试必须因准备接口和行为尚未实现而失败，再进入实现。

## Task 2：实现原生窗口恢复与稳定尺寸等待

**修改：**

- `internal/native/windowsmedia/bridge.h`
- `internal/native/windowsmedia/window_capture_windows.c`
- `internal/native/windowsmedia/window_capture_windows.go`
- `internal/native/windowsmedia/window_capture_stub.go`
- `internal/native/windowsmedia/capability.go`

实现 `PartyLAN_WindowCapturerPrepareBounds` 和 Go 桥接方法：

1. 校验输出指针和 HWND。
2. 正常窗口直接复用现有边界读取。
3. 最小化窗口读取 `WINDOWPLACEMENT.rcNormalPosition`，调用
   `ShowWindowAsync(hwnd, SW_RESTORE)`，不设置前台焦点。
4. 每 20 毫秒检查 `IsIconic` 和 `GetWindowRect`；实际尺寸连续两次稳定且不再是最小化
   外壳尺寸后返回。
5. 超时由调用方传入，产品路径固定为 1500 毫秒。
6. 窗口关闭返回 `WindowClosed`，恢复失败或超时返回 `Unavailable`。

不得修改现有 WGC 会话、NVENC 配置或共享源预览行为。

## Task 3：把准备步骤接入共享启动边界

**修改：**

- `internal/gamecast/host/windowcapture/windowcapture_windows.go`
- `internal/gamecast/host/windowcapture/windowcapture_stub.go`
- `app.go`
- 必要的客户端测试文件

测试先行覆盖：

1. `resolveWindowShareSelection` 在解析进程和创建发布器之前调用 `PrepareForShare`。
2. 返回的恢复后实际尺寸继续进入 `resolveShareResolution`。
3. 恢复失败时返回“所选窗口无法恢复，请手动还原后重试”。
4. 恢复失败不 claim 共享租约、不创建 P2P/SFU publisher、不启动 WGC 或 NVENC。
5. `Capturer.Start` 保留窗口二次有效性检查，覆盖准备后立即关闭的竞态。

实现应保持正常窗口、窗口音频进程选择和显示器共享路径不变。

## Task 4：Windows fixture 与实体 GPU 回归

**修改：**

- `internal/gamecast/host/windowcapture/windowcapture_windows_cgo_test.go`
- 必要的硬件探针测试

使用现有 fixture 验证：

1. 创建并显示 `800x500` 窗口。
2. 最小化后确认只读边界可表现为最小化外壳尺寸。
3. `PrepareForShare` 自动恢复窗口。
4. 返回尺寸等于恢复后的 `GetWindowRect` 且高度大于 100 像素。
5. 恢复后 WGC 可启动；显式启用 `PARTYLAN_TEST_NATIVE_ENCODER=1` 时，60 fps NVENC
   配置成功。

fixture 必须自行清理，不改变用户已有窗口。

## Task 5：完整验证、构建与本地提交

依次执行：

```powershell
go test ./internal/native/windowsmedia ./internal/gamecast/host/windowcapture
go test -tags partylan_native_media ./internal/native/windowsmedia ./internal/gamecast/host/windowcapture ./internal/video
go test ./...
go test -tags partylan_native_media ./...
node --test frontend/tests/*.test.mjs
npm.cmd run build --prefix frontend
git diff --check
```

然后：

1. 使用 `scripts/Start-PartyLAN.ps1 -Action build -Mode gamecast-client -UseNativeMedia` 构建。
2. 检查 `partylan_native_media`、`CGO_ENABLED=1`、EXE 大小、时间和 SHA-256。
3. 清理 Wails 生成的纯空白噪声，不回退用户或服务器改动。
4. 只提交本计划范围文件，不推送远端。
5. 由用户实机选择最小化窗口，确认窗口自动恢复且共享成功。

## STOP 条件

- `ShowWindowAsync(SW_RESTORE)` 在可控 fixture 上无法退出最小化状态。
- 恢复窗口必须调用 `SetForegroundWindow` 或修改系统级窗口策略才能生效。
- WGC 在窗口恢复后仍只提供最小化外壳内容。
- 修复需要修改 Screego、P2P/SFU 信令、网页端或广州节点。
- 新失败测试与已确认设计冲突。

## 完成标准

- 最小化窗口不会再以 `198x34` 等外壳尺寸进入 NVENC。
- 开始共享会自动恢复所选窗口但不主动抢占前台焦点。
- 恢复失败在媒体会话创建前终止并显示明确错误。
- 正常窗口、显示器共享、窗口音频和原生 NVENC 兼容重试保持现有行为。
- 全仓测试、前端构建和正式客户端构建通过。
