# PartyLAN CPU 感知的网页自适应与恢复实施计划

**日期：** 2026-08-24
**状态：** 已实施并部署广州节点，等待线上实测反馈
**设计依据：** `docs/superpowers/specs/2026-08-24-partylan-cpu-aware-adaptive-recovery-design.md`

## 任务一：建立 CPU/网络分类红灯

修改：

- `server/test_webrtc_adaptive_policy.mjs`

新增测试样本和断言：

- 采集 `60 FPS`、编码/发送 `3 FPS`、接收 `3 FPS` 且 `qualityLimitationReason=cpu` 时，接收端状态不是 impaired；
- 连续 CPU 压力只产生 `downshift-cpu`，每次下降一级，并且不能准备 `downshift-fast`；
- CPU 压力经过冷却和新的连续样本后才能继续降一级；
- 发送 `60 FPS`、接收 `3 FPS` 且接收报告新鲜时仍分类为网络异常；
- 网络快速降档根据可持续带宽计算目标，但单次最多跨两级。

先运行：

```powershell
node --test server/test_webrtc_adaptive_policy.mjs
```

确认旧 `adaptive-per-peer-v5` 因 CPU 误判、旧决策类型和无跨度上限而失败。

## 任务二：实现压力分类和受控降档

同步修改并最终保持字节一致：

- `server/webrtc_adaptive_policy.js`
- `server/screego/deploy/webrtc-adaptive.js`

实施：

- 策略版本升级为 `adaptive-per-peer-v6`；
- 将接收评估中的发送崩帧从 `decodeImpaired` 移出；
- 新增 CPU、网络、健康和不可用压力分类；
- CPU 连续两个样本后用 `downshift-cpu` 逐级下降，不设置接收驱动的快速降档状态；
- 网络路径保留普通降档，并将快速目标限制为 `currentLevel + 2`；
- 状态记录最近降档压力类型，供恢复探测选择正确提交条件；
- path/session 复位时清空新增状态。

运行任务一测试，直到全部通过；再运行集成副本一致性测试：

```powershell
node --test server/test_webrtc_adaptive_policy.mjs server/test_webrtc_adaptive_integration.mjs
```

## 任务三：建立恢复与反馈冻结红灯

修改：

- `server/test_webrtc_adaptive_policy.mjs`
- `server/test_webrtc_adaptive_integration.mjs`

新增或改写测试：

- 健康接收反馈即使 FPS 和码率没有比探测基线提高 `15%`，两个新鲜样本后仍提交 probe；
- CPU 降档后的 probe 在 CPU 压力解除且两个样本健康后提交；CPU 压力持续两个样本时回滚；
- `page-hidden`、无效样本和接收报告过期不累计 probe 失败、不提交、不回滚；
- 重复接收报告不累计有效样本；
- 恢复新鲜反馈后，需要两个样本才能恢复决策；
- `sampleAdaptiveStreaming()` 在共享页面隐藏时不调用每连接采样器。

先运行目标测试，确认旧实现因固定改善比例、超时回滚和隐藏页面仍采样而失败。

## 任务四：实现健康证据恢复和后台冻结

修改：

- `server/webrtc_adaptive_policy.js`
- `server/screego/deploy/webrtc-adaptive.js`
- `server/screego/deploy/inject.js`

实施：

- 删除固定 `PROBE_IMPROVEMENT_RATIO` 和改善计数作为提交门槛；
- probe 只累计新鲜、有效、与降档原因相匹配的健康/异常样本；
- 缺失、过期、重复或无效接收反馈时保持 probe 状态，不按墙钟超时回滚；
- 网络 probe 以接收健康为提交依据；CPU probe 额外要求 CPU 压力解除；
- 保持两个明确异常样本回滚和递增冷却；
- 共享页面隐藏时冻结自适应采样，恢复后由新鲜反馈重新推进状态机。

运行：

```powershell
node --test server/test_webrtc_adaptive_policy.mjs server/test_webrtc_adaptive_integration.mjs
```

## 任务五：修复诊断负载和资源版本

修改：

- `server/test_webrtc_adaptive_integration.mjs`
- `server/test_webrtc_recovery_integration.mjs`
- `server/screego/deploy/inject.js`
- `server/screego/deploy/nginx-screego.conf`

先增加红灯断言：

- 单流估算使用当前 `adaptiveTargetKbpsValue` 和实际上下行最大值；
- 估算公式不再使用原始 `targetKbpsValue`；
- 加载资源精确为 `webrtc-adaptive.js?v=8` 和 `inject.js?v=6.28`。

然后修改注入脚本与 Nginx 配置，运行：

```powershell
node --test server/test_webrtc_adaptive_integration.mjs server/test_webrtc_recovery_integration.mjs server/test_web_unified_sfu_integration.mjs
```

## 任务六：完整回归与交付检查

运行服务器全部自动测试：

```powershell
$serverTests = Get-ChildItem server -Filter 'test_*.mjs' | Select-Object -ExpandProperty FullName
node --test $serverTests
python -m unittest discover -s server -p "test_*.py"
```

由于注入资源会被客户端内嵌网页加载，再运行前端和 Go 全仓回归：

```powershell
$frontendTests = Get-ChildItem frontend/tests -Filter '*.test.mjs' | Select-Object -ExpandProperty FullName
node --test $frontendTests
npm run build --prefix frontend
go test ./... -count=1
go test -tags partylan_native_media ./... -count=1
```

最后执行：

- `git diff --check`；
- 源策略与部署策略摘要对比；
- 搜索密钥、口令、私钥和临时日志路径；
- 审计最终 diff，确认没有客户端媒体、SFU、TURN、Presence 或无关生成文件改动。

本轮先完成代码和本地验证；取得用户部署确认后，再按下述记录增量部署广州节点。

## 实施记录

- 已用失败测试复现 CPU 崩帧被当作接收网络异常、快速降档从 `L1` 直达 `L6`、恢复依赖固定 `15%` 增幅、报告过期导致 probe 回滚、页面隐藏保留旧证据，以及负载诊断固定使用 `50Mbps` 的问题。
- 策略已升级为 `adaptive-per-peer-v6`。CPU 压力使用 `downshift-cpu` 逐级下降，网络快速降档单次最多跨两级，两条路径分别保存降档原因并使用对应恢复条件。
- probe 现在以两个新鲜健康报告提交，以两个同原因异常报告回滚；无效、过期、重复和后台反馈均冻结。暂停时保留最后报告 ID，确保恢复后旧报告不能冒充新证据。
- 页面隐藏会清空该页所有发送连接的累计降档和 probe 证据，但保留当前档位、probe 起点及原因。
- TURN/SFU 容量显示改用当前自适应档位上限和实时流量；缓存键升级为 `webrtc-adaptive.js?v=8`、`inject.js?v=6.28`。
- 服务器 Node 测试 `137/137`、Python 测试 `38/38`、前端 Node 测试 `40/40`、前端生产构建、普通 Go 全仓测试和 `partylan_native_media` Go 全仓测试全部通过。
- `inject.js` 和两份策略脚本语法检查、`git diff --check`、敏感信息扫描及策略副本摘要核对通过；两份策略 SHA-256 均为 `7BC6876444F5888A91A17BCC42995204C2F2A675860B8CA3DA692DB7879B7765`。
- 未修改客户端原生媒体、SFU、TURN、Presence 或控制面实现。

## 广州节点部署记录

- 部署提交：`a516d56 fix: 修复网页共享自适应误降档与恢复`。
- 部署时间：`2026-08-24T09:27:27+08:00`。
- 回滚备份：`/root/screego/backups/cpu-aware-adaptive-20260824T092528+0800`。
- 暂存目录：`/root/screego/staging/cpu-aware-adaptive-20260824T092528+0800`。
- 仅原子替换线上两份 `inject.js` 和两份 `webrtc-adaptive.js`，并在生产 Nginx 配置中将缓存版本升级为 `webrtc-adaptive.js?v=8`、`inject.js?v=6.28`；未覆盖整份线上 Nginx 配置。
- 正式域名返回的 `inject.js` SHA-256 为 `74E57897732C9F43651D59F7818BA3B20F435D5B01DAB135C01E8AD0F17F6972`，`webrtc-adaptive.js` SHA-256 为 `7BC6876444F5888A91A17BCC42995204C2F2A675860B8CA3DA692DB7879B7765`，与部署文件一致。
- Nginx 配置测试和无中断重载通过；Nginx 主进程、屏幕 SFU、语音 SFU、控制面及 Screego 的 PID 均未变化，屏幕与语音容器 ID 和镜像未变化。
- 部署后 `/api/rooms` 返回零个活动房间，屏幕 SFU 和语音 SFU 健康检查通过，最近 200 条 Nginx 访问记录无 5xx。
- 部署脚本的预检查因 `pipefail` 在正式替换前触发过两次自动恢复；当时没有活动房间，恢复发生在资源切换前。最终线上版本、资源摘要、配置摘要、服务状态和进程快照均已独立复核为目标状态。
