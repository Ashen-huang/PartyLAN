# PartyLAN 网页与客户端自适应恢复、触底保护实施计划

**日期：** 2026-08-10
**状态：** 实施中
**设计依据：** `docs/superpowers/specs/2026-08-10-partylan-adaptive-recovery-and-floor-protection-design.md`

## Task 1：网页策略红灯测试

先修改 `server/test_webrtc_adaptive_policy.mjs` 和必要的注入集成测试，覆盖：

1. 720p 源生成 60/45/30/21、540p21、360p18，低分辨率源不放大；
2. 高分辨率源保持原生 60/45/30 后再进入 720p30/21；
3. 720p、1080p、1440p、4K 使用设计确认的复杂画面上限；
4. `qualityLimitationReason=bandwidth` 单独出现时不降档；
5. 实际复杂画面吞吐高于参考值时不降档；
6. 一次降档后，估算值仅随发送码率下降时不继续级联；
7. 360p18 触底后冻结；
8. 解除限速并持续健康后启动逐级 probe，probe 成功提交、失败回退。

先运行目标 Node 测试，确认它因旧固定七档、bandwidth 单信号和 120% 恢复死锁而失败。

## Task 2：实现网页质量带与恢复 probe

同步修改并保持字节一致：

- `server/webrtc_adaptive_policy.js`
- `server/screego/deploy/webrtc-adaptive.js`

实施内容：

- 从固定档数组改为按源分辨率构建且去重的质量带；
- 每档分离编码上限、参考码率和降档门槛；
- bandwidth 标记只作为佐证，不再单独构成坏样本；
- 状态记录上次降档估算、动态最大层、probe 原层/目标层、开始时间、坏样本和有效样本数；
- 连续健康 8 秒后逐级 probe，6 秒验证，两次坏样本回退；
- 360p18 进入 `floor-protected`；
- 保持 P2P 路径不分摊服务器预算，TURN/SFU 继续应用安全预算。

修改 `server/screego/deploy/inject.js` 传入新增样本字段、应用 probe/回退决策并输出诊断原因。将网页设置 FPS 从旧 35/45/60 对齐为 30/45/60，已有 35 FPS 配置按不放大原则落到 30 FPS。

## Task 3：客户端策略红灯测试

修改：

- `internal/gamecast/adaptive/controller_test.go`
- `internal/gamecast/adaptive/per_viewer_test.go`
- `gamecast_layer_pool_test.go`

覆盖：

1. 720p60/45/30/21 和高分辨率源顺序、码率及不放大；
2. 单个观看者只改变自身目标层；
3. 首次低估算可降档，但估算器随发送码率下降不能连续触底；
4. 进一步约 25% 恶化或真实接收劣化仍可继续降档；
5. 360p18 触底冻结；
6. 接收侧持续健康可在缺少 120% 估算余量时启动 probe；
7. probe 成功、失败回退和路径变化清理；
8. 聚合编码过载不会移除最低保护层。

先运行目标 Go 测试并记录正确红灯。

## Task 4：实现客户端级联阻断和逐观看者 probe

修改 `internal/gamecast/adaptive/controller.go` 与 `per_viewer.go`：

- `BuildLadder` 使用与网页一致的源感知阶梯和编码上限；
- 为 Go 策略增加参考码率和降档门槛纯函数，不改变媒体协议；
- 逐观看者状态增加上次降档估算和 probe 生命周期；
- 低估算降档使用进一步恶化保护，真实接收劣化可绕过该保护；
- 触底后只允许恢复；
- probe 切换仍通过现有层池、IDR 和会话层分配完成，不新增 RID/SDP。

确认 `gamecast_p2p_media.go` 的共享端聚合负载只移除高成本层，最低层保持可用；只在红灯证明现有行为不足时做最小修改。

## Task 5：部署资产版本与集成回归

- 若策略脚本内容变化，将 `webrtc-adaptive.js` 缓存键从 `v=5` 升级为 `v=6`；
- 若 `inject.js` 内容变化，将其从 `v=6.25` 升级为 `v=6.26`；
- 更新仅用于验证资源顺序和版本的测试；
- `sfu-client.js`、音频脚本、媒体协议和房间开关保持不变；
- 源策略和部署策略 SHA-256 必须相同。

## Task 6：完整验证、构建与记录

运行：

- `server` 全部 Node `test_*.mjs`；
- 服务器 Python 控制面和部署资产测试；
- `go test ./...`；
- `go test -tags partylan_native_media ./...`；
- 前端全部 Node 测试与生产构建；
- Wails 原生构建和 `verify-build.ps1`；
- `git diff --check`、敏感信息扫描和生成文件噪声检查。

记录新 `PartyLAN.exe`、`opus.dll` SHA-256 和构建标签。创建本地提交，不推送、不连接广州节点、不替换线上资源；线上部署和真实限速 A/B 需单独授权。

## 停止条件

- probe 必须修改 Screego/Pion SDP、RID 或房间协议才能实现；
- 网页源策略和部署副本无法保持字节一致；
- 级联阻断会让真实持续弱网完全无法降档；
- 最低保护层因编码器实际失败而无法工作；
- 自动回归出现与本次策略无关且无法稳定复现的新失败。
