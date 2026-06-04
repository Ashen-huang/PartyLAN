# PartyLAN 大厅态 / 房间态 UI 重构实施计划

> Based on `2026-05-28-partylan-lobby-room-ui-task-spec.md`

## 1. 目标

按已批准的 spec 落地以下结果：

- 大厅态和房间态拆成两套明显不同的界面结构
- 大厅态不再显示共享黑屏，首屏聚焦加入、创建和网络状态
- 房间态允许单人直接开始共享
- 单人共享时进入明确的本地共享态，不再显示 `Waiting for shared screen...`
- 后续成员加入时自动接上当前共享，不需要再次点击 `Start Sharing`
- 成员列表拆成 `Me / Others`
- `Network note` 按新的优先级显示最高价值信号
- 全局视觉升级为偏亮一点的赛博霓虹风格

## 2. 实施策略

这轮实现按四条主线推进：

1. 前端状态派生与布局壳体重构
2. 房间内关键组件语义改造
3. 后端共享会话补点
4. 验证与视觉收尾

关键路径是：

`P1 场景分支与主题变量 -> P2 房间组件行为 -> P3 后端共享补点 -> P4 联调验证`

其中 P1 和 P2 可以连续推进，P3 是让“单人可开播 + 后来者自动接流”真正成立的行为闭环，P4 负责把大厅态、房间态和诊断逻辑统一收口。

## 3. 现状约束

开始实现前，需要明确当前代码里的几个真实约束：

- `frontend/src/App.tsx` 现在始终采用同一套双栏结构，没有大厅态 / 房间态显式分支。
- `frontend/src/components/RoomControls.tsx` 仍以 `canShareToPeers` 作为 `Start Sharing` 的硬门槛。
- `app.go` 的 `StartSharing` 在 `len(peers) == 0` 时直接报错。
- `app.go` 当前只有在至少一个 sender 启动成功后才会把 `selfSharing` 视为有效共享。
- 新成员加入后，现有代码不会自动补发当前流状态，也不会自动为新 peer 附着 sender。
- `ScreenPreview` 的真实帧主要来自 `stream:frame`，当前本地共享时不保证有真实本地预览帧。

这意味着本轮实现必须把“用户体验上的共享已开始”和“远端 sender 是否已挂上”这两个概念解耦。

## 4. 分阶段计划

### P1. 场景状态与应用壳体重构

**目标**

先把应用骨架拆成大厅态和房间态两套明确布局，同时为后续组件改造提供稳定的场景派生值。

**任务**

1. 在 `frontend/src/App.tsx` 中整理场景派生值
   - 增加轻量派生状态：
     - `scene`
     - `selfMember`
     - `otherMembers`
     - `shareReady`
     - `networkNote`
   - 保持现有 reducer 结构，不引入新的全局状态模型

2. 重构主渲染结构
   - 大厅态：
     - 左侧为创建 / 加入主机主操作区
     - 右侧为欢迎 / 诊断 / 版本信息区
     - 不渲染 `ScreenPreview`
   - 房间态：
     - 左侧为成员与控制侧栏
     - 右侧为舞台和音频区

3. 调整全局布局样式
   - 更新 `frontend/src/App.css`
   - 为大厅态和房间态定义不同 grid 模板
   - 为窄窗口定义明确的内容优先级

4. 更新全局主题变量
   - 在 `frontend/src/style.css` 中完成主题色和基础面板变量切换
   - 保证所有组件尽量吃变量，而不是继续依赖散落的深色硬编码

5. 轻量调整 `StatusBar`
   - 保持数据来源不变
   - 调整视觉，使其更像半透明霓虹仪表条

**主要文件**

- `frontend/src/App.tsx`
- `frontend/src/App.css`
- `frontend/src/style.css`
- `frontend/src/components/StatusBar.tsx`
- `frontend/src/components/StatusBar.css`

**完成标准**

- 默认启动进入大厅态
- 大厅态不再出现共享黑屏
- 房间态和大厅态在结构上一眼可区分

### P2. 房间组件语义与空态重构

**目标**

把房间内的核心组件从“深色播放器 + 模糊成员表”改成明确服务于共享场景的舞台、成员和诊断组件。

**任务**

1. 改造 `MemberList`
   - 输入改成 `selfMember` 与 `otherMembers` 或等价结构
   - 渲染 `Me` / `Others` 两段
   - 为无其他成员提供清晰空态

2. 改造 `RoomControls`
   - 去掉“无观众不能开始共享”的禁用逻辑
   - `Start Sharing` 的门槛只保留“本机是否支持共享”和“当前是否忙碌”
   - 为单人房间增加轻量提示，说明进入的是本地共享态

3. 改造 `ScreenPreview`
   - 大厅态不渲染
   - 房间待机态区分：
     - 只有自己
     - 房间内已有其他成员但无人共享
   - 自共享时强制进入本地共享语义
   - 观看态保持“等待首帧”的 viewer 语义
   - 如果实现成本可控，补真实本地预览；否则先完成正确状态文案

4. 改造 `DiagnosticsPanel`
   - 提取 `Network note` 选择逻辑
   - 将 `path=` 作为第三优先级，而不是只识别 `path=derp`
   - 改 `Share` 徽标语义，使其表达：
     - `Live`
     - `Ready`
     - `Watching`
     - `Unavailable`

5. 调整 `AvailableNodes`
   - 强化大厅态主操作视觉
   - 让 joinable 主机信息更像“可加入房间清单”

6. 样式收尾
   - 完整更新以下样式文件，使视觉统一到新主题：
     - `MemberList.css`
     - `AvailableNodes.css`
     - `RoomControls.css`
     - `ScreenPreview.css`
     - `DiagnosticsPanel.css`

**主要文件**

- `frontend/src/components/MemberList.tsx`
- `frontend/src/components/MemberList.css`
- `frontend/src/components/RoomControls.tsx`
- `frontend/src/components/RoomControls.css`
- `frontend/src/components/ScreenPreview.tsx`
- `frontend/src/components/ScreenPreview.css`
- `frontend/src/components/DiagnosticsPanel.tsx`
- `frontend/src/components/DiagnosticsPanel.css`
- `frontend/src/components/AvailableNodes.tsx`
- `frontend/src/components/AvailableNodes.css`

**完成标准**

- 房间只有自己时可以点 `Start Sharing`
- 自共享时不再出现 `Waiting for shared screen...`
- 成员区明确分为 `Me / Others`
- `Network note` 优先级符合 spec

### P3. 后端共享会话补点

**目标**

在不重写媒体架构的前提下，让“单人可开播、后来者自动接流”真正成立。

**策略**

采用“轻量 host share session”思路，但不引入大规模新架构。

用户视角上，共享应在点击 `Start Sharing` 后立即成立；实现层允许先把共享会话激活，再按需为远端 peer 挂 sender。

**任务**

1. 在 `app.go` 中引入当前共享会话元数据
   - 记录至少以下信息：
     - display index
     - width
     - height
     - fps
     - codec / backend 元信息
   - 该状态用于：
     - UI 本地共享态
     - 新成员加入时自动附着
     - 停止共享时统一清理

2. 重构 `StartSharing`
   - 去掉 `len(peers) == 0` 的报错路径
   - 将“共享会话已启动”与“已有远端 sender”解耦
   - 点击开始后立即：
     - 标记 `selfSharing`
     - 缓存当前共享配置
     - 发出本地 `stream:started`
   - 对当前已有远端成员，循环尝试附着 sender

3. 抽出“附着当前共享到指定 peer”的 helper
   - 负责：
     - 建立 video 连接
     - 启动 sender
     - 向该 peer 发送当前流控制消息
   - 失败时记录日志，不让整个共享会话立刻崩掉

4. 在 `setupRoomCallbacks` 的 `OnPeerJoin` 中自动附着
   - 如果当前 `selfSharing` 为真：
     - 自动对新 peer 调用附着 helper
     - 自动补发当前共享状态

5. 为房间控制通道补最小能力
   - 在 `internal/room/room_impl.go` 中增加“向指定 peer 补发当前流状态”的能力
   - 避免后来加入的 peer 只连上控制连接，却收不到已有共享的 `stream start`

6. 收口停止与离开逻辑
   - `StopSharing`
   - `LeaveRoom`
   - `OnPeerLeave`
   - `OnFatal`
   都要统一清理 active share session 和 sender 映射，避免残留状态污染后续重试

7. 可选的本地预览增强
   - 如果有低成本方式从 sender 侧或本地回环获取预览帧，可在本阶段补充
   - 如果代价偏高，则只保证 UI 语义正确，不把“真实本地预览”作为本轮阻塞项

**主要文件**

- `app.go`
- `internal/room/room_impl.go`
- 视需要补充对应测试文件

**完成标准**

- 单人建房时 `StartSharing` 不再报“至少需要 1 个观众”
- 房主先开播后，后来加入的成员能自动接上共享
- 停止共享和离开房间后不会残留共享状态

### P4. 联调验证与视觉收尾

**目标**

把前端场景切换、后端共享行为和诊断优先级一起验证到 spec 的验收口径。

**任务**

1. 前端静态验证
   - `cd frontend && npm run build`

2. Go 测试
   - `go test ./...`

3. 单机手工验证
   - 启动后默认大厅态无黑屏
   - 单人建房后可直接开始共享
   - 共享时舞台进入本地共享态
   - 停止共享后回到房间待机态

4. 双机或双节点验证
   - 房主单人先开始共享
   - 第二个成员稍后加入
   - 不二次点击即可收到共享流

5. 诊断优先级验证
   - 验证 `custom DERP present`
   - 验证 `DERP map regions`
   - 验证 `path=...`
   - 验证泛化错误兜底

6. 视觉一致性检查
   - 大厅态和房间态的主题、边框、光感和状态条风格统一
   - 设置面板和音频面板在新主题下仍保持可读性

**完成标准**

- 满足 task spec 中的全部 10 条验收项

## 5. 建议实施顺序

推荐按以下顺序提交或至少按以下顺序开发验证：

1. `style.css` + `App.tsx` + `App.css`
2. `DiagnosticsPanel` 与 `MemberList`
3. `RoomControls` 与 `ScreenPreview`
4. `app.go` 共享会话补点
5. `internal/room/room_impl.go` 定向补发流状态
6. 统一联调和视觉收尾

原因：

- 先拆壳体，能最快让大厅态和房间态从结构上分离
- 再改组件，避免组件在旧壳体里来回返工
- 最后补后端共享行为，能更聚焦验证“单人可开播”和“后来者自动接流”

## 6. 风险与应对

### 风险 1：自共享时拿不到真实本地预览帧

应对：

- 先保证共享态语义正确
- 把真实本地预览作为“低成本可选增强”，不阻塞这轮主目标

### 风险 2：sender-per-peer 模型导致后来者自动接流实现分散

应对：

- 不重写成完整多路复用
- 只抽一个最小 `attachShareToPeer` helper 和当前共享配置缓存

### 风险 3：主题变量切换影响设置面板、音频面板可读性

应对：

- 所有颜色尽量回归变量
- 最后集中做一次视觉一致性检查

### 风险 4：`Network note` 逻辑散落导致大厅态和设置面板显示不一致

应对：

- 将优先级选择逻辑收敛到一个 helper
- 主界面和设置面板共用同一套规则

## 7. 结束条件

当以下条件同时满足时，本计划视为完成：

- 大厅态 / 房间态结构拆分完成
- 单人可直接开始共享
- 自共享时舞台语义正确
- 后来加入的成员自动接上当前共享
- 成员区分为 `Me / Others`
- `Network note` 优先级符合 spec
- 前端构建与 Go 测试通过
- 单机验证通过，双机补充验证通过或明确记录阻塞原因
