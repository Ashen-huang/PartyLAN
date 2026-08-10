# PartyLAN 原生 P2P 逐观看者质量层池实施计划

**状态：** 执行中
**设计依据：** `docs/superpowers/specs/2026-08-10-partylan-native-p2p-per-viewer-layer-pool-design.md`
**实施范围：** Windows 原生客户端 Screego P2P/UDP Host；不修改网页、SFU、语音和服务端协议

## Task 1：建立失败测试和纯 Go 层规划模型

先在实现前补充红灯测试，覆盖：

1. 原生 60/45/30、720p30/21、540p21、360p18 候选阶梯及码率目标。
2. 低于候选尺寸或帧率的源不放大，重复档位折叠。
3. 一至三种观看目标直接保留，超过三种目标时最高、最低和加权中间层选择稳定。
4. 每个目标映射到不高于其网络能力的活动层，缺少低层时进入 `layer-constrained`。
5. 每个观看者独立执行 2 秒采样、4 至 6 秒降档、120%余量/10 秒恢复、8/12 秒冷却。
6. ICE 代次、重复报告、过期报告和计数器重置清除或忽略旧窗口。
7. 初始化失败、连续编码失败、总负载超限的隔离和恢复状态机。

计划新增纯 Go 组件测试，不依赖 Windows NVENC，确保在普通 CI 中先得到正确的失败原因。

## Task 2：实现质量阶梯和逐观看者控制器

在 `internal/gamecast/adaptive` 增加 P2P 专用模型，保持 `native-global-adaptive-v2`
单层控制器接口兼容供 SFU 回退使用：

- 源尺寸派生的 `BuildP2PLadder` 和按分辨率/FPS 计算码率函数。
- `SelectActiveLevels`、目标到活动层的安全映射和确定性加权中间层选择。
- `PerViewerController` 与网页端一致的坏样本、稳定样本、路径代次、冷却和逐档决策。
- 对 GCC 估算值和已有观看端质量报告进行合并；估算器缺失时保持当前档，不盲目升档。
- `LayerPoolState` 记录引用、保温、隔离、恢复失败次数和 `layer-constrained` 原因。

所有新类型提供快照方法，避免把锁或可变切片暴露给媒体循环。

## Task 3：扩展原生 P2P Publisher 的会话路由和 GCC

修改 `internal/gamecast/screegop2p/publisher.go`：

1. `publisherSession` 增加当前质量层、Peer ID 和带宽估算器状态。
2. 每个 PeerConnection 通过 Pion `cc.NewInterceptor`、TWCC Header Extension 和 GCC 建立
   独立发送侧估算器；估算器不可用时仍按原流程建立单轨连接。
3. 增加只读会话快照，提供 Session ID、Peer ID、当前层、GCC 目标码率和连接代次。
4. 增加原子层切换接口：先向指定会话写入目标层 SPS/PPS/IDR，再提交新层绑定。
5. `WriteH264LayerSample` 只向绑定该层的会话写样本；音频仍广播到全部会话。
6. PLI/FIR 按会话路由到当前层；保留旧的全层关键帧回退门控。
7. 关闭会话时清理估算器和层绑定，防止遗留引用阻止层池回收。

补充 Publisher 单元和 Pion 集成测试：多会话层隔离、层切换首 IDR、音频广播、单轨 SDP、
TWCC 不可用回退和会话关闭清理。测试不得打印完整 ICE 凭据或 SDP。

## Task 4：重构原生 P2P 媒体循环为多层调度

在 `app.go` 保持 SFU/旧 Host 使用现有单层媒体循环；仅当 Host 实现 P2P 层池接口时启用
新调度器：

- 捕获 worker 保持现有单帧 mailbox 和窗口/整屏恢复逻辑。
- 移除关闭 `encoders[1:]` 的行为，按层池需要创建和复用最多三路编码器。
- 每层按独立 FPS 时钟编码当前帧，并把样本交给 Publisher 层路由。
- 每次质量更新先运行逐观看者控制器，再重新计算活动层集合和会话迁移计划。
- 层切换必须在媒体循环内串行执行，使用目标层有效 IDR 后再改变路由。
- 继续保留本地采集、编码、写入指标；扩展为按层和总负载记录。
- 层池启动、保温、隔离、旁路恢复和最终关闭全部绑定现有媒体 loop context。
- 音频 loop 继续调用相同 Publisher，不引入视频层分支。

本机保护按设计实现：初始化失败隔离 30 秒；连续三次或两秒失败率 20%隔离；总负载连续
超 85%关闭最高成本层；负载低于 70%稳定 10 秒后旁路恢复；同层两次恢复失败则本次共享
生命周期禁用。所有层失败才走现有视频失败处理。

## Task 5：状态、诊断和兼容性回归

- 为原生 P2P 统计增加策略版本、活动层、观看者层分布和隔离/迁移原因，沿用现有兼容字段。
- 保证 SFU Host 仍调用 `native-global-adaptive-v2` 单层路径。
- 保证网页端只看到标准 H.264 单轨，旧客户端观看端不需要更新。
- 运行静态资源、网页自适应、音频、Presence、ICE 恢复和房间生命周期现有测试，证明本轮没有
  修改服务端资源或网页链路。

## Task 6：验证、构建和实机灰度

按仓库现有门禁执行：

1. `go test ./internal/gamecast/adaptive ./internal/gamecast/screegop2p`。
2. `go test ./...`。
3. `go test -tags partylan_native_media ./...`。
4. 服务器 Node/Python 测试、前端 Node 测试和生产构建。
5. `git diff --check`、敏感信息扫描、生成文件噪声审计。
6. 重建带 `partylan_native_media` 的 PartyLAN Windows 客户端，记录 EXE 和 `opus.dll` 摘要。

候选客户端先在 `test` 房间完成至少 30 分钟矩阵实测：网页/客户端观看者、局域网/外网、
P2P/TURN、一个弱网与多个稳定观看者、1080p60/1440p60/可用时4K60、1～3路 NVENC 能力、
多次带宽往返和观看者进出。验收通过后再作为正式客户端构建；失败时回退上一版客户端，
不重启广州节点服务。

## 停止条件

- 单轨 H.264 切换分辨率后无法在网页和客户端连续解码。
- TWCC/GCC 破坏现有 Screego SDP/连接，且质量报告不足以安全降档。
- 三路编码在目标硬件上持续拖垮采集或主编码层。
- 灰度出现稳定观看者被弱网观看者连带降档或策略振荡超过现版本。

## 执行记录

待实现后填写变更文件、测试结果、构建摘要、实机灰度证据和回滚信息。
