# PartyLAN 最小化窗口共享恢复设计

## 背景

客户端当前在开始窗口共享前通过 `GetWindowRect` 获取编码尺寸。Windows 窗口最小化后，该 API 返回最小化外壳尺寸，例如 `198x34`。该尺寸随后被传给 NVENC，导致 `nvEncInitializeEncoder` 返回 `NV_ENC_ERR_INVALID_PARAM (8)`。即使使用兼容编码参数重试，也无法修复错误的输入尺寸。

Windows Graphics Capture 也无法可靠输出仍处于最小化状态的完整窗口内容。因此，单纯改用窗口的正常位置尺寸而不恢复窗口，只能避开编码器报错，不能保证共享画面正确。

## 目标

- 用户选择一个已最小化的窗口并开始共享时，客户端自动恢复该窗口。
- 在窗口恢复并取得稳定尺寸后，再启动 WGC 和 NVENC。
- 正常窗口沿用现有启动流程，不增加可感知等待或改变窗口状态。
- 恢复失败时给出明确的窗口恢复错误，不再进入 NVENC 初始化。

## 非目标

- 不改变共享源列表、预览生成或窗口筛选行为。
- 不主动把恢复后的窗口设置为前台窗口。
- 不修改网页端、Screego 协议、P2P/SFU 链路或广州服务器。
- 不尝试在窗口保持最小化时合成或缓存其完整内容。

## 设计

### 原生接口

在 Windows 窗口采集桥增加显式准备接口：

```text
PrepareBounds(windowHandle, timeoutMs) -> width, height, status
```

该接口与现有只读 `Bounds` 分开。共享源列表和预览继续使用现有逻辑，只有用户确认开始共享后才调用带副作用的准备接口。

原生实现执行以下步骤：

1. 校验 HWND 仍然存在。
2. 如果 `IsIconic` 为假，立即返回当前有效窗口尺寸。
3. 如果窗口已最小化，读取 `WINDOWPLACEMENT.rcNormalPosition` 作为预期恢复尺寸。
4. 调用 `ShowWindowAsync(hwnd, SW_RESTORE)`，不调用 `SetForegroundWindow`。
5. 每 20 毫秒检查一次 `IsIconic` 和 `GetWindowRect`。
6. 窗口不再最小化且尺寸连续两次稳定后返回实际尺寸。预期恢复尺寸仅用于避免把最小化外壳尺寸误判为稳定结果，不直接作为编码尺寸。
7. 最长等待 1500 毫秒。窗口关闭返回 `WindowClosed`；超时或恢复失败返回 `Unavailable`。

### Go 调用边界

`windowcapture.Capturer` 增加 `PrepareForShare`，负责调用原生准备接口并把状态映射为清晰错误。

`resolveWindowShareSelection` 改为调用 `PrepareForShare`，然后再解析窗口进程信息。返回的实际恢复尺寸继续进入现有 `resolveShareResolution`，因此“原始画质”和用户指定分辨率的行为保持不变。

`Capturer.Start` 仍保留现有窗口有效性检查，以覆盖准备完成后窗口立即关闭的竞态。

### 错误处理

- 窗口在恢复期间关闭：沿用“所选窗口已关闭”错误。
- 1500 毫秒内未恢复：返回“所选窗口无法恢复，请手动还原后重试”。
- 恢复成功后编码器仍失败：保留现有 NVENC 诊断，其中包含实际恢复后的尺寸。

客户端不会在恢复失败时创建 P2P/SFU 发布会话、WGC 会话或 NVENC 编码器。

## 测试

### 单元测试

- 正常窗口：准备接口直接返回当前尺寸，不调用恢复动作。
- 最小化窗口：恢复后返回正常且稳定的尺寸，不返回最小化外壳尺寸。
- 恢复超时：返回明确错误，调用方停止启动流程。
- 窗口关闭：保持 `ErrWindowClosed` 分类。

### Windows 集成测试

使用现有 WGC fixture 创建一个可控窗口：

1. 创建并显示 `800x500` fixture。
2. 最小化 fixture。
3. 调用 `PrepareForShare`。
4. 断言窗口已退出最小化状态，准备接口返回值等于恢复后的实际 `GetWindowRect` 尺寸，且高度大于 100 像素。
5. 启动 WGC 和 60 fps NVENC，确认配置成功。

硬件集成测试继续由 `PARTYLAN_TEST_NATIVE_ENCODER=1` 显式启用，普通 CI 不依赖实体 GPU。

## 验证与交付

- 运行窗口采集和原生编码焦点测试。
- 运行普通及 `partylan_native_media` 全仓 Go 测试。
- 运行前端 40 项 Node 测试和生产构建。
- 构建新的 `build/bin/PartyLAN.exe`，检查原生构建标签和 SHA-256。
- 实机选择最小化窗口，确认窗口自动恢复且共享启动成功。
