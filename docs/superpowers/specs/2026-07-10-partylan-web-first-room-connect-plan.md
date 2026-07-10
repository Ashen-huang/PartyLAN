# PartyLAN 网页端首次房间连接紧急修复计划

> Based on 2026-07-10-partylan-web-first-room-connect-design.md

## 1. 范围

只修改网页注入脚本的 /stream WebSocket 所有权时序，保留现有 SFU 消息过滤、Presence、共享锁、显式退出和页面样式。

## 2. 失败测试

新增 server/test_web_socket_lifecycle.mjs：

1. 在 VM 中加载当前 inject-merged.js，但阻止 init 执行并导出 WebSocket 生命周期测试钩子。
2. 使用 FakeWebSocket 创建两个尚未收到 room 消息的 /stream 连接。
3. 断言第二个 pending 连接创建时第一个连接没有被关闭。
4. 在当前实现上运行测试，确认因为第一个连接被立即关闭而失败。

补充覆盖：

- room 消息将 pending 连接提升为 active。
- 新 active 连接只在完成 room 握手后关闭旧 active。
- 旧连接延迟 close 不会清空新 active。
- 非 /stream WebSocket 保持透明。

## 3. 最小实现

修改 tmp/cross-client-compat/inject-merged.js：

1. decorateStreamSocket 不再在构造阶段关闭或覆盖 activeRoomSocket。
2. 解析入站 room 消息并在成功握手时调用 promoteRoomSocket。
3. promoteRoomSocket 原子替换 active 引用，然后静默关闭旧 active。
4. close 回调仅在 activeRoomSocket 仍等于自身时清理引用。
5. URL 监听不在 room 消息之前启动 Presence 和房间状态心跳。
6. room 握手成功后再执行历史、Presence 和心跳激活。

禁止修改主题 CSS、翻译、媒体协议和 Screego 基础 bundle。

## 4. 自动验证

- node --test server/test_web_socket_lifecycle.mjs
- node --test server/test_web_control.mjs
- node --check tmp/cross-client-compat/inject-merged.js
- git diff --check

## 5. 部署

1. 验证生产 /root/screego/frontend/inject.js 的 SHA-256 仍为诊断时版本。
2. 创建 /root/screego/backups/web-first-room-connect-<UTC>/inject.js。
3. 上传新脚本到临时路径并在服务器执行 node --check；服务器没有 Node 时使用本地语法检查结果并比对上传哈希。
4. 原子替换生产 inject.js。
5. 请求页面和脚本，确认 HTTP 200、脚本哈希正确、服务仍 active。

## 6. 验收

- 自动测试中的首次连接生命周期通过。
- 用户刷新网页后首次创建房间成功。
- 用户刷新网页后首次加入已有房间成功。
- 页面主题无变化。

## 7. STOP 条件

- 新测试不能在当前实现上复现“pending 连接被关闭”。
- 部署前生产脚本哈希与诊断版本不一致。
- 修复需要修改 Screego 基础 bundle 或主题。
- 自动测试或语法检查失败且无法由本次范围内的改动解决。
