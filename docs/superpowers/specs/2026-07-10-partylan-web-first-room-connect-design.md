# PartyLAN 网页端首次房间连接紧急修复设计

## 1. 问题与目标

网页端从大厅创建或加入房间时，首次点击会显示“网络连接出现异常，请稍后重试。”，第二次点击才能成功。生产访问日志显示同一浏览器会先出现一个没有业务数据的 /stream WebSocket，随后第二次操作建立的连接才收到房间数据。

本次修复目标：

- 页面刷新或重新登录后，首次点击创建房间即可成功。
- 页面刷新或重新登录后，首次点击加入已有房间即可成功。
- 重叠的 /stream 连接不能在新连接完成握手前互相关闭。
- 保留现有 SFU 消息过滤、显式退出和跨端 Presence 能力。
- 不修改网页主题、布局、颜色、Screego 基础 bundle 或媒体协议。

## 2. 根因边界

Screego 的原生流程在点击创建或加入后建立 /stream WebSocket，并以收到的首个 room 消息作为房间握手成功标志。

当前注入脚本在构造新的 /stream WebSocket 时立即：

1. 把新连接保存为 activeRoomSocket。
2. 静默关闭仍处于 CONNECTING 或 OPEN 状态的旧连接。

这使 URL 自动加入、React 初始化和用户点击产生的短暂重叠连接发生竞争。旧连接的关闭或错误回调会把房间状态重置为未连接，而新连接尚未收到 room 消息，表现为首次点击失败。

控制面接口在复测期间正常返回 200，因此 Presence、房间状态接口和认证不属于本次修复范围。

## 3. 连接所有权设计

注入脚本把 /stream 连接分成两个阶段：

- pending：WebSocket 已创建，但还没有收到首个有效 room 消息。
- active：已经收到首个有效 room 消息，可以代表当前房间。

创建 pending 连接时不关闭任何现有连接，也不覆盖 activeRoomSocket。

包装后的消息处理器在转发消息给 Screego 前检查消息类型。首个有效 room 消息到达时：

1. 将当前 socket 提升为 activeRoomSocket。
2. 若存在另一个 active socket，将旧 socket 标记为静默关闭后再关闭。
3. 记录已确认房间 ID。
4. 启动该房间的历史记录、Presence 和房间状态心跳。
5. 将原始 room 消息继续交给 Screego，不改变其业务顺序。

pending 连接失败或关闭时只清理自身引用，不能清空另一个已确认的 active socket。

## 4. URL 与控制面时序

monitorRoomUrl 继续负责离开房间和房间 ID 变化后的清理，但不再把 URL 中出现 room 参数当作媒体房间已经建立。

Presence 和房间状态心跳以 WebSocket 的 room 握手为准：

- 普通大厅点击：收到 room 后注册 Presence。
- 带 ?room= 的自动加入：同样等待 room 后注册。
- WebSocket 握手失败：不创建虚假的 Presence 房间。
- 已确认房间切换：先确认新房间，再释放旧连接；显式退出仍立即清理当前房间。

## 5. 错误处理

- 不自动模拟第二次点击，也不自动重复 create/join 请求，避免重复创建或重复加入。
- 未确认连接的错误仍交给 Screego 原生错误处理，保留真实故障反馈。
- 被新 active 连接替换的旧连接使用 _gcSilentClose，不显示误导性的网络错误。
- socket 的 close 回调只有在引用仍指向自身时才能清空 active 状态，避免旧连接晚到的 close 事件破坏新房间。

## 6. 测试

新增独立 Node 测试夹具，使用可控的 FakeWebSocket 覆盖：

1. 第一个 /stream 创建后不会被其他 pending 连接立即关闭。
2. 收到首个 room 消息的连接被提升为 active。
3. 新连接完成 room 握手后才静默关闭旧 active 连接。
4. 旧连接延迟触发 close 时不会清空新 active 连接。
5. pending 连接失败时不会触发自动 create/join 重试。
6. 非 /stream WebSocket 不受包装逻辑影响。

验证门禁：

- node --test server/test_web_control.mjs
- 新增 WebSocket 生命周期测试
- node --check 检查最终 inject.js
- 部署前后文件摘要、服务状态和生产页面静态资源检查
- 手工验证刷新后首次创建和首次加入各至少两轮

## 7. 部署与回滚

只部署 /root/screego/frontend/inject.js：

1. 创建带 UTC 时间戳的独立备份。
2. 上传到临时路径并执行语法检查。
3. 原子替换生产文件。
4. 不重启 Screego 基础服务；通过新页面加载验证脚本。
5. 若首次点击仍失败或出现主题变化，立即恢复本次备份。

## 8. 完成标准

- 刷新后首次创建房间成功且没有网络异常提示。
- 刷新后首次加入已有房间成功且没有网络异常提示。
- 第二个浏览器仍可加入同一房间。
- 离开房间后再次创建或加入仍然一次成功。
- Presence、共享锁、SFU 消息过滤和显式退出没有回退。
- 网页主题和布局与部署前一致。
