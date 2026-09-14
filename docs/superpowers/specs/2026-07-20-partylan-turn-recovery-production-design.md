# PartyLAN 劣质 TURN 自动恢复与生产一致性修复设计

> Scope: 修复生产质量反馈缺失、网页端劣质 TURN 只观察不恢复、控制面重复请求和诊断字段误导问题，并以可回滚方式启用单次自动 ICE restart。

## 1. 背景与结论

2026-07-17 与 2026-07-19 的生产共享记录全部使用 Screego 网页原生 PeerConnection，并通过 TURN/UDP 中继。7 月 19 日会话的目标码率仍为 6.5-10.8 Mbps，但实测媒体码率仅约 0.17-0.52 Mbps，聚合丢包约 12.8%，抖动最高 1626 ms。质量状态机两次给出 `would_restart_ice`，但影子策略没有执行恢复。

生产服务器上的 `inject.js`、`webrtc-stats.js` 和 `register.py` 仍是 2026-07-11 版本，缺少当前仓库中的观看端质量上报与主机质量快照闭环。服务进程本身持续健康，因此本轮聚焦媒体恢复、部署一致性和控制面请求治理，不把服务器进程容量视为主因。

## 2. 目标

- 将生产质量上报、质量快照、网页统计和 Nginx 路由同步到同一版本。
- 对持续劣质的单个 TURN 会话执行一次受控 ICE restart。
- restart 前先限制异常观看者的发送码率，避免继续堆积媒体队列。
- restart 失败时保持保护档，不进入自动重连循环。
- 稳定后逐级解除保护，不直接跳回高码率。
- 保持 PartyLAN 原生客户端现有全局动态编码策略，通过质量快照获得观看端反馈。
- 减少重复控制请求和未认证 401，同时保持 15 秒房间 TTL 语义。
- 区分实测媒体指标和容量估算，避免误判服务器实际负载。

## 3. 非目标

- 本轮不启用第二层 PeerConnection 重建。
- 本轮不强制把所有网页房间切换到自定义 SFU。
- 不因为连接使用 TURN、低码率、低帧率或高 RTT 任一单项而自动恢复。
- 不改变网页主题、布局、登录协议、房间所有权或共享锁语义。
- 不覆盖现有生产镜像或删除回滚制品。

## 4. 架构边界

### 4.1 网页 Screego 会话

网页原生 PeerConnection 继续使用现有 P2P/TURN 信令。每个观看者会话独立维护：

- selected candidate pair 和 ICE generation；
- 质量窗口、恢复次数和冷却时间；
- 当前保护码率；
- restart 进行状态和取消句柄。

只有持续劣化的目标观看者会话执行保护和 restart，其他观看者不受影响。

### 4.2 PartyLAN 原生客户端

网页或客户端观看端每 2 秒向 `/api/quality/report` 上报规范化增量样本。原生主机每 2 秒读取 `/api/quality/snapshot`，继续使用 `native-global-adaptive-v1` 调整 NVENC 档位。本轮不改变原生客户端的 PeerConnection 恢复模型。

### 4.3 生产控制服务

`register.py` 在内存中保存短期质量样本，并保持现有身份、房间成员和 TTL 校验。部署副本、根服务文件、Nginx 配置和网页资源必须来自同一验证批次。

## 5. 劣化判定

- 每 2 秒生成一个增量样本。
- 连接建立后的前 15 秒只观察。
- 只有 selected pair 为 TURN 的会话参与自动恢复。
- 使用最近 7 个有效样本；至少 5 个坏样本且最后 3 个连续为坏样本时确认劣化。
- 坏样本必须满足增量丢包率大于 5%，并同时满足下列至少一项：
  - jitter 大于 100 ms；
  - `availableOutgoingBitrate` 低于目标码率的 45%；
  - `qualityLimitationReason` 为 `bandwidth`；
  - 没有新增解码帧。
- 页面隐藏、候选对变化、generation 变化、计数器重置、包增量不足、源切换和分辨率变化的样本无效。

## 6. 恢复流程

1. 确认劣化后，将目标观看者 sender 限制为最大 1280x720、最大 3 Mbps。
2. 主机创建带 `iceRestart: true` 的新 offer，并通过现有 host/client 信令交换 offer、answer 和新代次候选。
3. 新 SDP 中的 ICE ufrag/generation 成为唯一有效代次，旧候选和旧异步结果全部丢弃。
4. restart 进行中的重复请求合并，不创建第二个协商任务。
5. 10 秒内没有恢复连接则判定失败，继续保持保护档。
6. 每个会话最多自动 restart 一次；失败或再次劣化时进入 `protected`，不再自动重连。
7. restart 后重新进入 15 秒观察期。
8. 新路径连续稳定 20 秒后，发送上限按 3、6、8 Mbps、用户目标逐级恢复，每级至少保持 20 秒，且任何一级不得超过用户目标。
9. 停止共享、成员离开、房间关闭、会话关闭或代次自然变化时立即取消恢复任务和计时器。

## 7. 控制面降频

- 房间 TTL 保持 15 秒。
- Presence、共享租约和房间所有者保活保持 5 秒。
- 成员列表、房间状态和 viewer intent 合并到同一 5 秒读取周期。
- 相同房间状态不重复 POST；只在状态变化或保活到期时发送。
- 未认证用户不发送需要身份的 room-state、presence join/leave 或共享写请求。
- `/config` 登录状态轮询从 1.5 秒调整为 5 秒；明确登录和退出事件仍立即刷新。
- 控制请求错误按端点和状态限频记录，不让持续 401 填满服务日志。

## 8. 遥测语义

诊断和落盘数据分开记录：

- 实测上行/下行码率；
- WebRTC 可用发送带宽；
- 当前编码目标和保护上限；
- TURN 容量估算。

现有 `目标码率 x 观看人数 x TURN 双向系数` 只命名为容量估算，不再称为服务器实际负载。每次恢复记录策略版本、触发窗口、动作、结果、前后候选对、generation 和保护档；不记录 cookie、TURN credential、候选地址或完整 SDP。

## 9. 错误处理与回滚

- 质量接口不可用时，网页继续共享；原生主机保持当前编码档位并限频记录错误。
- restart offer、remote description 或候选补加失败时结束本次恢复并保持保护档。
- restart 不得影响其他观看者；检测到全房间中断或 offer collision 时立即关闭自动恢复开关。
- 部署前若生产文件、容器或镜像哈希与记录基线不一致，停止部署并重新分析。
- 登录、房间、共享、观看、TURN、质量接口或主题出现回归时恢复完整备份和旧镜像。

## 10. 测试策略

### 10.1 失败测试先行

- restart 使用 `iceRestart: true` 创建新 offer。
- 新 generation 接受新候选并丢弃旧候选。
- 重复请求合并，单会话最多执行一次自动 restart。
- 10 秒超时、停止共享和成员离开可以取消恢复。
- 确认劣化先应用 3 Mbps 保护档。
- 稳定不足 20 秒不升档；稳定后按设计逐级恢复。
- 相同 room state 不重复写入，未认证用户不发送受保护写请求。
- 容量估算和实测负载字段、文案保持分离。

### 10.2 自动验证

- Screego ICE session 与 recovery controller Node 测试。
- WebRTC stats、quality policy、网页控制和集成 Node 测试。
- Screego UI production build。
- Screego WS 与 TURN Go 测试。
- 服务端 Python 控制面测试。
- PartyLAN `go test ./...` 与 `go test -tags partylan_native_media ./...`。
- PartyLAN 前端 Node 测试和生产构建。

### 10.3 手工验证

- 双网页用户交换共享方向，覆盖 P2P、正常 TURN 和持续劣质 TURN。
- 短时 2-6 秒波动、静止桌面和后台页面不得触发 restart。
- 持续复合异常只触发目标观看者的一次 restart。
- PartyLAN 客户端与网页端互看、互享、成员、共享锁和全屏均不回归。

## 11. 生产部署

1. 记录生产容器 inspect、镜像 ID、compose、systemd、Nginx 和网页资源哈希。
2. 创建 `/root/screego/backups/turn-recovery-<UTC>/` 完整备份。
3. 运行全部自动测试，任一失败不得进入部署。
4. 构建新的不可变镜像标签，不覆盖当前或历史镜像。
5. 使用非冲突 HTTP/TURN 端口启动临时容器并完成登录、房间、HTTP、TURN 和质量接口烟雾测试。
6. 原子替换维护副本和 `/var/www/screego` 资源，更新缓存版本。
7. 使用 `docker compose --env-file /etc/gamecast-sfu/runtime.env` 切换生产容器。
8. 验证公网资源哈希、服务状态、房间、共享、观看、质量上报和统计落盘。
9. 保留旧镜像、备份和一条命令回滚路径。

## 12. 完成标准

- 生产质量接口产生有效 viewer report 和 host snapshot。
- 持续劣质 TURN 在门槛后只执行一次 ICE restart。
- restart 失败后保持保护档且不循环。
- 正常连接和其他观看者不中断。
- 生产不再出现重复未认证 room-state 401 风暴。
- 日志能够区分实测码率、可用带宽、目标码率和容量估算。
- 自动测试、临时容器烟雾测试和跨端手工验证全部通过。
