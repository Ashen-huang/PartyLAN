# PartyLAN 网页端悬停工具层实施计划

**日期：** 2026-08-24  
**设计依据：** `docs/superpowers/specs/2026-08-24-web-hover-tools-design.md`  
**范围：** 仅网页端注入脚本与测试

## 任务一：建立悬停工具层生命周期

修改 `server/screego/deploy/inject.js`：

- 增加 `gc-hover-tools` 根节点、左右热点、成员抽屉和共享抽屉的稳定 ID。
- 增加 `ensureWebHoverTools`、`syncWebHoverToolsVisibility`、`destroyWebHoverTools` 等生命周期函数。
- 仅在当前 URL 存在房间且确认房间状态后显示；离开房间、URL 变化和 `beforeunload` 时销毁节点、定时器与事件监听。
- 桌面端使用 `mouseenter`/`mouseleave`、`focusin`/`focusout`；触摸设备使用手柄点击和外部点击关闭；`Escape` 关闭固定抽屉。
- 根容器使用 `position: fixed`、`pointer-events` 分层和 `transform` 动画，层级低于 `gc-stats-overlay`，不参与原页面布局。
- 注入独立 `gc-hover-tools-theme` 样式块，包含 reduced-motion、窄窗口和焦点样式。

## 任务二：拆分成员 Presence 渲染目标

修改 `server/screego/deploy/inject.js`：

- 将现有 `renderWebPresenceMembers` 拆为可复用的成员条目渲染函数。
- 同步渲染 Screego Tooltip 内的 `#partylan-presence-roster` 与悬停抽屉内的 `#gc-web-presence-roster`。
- 统一房间显示 PartyLAN Presence；非统一房间保持 Screego 原生名单，不显示 PartyLAN 抽屉内容。
- 保持成员去重、客户端类型、房主/成员、当前成员和共享中标签规则不变。
- 成员数据刷新只更新抽屉内容，不重建观看区或媒体元素。

## 任务三：实现紧凑共享抽屉

修改 `server/screego/deploy/inject.js`：

- 共享抽屉显示共享阶段、共享者、当前分辨率、FPS、质量偏好、音频模式和可用操作。
- 读取现有 `lastRoomState`、`webShareLease`、`activeDisplayStream`、`extCfg` 和 `webCanStartShare`，不创建新的媒体状态机。
- “开始共享”点击只调用现有设置弹窗入口；“停止共享”继续调用现有停止流程；不从 hover 回调中调用 `getDisplayMedia`。
- 共享锁定、租约失效、编解码不可用等错误在抽屉内显示可恢复文案。
- 统一刷新入口与 `syncShareButtonAvailability`、`rememberRoomState`、Presence 刷新保持一致。

## 任务四：紧凑化共享设置面板

修改 `server/screego/deploy/inject.js`：

- 将 `gc-share-setup-modal` 的宽度、内边距、源列表卡片和表单间距压缩为侧向面板尺寸。
- 维持明确点击、捕获 Promise、序号防重入、取消和失败清理逻辑。
- 捕获进行期间不因鼠标离开抽屉或面板而关闭；完成、取消和失败后恢复正常 hover 状态。
- 保留原生窗口/标签页选择器和音频模式规则，不改变浏览器权限处理。

## 任务五：回归测试与静态检查

修改或新增：

- `server/test_web_unified_sfu_integration.mjs`
- `server/test_web_share_audio_integration.mjs`（仅在共享入口或音频标签断言受影响时）
- 必要时新增 `server/test_web_hover_tools_integration.mjs`

测试覆盖：

1. 统一/非统一房间的抽屉显示边界。
2. Presence 双挂载点同步与成员更新。
3. hover、focus、Escape、触摸点击和外部点击清理。
4. 共享入口点击只打开现有设置流程，不提前调用捕获。
5. 设置捕获 Promise 未完成时面板保持打开，重复确认不创建第二次请求。
6. `gc-stats-overlay` 层级和内容不变。
7. 记录工具层显示前后观看区矩形尺寸一致。

运行：

```powershell
node --check server/screego/deploy/inject.js
node --test server/test_web_unified_sfu_integration.mjs server/test_web_share_audio_integration.mjs
node --test (Get-ChildItem server -Filter 'test_*.mjs' | Select-Object -ExpandProperty FullName)
```

## 任务六：缓存版本、构建与线上灰度

- 将 `nginx-screego.conf` 和生产配置中的注入脚本缓存版本从 `v6.29` 升级到 `v6.30`。
- 运行 `git diff --check`、全套网页 Node 测试和生产构建。
- 线上部署前确认无活动房间，备份 `/var/www/screego/inject.js`、`/root/screego/frontend/inject.js`、生产 Nginx 配置及进程/容器状态。
- 只原子替换网页注入脚本和生产配置中的缓存版本，执行 `nginx -t` 与平滑 reload；不重启 SFU、控制面、语音服务或容器。
- 线上验证 `inject.js?v=6.30` 摘要、统一/非统一房间显示、诊断面板、观看区尺寸和近期 5xx。
- 保留带时间戳的回滚备份，部署记录写入本计划文档。
