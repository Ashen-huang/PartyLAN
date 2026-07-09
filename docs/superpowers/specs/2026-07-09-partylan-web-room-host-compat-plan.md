# PartyLAN 网页房共享兼容实施计划

> Based on `2026-07-09-partylan-gamecast-window-share-audio-design.md`

## 1. 目标

让客户端在“进入网页端已创建的房间”后，也能以稳定、可理解的方式发起共享，同时尽量少改服务器，并保护网页端当前可用能力。

这一阶段的主目标是“可用和兼容”，不是强求所有路径统一成原生 host。

## 2. 交付策略

本轮围绕 `HostOrchestrator` 展开，核心是做出清晰决策：

- 哪些网页房场景可以低风险切到原生 host
- 哪些网页房场景直接走 `WebCompatHostPath`

关键路径：

`P1 房间类型识别 -> P2 host 选择策略 -> P3 WebCompatHostPath 接入 -> P4 状态与错误收尾`

## 3. 现状约束

- 当前 viewer 进入网页房后的共享接管路径不稳定。
- 当前前端状态机容易把 viewer、host、receiver、native host 混在一起。
- 当前服务端已有网页房流程必须继续工作，不能因客户端接管共享而破坏。

## 4. 分阶段计划

### P1. 网页房状态识别

**任务**

1. 明确定义“网页房”与“客户端自建房”的识别条件。
2. 记录当前房间的入口方式、当前 role、当前 room-state。
3. 为编排器提供稳定的判断输入。

**建议文件**

- `frontend/src/App.tsx`
- `internal/gamecast/host`

### P2. host 选择策略

**任务**

1. 定义原生接管共享的允许条件。
2. 定义直接回退 Web 兼容共享的条件。
3. 不允许在高风险场景下强行切原生 host。
4. 保证用户始终知道当前使用的是：
   - 原生共享
   - 兼容共享

**完成标准**

- 共享行为不再依赖隐式状态机碰运气

### P3. `WebCompatHostPath` 接入

**任务**

1. 复用现有非原生共享路径。
2. 将其明确作为网页房共享的兼容回退。
3. 保持网页 viewer 能继续观看。
4. 保持网页房内的历史、room-state、共享停止流程正常。

**建议文件**

- `frontend/src/media/gamecastHost.ts`
- `frontend/src/App.tsx`

**完成标准**

- 网页房内客户端至少有一条稳定可用的共享路径

### P4. 状态机与错误收尾

**任务**

1. 区分以下失败类型：
   - 切 host 失败
   - 原生共享启动失败
   - 兼容共享启动失败
   - viewer 重连失败
2. 保证失败时不强制退出房间。
3. 停止共享、重新共享、再次观看时，成员列表和顶部状态不乱。

**建议文件**

- `frontend/src/App.tsx`
- `frontend/src/components/ScreenPreview.tsx`
- `frontend/src/components/RoomToolsDialog.tsx`
- `internal/gamecast/ws/client.go`

**完成标准**

- 错误信息清晰
- 状态切换稳定

## 5. 验证

- `npm run build`
- `go test .`
- 手工验收：
  - 客户端进入网页房后开始共享
  - 停止共享
  - 重新开始共享
  - 网页 viewer 重新观看
  - 原生 viewer 重新观看
  - 共享失败时仍保留在房间中

## 6. 风险

- 如果强行把网页房统一到原生 host，容易引入新的 role / presence / ws 状态问题。
- WebCompatHostPath 虽然更稳，但需要和原生 host 的状态展示区分清楚。

## 7. 结束条件

- 网页房内客户端发起共享存在稳定可用路径
- 网页端原有共享和观看能力不回退
- 失败时错误提示清晰，房间状态不被破坏
