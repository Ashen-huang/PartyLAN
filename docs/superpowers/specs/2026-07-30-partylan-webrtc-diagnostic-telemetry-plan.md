# PartyLAN WebRTC 共享链路诊断遥测实施计划

> Based on `2026-07-30-partylan-webrtc-diagnostic-telemetry-design.md` and design commit `138a0b4`.

## 1. 目标

在不改变网页端质量、自适应、ICE 和共享行为的前提下，完成以下本地闭环：

- `buildWebRTCSample()` 输出采集、编码、发送、关键帧、双向 PLI/NACK、RTX 和发送队列诊断字段；
- 可选字段使用逐统计对象的累计值和差值，缺失时省略，单层重置不能被聚合增长掩盖；
- `inject.js` 将两个持久化窗口之间的基线变化可靠锁存到一次成功的 `/api/stats` 上报；
- `register.py` 白名单保留全部公开字段并继续拒绝敏感、未知和嵌套数据；
- 源文件与部署副本字节一致，Nginx 资源版本更新；
- 定向测试和相关服务端回归全部通过。

## 2. 实施边界

- 只修改已批准设计列出的 9 个本地文件。
- 不连接或修改生产服务器，不重载 Nginx、Screego 或 register service。
- 不打包客户端 EXE，不修改原生媒体链路。
- 不改变 `GameCastWebRTCQuality`、`GameCastWebRTCAdaptive`、ICE recovery 或 `/api/quality/report` 契约。
- 不新增采样定时器、统计接口、数据库、仪表盘或公开的 `diagnosticPacketsSentDelta` 字段。
- 不记录 candidate address、SDP、cookie、credential 或其他敏感数据。
- 不提交失败测试；每个任务在测试转绿后再形成提交。
- 全程仅按显式路径暂存，禁止 `git add -A`；未跟踪的 `build/bin.zip` 必须保持原样。

## 3. 当前基线

- 分支：`fix/web-first-room-connect`。
- 起始提交：`138a0b4 docs: 设计 WebRTC 诊断遥测补齐`。
- 现有定向基线：WebRTC stats 5/5、register control API 10/10、recovery integration 4/4。
- `server/webrtc_stats.js` 与 `server/screego/deploy/webrtc-stats.js` 当前字节一致。
- `server/register.py` 与 `server/screego/deploy/register.py` 当前字节一致。
- 唯一已知工作区外项：`?? build/bin.zip`，不得触碰。

## 4. Task 1：逐对象 WebRTC 诊断统计

### 4.1 先写失败测试

修改 `server/test_webrtc_stats.mjs`，先增加以下断言并确认当前实现失败：

1. 两次相隔 2 秒的样本输出全部公开累计值、窗口增量和 FPS。
2. `media-source.frames` 映射为 `framesCaptured`，多个 outbound 引用同一 `mediaSourceId` 时只计算一次。
3. 多个 outbound/inbound 的公开累计值求和；增量先按 report ID 求差再汇总。
4. `active: false` 层被诊断聚合排除；`active` 缺失以及仅有 `mediaType: 'video'` 的旧式报告仍参与。
5. 没有贡献对象、任一贡献对象缺失字段、值为 `NaN`/`Infinity` 或非法负数时，该指标及依赖项被省略，不制造 `0`。
6. 累计值为合法 `0` 时仍视为字段受支持。
7. outbound、inbound、media-source ID 集变化时，只省略对应组的诊断增量并生成固定 identity reason。
8. simulcast 单层计数器下降、但其他层增长使聚合总量仍上升时，该指标增量被省略并生成 `optional-counter-reset`。
9. 一个可选指标缺失或重置不阻断其他可比较指标。
10. `totalPacketSendDelay` 从秒转换为毫秒。
11. 平均发送排队延迟使用与延迟完全相同的 active outbound 集合，并分别按 report ID 对延迟和内部 `packetsSent` 求差。
12. 零发送包、单层包计数器重置、延迟缺失或 outbound identity 变化时省略平均值。
13. reset reason 去重并固定按 outbound、inbound、media-source、optional-reset 顺序输出。
14. 旧核心聚合算法的 `valid`、`invalidReason`、`packetsSentDelta` 结果保持不变。
15. 对外 `framesEncodedDelta` 和 `encodedFps` 使用诊断逐对象算法；它们省略时不改动旧核心有效性结果。
16. `buildViewerQualityReport()` 的输入和输出契约不包含新增字段。

建议使用至少两个 outbound、一个共享 media source 和一个 inbound 的固定 fixture，并明确计算预期值。平均延迟测试应包含两个层，使断言能证明它是包数加权平均，而不是各层平均值的简单平均。

运行并保存 red 证据：

```powershell
node --test server/test_webrtc_stats.mjs
```

失败必须来自缺少新字段或新 helper；若旧测试先失败，停止并查明基线漂移。

### 4.2 最小实现

修改 `server/webrtc_stats.js`，保留现有 `number()`、`totals()`、核心差值顺序和质量有效性门槛。新增诊断层可使用下列等价小 helper，名称可按现有风格调整：

```text
finiteCounter(value) -> number | undefined
isDiagnosticVideoRtp(report, type) -> boolean
sameIds(left, right) -> boolean
collectMetric(reports, field, scale) -> { total, byId } | undefined
deltaMetric(current, previous) -> { available, reset, value? }
collectDiagnosticSnapshot(stats) -> DiagnosticSnapshot
applyDiagnosticTelemetry(sample, current, previous, elapsedMs)
```

`finiteCounter()` 只接受有限、非负数；合法的 `0` 必须保留。不得通过 `number()` 或 `|| 0` 读取新增可选计数器。

诊断快照放在 `snapshot.diagnostic`，最小结构为：

```text
diagnostic: {
  outboundIds: string[],
  inboundIds: string[],
  mediaSourceIds: string[],
  outbound: {
    framesEncoded?, framesSent?, keyFramesEncoded?,
    pliCount?, nackCount?,
    retransmittedPacketsSent?, retransmittedBytesSent?,
    totalPacketSendDelayMs?, packetsSent?
  },
  inbound: { pliCount?, nackCount? },
  mediaSource: { framesCaptured? }
}

Metric: { total: number, byId: { [id]: number } }
```

实现顺序固定为：

1. 现有 `totals(stats)` 先生成核心旧总量，过滤条件不变。
2. 诊断层收集 `active !== false` 的视频 outbound/inbound，并按 ID 排序。
3. outbound 的每个可选字段必须由全部诊断对象提供；inbound 同理。
4. 所有 outbound 都必须能通过 `mediaSourceId` 解析到视频 `media-source.frames`，之后按 source ID 去重。
5. `totalPacketSendDelay` 进入映射时乘以 `1000` 转成毫秒。
6. 当前完整累计值先写入 sample；不支持的字段不创建属性。
7. 无上一诊断快照时只建立基线，不生成 reset reason。
8. 先比较三组 ID；身份变化时记录对应 reason，并省略该组窗口增量。
9. 身份相同时逐对象求差；任一对象下降只令该指标不可用，并增加 `optional-counter-reset`。
10. FPS 只在 `elapsedMs > 0` 且对应帧增量可用时生成。
11. 内部诊断包增量只服务于 `averagePacketSendDelayMs`，不得写入 sample、请求或 JSONL。
12. 新诊断字段和 reason 绝不修改 `sample.valid` 或 `sample.invalidReason`。

`framesEncoded` 必须保持双轨：

- `snapshot.framesEncoded` 继续保存旧核心总量，参与现有 `counter-reset` 和 `valid` 判定；
- 对外累计 `sample.framesEncoded`、`sample.framesEncodedDelta` 和 `sample.encodedFps` 使用诊断对象集合及逐对象算法；
- 旧核心总量下降时仍按原规则判 `counter-reset`，聚合未下降时有效性也保持旧结果。

### 4.3 同步与验证

单测转绿后，将同一实现机械同步到 `server/screego/deploy/webrtc-stats.js`，不要分别演进两份逻辑。为测试增加源文件与部署副本字节一致断言。

```powershell
node --test server/test_webrtc_stats.mjs
node --check server/webrtc_stats.js
node --check server/screego/deploy/webrtc-stats.js
git diff --check -- server/webrtc_stats.js server/screego/deploy/webrtc-stats.js server/test_webrtc_stats.mjs
```

绿色提交边界：

- `server/webrtc_stats.js`
- `server/screego/deploy/webrtc-stats.js`
- `server/test_webrtc_stats.mjs`

建议提交：`feat: 补齐 WebRTC 逐对象诊断统计`

## 5. Task 2：reset reason 锁存与网页上报

### 5.1 先写失败测试

在 `server/test_webrtc_stats.mjs` 为 UMD API 增加小型版本化 latch 及异步发送 helper 测试：

1. 多个 reason 去重并按固定顺序形成上报文本。
2. 成功确认只清除本次请求实际携带且版本未变化的 token。
3. 注入的发送函数返回 false、reject 或同步抛错时保留全部 token，异步 helper 返回 false 且不产生未处理 rejection。
4. 请求飞行期间加入不同 reason，旧请求成功不能清除新 token。
5. 请求飞行期间再次加入同名 reason，旧请求成功也不能清除新版本。
6. 清空后再次出现 reason 能正常进入下一批上报。

使用可控 deferred Promise 明确排列“helper 截取发送快照、产生新 reason、旧请求完成”的真实时序。测试必须调用生产导出的异步 helper，不能在测试中重写确认流程。HTTP 到 success boolean 的映射留给下方 `inject.js` 发送适配器契约。

在 `server/test_webrtc_recovery_integration.mjs` 增加失败契约断言：

- 每个 PeerConnection 独立持有 latch 状态；
- 每次 2 秒样本出现 `telemetryResetReason` 时立即记录到 latch；
- 第 5 个窗口使用 latch 快照构造 `reportPayload.telemetryResetReason`；
- `reportStats()` 返回 `Promise<boolean>`，只把 `response.ok` 视为成功；
- 网络失败和非 2xx 返回 false，且不向外抛出未处理 rejection；
- `inject.js` 将 latch、payload 和 `reportStats` 直接传给生产异步 helper，不自行复制确认流程；
- 显式复制列表包含全部公开字段，但不包含内部诊断包增量；
- 新增字段未进入 viewer quality report、质量动作或自适应输入。

运行 red：

```powershell
node --test server/test_webrtc_stats.mjs server/test_webrtc_recovery_integration.mjs
```

### 5.2 最小实现

在 `server/webrtc_stats.js` 的现有 UMD API 中增加版本化 reason latch helper 和 `sendTelemetryReport(latch, payload, send)` 等价异步 helper，不新增文件或采集器。建议状态为：

```text
{
  nextVersion: number,
  reasons: { [knownToken]: number }
}
```

每次记录 reason 都递增版本并覆盖对应 token 的版本号。发送前生成带各 token 版本的不可变快照；成功确认时，只删除当前版本仍等于已发送版本的 token。只接受设计规定的四个 token，防止任意文本进入状态。

异步 helper 是唯一确认编排：它截取 latch 快照，把排序后的 reason 写入 payload，调用注入的 `send(payload)`，并把同步抛错、reject 或 false 统一归一为失败。只有 true 才按已发送 token 版本确认；它返回 `Promise<boolean>` 供调用方观察。这样单元测试执行的就是生产使用的竞态逻辑。

同步 helper 到部署版 `webrtc-stats.js`。在 `inject.js` 中：

1. sample 生成后，将瞬时 `telemetryResetReason` 合并到 `pc` 的 latch。
2. 约每 10 秒上报时，将 latch、payload 和 `reportStats` 传给 UMD 异步 helper，由 helper 截取批次和写入 reason。
3. `reportStats(data)` 作为发送适配器返回 Promise；2xx 返回 true，非 2xx 和网络失败返回 false。
4. UMD 异步 helper 负责同步异常、reject、失败保留、成功确认和在途版本保护；`inject.js` 不再实现第二份 ack 逻辑。
5. 在途请求成功不得清除请求开始后新增或更新版本的 token。
6. 保持现有上报频率、其他 payload 字段和自适应 decision 清理语义不变。

逻辑上需要转发的字段完整列表如下。数值字段由现有显式复制列表处理；`telemetryResetReason` 必须以 latch 快照为准单独写入，不能被当前瞬时 sample 覆盖：

```text
framesCaptured, framesCapturedDelta, capturedFps,
framesEncoded, framesEncodedDelta, encodedFps,
framesSent, framesSentDelta, sentFps,
keyFramesEncoded, keyFramesEncodedDelta,
outboundPliCount, outboundPliCountDelta,
outboundNackCount, outboundNackCountDelta,
inboundPliCount, inboundPliCountDelta,
inboundNackCount, inboundNackCountDelta,
retransmittedPacketsSent, retransmittedPacketsSentDelta,
retransmittedBytesSent, retransmittedBytesSentDelta,
totalPacketSendDelayMs, totalPacketSendDelayDeltaMs,
averagePacketSendDelayMs, telemetryResetReason
```

### 5.3 验证

```powershell
node --test server/test_webrtc_stats.mjs server/test_webrtc_recovery_integration.mjs
node --check server/screego/deploy/inject.js
git diff --check -- server/webrtc_stats.js server/screego/deploy/webrtc-stats.js server/screego/deploy/inject.js server/test_webrtc_stats.mjs server/test_webrtc_recovery_integration.mjs
```

绿色提交边界：

- `server/webrtc_stats.js`
- `server/screego/deploy/webrtc-stats.js`
- `server/screego/deploy/inject.js`
- `server/test_webrtc_stats.mjs`
- `server/test_webrtc_recovery_integration.mjs`

建议提交：`feat: 可靠上报 WebRTC 遥测重置状态`

## 6. Task 3：服务端白名单与 JSONL 落盘

### 6.1 先写失败测试

扩展 `server/test_register_control_api.py`：

1. 构造包含全部公开数值字段、`telemetryResetReason` 和现有旧字段的 payload。
2. 断言新增及旧字段保持原数值或字符串，不被重命名或转型。
3. 断言 candidate address、credential、SDP、嵌套对象、未知字段和内部 `diagnosticPacketsSentDelta` 被丢弃。
4. 临时替换 `STATS_DIR`，调用 `append_stats()`，读回单条 JSONL。
5. 断言公开字段、reason 和 `_ts` 落盘，敏感及内部字段不存在。
6. 增加 `server/register.py` 与部署副本的字节一致性断言。

在 `server/test_webrtc_recovery_integration.mjs` 同时断言两组源/部署文件字节一致，并确认部署版 register 白名单包含全部公开字段。

运行 red：

```powershell
python -m unittest discover -s server -p "test_register_control_api.py"
node --test server/test_webrtc_recovery_integration.mjs
```

### 6.2 最小实现

只扩展 `STATS_STRING_FIELDS` 和 `STATS_NUMBER_FIELDS`：

- `telemetryResetReason` 加入字符串白名单；
- Task 2 列出的公开数值字段加入数值白名单；
- 已存在的 `framesEncodedDelta` 不重复改变语义；
- 不加入 `diagnosticPacketsSentDelta`；
- 不修改 `sanitize_stats_payload()`、`append_stats()`、接口路由、JSONL 文件名或 `_ts`。

先修改 `server/register.py`，测试转绿后机械同步到 `server/screego/deploy/register.py`。

### 6.3 验证

```powershell
python -m unittest discover -s server -p "test_register_control_api.py"
node --test server/test_webrtc_recovery_integration.mjs
git diff --check -- server/register.py server/screego/deploy/register.py server/test_register_control_api.py server/test_webrtc_recovery_integration.mjs
```

绿色提交边界：

- `server/register.py`
- `server/screego/deploy/register.py`
- `server/test_register_control_api.py`
- `server/test_webrtc_recovery_integration.mjs`

建议提交：`feat: 扩展 WebRTC 遥测落盘字段`

## 7. Task 4：缓存版本与完整回归

### 7.1 先写失败测试

在 `server/test_webrtc_recovery_integration.mjs` 更新资源版本契约：

- `webrtc-stats.js?v=4`；
- `inject.js?v=6.19`；
- `webrtc-quality.js?v=2`、`webrtc-adaptive.js?v=2`、`web-share-audio.js?v=1` 和 `sfu-client.js?v=1.8` 保持不变；
- 脚本加载顺序保持 stats、quality、adaptive、audio、inject、SFU。

先运行测试，当前 `v=3` 和 `v=6.18` 必须使新断言失败。

```powershell
node --test server/test_webrtc_recovery_integration.mjs
```

### 7.2 最小实现

仅修改 `server/screego/deploy/nginx-screego.conf` 的两个查询版本：

```text
webrtc-stats.js?v=3   -> webrtc-stats.js?v=4
inject.js?v=6.18      -> inject.js?v=6.19
```

不得改动其他资源版本、Nginx 路由或缓存头。

### 7.3 定向与完整验证

先运行定向测试：

```powershell
node --test server/test_webrtc_stats.mjs
node --test server/test_webrtc_recovery_integration.mjs
python -m unittest discover -s server -p "test_register_control_api.py"
node --check server/webrtc_stats.js
node --check server/screego/deploy/webrtc-stats.js
node --check server/screego/deploy/inject.js
```

再运行相关服务端回归：

```powershell
node --test server/test_webrtc_*.mjs server/test_web_*.mjs
python -m unittest discover -s server -p "test_*.py"
git diff --check
```

最后审计：

1. `server/webrtc_stats.js` 与部署副本字节一致。
2. `server/register.py` 与部署副本字节一致。
3. 仅 9 个批准文件发生业务改动。
4. `build/bin.zip` 仍为未跟踪且未被暂存。
5. `buildViewerQualityReport()`、quality/adaptive policy 和 ICE recovery 文件没有行为改动。
6. 没有候选地址、SDP、credential 或嵌套对象进入 JSONL。

绿色提交边界：

- `server/screego/deploy/nginx-screego.conf`
- `server/test_webrtc_recovery_integration.mjs`

建议提交：`chore: 更新 WebRTC 遥测资源版本`

完整回归若暴露批准范围外的既有失败，停止并报告，不把无关修复混入本任务。

## 8. STOP 条件

- 需要改变现有质量、自适应、ICE 或共享策略才能生成新增字段。
- 必须新增第二套采样器、接口或持久化结构。
- 浏览器可选字段缺失无法与合法零值区分。
- 逐对象诊断算法无法保持旧 `valid/invalidReason` 结果。
- `framesEncodedDelta` 的诊断输出仍会被单层重置掩盖。
- 平均排队延迟的分子与分母无法保证来自同一对象集合。
- reset reason 在失败上报或在途请求竞态中仍可能丢失。
- 源文件与部署副本无法保持字节一致。
- 实现需要修改批准列表以外的文件。
- 定向测试、相关质量/恢复回归或敏感字段拒绝测试失败。

触发 STOP 条件时保留失败证据并报告，不扩大范围猜测修复。

## 9. 完成标准

- 设计列出的公开字段全部进入浏览器样本、约 10 秒上报和 JSONL。
- 可选字段缺失不制造零值，合法零值仍保留。
- 多层 RTP 指标逐对象求差，单层重置不会产生伪聚合增量。
- media source 正确关联并去重，采集/编码/发送 FPS 语义明确。
- PLI/NACK 方向、RTX 数量和发送队列延迟单位正确。
- 平均排队延迟使用同一 outbound 集合的内部包增量，零包时省略。
- reset reason 跨未上报窗口、失败请求和在途竞态均不丢失。
- 旧 `valid/invalidReason`、viewer quality report、自适应和恢复行为不变。
- 两组源/部署副本字节一致，资源缓存版本正确。
- 定向测试、相关 Node/Python 回归和 `git diff --check` 全部通过。
- 本地实现没有改变生产服务器状态，也没有触碰 `build/bin.zip`。

## 10. 执行授权

本计划文档完成并提交后等待用户确认。获得明确实施授权前，不修改上述 9 个业务或测试文件。
