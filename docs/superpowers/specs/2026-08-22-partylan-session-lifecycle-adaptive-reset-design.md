# PartyLAN 共享会话生命周期与自适应状态复位设计

**日期：** 2026-08-22  
**状态：** 设计已确认，实施完成
**范围：** Screego 原生 P2P/TURN 网页共享链路

## 背景

GAY 房间实测出现以下时序：共享开始时画面正常；部分观看者退出后，成员列表中的共享者消失，但媒体仍继续传输；随后共享质量持续跌至低档。重新刷新网页并重新共享后，旧的低质量状态仍可能影响新连接。

现有实现把 Presence、共享租约和 WebRTC PeerConnection 分开维护。连接只有进入 `closed` 或 `failed` 才从活动列表移除，`disconnected`、旧 Screego session 和过期质量样本可能继续参与自适应和预算计算。重新共享时只重置网页自适应状态，没有同步清除 ICE 保护上限和所有旧媒体连接。

## 目标与非目标

### 目标

- 普通观看者退出时，只关闭和清理该观看者的 PeerConnection，不影响其他观看者。
- 共享者 Presence 短暂丢失时保持媒体共享，自动重新加入 Presence 并刷新共享租约。
- Presence 自愈失败达到阈值后，安全停止共享并释放租约，避免残留状态继续污染房间。
- 停止后重新共享从全新 session、L0 和无 ICE 保护上限开始。
- 孤立连接不再计入 `pcCount`、TURN 预算或质量快照映射。
- 保持现有 Screego P2P/TURN 媒体协议、成员兼容性、语音链路和画质阶梯不变。

### 非目标

- 不修改 Screego `/stream` 协议、SDP、编码器或 TURN 服务。
- 不把多个观看者合并成全局自适应状态。
- 不改变用户手动选择的分辨率和帧率上限。

## 设计

### 1. 连接身份与生命周期

每个活动 PeerConnection 继续使用 Screego `sid`、对端 `peerId` 和角色作为运行时身份。注入脚本维护统一的 session 注册表，并在以下事件执行幂等清理：

- Screego session close 或被新 PeerConnection 替换；
- `RTCPeerConnection` 进入 `closed`、`failed`，或 `disconnected` 超过短暂恢复窗口；
- Presence 列表确认对应参与者已离开；
- 共享停止、页面离房或共享租约失效。

清理动作包括：移除活动连接、删除 session 到 PeerConnection 映射、删除质量快照、停止该连接统计、重置自适应状态和 ICE 保护字段。清理必须按连接执行，不能调用全局 `closeActivePeerConnections()`，除非共享者明确停止或自愈失败。

### 2. 观看者退出处理

共享端定期刷新 Presence。将当前 Presence participant ID 与活动 Screego session 的对端标识建立映射。确认观看者离开后：

1. 标记对应 session 为 `orphaned`，不再参与质量快照和服务器预算；
2. 给 PeerConnection 一个短暂的 `disconnected` 恢复窗口；
3. 窗口结束仍未恢复时关闭并移除该连接；
4. 重新加入的同一用户必须通过新 `sid` 建立全新自适应状态。

其他 PeerConnection 不重置、不降档、不重建。

### 3. 共享者 Presence 自愈

共享媒体仍处于 `live` 且共享租约有效时，Presence 心跳或刷新失败不得立即停止媒体。自愈流程为：

1. 暂停旧 Presence 写入，保留媒体和共享租约；
2. 使用当前房间、当前用户和新的/原 participant ID 幂等执行 `presence/join`；
3. 成功后恢复 Presence 心跳、刷新成员列表和质量快照授权；
4. 对共享租约执行 heartbeat，确保服务端仍把当前参与者识别为共享者；
5. 连续恢复失败达到阈值，或共享租约已明确过期/无效时，调用安全停止流程。

自愈期间不把“成员列表暂时缺少共享者”当作媒体断开，也不自动把媒体降到低画质。

### 4. 重新共享的完整复位

明确停止共享、共享租约失效或 Presence 自愈最终失败时，执行一次完整复位：

- 停止共享轨道和 SFU/P2P host；
- 关闭全部旧 PeerConnection；
- 清空 session 注册表和质量快照；
- 清除 `_gcAdaptiveState`、`_gcAdaptiveProfile`、pending decision、probe 和稳定计时；
- 清除 `_gcQualityState`；
- 清除 `_gcProtectionMaxBitrate`、`_gcProtectionMaxWidth`、`_gcProtectionMaxHeight`、恢复步进和保护时间戳；
- 重新领取共享租约后，新 PeerConnection 从 L0、无 protection 上限开始。

只刷新页面但旧连接尚未关闭时，不能复用旧 session 的状态；新 session 必须覆盖旧映射并触发旧连接清理。

### 5. 自适应和保护上限约束

- `adaptiveMaxBitrate` 只对当前 PeerConnection 生效。
- `protectionMaxBitrate` 不能跨 PeerConnection、跨 Screego `sid` 或跨一次共享生命周期继承。
- Presence 缺失不等于网络变差，不得单独触发降档。
- 只有对应观看者的有效接收反馈或该连接自身的发送统计才能改变该连接的档位。
- 孤立连接从预算和样本快照中排除后，剩余连接不应因观看者数量变化被整体限速。

## 错误处理

- Presence `404/409`：先重新获取房间状态并幂等重入；媒体保持不变。
- Presence `401`：暂停写入并重新获取登录状态；若共享租约也失效，安全停止。
- Share heartbeat `share_lease_expired/share_lease_invalid`：不再继续发送媒体，执行完整复位。
- 单个 PeerConnection 清理失败：记录诊断并继续清理其映射，不影响其他连接。
- 控制面短暂不可用：保留媒体不超过现有租约安全窗口，超过窗口后停止共享并清理。

## 测试与验收

自动测试覆盖：

- 观看者离开只清理对应 PeerConnection；其他连接对象、档位和轨道保持不变；
- `disconnected` 在恢复窗口内恢复时不清理，超时后清理；
- Presence 消失时媒体保持，自动 `join` 和 share heartbeat 恢复；
- 连续自愈失败后释放租约并完整清空自适应、质量和 ICE 保护状态；
- 新共享 session 不继承旧 `adaptiveLevel`、`probe` 或 `protectionMaxBitrate`；
- 旧 sid 的质量样本不污染新 sid 或其他观看者；
- 孤立连接不计入成员预算和 `pcCount`；
- 多观看者中一人退出不触发其他观看者降档；
- 现有网页、客户端、P2P/TURN、音视频和 Presence 回归测试保持通过。

线上 `test` 灰度验收：单观看者、多观看者、两名观看者连续退出、共享者 Presence 恢复、刷新后重新共享，以及限速/解除限速。验收以实际接收分辨率、帧率、码率、连续画面和成员列表一致性为准。

## 部署与回滚

实现前分别备份注入脚本、Screego 静态副本和控制面文件。优先部署兼容的静态资源和控制面逻辑，不修改 TURN/SFU。灰度期间只观察 `test` 房间；若出现媒体中断、旧连接污染新共享、成员列表与媒体长期失步或质量再次锁死，立即恢复本次独立备份。

## 实施记录

- 注入脚本增加按 Screego session 的幂等清理、旧 session 替换处理、`disconnected` 超时清理和 Presence 孤立连接回收。
- 共享者 Presence 丢失时保留媒体，执行幂等重入、Presence 刷新和共享租约心跳；连续三次恢复失败后才安全停止共享。
- 停止、离房或重新共享时关闭旧 PeerConnection，并清除自适应、质量快照和 ICE 保护上限，保证新 session 从 L0 开始。
- 回归验证：Node 测试 130/130，Python 测试 38/38，`node --check` 与 `git diff --check` 通过。
