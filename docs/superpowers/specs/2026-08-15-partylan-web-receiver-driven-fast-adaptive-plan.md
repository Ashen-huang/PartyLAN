# PartyLAN 网页端接收反馈驱动的快速自适应实施计划

**日期：** 2026-08-15
**状态：** 待实施
**设计依据：** `docs/superpowers/specs/2026-08-15-partylan-web-receiver-driven-fast-adaptive-design.md`

## 控制面与会话绑定红灯

先扩展以下测试：

- `server/test_gamecast_control.py`
- `server/test_register_control_api.py`
- `server/test_screego_ice_lifecycle.mjs`
- `server/test_webrtc_adaptive_integration.mjs`

覆盖网页房主可以读取本房间质量快照、普通成员仍被拒绝、质量样本按 participant 与 Screego session 分开保存、错误和过期 session 不进入活动快照，以及 Screego host/client peer session 会把同一 `sid`、角色、对端和连接实例交给可选 PartyLAN 钩子。

先运行这些目标测试并确认旧实现因网页房主授权限制、participant 单键覆盖、缺少 `screegoSessionId` 和缺少连接钩子而失败。

## 自适应策略红灯

修改：

- `server/test_webrtc_adaptive_policy.mjs`
- `server/test_webrtc_adaptive_integration.mjs`
- 必要时扩展 `server/test_webrtc_stats.mjs`

覆盖：

- 精确绑定的接收损伤可以绕过旧 25% 级联阻断；
- 首次严重损伤逐档下降，下一周期仍严重时按容量跳到可持续层；
- `540p21` 在约 1 Mbps、极低解码帧率下继续降到 `360p18`；
- 静止/最小化源没有持续发送帧时不把低解码帧率误判为网络损伤；
- 连续健康约六秒启动单档 probe；
- probe 必须由对应 session 的接收改善提交，报告过期、无改善或恶化时回退；
- 手动 `720p60/45/30/21` 上限可以在降档后恢复到原选择；
- 单次抖动尖峰不清空全部恢复进度，持续异常仍阻止升档。

运行目标 Node 测试并记录旧 `adaptive-per-peer-v4` 的正确红灯。

## 实现精确会话链路

新增后置 Screego 补丁 `server/screego/patches/0005-partylan-peer-session-metadata.patch`：

- 从已经统一的 `createPeerSession` 边界调用可选全局钩子；
- 传入 `pc`、`sid`、`peerId` 和 `role`；
- 钩子异常只记录诊断，不中止 Screego 协商；
- peer session 关闭时通知解绑；
- 为补丁内的 TypeScript 行为增加 Node 测试。

修改 `server/screego/deploy/inject.js`：

- 在 Screego 创建连接前注册全局钩子；
- 把 session 元数据绑定到准确的 `RTCPeerConnection`；
- 观看端质量报告携带 session；
- 网页共享端每周期只拉取一次房间质量快照并映射到对应发送连接；
- 离房、停止共享和连接关闭时清理绑定、快照和自适应状态；
- 缺失、错误或过期反馈退回发送统计模式。

修改 `server/gamecast_control.py` 与 `server/screego/deploy/register.py`：

- 允许当前网页房主读取本房间快照，保留普通成员拒绝和原生客户端兼容路径；
- 校验 session 长度与格式；
- 以 participant + session 为质量样本键；
- 保持活动 TTL、Presence 归属过滤和内存清理。

## 实现快速换档与接收验证恢复

同步修改并保持字节一致：

- `server/webrtc_adaptive_policy.js`
- `server/screego/deploy/webrtc-adaptive.js`

策略升级为新的逐观看者版本，并实施：

- 两秒采样、连续两个有效接收损伤确认；
- 首次下降一层，持续严重损伤按当前容量选择可持续较低层；
- 只在纯发送估算回落时保留旧级联保护；
- 最低层继续记录损伤但不重复降档；
- 稳定约六秒后单档 probe；
- probe 保存接收基线，以实际解码帧率、接收吞吐和连续性改善作为提交条件；
- 失败立即回退，并使用短冷却与有限递增退避；
- 使用平滑接收趋势，避免单次尖峰造成恢复死锁。

`inject.js` 组合该连接的发送统计和精确接收样本，传入发送/解码帧率、接收码率、报告年龄、session 绑定状态和损伤原因；诊断事件输出相同决策依据。

## 资源、补丁与完整回归

- 将 Nginx 中 `webrtc-adaptive.js` 缓存键升级为 `v=7`；
- 将 `inject.js` 缓存键升级为 `v=6.27`；
- 更新部署资产测试，保证脚本顺序、版本和策略副本一致；
- 在干净 Screego 基线上应用全部补丁，运行 UI Node、生产构建、目标 Go 和 auth race 验证；
- 运行服务器全部 Node/Python 测试、普通及 `partylan_native_media` Go 全仓测试、前端 Node 测试和生产构建；
- 运行 `git diff --check`、敏感信息扫描、生成文件噪声检查和摘要核对。

本轮不需要重建 PartyLAN 原生客户端，因为客户端媒体算法和构建产物不在修改范围内。

## 广州节点增量部署

部署前采集当前提交、活动镜像、容器、服务、静态资源、控制面和生产 Nginx 摘要，在 `/root/screego/backups/` 创建独立回滚目录。只替换：

- 新 Screego 不可变镜像与 compose 中的精确镜像标签；
- `/var/www/screego` 的活动网页资源；
- `/root/screego/frontend` 的维护副本；
- `/root/screego/register.py` 与对应控制面模块；
- 生产 Nginx 中两个精确缓存版本字符串。

先部署向后兼容的控制面，再重建 Screego/静态资源并短暂重启 Screego，最后平滑重载 Nginx。屏幕 SFU、语音 SFU和无关服务不重启。

部署后检查镜像、进程、端口、HTTP 摘要、Nginx 语法和控制面 API，再在 `test` 房间保留灰度开关供真实 P2P、TURN、多观看者和限速恢复验证。出现会话建立失败、session 无法精确映射、观众互相影响、换档振荡、无法自动恢复或服务异常时，按本次备份回滚。
