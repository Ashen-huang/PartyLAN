# PartyLAN 原生窗口共享与共享音频实施计划

> Based on `2026-07-09-partylan-gamecast-window-share-audio-design.md`

## 1. 目标

为默认 `gamecast-client` 的原生 host 补齐：

- 整屏 / 窗口 两类共享源选择
- 整屏默认系统音频
- 窗口默认绑定所属进程音频
- Opus 音频轨并入现有 GameCast native host

这一阶段以“客户端自建房间中的原生共享”作为主战场，不先解决网页房接管共享。

## 当前执行状态（2026-07-10）

- P1 已完成：共享源模型、显示器/窗口目录、窗口预览与进程元数据已接通。
- P2 已完成：整屏/窗口选择、共享音频策略和共享源展示已接通。
- P3 的适配器、错误分类与生命周期已完成：临时无帧会持续重试，窗口句柄销毁会自动停止共享并上报明确错误。当前实现仍为 `PrintWindow + BitBlt`，不是真正的 Windows Graphics Capture；由于当前 MinGW 构建环境没有 Windows SDK/C++/WinRT 头文件，最小化与切桌面只能按现有 GDI 能力尽力工作。
- P4 已完成：GameCast native host 已包含 Opus track；整屏系统音频、窗口所属进程音频、手动关闭和手动改绑均已接入。指定进程音频失败时不再静默回退到全局系统音频，而是关闭音频、保留视频并提示用户；运行中音频源失效也执行相同降级。
- P5 代码收尾已完成：按源类型选择捕获路径，指标使用真实发送数据，窗口关闭、音频失效、手动停止和重新共享均通过会话身份保护与统一清理处理；网页兼容共享协议未改动。

自动验证已通过：

- `npm run build`
- `node --test --experimental-strip-types tests/gamecastOrchestrator.test.mjs`
- `go test .`
- `go test ./internal/gamecast/...`
- `go test ./internal/native/windowsaudio`

仍需真机验收或后续工具链工作：

- 安装 Windows SDK/C++/WinRT 后，将 GDI 窗口适配器迁移为真正的 Windows Graphics Capture。
- 验收窗口最小化、遮挡、切桌面、关闭窗口，以及进程音频在暂停、恢复和进程退出时的行为。
- 分别用网页 viewer 和原生 viewer 验收视频与共享音频。

## 2. 交付策略

本轮实现按五条主线推进：

1. 共享源模型与目录枚举
2. 共享 UI 骨架
3. 窗口捕获适配器
4. 共享音频路由与 Opus track
5. 原生 host 编排与收尾

关键路径：

`P1 共享源模型 -> P2 UI 骨架 -> P3 窗口捕获 -> P4 音频轨接入 -> P5 联调`

## 3. 现状约束

- 当前 GameCast 原生 host 只有显示器级捕获，且 `displayIndex` 仍固定。
- 当前共享音频能力只存在于旧媒体路径，尚未真正接入 GameCast native host。
- 当前窗口选择 UI 不存在。
- 当前房间工具页仍只表达“分辨率 / 帧率”，不表达共享源类型。

## 4. 分阶段计划

### P1. 共享源模型与目录枚举

**任务**

1. 定义共享源模型：
   - `display`
   - `window`
2. 定义共享源公共字段：
   - id
   - kind
   - label
   - process id
   - process name
   - window handle
   - icon
3. 实现显示器枚举。
4. 实现顶层窗口枚举。
5. 过滤明显不适合作为共享目标的窗口：
   - 无标题
   - 不可见
   - 工具窗口
   - 当前 PartyLAN 自身窗口

**建议文件**

- `internal/gamecast/sharecatalog`
- `frontend/src/types.ts`

**完成标准**

- 前后端都能识别共享源类型和共享源元数据

### P2. 房间工具 UI 骨架

**任务**

1. 在共享页新增共享源类型选择：
   - 整个屏幕
   - 窗口
2. 根据类型渲染目标列表：
   - 显示器列表
   - 窗口列表
3. 在音频页展示当前默认音频模式：
   - 整屏默认系统音频
   - 窗口默认窗口所属进程音频
4. 允许手动切换为：
   - 关闭
   - 其他活跃进程音频
5. 共享开始后，顶部显示当前共享源名称。

**建议文件**

- `frontend/src/components/RoomToolsDialog.tsx`
- `frontend/src/components/AudioPanel.tsx`
- `frontend/src/i18n.ts`

**完成标准**

- 用户可在 UI 中明确选择整屏或窗口
- 音频页能表达默认音频联动

### P3. 原生窗口捕获适配器

**任务**

1. 新增 `WindowCaptureAdapter`。
2. 使用 Windows Graphics Capture 路径捕获指定窗口。
3. 保证窗口被遮挡、最小化、切桌面后仍继续共享。
4. 窗口关闭时，自动停止共享并上报可理解错误。
5. 保留现有整屏捕获路径作为 `DisplayCaptureAdapter`。

**建议文件**

- `internal/gamecast/host/windowcapture`
- `internal/gamecast/host/displaycapture`
- 必要时扩展 `internal/native/windowsmedia`

**完成标准**

- 单窗口共享可持续输出稳定视频帧

### P4. 共享音频并入 GameCast native host

**任务**

1. 在原生 host 中新增 Opus audio track。
2. 整屏共享时默认系统音频 loopback。
3. 窗口共享时默认绑定窗口所属进程音频。
4. 支持手动关闭共享音频。
5. 支持手动改绑为其他活跃进程音频。
6. 音频源失效时：
   - 自动停用音频 track
   - 给出明确提示
7. 停止共享时释放音频资源。

**建议文件**

- `internal/gamecast/audio`
- `internal/gamecast/nativehost`
- `internal/native/windowsaudio`
- `app.go`

**完成标准**

- viewer 端能收到视频 + 共享音频

### P5. 原生 host 编排与收尾

**任务**

1. Host 编排器接收新的共享源配置。
2. 根据共享源类型选择整屏或窗口捕获。
3. 根据共享源类型确定默认音频策略。
4. 共享中的顶部指标显示真实分辨率、FPS、码率。
5. 共享结束、窗口关闭、音频源消失时统一清理。

**建议文件**

- `app.go`
- `frontend/src/App.tsx`
- `frontend/src/components/ScreenPreview.tsx`

**完成标准**

- 原生 host 自建房共享整屏/窗口都可用

## 5. 验证

- `npm run build`
- `go test .`
- `go test ./internal/gamecast/nativehost`
- 补充新增模块单元测试：
  - 共享源模型
  - 窗口过滤规则
  - 默认音频策略
- 手工验收：
  - 自建房整屏共享 + 系统音频
  - 自建房窗口共享 + 窗口所属进程音频
  - 网页 viewer 观看
  - 原生 viewer 观看
  - 窗口最小化/遮挡后继续共享
  - 关闭窗口后自动停止

## 6. 风险

- Windows Graphics Capture 路径集成复杂度较高。
- 共享音频接入 native host 后需要确认网页 viewer 兼容性。
- 系统音频与窗口进程音频两种默认策略需要防止混淆。

## 7. 结束条件

- 客户端自建房时支持整屏 / 窗口共享
- 整屏默认系统音频可用
- 窗口默认进程音频可用
- viewer 端可继续观看和收听
