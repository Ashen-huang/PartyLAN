# PartyLAN P2P 自适应稳定性修复实施计划

**状态：** 已完成，待实机 A/B 验收
**日期：** 2026-08-08
**设计依据：** `docs/superpowers/specs/2026-08-08-partylan-p2p-adaptive-stability-design.md`

## Task 1：网页策略红灯测试

修改 `server/test_webrtc_adaptive_policy.mjs` 和集成测试，先证明当前实现存在以下问题：

1. 仍从原始档直接跳到 720p24/3 Mbps。
2. 降档确认需要约 15 秒而非 4 至 6 秒。
3. 恢复只检查当前档并等待 30 秒，未检查下一档 120% 余量。
4. `test` 房间没有 browser baseline 作用域。

## Task 2：网页策略实现

同步修改源策略与部署策略，生成原始 60/45/30、720p30、540p24、360p18 档位；调整确认、
冷却和恢复门槛。修改 `inject.js`，让 `test?adaptive=baseline` 跳过 sender profile 写入和
策略决策但保留 stats。升级静态资源版本并保持源/部署策略逐字节一致。

## Task 3：原生策略红灯测试

修改 `internal/gamecast/adaptive/controller_test.go`，覆盖：

1. 1654x940@60 先生成同尺寸 45/30 档。
2. 6 秒内确认本地性能压力，10 秒健康后逐档恢复。
3. 单个或多个弱观看者不触发全局降档。
4. 采集停顿、编码过载和写入失败得到不同原因。

## Task 4：原生策略实现

修改 `internal/gamecast/adaptive/controller.go` 和必要的事件映射。用源尺寸派生阶梯替代只过滤
固定 1920 宽档位的逻辑；只允许本地压力改变共享编码层；恢复比较待恢复档位的本地能力。
保持接口兼容并升级策略版本。

## Task 5：验证、构建和灰度

运行相关单测后执行服务器 Node/Python、Go 全仓、`partylan_native_media` Go 全仓、前端测试
与生产构建。审计差异、敏感信息和生成噪声，重建 PartyLAN 客户端。生产变更只允许原子
替换网页静态资源和 Nginx 中的缓存版本，不重启屏幕/语音 SFU 或 Screego 容器；完成
`test` baseline 与修复策略的服务端可达性检查并保留回滚备份。

## 执行记录

- 网页策略升级为 `adaptive-per-peer-v3`，部署资产版本为
  `webrtc-adaptive.js?v=4`、`inject.js?v=6.25`。
- `test` 房间可通过 `?room=test&adaptive=baseline` 启用
  `browser-bwe-baseline-v1`；其他房间忽略该参数。
- 原生策略升级为 `native-global-adaptive-v2`，非标准源先生成同尺寸 45/30 FPS 档；
  单个弱观看者只进入诊断，不改变共享编码层。
- 采集暂不可用、编码失败/过载和传输写入失败分别归因为 `capture-stalled`、
  `encoder-overload`、`transport-write`。
- 自动化通过：服务器 Node 112、Python 37、前端 Node 40；普通 Go 全仓与
  `partylan_native_media` Go 全仓通过；前端生产构建与 Wails 原生构建通过。
- 广州节点回滚备份：
  `/root/screego/backups/p2p-adaptive-20260808T174531+0800`。
- 线上 `inject.js` SHA-256：
  `6f30ae3a7a558c90ee0e79a946b86e69b324c2f39d148dadf47bcc9cfeb59445`。
- 线上 `webrtc-adaptive.js` SHA-256：
  `597c42727c045121b3a8b857a038877f2e2976786bc21bbd0804a40b15c94ce8`。
- 部署后 `screego-register`、屏幕 SFU、Screego 容器和语音容器 PID 均未变化；
  屏幕媒体开关保持 `SFU=false`、canary 为空、legacy P2P 允许。
- 最终 `PartyLAN.exe` SHA-256：
  `D15D263D482529F3C0C00CCDEED35DE895BED42C7C6E23418E647B20372ED682`。
- `opus.dll` SHA-256：
  `2134411EA3DAF238B9FA163917292E8C9CF80593822A338A7938B3685269AF1F`。
