# PartyLAN 本机闭环屏幕共享演示设计

> 目标：在不依赖 tsnet 和语音链路的前提下，优先打通 `创建房间 -> 开始共享 -> 前端显示真实桌面画面 -> 停止共享恢复空态` 的同机演示闭环。

## 1. 背景

当前仓库已经具备以下基础：

- Wails + React + TypeScript 前后端骨架
- `App -> Room -> StreamSender/Receiver -> Wails 事件` 的主调用链
- 屏幕捕获、编码、解码、房间协议的原型实现

当前主要问题：

- 默认网络层仍是 `StubNetwork`，无法形成真实视频流接收闭环
- 前端 UI 主要使用本地假状态，未真正接入 Wails API 和事件
- `ScreenPreview` 仍是占位渲染，不能显示真实视频帧
- 房间内没有“本机 viewer”概念，导致共享逻辑对无 peer 场景不友好

因此本阶段不追求完整一期 MVP，而是优先构建一个低风险、低返工的同机真实演示链路。

## 2. 目标与非目标

### 2.1 目标

- 创建房间后，应用进入可共享状态
- 房间内自动存在一个本机演示 viewer
- 开始共享后，后端捕获真实屏幕并走现有 sender/receiver 链路
- 前端收到真实帧并绘制到 canvas
- 停止共享后，画面恢复空态
- 整个流程不依赖真实 tsnet 网络

### 2.2 非目标

- 不实现 tsnet、Tailscale 节点发现、P2P 穿透
- 不实现双机共享
- 不实现 WebRTC 语音链路
- 不强制替换现有自定义视频编码为真正 H.264
- 不在本阶段处理 GPU 编码、驱动、WASAPI、AEC

## 3. 方案比较

### 方案 A：保留现有链路，补本机回环层

保留 `App -> Room -> StreamSender/Receiver` 主结构，只为演示模式补一个同进程回环链路和本机 viewer。

优点：

- 改动集中，能验证捕获、编码、传输、解码、前端渲染整条链路
- 对后续接回真实网络层最友好
- 能保留现有房间和事件结构

缺点：

- 需要为房间和网络增加少量演示模式逻辑

### 方案 B：绕过 Room，直接在 App 内预览

`StartSharing` 直接把采集结果推给前端，不经过 sender/receiver。

优点：

- 开发最快

缺点：

- 没有验证传输链路
- 后续回归真实房间/网络时返工最大

### 方案 C：仅后端采集 + 前端占位升级

保持大部分逻辑不变，只让前端显示某种简化预览。

优点：

- 风险低

缺点：

- 演示价值低，不能证明链路可用

### 结论

采用方案 A。

## 4. 总体设计

### 4.1 核心闭环

目标链路如下：

`CreateRoom -> 注入本机 viewer -> StartSharing -> ScreenCapturer -> SoftwareEncoder -> LoopbackConn -> StreamReceiver -> SoftwareDecoder -> EventStreamFrame -> Frontend Canvas`

### 4.2 设计原则

- 复用现有结构，不新增第二套共享流程
- 同机演示逻辑与未来真实网络逻辑边界清晰
- 前端尽量只做状态展示和画面绘制，不承担解码职责
- 一切以“快速出真实画面”为验收标准

## 5. 模块设计

### 5.1 网络层

新增一个仅用于本机演示的网络实现，例如 `LoopbackNetwork`。

职责：

- 兼容现有 `network.Network` 接口
- 提供可 `Listen` / `Dial` 的同进程连接
- 在 `Dial` 时将连接接到同一进程内对应端口的 listener

实现建议：

- 使用 `net.Pipe()` 作为底层连接
- 为每个端口维护内存 listener
- `Dial(ctx, nodeID, port)` 忽略真实 node 寻址，只在演示模式中将连接交付给本地 listener

不修改 `StubNetwork` 的职责定义，避免把“纯桩”与“可演示实现”混在一起。

### 5.2 房间层

`RoomImpl` 新增演示模式下的本机 viewer 注册逻辑。

行为：

- 房间创建成功后，自动注入一个逻辑 peer，例如 `loopback-viewer`
- 该 peer 出现在 `GetPeers()` 返回结果中
- `ConnectVideoToPeer("loopback-viewer")` 能通过 loopback network 成功建立视频连接
- 该 peer 不需要完整协议握手，只要能支持视频接收即可

约束：

- 本机 viewer 只用于演示模式
- 不污染未来真实 peer 协议设计

### 5.3 App 层

`App` 继续作为前后端编排入口。

关键调整：

- `CreateRoom` 成功后，前端能收到真实房间创建事件
- `StartSharing` 不再因“没有真实远端 peer”而空转
- `startReceiving` 收到解码帧后，发出包含真实画面数据的 `stream:frame`
- `StopSharing` 关闭 sender/receiver，并恢复前端空态

### 5.4 视频渲染链路

当前 `StreamReceiver` 已可输出解码后的 `video.Frame`。本阶段沿用该结构。

后端对前端输出建议：

- 直接发送 `width`、`height` 和 RGBA 帧数据
- 前端负责将 RGBA 数据写入 `ImageData` 再绘制到 canvas

原因：

- 前端无需参与视频解码
- 可最大化复用后端已有解码结果
- 更适合同机演示阶段快速验证

## 6. 前端设计

### 6.1 状态来源

前端不再用本地假数据模拟房间流程，改为：

- 调用 Wails 生成的 `CreateRoom` / `JoinRoom` / `LeaveRoom` / `StartSharing` / `StopSharing`
- 监听 `room:*`、`peer:*`、`stream:*` 事件驱动 UI 状态

### 6.2 页面行为

- 点击“创建房间”：调用后端 API，进入房间态
- 成员列表：显示本机和 `loopback-viewer`
- 点击“开始共享”：进入共享态
- 预览区：显示真实桌面画面
- 点击“停止共享”：恢复等待态

### 6.3 ScreenPreview

`ScreenPreview` 从占位组件改为真实画面组件：

- 订阅 `stream:frame`
- 将帧数据转换为 `ImageData`
- 绘制到 canvas
- 在没有画面时显示空态提示

不在本阶段引入 WebGL、离屏渲染或帧缓存优化。

## 7. 事件协议

沿用现有事件名，减少前后端接口震荡。

### 7.1 保留事件

- `room:created`
- `room:joined`
- `room:left`
- `peer:joined`
- `peer:left`
- `stream:started`
- `stream:stopped`

### 7.2 扩展事件

`stream:frame` 需要扩展为可直接渲染的负载：

```json
{
  "peerId": "loopback-viewer",
  "width": 1920,
  "height": 1080,
  "timestamp": 1710000000,
  "rgba": "base64..."
}
```

说明：

- `rgba` 使用 base64 字符串传输
- 该格式不适合长期高性能方案，但适合本阶段快速打通闭环

## 8. 错误处理

### 8.1 共享启动失败

以下情况应立即中止共享并给前端发送错误：

- 无可用显示器
- 屏幕捕获启动失败
- 编码器配置失败
- loopback 连接建立失败
- receiver 启动失败

### 8.2 共享中断

以下情况应停止共享并恢复空态：

- 捕获连续失败
- 连接关闭
- 解码失败导致流不可继续

### 8.3 重试要求

- 失败后允许用户再次点击开始共享
- `StopSharing` 多次调用应安全
- 离开房间后应释放 sender、receiver、listener

## 9. 验证标准

### 9.1 功能验收

满足以下流程即视为本阶段完成：

1. 启动应用
2. 点击“创建房间”
3. 成员列表出现本机和本机 viewer
4. 点击“开始共享”
5. 1-2 秒内预览区显示真实桌面画面
6. 点击“停止共享”
7. 预览区恢复空态

### 9.2 最低测试覆盖

- `LoopbackNetwork` 的 `Listen` / `Dial` / 收发数据测试
- `RoomImpl` 在演示模式下注入本机 viewer 的测试
- `App.StartSharing` 在无真实远端网络时可完成 sender/receiver 启动的测试
- 前端构建通过

## 10. 风险与后续

### 10.1 已知风险

- base64 传输 RGBA 帧的效率较低，只适合作为演示方案
- 现有自定义编码不是 H.264，不能代表最终协议
- `net.Pipe()` 路径证明的是链路可用性，不代表真实网络表现

### 10.2 后续衔接

本方案完成后，下一步应按以下顺序推进：

1. 将前端演示链路稳定化
2. 将 loopback network 抽象为可替换实现
3. 接入真实 tsnet 网络层
4. 再推进语音链路与一期计划对齐
