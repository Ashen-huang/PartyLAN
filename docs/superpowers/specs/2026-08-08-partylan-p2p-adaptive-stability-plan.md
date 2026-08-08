# PartyLAN P2P 自适应稳定性修复实施计划

**状态：** 实施中
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
