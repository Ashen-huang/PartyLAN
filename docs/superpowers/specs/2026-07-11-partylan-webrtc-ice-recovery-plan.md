# PartyLAN WebRTC ICE 选路与劣质中继恢复实施计划

> Based on `2026-07-11-partylan-webrtc-ice-recovery-design.md`

## 1. 目标

完成以下闭环：

- Screego 网页端不再因 PeerConnection 登记或远端 SDP 时序丢失 ICE 候选。
- 诊断数据能区分 P2P、TURN/UDP、TURN/TCP、IPv4 和 IPv6。
- 使用增量样本与保守状态机识别持续劣质 TURN，短暂网络波动不触发恢复。
- 首次确认劣化只对异常观看者执行 ICE restart。
- 再次确认劣化时由服务端为该观看者重建 P2P session。
- 仍不稳定时只对该观看者启用 `720p30 / 3-4 Mbps` 保护档，并在稳定后逐级恢复。
- 所有恢复动作均可配置、可追踪、可关闭、可回滚。

## 2. 实施边界

- 不修改网页主题、布局、颜色和 Screego 基础交互。
- 不因连接使用 TURN 而直接判差。
- 不以低码率、低帧率或高 RTT 单项触发恢复。
- 不增加固定播放延迟。
- 不在影子判定数据通过前开放自动重连。
- 不重置或覆盖当前工作区中的既有未提交改动。
- 生产 nginx 实际提供 `/var/www/screego/inject.js`，`/root/screego/frontend/inject.js` 是维护副本；部署时两份必须保持相同哈希。
- 当前 Screego 容器为无挂载的 `gamecast-sfu:v2`，核心 ICE 修复必须重建镜像，不能只依赖注入脚本 monkey patch。

## 3. 源码与构建基线

当前 `tmp/gamecast-sfu-source` 和 `tmp/cross-client-compat` 被 `.gitignore` 忽略。实施前先建立可追溯构建边界：

1. 记录当前生产镜像 ID、容器 inspect、compose 文件和关键环境变量，不把 secret 写入仓库或测试日志。
2. 记录作为构建输入的 Screego 源码来源、提交或归档 SHA-256，以及 `ui/src/useRoom.ts` 基线哈希。
3. 在受版本控制的 `server/screego/` 下保存：
   - 上游版本与哈希说明；
   - 最小 UI/WS 补丁；
   - 补丁应用脚本；
   - 构建、验证和回滚清单。
4. 补丁应用脚本必须先校验基线哈希；不匹配时停止，不尝试模糊应用。
5. 构建产物使用新标签，例如 `gamecast-sfu:ice-recovery-<commit>`，不得覆盖现有 `gamecast-sfu:v2`。

新增以下受版本控制文件：

- `server/screego/README.md`
- `server/screego/upstream.lock`
- `server/screego/patches/0001-reliable-ice-sessions.patch`
- `server/screego/patches/0002-peer-session-restart.patch`
- `server/screego/apply-patches.ps1`
- `server/screego/verify-build.ps1`

## 4. P1 ICE 候选可靠性交换

### 4.1 失败测试

先为 Screego UI 增加纯逻辑时序测试，当前实现必须失败：

1. `hostice` 在 host PeerConnection 完成异步协商前到达，候选不会丢失。
2. `hostice` 在 client 远端 SDP 设置前到达，候选先入队后补加。
3. `clientice` 在 host 远端 SDP 设置前到达，行为对称。
4. 多个候选交错到达时保持原顺序。
5. `addIceCandidate()` 因 `InvalidStateError` 暂时失败时保留并重放。
6. session 关闭后队列清空，迟到候选不进入下一代连接。
7. ICE restart 后旧 username fragment 的候选被丢弃，新代次候选正常添加。
8. `setRemoteDescription()` 或候选补加失败时产生结构化诊断事件，不出现未处理 Promise rejection。

实施文件：

- `tmp/gamecast-sfu-source/ui/src/iceSession.ts`
- `tmp/gamecast-sfu-source/ui/src/iceSession.test.mjs`
- `tmp/gamecast-sfu-source/ui/src/useRoom.ts`

测试命令：

```powershell
node --test --experimental-strip-types tmp/gamecast-sfu-source/ui/src/iceSession.test.mjs
```

### 4.2 最小实现

将 `host` 和 `client` 的值从裸 `RTCPeerConnection` 改为 `PeerSession`：

- `sid`、`peerId`、`role`；
- `pc`；
- `remoteDescriptionReady`；
- `pendingRemoteCandidates`；
- 当前 ICE username fragment；
- negotiation generation；
- `closed`、`recoveryCount` 和冷却状态。

收到 `hostsession` 或 `clientsession` 时：

1. 同步创建并登记 session。
2. 再异步设置 track、local description 和发送 offer。
3. 所有异步分支显式捕获错误并携带 `sid/role/generation` 上报。

收到 offer、answer 或 ICE 时：

- 先定位 session；尚未登记时进入按 `sid` 隔离的早期消息队列。
- 远端 SDP 未就绪时只缓存候选。
- SDP 就绪后按序补加并清空成功项。
- 关闭 session 时同时清理候选、计时器和恢复任务。

### 4.3 验证

- P1 时序测试全部通过。
- `npm run build` 在 `tmp/gamecast-sfu-source/ui` 通过。
- 浏览器控制台无未处理的 SDP/ICE Promise 错误。
- 双用户各交换共享方向至少 10 次，统计中不再出现可归因于候选丢失的单向稳定 TURN。

## 5. P2 连接遥测与指标修正

### 5.1 失败测试

为 stats 解析提取纯函数并先写测试：

1. 从 selected candidate pair 解析本地/远端 candidate type。
2. 正确区分 P2P、TURN/UDP 和 TURN/TCP。
3. 正确记录 IPv4/IPv6、`protocol` 和 `relayProtocol`。
4. 使用相邻样本计算包、丢包、字节和帧增量。
5. 计数器重置、候选对切换或时间倒退时样本无效，而不是产生负值或异常高值。
6. 静止画面低码率但没有丢包/抖动时不会标记异常。
7. “实测媒体码率”与“预计服务器容量占用”使用不同字段和文案。

实施文件：

- `server/webrtc_stats.js`
- `server/test_webrtc_stats.mjs`
- `tmp/cross-client-compat/inject-merged.js`
- `server/register.py`
- `server/test_register_control_api.py`

### 5.2 实现

每个 PeerConnection 每 2 秒采样，不再依赖用户是否展开统计面板。标准化样本包含：

- 匿名 peer/session、角色、协商代次；
- selected pair id 和切换时间；
- local/remote candidate type、protocol、relayProtocol、address family；
- RTT、jitter、available outgoing bitrate；
- 包、丢包、字节、编码帧、解码帧和丢帧增量；
- `qualityLimitationReason`；
- 页面可见性、共享源/分辨率变更标志；
- 实测上行/下行码率和单独的容量估算。

`/api/stats` 保持兼容旧字段，同时接受新结构。服务端限制单条 body 大小、字段长度和采样频率；不得记录 cookie、credential、完整 SDP 或候选中的私网地址原文。

### 5.3 验证

- Node stats 测试和 Python API 测试通过。
- 生产样本能明确显示 `turn/udp` 或 `turn/tcp`。
- 页面收起统计面板时仍按受控频率产生链路样本。
- stats 文件增长量符合预估，日志轮转不被破坏。

## 6. P3 低误判质量状态机与影子模式

### 6.1 失败测试

新增纯函数状态机测试：

1. 连接前 15 秒只观察，不触发。
2. 2 至 6 秒丢包/抖动尖峰不触发。
3. 7 个有效样本中少于 5 个坏样本不触发。
4. 满足 5/7 但最后 3 个不连续异常时不触发。
5. 增量丢包大于 5%，且 jitter 大于 100 ms，满足窗口条件时确认劣化。
6. 丢包条件加 `bandwidth`、可用带宽不足或解码停滞之一，也可形成坏样本。
7. 单独低码率、低帧率、高 RTT、TURN 或静止画面不触发。
8. 后台、源切换、分辨率变更、包增量不足和协商代次变化的样本无效。
9. 恢复后 60 秒冷却生效。
10. 连续 20 秒丢包低于 1%、jitter 低于 40 ms 才恢复稳定状态。
11. 每条共享最多两次自动连接恢复，达到上限后进入保护状态。

实施文件：

- `server/webrtc_quality_policy.js`
- `server/test_webrtc_quality_policy.mjs`
- `tmp/cross-client-compat/inject-merged.js`

### 6.2 影子实现

- 策略版本固定写入每条判断事件。
- 状态机只对 selected pair 为 TURN 的连接计算自动恢复资格。
- 第一阶段只写入 `would_restart_ice`、`would_rebuild_session`、`would_protect_bitrate`，不执行动作。
- 候选对自然切换或 ICE generation 变化时清空窗口并重新进入 15 秒观察期。
- 阈值由集中配置提供，默认关闭自动动作。

### 6.3 影子阶段退出门槛

影子模式至少覆盖：

- 20 次双用户互相共享；
- 至少 3 种网络环境；
- 静止桌面、视频播放和快速滚动三类内容；
- 至少一次人工注入的短波动和一次持续劣化。

只有同时满足以下条件才进入 P4：

- 2 至 6 秒短波动触发率为 0；
- 静止/后台场景误触发率为 0；
- 持续复合异常能够在约 14 至 20 秒内产生影子触发；
- 每次触发均能从原始增量样本复算。

## 7. P4 自动 ICE restart

### 7.1 失败测试

1. 只有 host/offerer 可以发起 restart，viewer 不会并发协商。
2. 状态机确认前不调用 restart。
3. restart 使用 `createOffer({iceRestart: true})`，发送新的 `hostoffer`。
4. client 对新 offer 设置远端 SDP、创建 answer，并按新 ufrag 处理候选。
5. restart 进行中重复请求被合并。
6. 停止共享、成员离开或房间关闭会取消 restart。
7. restart 后 P2P 稳定或 TURN 指标恢复时不继续升级动作。
8. 超时或仍持续异常时只记录下一层资格，不立即循环。

实施文件：

- `tmp/gamecast-sfu-source/ui/src/iceSession.ts`
- `tmp/gamecast-sfu-source/ui/src/recoveryController.ts`
- `tmp/gamecast-sfu-source/ui/src/recoveryController.test.mjs`
- `tmp/gamecast-sfu-source/ui/src/useRoom.ts`

### 7.2 实现和开关

- 仅开启 `shadow + ice-restart` 阶段。
- 第一次确认劣化后，对对应 session 执行一次 restart。
- 动作后进入 60 秒冷却和新的 15 秒观察期。
- 记录恢复前后候选对、传输协议、动作耗时和结果。
- 服务器/部署配置可立即关闭自动 restart，但不关闭遥测。

### 7.3 验收

- 人工构造持续劣质 TURN 时在门槛后执行一次 restart。
- 画面中断控制在约 1 至 3 秒。
- 同房其他正常观看者不中断。
- 短波动、静止画面和后台页面不触发。
- restart 后不会产生 offer collision 或未处理 ICE 错误。

## 8. P5 单观看者 session 重建

### 8.1 服务端失败测试

为 Screego WS 新增 `restartsession` 事件测试：

1. 只有该 session 的 host 可以请求重建。
2. session 不存在、已关闭或 host 未共享时拒绝。
3. 请求后只关闭指定旧 session，并为同一 host/client 创建新 session id。
4. 其他观看者 session 保持不变。
5. 旧 TURN credential 被撤销，新 session 获得新 credential。
6. 重复请求和并发请求保持幂等或返回结构化冲突。

实施文件：

- `tmp/gamecast-sfu-source/ws/event_restartsession.go`
- `tmp/gamecast-sfu-source/ws/event_restartsession_test.go`
- `tmp/gamecast-sfu-source/ws/room.go`
- `tmp/gamecast-sfu-source/ws/outgoing/messages.go`
- `tmp/gamecast-sfu-source/ui/src/message.ts`
- `tmp/gamecast-sfu-source/ui/src/useRoom.ts`

### 8.2 实现

第二次确认劣化且冷却结束后，host 发送 `{type: "restartsession", payload: {sid}}`。服务端验证所有权后：

1. 向旧 session 双方发送 `endshare`。
2. 撤销旧 TURN credential 并删除旧 session。
3. 为同一 host/client 调用 `newSession` 创建新 sid。
4. 双方使用既有 `hostsession/clientsession` 流程建立新 PeerConnection。

该流程不停止 host 的 MediaStream，不影响房间其他成员。

### 8.3 验证

- Go WS 测试和 UI 时序测试通过。
- 两个以上观看者时，只重建被注入劣化的观看者连接。
- 旧 session 的迟到消息不影响新 session。
- 新 session 重新执行 15 秒观察期。

## 9. P6 TURN 保护档与逐级恢复

### 9.1 失败测试

1. 只有经过两次连接恢复仍确认劣化的 session 进入保护档。
2. 保护上限为该 session 的 sender max bitrate，不修改其他观看者。
3. 注入脚本的常规 `applyBitrate()` 不会覆盖保护上限。
4. 连续稳定不足 20 秒时不升档。
5. 稳定后逐级恢复，例如 `4 -> 6 -> 8 Mbps -> 用户目标`。
6. 任一级重新劣化时退回保护档，不再执行无限连接恢复。
7. 手动“重新优化连接”不会并发启动多个任务。

实施文件：

- `tmp/gamecast-sfu-source/ui/src/recoveryController.ts`
- `tmp/cross-client-compat/inject-merged.js`
- `server/test_webrtc_quality_policy.mjs`

### 9.2 实现

- 在 PeerConnection 上维护受控的 per-session protection cap。
- `applyBitrate()` 使用 `min(正常目标, protection cap)`。
- 默认保护档为最大 4 Mbps，并保持 30 FPS；若浏览器无法单独限制帧率，只限制该 sender 码率，不降低共享源全局帧率。
- 达到恢复次数上限后保持保护档，并在诊断面板显示可理解的状态与手动恢复入口。

## 10. 自动验证总表

```powershell
node --test --experimental-strip-types tmp/gamecast-sfu-source/ui/src/iceSession.test.mjs
node --test --experimental-strip-types tmp/gamecast-sfu-source/ui/src/recoveryController.test.mjs
node --test server/test_webrtc_stats.mjs
node --test server/test_webrtc_quality_policy.mjs
node --test server/test_web_socket_lifecycle.mjs
node --test server/test_web_control.mjs
node --check tmp/cross-client-compat/inject-merged.js

Push-Location tmp/gamecast-sfu-source/ui
npm run build
Pop-Location

Push-Location tmp/gamecast-sfu-source
go test ./ws/... ./turn/...
Pop-Location

python -m unittest discover -s server -p "test_*.py"
git diff --check
```

## 11. 构建与部署

### 11.1 预部署

1. 记录生产容器、镜像、compose、inject.js 和服务状态。
2. 创建 `/root/screego/backups/ice-recovery-<UTC>/`。
3. 备份生产镜像标签、compose 文件、`/var/www/screego/inject.js`、维护副本和服务配置。
4. 校验生产哈希仍等于实施开始时记录；不一致则 STOP 并重新分析差异。
5. 在新标签构建镜像，完成离线 UI、Go 和容器烟雾测试。

### 11.2 分阶段部署

1. 部署 P1 候选修复与 P2 遥测，自动恢复关闭。
2. 启用 P3 影子状态机并完成退出门槛。
3. 只启用 P4 ICE restart，观察真实测试结果。
4. 最后启用 P5 session 重建和 P6 保护档。

每阶段均先更新维护副本，再原子替换线上文件；两份 inject.js 哈希必须一致。容器替换时保留旧镜像和原容器参数，健康检查通过后才移除临时容器。

### 11.3 回滚

- 网页注入回滚到备份脚本并验证公网响应哈希。
- 容器回滚到原 `gamecast-sfu:v2`，使用备份 compose/inspect 参数重建。
- 自动恢复开关可以独立关闭，不需要回滚候选修复和遥测。
- 回滚后验证 nginx、Screego、register service、房间创建/加入、共享与观看。

## 12. 手工验收矩阵

1. 两个网页用户交换共享方向各 10 次，记录每次候选对和协议。
2. P2P 正常、TURN/UDP 正常、TURN/TCP 正常均不触发恢复。
3. 注入 2、4、6 秒短波动，不触发自动动作。
4. 注入持续 14 秒以上复合异常，触发一次 restart。
5. restart 恢复后不执行 session 重建。
6. restart 后仍异常，冷却结束后只重建目标观看者。
7. 两名以上观看者时，正常观看者画面不中断。
8. 两次恢复后仍异常，仅目标连接进入保护档并逐级恢复。
9. 静止桌面、后台标签页、视频播放、快速滚动均符合判定预期。
10. 网页端与客户端房间互看、互享、成员列表、共享锁和全屏均不回归。
11. 网页主题、布局和现有样式保持不变。

## 13. STOP 条件

- 无法确认或复现当前 Screego 构建源码基线。
- P1 失败测试不能证明候选时序问题，或修复需要改动无关媒体协议。
- 新遥测无法区分 TURN/UDP 与 TURN/TCP。
- 影子阶段出现任一静止/后台误触发，或短波动误触发。
- ICE restart 导致 offer collision、全房间中断或超过两次循环。
- 单观看者重建无法在不影响其他观看者的情况下实现。
- 新镜像导致登录、首次创建/加入房间、网页主题或基础共享流程回归。
- 部署前生产文件或镜像哈希与记录不一致。

触发 STOP 条件时不扩大范围猜测修复；保留诊断数据，回滚对应阶段并报告。

## 14. 完成标准

- P1 至 P6 自动测试全部通过。
- 影子阶段误判门槛通过后才启用自动动作。
- 双用户交换方向不再因候选时序稳定退化到 TURN。
- 持续劣质 TURN 可在约 14 至 20 秒确认，并在受控次数内恢复或进入保护档。
- 2 至 6 秒网络波动、静止画面和后台页面不触发。
- 每次恢复均可从 stats 数据还原候选协议、触发窗口、动作和结果。
- 网页与客户端跨端媒体、成员、共享锁、首次连接和全屏回归通过。
- 生产部署和回滚记录追加到本计划执行记录。

## 15. 执行状态

- 状态：待实施
- 当前阶段：P0 源码与构建基线
- 自动恢复默认状态：关闭
