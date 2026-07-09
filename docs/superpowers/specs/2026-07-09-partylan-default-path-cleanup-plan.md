# PartyLAN 默认路径清理实施计划

> Based on `2026-07-09-partylan-gamecast-window-share-audio-design.md`

## 1. 目标

先把默认 `gamecast-client` 用户路径清理和稳定化，避免旧 Tailscale 主线残留、窗口行为异常、错误文案乱码和观看状态混乱继续干扰后续窗口共享与共享音频开发。

本阶段完成后，默认用户路径应只围绕 GameCast 工作，不再混杂旧网络模式心智。

## 2. 交付策略

这轮只做“默认路径稳定化”，不进入窗口共享或共享音频的底层实现。

实施顺序：

1. 默认 UI 与 legacy 文案清理
2. 窗口壳体与拖动/标题栏统一
3. 观看态与重连稳定化
4. 共享顶部真实指标与错误文案修正

关键路径：

`P1 UI 清理 -> P2 窗口壳体 -> P3 viewer 状态 -> P4 观测性收尾`

## 3. 范围

### 包含

- 清理默认 `gamecast-client` 路径中的 Tailscale / tsnet / DERP / Auth Key / 节点加入残留
- 窗口拖动、标题栏和无边框壳体统一
- 正常中文错误文案
- viewer 断线/重连后的状态稳定化
- 顶部真实分辨率、真实 FPS、真实码率展示
- 成员列表与 presence 的基础准确性修复

### 不包含

- 窗口级共享
- 系统音频 / 进程音频并入 GameCast host
- 网页房接管共享的完整编排

## 4. 现状约束

- 默认主界面仍夹带旧 PartyLAN / Tailscale 路径文案和组件残留。
- 前端自绘标题栏与系统标题栏可能同时存在。
- 错误提示里存在历史乱码字符串。
- viewer 状态在 host 重新共享或连接波动后会出现关闭但不恢复的情况。
- 顶部显示的分辨率 / FPS / 码率并不总是取自真实视频流或真实轨道状态。

## 5. 分阶段计划

### P1. 默认 UI 与 legacy 清理

**任务**

1. 识别默认 `gamecast-client` 路径中所有旧 Tailscale 主线残留：
   - 文案
   - 输入项
   - 节点列表
   - 诊断标签
2. 将这些内容限制到：
   - legacy 模式
   - 或开发 / 诊断面板
3. 确保大厅和房间主路径只保留：
   - GameCast 服务器
   - 房间
   - 共享
   - 音频
   - 观看状态

**主要文件**

- `frontend/src/App.tsx`
- `frontend/src/components/DiagnosticsPanel.tsx`
- `frontend/src/i18n.ts`

**完成标准**

- 默认主界面不再出现旧 Tailscale 主路径内容

### P2. 窗口壳体与拖动统一

**任务**

1. 确认使用自绘标题栏还是系统标题栏，默认只保留一种。
2. 若使用自绘标题栏：
   - 窗口改为真正无边框
   - 标题栏可拖动
   - 按钮区域不可拖动
3. 消除双标题栏和窗口不可拖动问题。

**主要文件**

- `main.go`
- `frontend/src/App.css`
- `frontend/src/App.tsx`

**完成标准**

- 只有一层标题栏
- 窗口可正常拖动

### P3. viewer 状态与重连稳定化

**任务**

1. 梳理 GameCast viewer 在以下场景的状态切换：
   - host 停止共享
   - host 重新共享
   - `/sfu-ws` 非正常关闭
2. 增加仅限 viewer 模式的轻量自动重连。
3. 保证重连失败时错误信息清楚，不误把房间状态清空。
4. 梳理 presence / 成员列表重建逻辑，减少残留成员和错误角色。

**主要文件**

- `frontend/src/App.tsx`
- `frontend/src/media/gamecastReceiver.ts`
- `internal/gamecast/ws/client.go`

**完成标准**

- 网页端重新共享后，原生 viewer 能尽量自动恢复观看
- 成员列表不再长期保留过期状态

### P4. 观测性与错误文案收尾

**任务**

1. 将所有用户可见乱码错误文案替换成正常中文。
2. 顶部共享指标优先显示真实值：
   - 真实分辨率
   - 真实解码 FPS
   - 真实源 FPS
   - 真实码率
3. 启动、共享、拒绝和重连路径保留明确日志。

**主要文件**

- `app.go`
- `frontend/src/components/ScreenPreview.tsx`
- `pkg/diaglog/*`

**完成标准**

- 用户界面不再显示乱码错误
- 顶部指标尽量与真实流一致

## 6. 验证

- `npm run build`
- `go test .`
- `go test ./internal/gamecast/nativehost`
- 手工验证：
  - 默认大厅无旧 Tailscale 主路径内容
  - 窗口可拖动
  - 共享错误为正常中文
  - 网页 host 重新共享后 viewer 能恢复或给出明确状态

## 7. 结束条件

- 默认主界面路径已清理
- 标题栏与拖动行为正确
- 观看态重连稳定化到可接受水平
- 共享顶部指标和错误文案可用
