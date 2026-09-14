# PartyLAN 跨端房间兼容实施计划

> Based on `2026-07-10-partylan-cross-client-room-compat-design.md`

## 1. 目标

实现以下闭环：

- 房间来源可被服务端可靠记录为 `web` 或 `client`。
- 客户端点击网页房时由默认浏览器加入，点击客户端房时使用原生 viewer。
- 客户端房支持客户端或网页端作为唯一共享者。
- 房间共享权由服务端原子租约保护，不允许同时共享或自动抢占。
- 网页端和客户端使用统一 Presence，双方成员互相可见。
- 客户端原生共享可被网页 viewer 观看，网页共享可被客户端 viewer 观看。
- 不修改网页主题、布局和样式资源。

## 2. 约束与实施边界

- 当前工作区包含大量未提交的既有功能改动，执行时不得重置或覆盖。
- 服务器扩展服务当前位于 `/root/screego/register.py`，网页逻辑位于 `/root/screego/frontend/inject.js` 和 `sfu-client.js`。
- 服务器部署前必须生成带时间戳备份，并记录服务状态、文件摘要和回滚命令。
- `gamecast-sfu.service` 的媒体协议不在本轮重写，只验证现有 host/viewer 协议。
- 旧 `POST /api/room-state` 在迁移期间继续可用，但不能覆盖有效共享租约。
- 所有共享冲突通过结构化错误码返回，两端不直接显示跨运行时底层错误文本。

## 3. P1 服务端控制面

### 3.1 失败测试

为 `register.py` 的状态存储提取可测试逻辑，覆盖：

1. 首个创建者确定 `origin`，有效期内后加入者不能覆盖。
2. Presence join、heartbeat、leave 与 TTL 过期。
3. 两个参与者并发 claim 时只有一个成功。
4. 非租约所有者不能 heartbeat 或 release。
5. 租约过期后自动清空 `sharing`、共享者和 `sfu`。
6. 普通 room-state 心跳不能覆盖有效租约。
7. `/api/rooms` 与 `/api/room-state` 返回来源、共享者和有效成员信息。

测试必须先在现有实现上因缺少行为而失败。

### 3.2 实现

服务器新增：

- 房间元数据存储。
- 统一 Presence 存储。
- 房间级共享租约存储。
- 对存储操作的单进程锁，保证 claim 为原子操作。
- 统一的 TTL 清理入口。

新增 HTTP 接口：

- `POST /api/presence/join`
- `POST /api/presence/heartbeat`
- `POST /api/presence/leave`
- `GET /api/presence`
- `POST /api/share/claim`
- `POST /api/share/heartbeat`
- `POST /api/share/release`

响应错误码：

- `room_not_found`
- `room_origin_conflict`
- `participant_not_found`
- `share_locked`
- `share_lease_invalid`
- `share_lease_expired`

### 3.3 验证

- Python 单元测试全部通过。
- 使用 `curl` 验证 join、claim、冲突、release 和列表响应。
- `screego-register.service` 重启后健康。
- 旧 `/api/room-state` 调用仍返回兼容响应。

## 4. P2 Go 协议与客户端生命周期

### 4.1 失败测试

扩展 `internal/gamecast` 测试：

1. `RoomState` 正确解析 `origin` 与共享租约所有者。
2. Presence 请求包含稳定 `participantId`、`clientType=client` 和角色。
3. claim 冲突保留结构化错误码。
4. lease heartbeat 和 release 携带正确 `leaseId`。
5. 客户端进程生命周期内 participant ID 稳定，重新启动后可变化。
6. 创建客户端房写入 `origin=client`，加入成员不能覆盖来源。
7. 离开房间停止 Presence 与租约心跳。

### 4.2 实现

主要文件：

- `internal/gamecast/roomstate.go`
- `internal/gamecast/client.go`
- `internal/gamecast/client_test.go`
- `app.go`
- `app_test.go`

新增 Go 模型与方法：

- `PresenceParticipant`
- `PresenceRequest` / `PresenceResponse`
- `ShareClaimRequest` / `ShareLeaseResponse`
- `JoinPresence` / `HeartbeatPresence` / `LeavePresence`
- `ListPresence`
- `ClaimShare` / `HeartbeatShare` / `ReleaseShare`

`App` 负责：

- 为当前进程创建 participant ID。
- 创建或进入客户端房时启动 Presence 心跳。
- 共享开始前 claim，成功后才启动媒体。
- 启动失败、停止共享、离开房间时释放租约。
- 租约 heartbeat 更新实际 `sfu` 与音频模式。
- Presence 变化通过 Wails 方法或事件暴露给前端。

## 5. P3 客户端来源分流与成员列表

### 5.1 失败测试

前端纯逻辑测试覆盖：

1. `origin=web` 返回 `open-browser` 加入决策。
2. `origin=client` 返回 `native-viewer` 加入决策。
3. 缺少来源返回 `open-browser`，避免误走原生路径。
4. 房间来源标签映射为“网页端”“客户端”“兼容房间”。
5. Presence 成员按 participant ID 去重并映射来源标签。
6. 有其他有效共享者时禁用共享。
7. 自己持有租约时允许停止共享，不允许第二次 claim。
8. `share_locked` 映射为正常中文。

### 5.2 实现

主要文件：

- `frontend/src/types.ts`
- `frontend/src/media/gamecastOrchestrator.ts`
- `frontend/tests/gamecastOrchestrator.test.mjs`
- `frontend/src/App.tsx`
- `frontend/src/components/MemberList.tsx`
- `frontend/src/i18n.ts`

行为：

- 房间卡片显示来源标签。
- 点击网页房只调用 `BrowserOpenURL`，不修改 PartyLAN 房间状态。
- 点击客户端房才调用 `ConnectGameCastViewer`。
- 删除网页房的 `WebCompatHostPath` 入口，保留代码隔离直至验证完成后再决定是否删除未使用模块。
- 客户端房轮询统一 Presence，成员列表展示网页端/客户端来源和共享者。
- 客户端房由网页成员持有共享租约时，空闲客户端自动成为 viewer。
- 有共享租约时共享入口禁用；服务端冲突作为最终保护。

不修改现有 CSS 主题，只复用现有 badge、文本和成员行结构。

## 6. P4 网页端控制面与跨端媒体

### 6.1 失败测试

为网页注入逻辑增加可独立运行的纯函数测试或 Node 测试夹具：

1. 网页首次创建房间注册 `origin=web`。
2. 加入 `origin=client` 房间不会覆盖来源。
3. 网页标签页注册 `clientType=web` Presence。
4. 网页端成员合并按 participant ID 去重。
5. 已有租约时网页共享按钮被禁用。
6. 网页 claim 失败时不调用 `getDisplayMedia`。
7. 用户取消捕获或 SFU host 启动失败时释放租约。
8. 客户端 native share 激活时网页以 SFU viewer 连接。
9. 网页 share 激活时其他网页成员保持 SFU viewer。

### 6.2 实现

仅修改服务器中的：

- `/root/screego/frontend/inject.js`
- 必要时 `/root/screego/frontend/sfu-client.js`

行为：

- 生成标签页级 participant ID，并注册/续约 Presence。
- 房间离开与页面卸载时尽力注销。
- 用统一 Presence 补充现有成员列表，按 participant ID 去重。
- 开始共享前 claim；冲突时显示本地化提示。
- 网页在客户端房共享时使用现有 SFU host。
- 客户端共享时网页使用现有 SFU viewer。
- 共享停止、捕获结束、页面退出或错误时 release。

禁止修改：

- 主题 CSS。
- Screego 基础 bundle。
- 页面布局与颜色变量。
- 与控制面和 SFU 兼容无关的注入功能。

## 7. P5 部署与端到端验收

### 7.1 部署顺序

1. 备份 `register.py`、`inject.js`、`sfu-client.js`、nginx 配置和 systemd service 定义。
2. 部署服务端控制面并验证旧接口。
3. 部署网页注入逻辑，确认首页主题与静态资源未改变。
4. 构建新的 `build/bin/PartyLAN.exe`。
5. 执行跨端验收矩阵。

### 7.2 自动验证

- 服务端 Python 测试。
- `node --test --experimental-strip-types frontend/tests/gamecastOrchestrator.test.mjs`
- 新增网页控制面测试。
- `npm run build`，工作目录 `frontend`。
- `go test .`
- `go test ./internal/gamecast/...`
- `scripts/Start-PartyLAN.ps1 -Action build`

### 7.3 手工验收

1. 网页房在客户端列表标记“网页端”，点击后浏览器加入，PartyLAN 保持大厅。
2. 客户端房标记“客户端”，网页与客户端双方成员列表互相可见。
3. 客户端共享，网页端能看到视频并听到共享音频。
4. 客户端共享期间，网页端不能开始共享。
5. 客户端停止后，网页成员可开始共享，客户端能够观看。
6. 网页共享期间，客户端不能开始共享。
7. 两端同时点击时只有一端成功。
8. 共享者异常退出后 15 秒内恢复可共享。
9. 网页主题、布局和原有按钮样式保持不变。

## 8. STOP 条件

- 现有 SFU 无法让网页 host 与客户端 viewer 在同一客户端来源房间完成媒体协商。
- 服务端无法在不破坏旧 `/api/room-state` 的情况下部署兼容层。
- 网页成员列表没有稳定且可隔离的挂载位置，必须修改基础 bundle 或主题才能实现。
- 自动测试无法复现来源误判、共享并发或 Presence 缺失中的任一核心问题。
- 服务器部署后出现网页主题或基础 Screego 工作流回归。

触发 STOP 条件时立即回滚服务器文件并报告，不用未经批准的替代架构继续实现。

## 9. 完成标准

- 设计中的 9 项跨端验收矩阵全部通过。
- 服务端来源、Presence 和共享租约测试通过。
- 客户端与网页端构建和测试通过。
- 服务器主题与基础网页房流程未回退。
- 实施结果与偏差记录在本计划的执行记录中。

## 10. 执行状态

- 状态：实现完成，等待真实双端媒体验收
- 当前阶段：P5 手工验收

### 10.1 已完成

- P1：服务端房间来源、统一 Presence、原子共享租约与兼容接口已部署。
- P2：Go 客户端已接入稳定 participant ID、Presence 生命周期和共享租约生命周期。
- P3：客户端房间来源标签、浏览器分流、统一成员列表、共享占用提示和远端共享状态收尾已完成。
- P4：网页端已接入 Presence、共享前 claim、异常 release、客户端房强制 SFU viewer/host 切换和客户端成员补充。
- P5：生产前端与 `build/bin/PartyLAN.exe` 已构建，登录页部署前后布局盒坐标一致，浏览器控制台无脚本错误。

### 10.2 部署记录

- 网页脚本备份：`/root/screego/backups/cross-client-web-20260710T081500Z`
- 部署脚本 SHA-256：`070cd9a33c4255680cd23dc17366db8eedf5130a826d9ff8eec7a3dcd41efaec`
- `sfu-client.js` 与 nginx 配置哈希未变化，未修改网页主题 CSS 或 Screego 基础 bundle。

### 10.3 自动验证

- `npm run build`：通过。
- `go test .`：通过。
- `go test ./internal/gamecast/...`：通过。
- 客户端编排 Node 测试：14 项通过。
- 网页控制面 Node 测试：6 项通过。
- 服务端 Python 测试：9 项通过。
- Wails production build：通过；原生媒体元数据与 Opus runtime 校验通过。
- `PartyLAN.exe` 烟雾启动：进程保持运行，未发生启动即退出。

### 10.4 待验收

- 使用实际登录的网页端与新构建客户端执行第 7.3 节九项双端矩阵，重点验证客户端原生共享的网页音画、网页共享的客户端音画，以及双方同时点击共享时的唯一租约结果。
