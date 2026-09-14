# PartyLAN 劣质 TURN 自动恢复与生产一致性修复实施计划

> Based on `2026-07-20-partylan-turn-recovery-production-design.md`

## 1. 目标

- 同步生产质量上报、质量快照、网页统计和 Nginx 路由。
- 对持续劣质的单个网页 TURN 会话施加保护档并自动执行一次 ICE restart。
- restart 失败时保持保护档，不执行第二次自动连接恢复。
- 稳定后逐级解除保护。
- 降低控制面重复请求和未认证 401。
- 修正实测媒体指标与容量估算的字段和文案。

## 2. 实施边界

- 不启用单观看者 PeerConnection 重建。
- 不强制网页房间切换到自定义 SFU。
- 不修改网页主题、布局、账号、房间所有权或共享锁语义。
- 不覆盖现有生产镜像，不重置当前工作区改动。
- 自动 restart 仅适用于 Screego 网页 host/client 会话；PartyLAN 原生主机继续使用质量快照动态调档。

## 3. Task 1：恢复控制器失败测试

**新增或修改：**

- `tmp/gamecast-sfu-source/ui/src/recoveryController.ts`
- `tmp/gamecast-sfu-source/ui/src/recoveryController.test.mjs`
- `server/screego/deploy/webrtc-quality.js`
- `server/test_webrtc_quality_policy.mjs`

先增加并运行失败测试：

1. 复合异常满足 5/7 且末 3 连续时返回 `protect-and-restart-ice`。
2. 2-6 秒短波动、静止画面、后台和非 TURN 路径不触发。
3. 同一会话重复请求合并，自动 restart 次数上限为 1。
4. restart 10 秒超时后进入 `protected`。
5. 停止或关闭会话取消计时器。
6. 稳定 20 秒后按 3、6、8 Mbps、用户目标逐级恢复，且不超过目标。

## 4. Task 2：Screego 单会话 ICE restart

**修改：**

- `tmp/gamecast-sfu-source/ui/src/iceSession.ts`
- `tmp/gamecast-sfu-source/ui/src/iceSession.test.mjs`
- `tmp/gamecast-sfu-source/ui/src/useRoom.ts`
- `server/screego/patches/0001-partyLAN-ice-and-fullscreen.patch`

测试先行覆盖：

1. host 使用 `createOffer({iceRestart: true})`。
2. restart 进行中重复调用返回同一任务。
3. 新 SDP/ufrag 推进 generation，旧候选被丢弃。
4. client answer 完成后 restart 任务结束。
5. 10 秒超时、endshare、stopShare 和连接关闭取消任务。

实现要求：

- 每个 host session 暴露受控恢复入口，不把 WebSocket 或 SDP 暴露给注入脚本。
- 恢复入口负责创建 offer、设置 local description、发送 hostoffer 和记录结构化诊断。
- client 沿用现有 hostoffer/clientanswer 流程，按新 generation 处理候选。
- 每个 session 最多自动调用一次。

## 5. Task 3：网页保护档与恢复联动

**修改：**

- `server/screego/deploy/inject.js`
- `server/screego/deploy/webrtc-adaptive.js`
- `server/screego/deploy/webrtc-quality.js`
- 相应 Node 测试

实现要求：

- 质量确认后先为目标 sender 设置最大 1280x720、3 Mbps，再调用核心恢复入口。
- 保护上限与现有逐观看者自适应取最小值，常规 `applyBitrate()` 不得覆盖保护档。
- restart 后重新观察；稳定后逐级恢复。
- 失败或再次确认劣化时保持保护档并显示 `protected`。
- 所有异步失败显式捕获并写入受控诊断，不产生未处理 Promise。

## 6. Task 4：控制面降频和认证门控

**修改：**

- `server/screego/deploy/inject.js`
- `server/test_web_control.mjs`
- 必要的控制面集成测试

测试先行覆盖：

1. 未认证用户不发送受保护 room-state、presence 和 share 写请求。
2. 相同 room-state 在保活窗口内不重复 POST。
3. 房间状态、presence 和 viewer intent 使用统一 5 秒调度器。
4. `/config` 轮询间隔为 5 秒，显式认证变化仍立即刷新。
5. 15 秒 TTL 下心跳不会过期。

## 7. Task 5：质量闭环与遥测语义

**同步并验证：**

- `server/register.py`
- `server/screego/deploy/register.py`
- `server/screego/deploy/webrtc-stats.js`
- `server/screego/deploy/nginx-screego.conf`
- `server/test_register_control_api.py`
- `server/test_webrtc_stats.mjs`

要求：

- `/api/quality/report` 与 `/api/quality/snapshot` 的身份、房间隔离和 TTL 测试通过。
- 实测码率、可用发送带宽、目标码率、保护上限和 TURN 容量估算使用独立字段。
- 旧字段仅在兼容需要时保留，不再以“服务器实际负载”展示容量估算。
- 更新网页资源缓存版本。

## 8. Task 6：自动验证

依次运行：

```powershell
node --test --experimental-strip-types tmp/gamecast-sfu-source/ui/src/iceSession.test.mjs
node --test --experimental-strip-types tmp/gamecast-sfu-source/ui/src/recoveryController.test.mjs
node --test server/test_webrtc_*.mjs server/test_web_*.mjs
python -m unittest discover -s server -p "test_*.py"
server/screego/verify-build.ps1
go test ./...
go test -tags partylan_native_media ./...
node --test frontend/tests/*.test.mjs
npm run build --prefix frontend
git diff --check
```

## 9. Task 7：生产预部署与临时容器

1. 重新读取并记录生产容器、镜像、compose、systemd、Nginx 和网页资源哈希。
2. 与分析阶段基线比较；任何未解释变化触发 STOP。
3. 创建 `/root/screego/backups/turn-recovery-<UTC>/`，保存完整回滚输入。
4. 构建新的不可变镜像标签。
5. 使用非冲突 HTTP/TURN 端口启动临时容器。
6. 验证登录、房间、HTTP、TURN、质量接口和网页资源。

## 10. Task 8：生产切换与验收

1. 原子更新 `/root/screego` 维护副本和 `/var/www/screego` 线上资源。
2. 更新 register 服务并重启，确认 active 且质量接口可用。
3. 使用 `docker compose --env-file /etc/gamecast-sfu/runtime.env` 切换新镜像。
4. 验证公网资源哈希、登录、房间、共享、观看、质量上报和 stats 落盘。
5. 双用户覆盖 P2P、正常 TURN 和持续劣质 TURN，确认只执行一次 restart。
6. 监控服务错误、401、质量动作和 TURN 指标；异常立即回滚。

## 11. Task 9：客户端制品

1. 重新运行 PartyLAN 客户端生产构建。
2. 使用正式打包脚本生成 EXE 和 ZIP。
3. 校验 build EXE、ZIP 内 EXE 与 SHA256 清单一致。

## 12. STOP 条件

- 生产文件、镜像或 compose 哈希与记录基线不一致且无法解释。
- 失败测试不能复现缺少 restart、重复请求或保护档被覆盖的问题。
- restart 产生 offer collision、全房间中断或同会话第二次自动动作。
- 单观看者保护无法避免影响其他观看者。
- 临时容器登录、房间、HTTP、TURN 或质量接口失败。
- 生产切换后网页主题、基础共享或客户端跨端行为回归。
- 自动验证出现不能在限定范围内解释的失败。

## 13. 完成标准

- 全部自动验证通过。
- 临时容器和生产烟雾验证通过。
- 生产质量反馈闭环产生有效样本。
- 劣质 TURN 只对目标观看者执行一次 restart，失败后保持保护档。
- 控制面不再产生重复未认证 room-state 401 风暴。
- 旧镜像、备份和单命令回滚路径可用。

