# PartyLAN GameCast 窗口共享与共享音频设计

> Scope: 为默认 `gamecast-client` 路径补齐 Windows 原生 `整屏 / 窗口` 共享选择、共享音频并入 GameCast、网页房共享兼容回退，以及默认用户路径中的 legacy Tailscale 清理。

## 1. 背景

当前 PartyLAN 已经把默认主线切到 `gamecast-client`，并且已经具备：

- 连接现有 GameCast / Screego 服务端
- 创建和加入房间
- 原生 viewer 观看网页端或客户端 host
- 原生 host 进行整屏共享
- 基础共享音频枚举和进程音频捕获能力

现阶段的主要缺口集中在 host 体验：

- 只能默认整屏共享，不能像网页端一样选择整屏或窗口
- GameCast 原生 host 还没有真正把共享音频作为独立媒体轨发给 viewer
- 进入网页端已创建的房间后，共享接管路径仍不稳定
- 默认 UI 里还残留一些旧 `Tailscale / tsnet / DERP / Auth Key / 节点加入` 痕迹

这份设计的目标不是重做网页端，而是在尽量少改服务器、保持网页端继续可用的前提下，把原生客户端补到一个接近网页端日常使用体验的程度。

## 2. 目标

- 在原生客户端里支持两类共享源：
  - 整个屏幕
  - 单个窗口
- 整屏共享时默认共享系统音频。
- 窗口共享时默认自动绑定该窗口所属进程音频。
- 允许用户在共享音频页把默认音频改成关闭，或改绑到其他活跃进程。
- 网页 viewer 与原生 viewer 都能继续观看客户端共享。
- 尽量不修改服务器协议和网页端已有观看路径。
- 对于“进入网页端已创建房间后发起共享”，优先保证可用和稳定；如原生接管代价高，则复用现有 Web 兼容共享路径。
- 默认 `gamecast-client` 用户路径中清理旧 Tailscale 主线残留。

## 3. 非目标

- 不要求网页端 host 同步支持窗口级共享。
- 不重写 Screego / GameCast 服务端房间模型。
- 不新增新的房间接口或新的 viewer 协议，除非验证表明确实不可避免。
- 第一版不做多窗口同时共享。
- 第一版不做无窗口后台进程的纯进程画面共享。
- 第一版不做复杂音频混音、录音、回放、空间音频或语音增强。

## 4. 核心原则

- 服务器最小改动优先。
- 网页端当前可用能力优先保护。
- 对复杂链路先提供稳定回退，再追求统一。
- 视频轨和共享音频轨分离，语音聊天继续作为独立能力。
- 默认用户旅程只暴露 GameCast 主线，不再暴露旧 Tailscale 工具心智。

## 5. 方案对比

### 5.1 方案 A：Windows Graphics Capture + 原生音频轨

为窗口共享新增 Windows Graphics Capture，为整屏共享继续保留原生整屏路径；音频通过进程 loopback 或系统 loopback 编码成 Opus track，并接入现有 GameCast native host。

优点：

- 最接近网页端“整屏 / 窗口二选一”的使用体验。
- 更适合支持“窗口被遮挡、最小化后仍可继续共享”的需求。
- 对 viewer 端仍保持现有 WebRTC 观看协议。

缺点：

- 需要新增窗口枚举和窗口捕获适配器。
- 需要把共享音频真正接到 GameCast native host 中。

结论：推荐。

### 5.2 方案 B：WebView2 负责窗口共享，原生 host 负责其他能力

窗口共享继续走 Web 兼容共享流程，整屏共享走原生 host。

优点：

- 初期实现路径可能更短。

缺点：

- 体验割裂。
- 共享源和音频策略难以统一。
- viewer 进入网页房后共享逻辑会更加分叉。

结论：仅作为网页房接管共享时的回退方案，不作为主线。

### 5.3 方案 C：继续扩 DXGI 整屏捕获到窗口级

优点：

- 复用现有原生媒体桥较多。

缺点：

- 很难满足“窗口被遮挡或最小化后仍继续共享”的要求。
- 从能力边界上不适合承担窗口共享主线。

结论：不推荐。

## 6. 目标架构

```text
PartyLAN Client
  React UI
    room tools / share source / audio routing / diagnostics
  Wails Go Backend
    GameCast profile / room-state / host orchestration
    share source catalog
    display capture adapter
    window capture adapter
    process/system audio routing
    native GameCast host
  Native Capture
    display capture
    window capture (Windows Graphics Capture)
  Native Audio
    system loopback
    process loopback
        |
        | WebRTC H264 + Opus
        v
GameCast / Screego Server
  existing /sfu-ws
  existing room-state / history / config endpoints
        |
        v
Web Viewer / Native Viewer
```

客户端内部将同时保留两条 host 路径：

- `NativeHostPath`
  - 客户端自建房间时优先使用
  - 支持整屏共享、窗口共享、共享音频
- `WebCompatHostPath`
  - 主要用于“进入网页端已创建房间后发起共享”的兼容回退
  - 复用现有非原生共享逻辑

## 7. UI 与交互

### 7.1 房间工具中的共享页

共享页调整为三段：

- `共享源类型`
  - 整个屏幕
  - 窗口
- `共享目标选择`
  - 整屏模式下列出显示器
  - 窗口模式下列出当前可共享窗口
  - 每项显示名称、应用名、图标
- `质量设置`
  - 分辨率
  - 帧率

### 7.2 音频页行为

- 整屏共享：
  - 默认系统音频
  - 可切换为关闭
  - 可切换为指定活跃进程音频
- 窗口共享：
  - 默认自动绑定窗口所属进程音频
  - UI 明确显示当前自动绑定对象
  - 允许切换为关闭
  - 允许切换为别的活跃进程音频

### 7.3 共享中的顶部状态

共享视图顶部必须优先显示真实值，而不是仅显示设定值：

- 真实分辨率
- 真实解码 FPS
- 真实源 FPS
- 真实码率
- 当前共享源名称
- 当前音频模式

### 7.4 网页房中的共享行为

当客户端进入网页端已创建的房间后：

- 如果原生 host 接管共享只需要低风险切换，则允许切到原生 host
- 如果需要重写较大状态机或影响稳定性，则直接走 `WebCompatHostPath`
- 无论选哪条路径，目标都是“优先保证可用和兼容”，不是强求统一

## 8. 模块拆分

### 8.1 `sharecatalog`

新增共享源目录模块，职责：

- 枚举显示器
- 枚举可共享窗口
- 返回窗口标题、进程名、进程 ID、窗口句柄、可捕获状态

建议位置：

- `internal/gamecast/sharecatalog`

### 8.2 `DisplayCaptureAdapter`

职责：

- 负责整屏共享
- 复用现有显示器级原生捕获路径

建议位置：

- `internal/gamecast/host/displaycapture`

### 8.3 `WindowCaptureAdapter`

职责：

- 负责窗口共享
- 使用 Windows Graphics Capture
- 允许窗口被遮挡或最小化后继续采集

建议位置：

- `internal/gamecast/host/windowcapture`

### 8.4 `AudioRouting`

职责：

- 决定整屏共享默认系统音频
- 决定窗口共享默认绑定窗口所属进程音频
- 支持关闭、重绑、失效时回退

建议位置：

- `internal/gamecast/audio`

### 8.5 `HostOrchestrator`

职责：

- 接收 UI 的“开始共享”请求
- 判断当前房间类型和当前共享源类型
- 选择 `NativeHostPath` 或 `WebCompatHostPath`
- 统一启动 / 停止视频和音频
- 处理失败回退

建议位置：

- `internal/gamecast/host`

## 9. 数据流

### 9.1 客户端自建房间 + 原生整屏共享

1. 用户创建房间
2. 选择“整个屏幕”
3. 默认音频模式为系统音频
4. 编排器选择 `NativeHostPath`
5. 原生整屏捕获启动
6. 系统音频 loopback 启动
7. native host 发送 H.264 + Opus 到现有 SFU

### 9.2 客户端自建房间 + 原生窗口共享

1. 用户创建房间
2. 选择“窗口”
3. 默认音频绑定该窗口所属进程
4. 编排器选择 `NativeHostPath`
5. 窗口捕获启动
6. 进程音频 loopback 启动
7. native host 发送 H.264 + Opus 到现有 SFU

### 9.3 进入网页端房间后发起共享

1. 用户先以 viewer 身份进入网页端房间
2. 用户点击开始共享
3. 编排器判断当前房间是否适合低风险切到原生 host
4. 如代价高或不稳定，直接切到 `WebCompatHostPath`
5. 保持房间可观看和网页 viewer 兼容优先

## 10. 错误处理与回退

### 10.1 窗口共享

- 目标窗口关闭：
  - 自动停止共享
  - 明确提示“共享窗口已关闭，请重新选择窗口”
- 目标窗口最小化、被遮挡、切换桌面：
  - 继续共享
- 目标窗口采集初始化失败：
  - 允许用户退回整屏共享

### 10.2 音频

- 窗口绑定的进程音频不可用：
  - 默认回退到静音共享
  - 不自动乱切到其他进程
- 整屏系统音频不可用：
  - 回退到静音共享
  - 给出明确提示

### 10.3 网页房共享

- 原生接管代价高：
  - 直接回退 `WebCompatHostPath`
- Web 兼容共享也失败：
  - 给出清晰错误
  - 保留 viewer 状态，不强制退出房间

## 11. Legacy Tailscale 清理

默认 `gamecast-client` 模式下，主界面不再暴露以下旧主线路径内容：

- Tailscale
- tsnet
- DERP
- Auth Key
- 目标节点 ID
- 旧网络节点列表

这些内容只能保留在：

- 显式 legacy 模式
- 或开发/诊断面板中

普通用户默认路径只保留：

- GameCast 服务器
- 房间
- 共享
- 音频
- 观看与状态

## 12. 分阶段实施

### 12.1 P1 默认路径清理与稳定化

- 清理默认 UI 中残留的旧 Tailscale 主路径
- 修复共享顶部真实指标、错误文案、窗口拖动和标题栏
- 稳定 viewer 重连和状态展示

### 12.2 P2 共享源模型与 UI 骨架

- 建立 `display / window` 共享源模型
- 构建共享页和音频页的新交互骨架
- 枚举显示器和窗口
- 建立默认音频联动规则

### 12.3 P3 原生窗口共享与共享音频

- 新增窗口捕获路径
- 整屏系统音频共享
- 窗口进程音频共享
- 把 Opus 音频轨接入 native host

### 12.4 P4 网页房共享兼容收尾

- 为网页房共享决定原生接管或 Web 兼容回退
- 稳定共享重新开始、停止、再次观看等状态机

## 13. 验收标准

### 13.1 功能验收

- 可在客户端自建房间中选择整屏或窗口共享
- 整屏共享默认系统音频可用
- 窗口共享默认绑定窗口进程音频可用
- 网页 viewer 和原生 viewer 均能观看
- 关闭窗口后共享自动结束
- 窗口最小化或遮挡后仍继续共享

### 13.2 兼容验收

- 网页端原有观看功能不回退
- 网页房中客户端发起共享至少存在一条稳定可用路径
- 默认主界面不再出现旧 Tailscale 主线路径残留

### 13.3 观测验收

- 顶部显示真实分辨率、真实 FPS、真实码率
- 共享失败提示为正常中文
- 日志能区分：
  - 共享源选择
  - 捕获启动
  - 音频路由
  - WebRTC 连接
  - room-state / host orchestration

## 14. 风险

- 窗口捕获需要新增 Windows Graphics Capture 路径，复杂度高于现有整屏捕获
- 原生 host 新增音频轨后，需要确认网页 viewer 与原生 viewer 都能继续稳定消费
- 网页房接管共享这条链路，如果强行统一成原生 host，容易再次引入状态机不稳定
- 现有前端和主界面仍夹带不少 legacy 残留，需要先清理再叠功能

## 15. 推荐下一步

下一步不要直接开始“大一锅”实现。

建议先写三份实施计划：

- `P1-default-path-cleanup`
- `P2-native-window-share-and-audio`
- `P3-web-room-host-compat`

其中 P1 应作为最先执行项，先把默认路径和观测性收干净，再进入窗口共享和共享音频实现。
