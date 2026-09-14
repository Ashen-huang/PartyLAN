# PartyLAN 网页端质量阶梯对齐实施计划

**状态：** 已发布，网页静态灰度通过；认证共享实测待手动确认
**设计依据：** `docs/superpowers/specs/2026-08-10-partylan-web-quality-ladder-alignment-design.md`
**实施范围：** 网页端 `computeEncodingProfile` 档位；不修改自适应决策、媒体协议或线上服务

## Task 1：补充网页质量阶梯失败测试

先修改 `server/test_webrtc_adaptive_policy.mjs`，使测试明确断言：

1. 1080p60 的七档依次为原生 60/45/30、720p30/21、540p21、360p18；
2. 对应上限依次为 10/8.5/6.5/4/3/2/0.8 Mbps；
3. 1440p60 原生三档为 14/11.9/9.1 Mbps；
4. 4K60 原生三档为 20/17/13 Mbps；
5. 低分辨率和低 FPS 源不放大、不提高 FPS；
6. 服务端预算与用户目标仍可进一步降低档位码率。

单独运行该策略测试并确认它因当前只有六档、档位参数不一致而失败，而不是因为语法或导入错误。

## Task 2：实现动态原生档和七档配置

同时修改并保持字节一致：

- `server/webrtc_adaptive_policy.js`
- `server/screego/deploy/webrtc-adaptive.js`

实施内容：

- `MAX_LEVEL` 从 5 调整为 6；
- 静态档位改为 720p30/4 Mbps、720p21/3 Mbps、540p21/2 Mbps、360p18/800 Kbps；
- 原生三档保留源尺寸，通过独立纯函数按源分辨率返回 10/14/20 Mbps；
- 依照 60/45/30 的 100%/85%/65%比例计算原生档上限；
- `computeEncodingProfile` 继续对分辨率和 FPS 使用 `Math.min`，最终码率仍取用户目标、档位上限、
  保护上限和可选服务端预算的最小值；
- 不改 `evaluateAdaptiveSample`、观察窗口、坏样本、恢复和冷却逻辑。

实现后先运行 Task 1 的测试，确认新断言转绿，再运行原有策略测试全部用例。

## Task 3：更新部署缓存版本和一致性断言

将 `server/screego/deploy/nginx-screego.conf` 中的网页策略资源从
`webrtc-adaptive.js?v=4` 升级为 `v=5`，避免浏览器继续使用旧档位。同步更新仅用于验证资源清单的
Node 测试期望；不得递增 `inject.js` 或修改其他资源内容。

运行：

- `server/test_webrtc_adaptive_integration.mjs`
- `server/test_webrtc_recovery_integration.mjs`
- 所有引用 `webrtc-adaptive.js?v=4` 的部署资产测试

源策略和部署策略必须字节一致，脚本仍在 `inject.js` 之前加载。

## Task 4：兼容性回归

从 `server` 目录运行全部 Node `test_*.mjs`，覆盖：

- 网页逐 Peer 自适应；
- Screego P2P/网页 SFU 注入；
- WebSocket 生命周期与 ICE 恢复；
- 网页共享音频与 Presence；
- 遥测和部署资源顺序。

运行服务器 Python 测试，确认注册控制面和部署资产未受影响。然后运行前端生产构建；本轮虽不改 React
代码，仍用构建证明资源类型和打包没有回归。

## Task 5：审计、记录和本地提交

- `git diff --check`；
- 确认只有网页策略、策略测试、缓存版本测试和计划记录发生变化；
- 扫描改动中是否误入凭据、令牌、TURN 密码或公网敏感值；
- 在本计划的执行记录中填写测试结果与部署注意事项；
- 本地提交，不推送、不登录广州节点、不替换线上静态资源。

## 停止条件

- 新档位要求修改 `evaluateAdaptiveSample` 或改变现有时序才能通过；
- 需要更改 Screego/SFU SDP、RID/Simulcast 或 PeerConnection 生命周期；
- 源策略和部署副本无法保持字节一致；
- 全量网页回归出现与档位无关且无法稳定复现的新增失败。

## 执行记录

2026-08-10 已完成：

- `server/webrtc_adaptive_policy.js` 与部署副本改为七档网页质量阶梯，原生三档按源尺寸和实际
  档位 FPS 动态计算 10/14/20 Mbps 上限；两份文件 SHA-256 均为
  `6CEA7EDB92A791430D9984307EAAA9AAB2419A99DC7DAD7D0E6DDB4A2A4C8840`。
- 网页策略缓存键从 `webrtc-adaptive.js?v=4` 升级到 `v=5`；`inject.js`、`sfu-client.js` 和其他
  媒体资源版本未改变。
- 红灯测试先确认旧六档、固定 10 Mbps 源上限和低 FPS 码率不匹配，再由实现转绿。
- `server` 全部 13 个 Node 测试文件共 114 项通过；3 个 Python 测试模块共 37 项通过。Python
  首轮因当前终端设置 `HTTP_PROXY` 且未绕过 localhost 被代理返回空体 404，测试命令设置
  `NO_PROXY=127.0.0.1,localhost` 后全部通过，生产代码无需修改。
- `frontend/npm run build` 通过；`git diff --check` 和敏感信息扫描通过。
- 广州节点于 2026-08-10 完成增量发布，回滚备份为
  `/root/screego/backups/web-quality-ladder-20260810T135717+0800`。仅原子替换
  `/var/www/screego/webrtc-adaptive.js`、`/root/screego/frontend/webrtc-adaptive.js`，并将线上
  Nginx 注入缓存键从 `webrtc-adaptive.js?v=4` 升级到 `v=5`；未重启或重建屏幕/语音容器。
- 发布后两份线上策略 SHA-256 均为
  `6CEA7EDB92A791430D9984307EAAA9AAB2419A99DC7DAD7D0E6DDB4A2A4C8840`；`nginx -t` 和平滑 reload
  通过。屏幕容器 ID/启动时间、语音容器 ID/启动时间、注册控制面 PID 与 Nginx master PID 均保持不变。
- Playwright 使用全新页面访问 `?room=test&gray=20260810-2`，确认实际脚本为 `v=5`，策略版本为
  `adaptive-per-peer-v3`，七档输出为 1080p60/45/30、720p30/21、540p21、360p18；
  `api/room-state?room=test` 返回 200 且房间当时为空闲 `legacy-p2p`，控制台无 Error。
- 自动化浏览器未持有既有登录会话，未使用或提取账号凭据，因此没有执行真实捕获源选择、发送端和观看端的
  认证媒体通话；需由已登录 Edge 在 `test` 房间手动确认共享画面和动态降档。
