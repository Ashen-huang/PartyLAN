# PartyLAN 自建中继第一阶段稳定化设计

> Scope: 第一阶段继续使用官方 Tailscale 控制面，稳定接入并诊断自建 DERP 与自建 TURN，使 PartyLAN 在 NAT 直连失败时能够更可靠地使用自有公网资源，同时为第二阶段自建控制面预埋切换入口。

## 1. 背景

当前 PartyLAN 已经具备以下基础能力：

- `tailscale-system` 与 `tsnet` 两条 tailnet 接入路径
- WebRTC 作为当前屏幕共享媒体面
- 自定义 DERP 期望值配置与日志诊断
- 通过环境变量注入 ICE / TURN 服务器

最近的现象说明问题不再是“客户端完全不支持自建中继”，而是“自建中继链路没有被稳定、清晰、可验证地管理”：

- 历史日志已经出现过 `custom DERP present` 和 `relay=cn1`
- 最近也出现了 `relay=sfo`
- 这说明自建 DERP 曾经进入有效 DERP map，但实际会话并不总是使用它

同时，屏幕共享流畅度的关键并不只在 Tailnet 回落中继。PartyLAN 当前的共享媒体面已经是 WebRTC，因此在浏览器或 WebView2 无法和对端直连时，媒体轨道是否能稳定走自建 TURN，往往比 DERP 是否生效更直接地影响 `720p30` 体验。

因此，第一阶段不能只做“DERP 是否存在”的被动检查，而要把以下两条链路分开治理：

- Tailnet 回落链路：DERP
- WebRTC 媒体回落链路：TURN

## 2. 目标

第一阶段的目标是：

- 保持官方 Tailscale 控制面不变
- 让 PartyLAN 能稳定识别并诊断自建 DERP 是否进入有效 DERP map
- 让 PartyLAN 能稳定识别并诊断当前 Tailnet 会话是否真的走了自建 DERP
- 让 PartyLAN 的 WebRTC 媒体面正式纳入自建 TURN 配置与诊断
- 在运行时明确区分 Tailnet 路径状态与媒体路径状态
- 为第二阶段自建控制面预埋 `tsnet.ControlURL` 配置入口，但第一阶段默认不启用

第一阶段的核心成果不是“强制所有连接都走自建 relay”，而是：

- 把结果看清楚
- 把失败落点说清楚
- 把配置入口收敛清楚
- 为后续真正切换自建控制面留出稳定接口

## 3. 非目标

第一阶段明确不做以下内容：

- 不在 PartyLAN 内自动修改 tailnet policy `derpMap`
- 不替换官方 Tailscale 控制面
- 不立即切换到 Headscale 或其他自建控制面
- 不在 `tailscale-system` 模式下尝试由 PartyLAN 强制系统 Tailscale 选择特定 DERP
- 不把 TURN 密钥明文长期保存到普通用户配置中
- 不在本阶段建设完整 SFU 或多人媒体服务器
- 不承诺所有网络环境下都能稳定 720p30

## 4. 路线选择

本阶段评估三条路线：

### 4.1 方案 A：只修 DERP

只增强 Tailnet 侧的 DERP 配置与诊断。

优点：

- 改动最小
- 与当前代码连续性最好

缺点：

- 只能解释 Tailnet 回落中继走谁
- 无法单独解决 WebRTC 媒体轨道的回落稳定性

### 4.2 方案 B：DERP + TURN 双通道治理

继续使用官方控制面，但把 Tailnet 中继与 WebRTC 媒体中继分开治理。

优点：

- 最贴近真实用户需求
- 能解释“为什么 Tailnet 已经正常，但共享仍然不流畅”
- 与现有 PartyLAN 架构最匹配

缺点：

- 配置和诊断模型比纯 DERP 更复杂

### 4.3 方案 C：现在就全自建

立即为 `tsnet` 与未来系统路径切向自建控制面做整体切换设计。

优点：

- 长期控制力最强

缺点：

- 第一阶段风险最大
- 与当前 `tailscale-system` 路线边界不一致

### 4.4 推荐

第一阶段采用方案 B。

第二阶段在方案 B 基础上切入自建控制面。

## 5. 产品边界

第一阶段产品边界如下：

- 官方 Tailscale 控制面继续负责节点身份、tailnet 成员关系与有效 DERP map 下发
- 自建 DERP 负责 Tailscale / tsnet 在不能直连时的 Tailnet 回落中继
- 自建 TURN 负责 WebRTC 屏幕共享在 ICE 直连失败时的媒体中继
- PartyLAN 负责读取配置、观测有效结果、汇总诊断、向用户展示失败落点

必须明确的一点是：

- `tailscale-system` 模式下，PartyLAN 只能观测系统 Tailscale 的有效 DERP map 与已选路径
- 如果系统 Tailscale 最终仍走 `sfo` 而不是 `cn1`，PartyLAN 应该把它标记为“控制面策略或选路结果问题”，而不是误报为 PartyLAN 客户端逻辑失败

## 6. 第一阶段架构

第一阶段不改变 PartyLAN 的根本网络模型，而是明确以下职责分工：

### 6.1 控制面

- 官方 Tailscale 控制面
- 负责节点身份、tailnet 状态、DERP map 下发

### 6.2 Tailnet 连接层

- `tailscale-system`
- `tsnet`
- 负责 peer 发现、房间控制连接、Tailnet 回落链路状态采集

### 6.3 媒体层

- `internal/media`
- 负责 WebRTC PeerConnection、ICE/TURN 配置、媒体路径诊断

### 6.4 应用运行态汇总层

- `app.go`
- 负责统一解析运行时中继配置
- 将 Tailnet 诊断和媒体诊断汇总到运行态事件中

### 6.5 前端诊断层

- 继续复用现有 `RuntimeStatus` 与诊断面板
- 展示 DERP 状态、Tailnet 当前路径、媒体当前路径

## 7. 配置模型

第一阶段采用统一的运行时配置解析层，不再让每个模块分别直接读取环境变量。

### 7.1 配置优先级

统一优先级为：

`环境变量 > 专用启动脚本默认值 > 本地 config.json > 代码默认值`

### 7.2 DERP 配置

继续使用现有字段：

- `PARTYLAN_DERP_REGION_ID`
- `PARTYLAN_DERP_REGION_CODE`
- `PARTYLAN_DERP_HOSTNAME`
- `PARTYLAN_DERP_PORT`

本地配置仍可保留现有字段：

- `derpRegionId`
- `derpRegionCode`
- `derpHostName`
- `derpPort`

这些字段的职责是“声明期望看到的 DERP”，而不是直接替换官方控制面下发的 DERP map。

### 7.3 TURN / ICE 配置

第一阶段正式纳入以下环境变量：

- `PARTYLAN_ICE_SERVERS`
- `PARTYLAN_TURN_USERNAME`
- `PARTYLAN_TURN_CREDENTIAL`

这些配置默认由启动脚本注入，不进入普通设置面板，也不默认长期保存到用户配置文件。

原因如下：

- TURN 凭据本质上更接近部署侧密钥
- 当前 PartyLAN 的交付方式更适合通过分发脚本或打包产物固化默认值
- 避免把第一阶段范围扩展到敏感配置管理 UI

### 7.4 第二阶段预埋配置

为未来自建控制面预埋：

- `PARTYLAN_TS_CONTROL_URL`

第一阶段规则：

- 只对 `tsnet` 生效
- 默认为空
- 默认仍指向官方 Tailscale 控制面

### 7.5 统一运行时配置结构

第一阶段建议新增一个应用级统一配置对象，至少表达：

- DERP 期望值
- ICE 服务器列表
- 是否存在 TURN 凭据
- `tsnet` 是否显式指定 `ControlURL`

该对象由启动时一次性解析，再分别传入：

- `internal/network`
- `internal/media`
- `app.go` 运行态汇总逻辑

## 8. 模块变化方向

### 8.1 `internal/network`

保留现有 DERP 诊断能力，但职责升级为：

- 从统一运行时配置读取 DERP 期望值
- 报告有效 DERP map 中是否存在目标自建 DERP
- 报告 peer 当前路径是 `direct`、`derp` 还是 `peer-relay`
- 在 `tsnet` 模式下预埋 `ControlURL` 配置入口

第一阶段不要求 `internal/network` 主动注入自定义 DERP map。

### 8.2 `internal/media`

职责升级为：

- 从统一运行时配置读取 ICE / TURN 设置
- 在 WebRTC 会话建立时记录“是否加载 TURN”“是否拿到 relay 候选”“最终选中什么 candidate path”
- 输出可被 `app.go` 汇总的媒体诊断结果

### 8.3 `app.go`

职责升级为：

- 启动时解析统一中继配置
- 将配置分别传给 `network` 与 `media`
- 将 Tailnet 诊断与媒体诊断分开组装到运行态事件中

### 8.4 前端诊断组件

继续复用现有诊断面板承载能力，但展示目标从“一条 network note”升级为：

- DERP 状态
- 当前 Tailnet 路径
- 当前媒体路径

## 9. 数据流

### 9.1 启动阶段

1. 读取本地配置
2. 读取环境变量
3. 解析统一运行时中继配置
4. 将 DERP 期望值注入网络层
5. 将 ICE / TURN 配置注入媒体层
6. 启动网络后端
7. 采集 Tailnet 诊断并发布运行态

### 9.2 房间阶段

1. 房间控制仍使用现有网络层
2. Tailnet 连接状态由网络层周期刷新
3. 诊断中持续记录 peer 当前路径

### 9.3 开始共享阶段

1. `MediaManager` 使用统一 ICE / TURN 配置创建 PeerConnection
2. 记录 ICE server 数量、是否存在 TURN 凭据
3. 建连过程中采集 ICE state
4. 建连成功后记录最终 candidate path
5. 将媒体路径结果回传到应用运行态

### 9.4 运行态展示阶段

运行态最终至少需要回答三个问题：

- 自建 DERP 是否进入有效 DERP map
- 当前 Tailnet 会话是否走了目标 DERP
- 当前媒体轨道是否走了 relay

## 10. 诊断模型

第一阶段必须把 Tailnet 诊断与媒体诊断明确分层。

### 10.1 Tailnet 诊断

日志使用既有前缀：

- `[network/derp] custom DERP present`
- `[network/derp] custom DERP missing`
- `[network/derp] peer <name> path=direct`
- `[network/derp] peer <name> path=derp relay=<region>`
- `[network/derp] peer <name> path=peer-relay addr=<value>`

### 10.2 媒体诊断

建议新增前缀：

- `[media/ice] ice servers loaded count=<n> turn=<true|false>`
- `[media/ice] peer=<id> state=<checking|connected|failed>`
- `[media/ice] peer=<id> selected path=host|srflx|relay protocol=udp|tcp`
- `[media/ice] peer=<id> relay via=<host:port>`

### 10.3 运行态承载

第一阶段的运行态事件仍然复用现有 `RuntimeStatus` 入口，但应从“单一网络健康列表”升级为“至少能区分 Tailnet 与媒体诊断”。

推荐方向：

- 保留 `networkHealth` 表达 Tailnet 诊断
- 新增 `mediaHealth` 表达 WebRTC 媒体诊断

如果为了降低改动量暂时不扩展事件结构，则必须在同一诊断列表中保留清晰前缀，确保前端能够区分：

- DERP 状态行
- Tailnet 路径行
- 媒体路径行

### 10.4 前端展示优先级

前端诊断面板优先提炼并展示：

1. DERP 状态
2. 当前 Tailnet 路径
3. 当前媒体路径

用户最终必须能一眼判断：

- Tailnet 是否已识别 `cn1`
- 当前会话是否使用 `cn1`
- 当前屏幕共享媒体是否使用 TURN relay

## 11. 错误处理与降级

第一阶段将错误分为以下几类：

### 11.1 DERP 配置缺失

表现：

- 没有配置 `PARTYLAN_DERP_*`

处理：

- 不阻止启动
- 只标记“未启用自建 DERP 诊断”

### 11.2 DERP 配置存在但未生效

表现：

- 已配置期望 DERP
- 有效 DERP map 中找不到对应 region 或 node

处理：

- 网络继续工作
- 明确提示“当前 Tailnet 并未加载你的自建 DERP”

### 11.3 DERP 已生效但当前会话未选中

表现：

- DERP map 中能看到目标 `cn1`
- 当前 peer 仍显示 `relay=sfo` 或其他默认区域

处理：

- 网络继续工作
- 诊断明确标记“自建 DERP 可用，但当前会话未选中”

### 11.4 TURN 未配置

表现：

- 没有 ICE / TURN 配置

处理：

- 共享仍可尝试启动
- 诊断明确标记“媒体中继未启用”

### 11.5 TURN 已配置但中继失败

表现：

- TURN 凭据存在
- ICE 建连未出现 relay 候选或 relay 连接失败

处理：

- 不把 Tailnet 判定为失败
- 单独报告“媒体中继不可用”

### 11.6 LocalAPI 或诊断采集失败

表现：

- 无法读取 DERP map
- 无法读取 peer path
- 无法获取媒体统计

处理：

- 不阻止 PartyLAN 启动
- 在运行态中明确报告“诊断不可用”

## 12. 模式差异

### 12.1 `tailscale-system`

第一阶段策略：

- 以清晰诊断为主
- 不承诺 PartyLAN 能强制系统客户端改用目标 DERP
- 当最终仍走 `sfo` 时，明确显示这是选路结果而非应用内部故障

### 12.2 `networked` / `tsnet`

第一阶段策略：

- 继续保持现有内嵌路径
- 预埋 `ControlURL` 配置入口
- 继续采集 DERP map 与 peer path

### 12.3 `lan`

第一阶段策略：

- 行为不变
- 不引入 DERP 依赖
- TURN 仅在 WebRTC 媒体面需要时参与

## 13. 安全与配置管理边界

第一阶段不建议把 TURN 凭据做成设置面板字段，也不建议默认持久化在普通用户配置文件中。

推荐做法：

- 由 `Start-PartyLAN-DERP.ps1` 或新的统一启动脚本注入默认 TURN / ICE 配置
- 将分发侧的 relay 配置与客户端普通偏好设置隔离

这样既保留现有“开箱即用”的分发方式，也避免过早引入敏感配置管理复杂度。

## 14. 验收标准

第一阶段验收以可观测结果为准：

- 使用官方控制面时，PartyLAN 能稳定显示自建 DERP 是否进入有效 DERP map
- 当 NAT 直连失败且自建 DERP 已生效时，日志或诊断中能够看到 `relay=cn1` 一类结果，而不是只有 `sfo`
- 当配置 TURN 后，WebRTC 建连日志能明确显示已加载 TURN
- 至少在一组不可直连环境里，媒体诊断能显示 `selected path=relay`
- 当自建 DERP 或 TURN 不可用时，用户能在诊断面板里看到明确失败落点，而不是只有笼统的网络失败
- 现有 `tailscale-system`、`networked/tsnet`、`lan` 模式不发生行为回归

## 15. 第二阶段预埋点

第一阶段实施时必须避免把结构写死，为第二阶段保留以下扩展点：

- `tsnet.ControlURL` 可从统一运行时配置接入
- Tailnet 诊断与媒体诊断在运行态上可继续结构化
- TURN 配置未来可以升级为短时令牌或更细粒度下发
- 自建控制面切换后，DERP map 的来源可以从官方控制面平滑过渡到自建控制面

## 16. 推荐实施顺序

建议第一阶段按以下顺序落地：

1. 建立统一运行时中继配置解析层
2. 让 `network` 使用统一 DERP 配置，而不是各自直接读环境变量
3. 让 `media` 使用统一 ICE / TURN 配置，并产出媒体诊断
4. 扩展运行态事件与诊断面板，分开展示 Tailnet 与媒体状态
5. 更新启动脚本与 smoke test，使默认分发路径能够同时验证 DERP 与 TURN 配置是否已注入

这样可以先把“配置来源一致”和“结果可观测”做好，再逐步提升实际 relay 选中率与排障效率。
