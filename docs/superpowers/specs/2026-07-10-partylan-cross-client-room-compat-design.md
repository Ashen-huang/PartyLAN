# PartyLAN 网页端与客户端房间兼容设计

**状态：** 已确认  
**日期：** 2026-07-10

## 1. 目标

建立明确、可验证的跨端兼容边界：

- 客户端列表能可靠区分网页端房间与客户端房间。
- 点击网页端房间时，使用系统默认浏览器加入，不再由 PartyLAN WebView 接管网页房媒体。
- 客户端自建房继续使用原生 SFU host，同时允许网页用户加入观看。
- 客户端自建房空闲时，网页用户也可以发起共享，客户端和其他网页用户能够观看。
- 一个房间同一时间只允许一位成员共享。
- 网页端和客户端看到一致的成员列表，并能识别成员来源。

本设计不修改网页主题、配色或页面结构，也不允许多路同时共享。

## 2. 当前问题

当前系统存在三套彼此未完整对齐的状态：

1. Screego 自身房间与成员状态。
2. `/api/room-state` 保存的扩展房间状态。
3. `/sfu-ws` 中维护的 host、viewer 与 presence 状态。

由此产生以下故障：

- `RoomState` 没有房间来源字段，客户端无法判断房间由网页端还是客户端创建。
- 客户端进入网页房后尝试使用 WebView 兼容 host，但 WebView 捕获与实际网页信令并不可靠。
- 客户端原生 SFU host 与网页端 viewer 的启动条件没有形成稳定契约。
- 客户端只出现在自定义 SFU presence 中，不会自动出现在 Screego 网页成员列表。
- `sharing` 只是可覆盖状态，不具备原子互斥能力，两个成员可能同时开始共享。
- 底层错误文本经过不同运行时后可能显示为 `????????`。

## 3. 总体架构

服务器扩展服务成为跨端控制面的唯一事实来源，负责三类数据：

- 房间元数据：房间来源与房主。
- 统一成员 Presence：当前有效成员及其客户端类型。
- 共享租约：当前唯一共享者与租约有效期。

媒体仍由现有组件负责：

- 网页端房间继续使用 Screego 网页工作流。
- 客户端房间的跨端媒体使用现有 GameCast SFU。
- 客户端原生共享使用 native host。
- 网页用户在客户端房间共享时使用网页 SFU host。

控制面只决定谁可以共享以及其他成员应连接哪条媒体路径，不转发媒体。

## 4. 数据模型

### 4.1 房间元数据

`RoomState` 增加以下字段：

```json
{
  "room": "room-id",
  "active": true,
  "origin": "web",
  "ownerParticipantId": "participant-id",
  "ownerName": "display-name",
  "sharing": false,
  "shareOwnerParticipantId": "",
  "shareOwnerName": "",
  "shareOrigin": "",
  "sfu": false,
  "audioMode": "off",
  "ttl": 15,
  "updatedAt": "2026-07-10T00:00:00Z"
}
```

规则：

- `origin` 只允许 `web` 或 `client`。
- 房间处于有效期内时，`origin` 不允许被后加入成员覆盖。
- 房间过期并重新创建时，新的首个创建者可以确定新的 `origin`。
- `sharing`、共享者字段和 `sfu` 由共享租约派生，普通心跳不能直接覆盖。

### 4.2 统一成员 Presence

每个客户端进程和网页标签页拥有一个会话级稳定 `participantId`：

```json
{
  "participantId": "participant-id",
  "room": "room-id",
  "name": "display-name",
  "clientType": "client",
  "role": "owner",
  "active": true,
  "ttl": 15,
  "lastSeenAt": "2026-07-10T00:00:00Z"
}
```

规则：

- `clientType` 只允许 `web` 或 `client`。
- `role` 用于表达 `owner` 或 `member`，不直接等同于当前媒体 host/viewer。
- `sharing` 不由成员自行上报，而是根据共享租约所有者计算。
- 心跳每 5 秒更新一次，15 秒未续约的成员自动移除。
- 正常退出时主动注销，异常关闭依靠 TTL 清理。

### 4.3 共享租约

共享租约至少包含：

```json
{
  "room": "room-id",
  "leaseId": "lease-id",
  "participantId": "participant-id",
  "clientType": "web",
  "sfu": true,
  "audioMode": "system",
  "expiresAt": "2026-07-10T00:00:15Z"
}
```

服务器以房间为粒度执行原子 compare-and-set：

- 没有有效租约时允许领取。
- 同一成员携带正确 `leaseId` 时允许续约或释放。
- 已有其他有效租约时拒绝领取，返回 `share_locked`。
- 租约超时后自动释放，并同步清空房间共享状态。

## 5. 服务端接口

新增或收敛为以下接口：

- `POST /api/presence/join`：注册或恢复成员。
- `POST /api/presence/heartbeat`：续约成员。
- `POST /api/presence/leave`：主动离开。
- `GET /api/presence?room=<id>`：读取统一成员列表。
- `POST /api/share/claim`：原子领取共享租约。
- `POST /api/share/heartbeat`：续约共享租约并更新媒体属性。
- `POST /api/share/release`：释放共享租约。

`GET /api/rooms` 和 `GET /api/room-state` 返回扩展后的房间字段。

现有 `POST /api/room-state` 在迁移期保留，但不再允许绕过共享租约直接覆盖共享者。部署顺序为服务端兼容层、网页注入逻辑、客户端，避免混合版本期间破坏现有房间。

## 6. 房间列表与加入分流

客户端房间卡片显示来源标签：

- `origin=web`：显示“网页端”。
- `origin=client`：显示“客户端”。
- 迁移期缺少来源：显示“兼容房间”，并使用浏览器打开，避免误走原生媒体路径。

点击行为：

- 网页端房间：调用 `BrowserOpenURL(<base>/?room=<id>)`，PartyLAN 保持在大厅，不建立客户端 viewer 或 host 连接。
- 客户端房间：使用现有 PartyLAN viewer 加入流程。
- 客户端自己的房间：保持原生 host-ready 流程。

此前的 `WebCompatHostPath` 不再用于网页端来源房间。相关错误和状态不会再暴露给用户。

## 7. 客户端房间的跨端媒体流程

### 7.1 客户端开始共享

1. 客户端向服务器领取共享租约。
2. 领取成功后启动 native SFU host。
3. native host 就绪后通过共享心跳写入 `sfu=true` 和实际音频模式。
4. 网页端检测到有效共享租约和 `sfu=true`，以 viewer 身份连接 `/sfu-ws`。
5. 任何步骤失败都释放租约；房间和成员 Presence 保持有效。

### 7.2 网页用户开始共享

1. 网页端先向服务器领取共享租约。
2. 领取成功后调用浏览器屏幕捕获并启动网页 SFU host。
3. 客户端房主处于空闲状态时自动切换为 viewer，连接该网页 SFU host。
4. 其他网页成员同样以 viewer 身份观看。
5. 用户取消选择、捕获失败或 SFU 启动失败时立即释放租约。

### 7.3 已有人共享

- 网页端和客户端都根据房间状态禁用“开始共享”。
- 即使两个成员在状态刷新间隔内同时点击，服务器也只允许一个领取成功。
- 被拒绝方根据 `share_locked` 显示本地化提示：“当前已有成员正在共享。”
- 不允许自动抢占或替换现有共享者。

## 8. 统一成员列表

网页端与客户端都读取 `/api/presence`：

- 网页成员显示“网页端”。
- 客户端成员显示“客户端”。
- 当前共享者根据共享租约显示共享状态。
- 房主与普通成员根据 Presence role 显示。

网页端把统一 Presence 合并到现有成员列表容器中，使用独立数据属性标识注入项并按 `participantId` 去重。只补充成员数据和来源标识，不改动原有主题样式。

客户端成员列表使用同一份 Presence 数据，不再仅依赖 SFU viewer presence。这样网页成员尚未建立媒体连接时也能正确显示。

## 9. 错误处理

服务端返回稳定错误码，不依赖跨运行时传递中文原始错误：

- `room_not_found`
- `room_origin_conflict`
- `participant_not_found`
- `share_locked`
- `share_lease_invalid`
- `share_lease_expired`

客户端和网页端分别把错误码映射为本地化中文。未知错误显示通用提示并写入诊断日志，从而避免界面出现 `????????`。

## 10. 测试与验收

### 10.1 自动测试

- 房间来源由首个创建者确定，后加入成员不能覆盖。
- 网页房点击只打开浏览器，不建立客户端连接。
- 客户端房点击继续使用客户端 viewer。
- Presence 加入、续约、主动离开与 TTL 过期。
- 网页端和客户端成员按 `participantId` 去重。
- 两个成员并发领取共享权时只有一个成功。
- 非租约所有者不能续约或释放。
- 租约超时后房间恢复可共享状态。
- 结构化错误码能映射为正常中文。

### 10.2 跨端验收矩阵

1. 网页创建房间，客户端列表标记“网页端”，点击后由默认浏览器加入。
2. 客户端创建房间，网页成员加入后双方成员列表互相可见。
3. 客户端共享，网页成员可以观看视频和音频。
4. 客户端共享期间，网页成员不能开始共享。
5. 客户端停止共享后，网页成员可以开始共享。
6. 网页成员共享时，客户端和其他网页成员可以观看。
7. 网页成员共享期间，客户端不能开始共享。
8. 共享者异常退出后，最多 15 秒恢复可共享状态。
9. 停止共享、重新共享和成员重连后没有残留成员或错误共享者。

## 11. 非目标

- 不支持同一房间多路同时共享。
- 不允许新共享者抢占当前共享者。
- 不把网页端来源房间强行迁移到客户端原生媒体路径。
- 不修改网页主题、配色、布局或营销内容。
- 本阶段不调整带宽分配和画质自适应策略。
