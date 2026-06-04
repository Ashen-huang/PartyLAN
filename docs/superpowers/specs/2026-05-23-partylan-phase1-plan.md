# PartyLAN 第一阶段 MVP 实施计划

> 目标：实现可用的 P2P 屏幕共享 + 语音聊天，验证核心架构

## 任务依赖图

```
T1: 项目骨架 ──┬──▶ T2: tsnet 集成 ──▶ T4: 视频流传输 ──▶ T6: 集成测试
               │                        │
               ├──▶ T3: 屏幕捕获 ───────┘
               │
               ├──▶ T5: 语音聊天 ────────▶ T6: 集成测试
               │
               └──▶ T7: 基础 UI ──────────▶ T6: 集成测试
```

## 任务详情

### T1: 项目骨架搭建

**目标**：初始化 Wails + Go + React 项目，建立基本目录结构

**步骤**：
1. 安装 Wails CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
2. 运行 `wails init -n PartyLAN -t react-ts` 初始化项目
3. 按设计文档创建 `internal/` 目录结构（空文件 + 接口定义）
4. 配置 `go.mod`，预添加依赖：`tailscale.com/tsnet`、`github.com/pion/webrtc`
5. 创建 `pkg/config/` 配置管理模块
6. 创建 `internal/app/events.go` 事件类型定义
7. 验证 `wails dev` 能正常启动

**交付物**：可运行的空白 Wails 应用，目录结构就绪

**预计工时**：2-3 天

---

### T2: tsnet 集成与节点发现

**目标**：应用启动后自动加入 Tailscale 网络，在线成员可互相发现

**步骤**：
1. 实现 `internal/network/tsnet.go`
   - 创建 tsnet.Server 实例，配置控制服务器地址
   - 实现 auth key 认证流程
   - 节点身份持久化（密钥存储到本地配置目录）
2. 实现 `internal/network/discovery.go`
   - 通过 Tailscale API 查询在线节点列表
   - 匹配应用层房间成员
   - 节点状态变更通知（上线/下线）
3. 实现 `internal/network/conn.go`
   - 封装 tsnet 连接的 Dial/Listen
   - 连接池管理
   - 连接质量检测（延迟、丢包率）
4. 编写单元测试：模拟两个 tsnet 节点互相连接

**依赖**：T1

**交付物**：两个实例可自动组网并发现对方

**预计工时**：5-7 天

---

### T3: DXGI 屏幕捕获

**目标**：实现 GPU 加速的屏幕捕获，输出原始帧数据

**步骤**：
1. 编写 C++ 桥接代码 `third_party/wasapi/dxgi_capture.cpp`
   - 初始化 D3D11 设备
   - 创建 DXGI Output Duplication
   - 抢帧 + 纹理拷贝
   - 导出为 NV12/I420 格式
2. 实现 `internal/video/capture.go`（CGO 调用）
   - 封装 C++ 捕获接口
   - 帧缓冲区管理（环形缓冲）
   - 可配置参数：显示器选择、捕获区域、帧率上限
3. 实现 `internal/video/encoder.go`
   - 初始版本使用软件编码（x264 via CGO 或 Go 原生 H.264 库）
   - 统一编码器接口（Encode(frame) → NAL units）
   - 码率/质量控制
4. 编写基准测试：测量捕获+编码延迟和 CPU/GPU 占用

**依赖**：T1

**交付物**：可捕获屏幕并编码为 H.264 流

**预计工时**：7-10 天

---

### T4: 视频流传输

**目标**：通过 tsnet 网络将视频流从分享端推送到观看端

**步骤**：
1. 实现 `internal/room/protocol.go`
   - Protobuf 消息定义（RoomCreate, RoomJoin, StreamStart, StreamStop, Heartbeat）
   - 消息序列化/反序列化
2. 实现 `internal/room/room.go`
   - 房间创建、加入、离开
   - 成员状态管理
   - 心跳机制
3. 实现视频流传输管道
   - 分享端：capture → encoder → tsnet TCP/UDP 推送
   - 观看端：接收 → 解码 → 渲染
   - 帧时间戳同步
4. 实现 `internal/video/decoder.go`
   - H.264 软件解码
   - 解码后帧传递给前端渲染

**依赖**：T2, T3

**交付物**：分享端的屏幕画面可在观看端实时显示

**预计工时**：7-10 天

---

### T5: 基础语音聊天

**目标**：建立独立于屏幕流的语音通道

**步骤**：
1. 实现 `internal/voice/channel.go`
   - 使用 Go Pion WebRTC 建立语音连接
   - SDP offer/answer 交换通过 tsnet 信令通道
   - Opus 编解码
2. 实现 `internal/audio/capture.go`
   - WASAPI 捕获麦克风输入
   - 环回捕获系统音频（MVP 阶段不分离）
3. 实现基础音频控制
   - 静音/取消静音
   - 音量调节
4. 语音与视频的同步播放

**依赖**：T2

**交付物**：团队成员可实时语音对话

**预计工时**：5-7 天

---

### T6: 集成测试与联调

**目标**：端到端验证所有模块协同工作

**步骤**：
1. 双机联调：分享端 + 观看端完整流程
2. 多人联调：3+ 成员同时在线
3. 性能基线测量：
   - 屏幕共享延迟（端到端 < 200ms）
   - 语音延迟（< 100ms）
   - CPU/内存占用
4. Bug 修复和稳定性优化

**依赖**：T4, T5, T7

**交付物**：可用的 MVP

**预计工时**：3-5 天

---

### T7: 基础 UI

**目标**：实现设计文档中的基础界面

**步骤**：
1. 实现成员列表组件
   - 在线状态指示（●/○）
   - 语音状态图标（🎤/🔇）
   - 共享状态标识
2. 实现屏幕预览组件
   - 视频流渲染（Canvas/WebGL）
   - 全屏切换
3. 实现音频控制面板
   - 麦克风开关按钮
   - 音量滑块
4. 实现房间操作
   - 创建房间按钮
   - 加入房间对话框
5. 实现状态栏
   - 延迟、码率、连接类型显示

**依赖**：T1

**交付物**：完整的基础 UI

**预计工时**：5-7 天

---

## 总结

| 任务 | 预计工时 | 依赖 |
|------|----------|------|
| T1: 项目骨架 | 2-3 天 | 无 |
| T2: tsnet 集成 | 5-7 天 | T1 |
| T3: 屏幕捕获 | 7-10 天 | T1 |
| T4: 视频流传输 | 7-10 天 | T2, T3 |
| T5: 语音聊天 | 5-7 天 | T2 |
| T6: 集成测试 | 3-5 天 | T4, T5, T7 |
| T7: 基础 UI | 5-7 天 | T1 |

**关键路径**：T1 → T2 → T4 → T6（约 17-25 天）

**可并行**：
- T2 和 T3 和 T7 可同时进行（都只依赖 T1）
- T4 和 T5 可同时进行（T4 依赖 T2+T3，T5 依赖 T2）

**MVP 总预计工时**：4-6 周（含缓冲）
