# PartyLAN 跨端兼容默认共享档位与网页窗口捕获回退实施计划

**日期：** 2026-08-24  
**状态：** 已实施并完成本地验证，等待线上部署确认
**设计依据：** `docs/superpowers/specs/2026-08-24-partylan-compatible-default-share-design.md`

## 任务一：为网页捕获和弹窗时序建立红灯

修改 `server/test_web_unified_sfu_integration.mjs`，扩展现有注入脚本 VM harness：

- 暴露 `buildDisplayMediaRequest`、`primeDisplayMediaRequestForShare` 和设置弹窗启动辅助函数的可测试入口；
- 模拟首选 `getDisplayMedia` 失败，断言可恢复错误只创建一次兼容请求，取消授权不创建第二次请求；
- 模拟设置弹窗确认，断言系统选择器 Promise 完成前不点击原始按钮，Promise 完成后只点击当前仍连接的按钮一次；
- 断言重复确认、按钮重绘和请求失败都会清理共享租约/轨道，不留下 pending capture。

先运行：

```powershell
node --test server/test_web_unified_sfu_integration.mjs
```

确认旧实现因没有回退入口和“立即 `.click()`”时序而失败。

## 任务二：统一客户端和网页端默认档位

修改：

- `frontend/src/components/RoomToolsDialog.tsx`
- `frontend/tests/roomVoiceControls.test.mjs`
- `server/screego/deploy/inject.js`
- `server/test_web_unified_sfu_integration.mjs`

实施：

- 客户端初始分辨率改为 `1280x720`，初始帧率改为 `30`；保留“原始画质”、1080p、45/60 FPS 选项；
- 网页端仅在没有已有用户设置时写入 `resolution=1280x720`、`framerate=30`；
- 不覆盖已有更高画质设置，不改变动态自适应策略和服务端质量阶梯。

## 任务三：实现浏览器窗口捕获兼容回退

修改：

- `server/screego/deploy/inject.js`
- 如副本存在差异，同步 `server/screego/deploy` 相关资源测试所需的版本断言

实施：

- 保留首选约束请求；
- 对 `TypeError`、`OverconstrainedError`、`NotReadableError` 仅尝试一次基础请求；
- 基础请求只使用 `video: true` 和按音频模式决定的 `audio: true/false`，不附加窗口/系统音频、surface、分辨率或帧率扩展约束；
- `NotAllowedError`、`AbortError`、`NotFoundError`、`InvalidStateError` 直接结束；
- 首选和回退请求的异常、轨道和共享租约均按现有生命周期清理；
- 回退仍进入现有音频安全过滤、SFU 编解码校验和 WebRTC 自适应发送参数。

## 任务四：修复设置弹窗启动竞态

修改 `server/screego/deploy/inject.js` 及其测试：

- 增加单次启动序号/进行中 Promise 防重入；
- 确认按钮在用户手势中创建捕获请求后关闭弹窗，但不立即点击旧按钮；
- 捕获 Promise 成功后重新查找当前有效的 Screego 开始按钮，设置旁路标记并只点击一次；
- 捕获失败或取消时释放租约、停止临时轨道、清除 pending 状态并展示错误；
- 页面重绘、按钮失效和重复确认不得触发第二个隐藏捕获请求。

## 任务五：验证与资源检查

运行：

```powershell
node --test server/test_webrtc_adaptive_policy.mjs server/test_webrtc_adaptive_integration.mjs server/test_web_unified_sfu_integration.mjs server/test_web_share_audio_policy.mjs server/test_web_share_audio_integration.mjs
node --test (Get-ChildItem server -Filter 'test_*.mjs' | Select-Object -ExpandProperty FullName)
python -m unittest discover -s server -p 'test_*.py'
node --test (Get-ChildItem frontend/tests -Filter '*.test.mjs' | Select-Object -ExpandProperty FullName)
npm run build --prefix frontend
go test ./... -count=1
go test -tags partylan_native_media ./... -count=1
```

最后执行脚本语法检查、`git diff --check`、策略副本摘要核对和敏感信息扫描。完成本地验证后停止，广州节点部署需用户另行确认。

## 执行记录

- 已先以红灯复现客户端默认值不一致、网页注入脚本缺少兼容捕获请求，以及设置弹窗在捕获 Promise 完成前立即点击旧按钮的问题。
- 客户端默认改为 `1280×720@30 FPS`；网页端继续只在没有用户保存设置时使用 `1280×720@30 FPS`，不覆盖既有高画质偏好。
- 网页端新增一次性兼容捕获：仅对 `TypeError`、`OverconstrainedError`、`NotReadableError` 重试 `video:true` 加按模式决定的 `audio`，取消授权等错误不重试。
- 设置弹窗改为等待捕获 Promise 完成后重新查找当前开始按钮并只点击一次；重复确认、页面重绘和失败路径均清理 pending 请求与共享租约。
- 缓存版本从 `inject.js?v=6.28` 升级为 `inject.js?v=6.29`；未修改自适应策略、SFU、TURN、Presence、语音隔离或客户端最小化窗口还原逻辑。
- 服务器 Node 测试 `143/143`、Python 测试 `38/38`、前端 Node 测试 `40/40`、前端生产构建、普通 Go 全仓测试和 `partylan_native_media` Go 全仓测试全部通过。
- Windows 客户端构建完成：`build/bin/PartyLAN.exe`，大小 `21,591,040` 字节，SHA-256 `D4B8C078209020CEE7C312280C8ACDE2466DB7EDA97DE18CBD51B1DAEFEAF5DB`。
- 广州节点尚未部署本轮 `inject.js?v=6.29` 和 Nginx 版本变更；部署需用户另行确认。
