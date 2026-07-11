# PartyLAN 网页共享音频来源隔离实施计划

> Based on `2026-07-11-partylan-web-share-audio-isolation-design.md`

## 1. 目标

在网页端为 `getDisplayMedia()` 返回流增加采集后音频安全校验：标签页模式只保留
`browser` 音频，全屏系统模式只保留 `monitor` 音频，普通窗口和未知来源始终静音。

## 2. 实施边界

- 不修改 PartyLAN 原生客户端音频捕获。
- 不修改 Screego 基础 bundle 或网页主题资源。
- 不使用 UA、浏览器版本或音轨名称进行来源推断。
- 共享租约和 Presence 上报实际生效的音频模式。
- 生产部署前必须备份网页注入文件和 Nginx 配置。

## 3. P1 失败测试与策略模块

新增独立 UMD 策略模块及 Node 测试，先覆盖并观察失败：

1. 标签页模式请求 `systemAudio=exclude`、`windowAudio=exclude`。
2. 全屏系统模式请求 `systemAudio=include`、`windowAudio=exclude`。
3. 标签页模式只允许 `browser` 音频。
4. 全屏系统模式只允许 `monitor` 音频。
5. `window`、未知来源和关闭模式停止并移除全部音频轨。
6. 清理结果返回 `tab`、`system` 或 `off` 实际模式。
7. 原始流无法可靠移除音频时，返回只含视频轨的新流。

建议文件：

- `server/web_share_audio_policy.js`
- `server/screego/deploy/web-share-audio.js`
- `server/test_web_share_audio_policy.mjs`

## 4. P2 网页采集接入

先扩展注入集成测试复现现有缺口，再修改：

- `server/screego/deploy/inject.js`
- `server/test_web_share_audio_integration.mjs`

实施内容：

1. `buildDisplayCapturePrefs()` 使用策略模块产生来源偏好。
2. `activateCapturedDisplayStream()` 在任何发布逻辑前清理流。
3. 预热采集和普通采集复用同一激活入口。
4. P2P/SFU 只接收策略返回的安全流。
5. 每次共享保存实际音频模式，共享结束时重置为 `off`。
6. 音频因来源不匹配被移除时显示一次非阻塞提示。
7. 标签和提示改为“标签页音频”“全屏系统音频”，不改变现有控件布局和 CSS 主题。

## 5. P3 控制面与诊断

扩展集成测试覆盖：

1. claim 仍可携带用户请求模式，因为此时选择器尚未返回。
2. 采集激活后的 room-state 与 heartbeat 使用实际模式。
3. 没有活动共享时模式为 `off`。
4. 被移除音频不会出现在实际发布流中。

如现有服务端白名单已接受 `audioMode`，不得扩大敏感字段范围。

## 6. P4 静态资源与回归

修改 Nginx 部署配置：

- 新增 `/web-share-audio.js` 静态资源。
- 在 `inject.js` 之前加载策略模块。
- 提升 `inject.js` 缓存版本。

运行：

- `node --check server/screego/deploy/inject.js`
- `node --check server/web_share_audio_policy.js`
- `node --test server/test_web_share_audio_*.mjs`
- `node --test server/test_webrtc_*.mjs`
- `python -m unittest discover -s server -p 'test_*.py'`
- `git diff --check`，仅检查本次文件

## 7. P5 部署与浏览器验收

1. 确认生产无活动房间。
2. 备份 `inject.js`、Nginx 配置和已有静态策略资源。
3. 上传并校验 SHA-256。
4. 执行远端 JavaScript 语法检查和 `nginx -t`。
5. 重载 Nginx。
6. 用真实 Chromium 检查策略脚本 200、加载顺序、策略版本和控制台错误。
7. 手工选择标签页、窗口和整个屏幕，分别验证实际音轨与观众听感。

## 8. STOP 条件

- 采集结果没有视频轨或 `displaySurface` 且无法按默认静音安全处理。
- 清理后的流无法被 Screego P2P 或 SFU 发布。
- 必须修改 Screego 基础 bundle 才能阻止原始音频流发布。
- 生产网页基础房间流程、主题或共享选择器发生回归。

## 9. 完成标准

- 普通窗口和未知来源不能发布任何音频轨。
- 标签页模式仅在 `browser` 保留音频。
- 全屏系统模式仅在 `monitor` 保留音频。
- 状态接口上报实际模式。
- 自动测试、生产静态加载和真实来源矩阵全部通过。
