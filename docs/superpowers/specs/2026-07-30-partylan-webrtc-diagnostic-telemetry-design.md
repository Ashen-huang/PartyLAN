# PartyLAN WebRTC 共享链路诊断遥测设计

> Scope: 在现有网页端 WebRTC 统计链路中补齐采集、编码、发送、关键帧、PLI/NACK、RTX 和发送队列延迟指标，用于定位跨网或中继场景的极低帧率与黑屏问题。本设计只增加诊断证据，不调整画质、自适应、ICE 或共享策略。

## 1. 背景与结论

现有 `buildWebRTCSample()` 已能记录候选路径、RTT、抖动、丢包、码率、解码帧和编码帧增量，但无法区分以下故障阶段：

- 共享源没有持续产帧；
- 浏览器采集正常，但编码器没有持续编码；
- 编码正常，但 RTP 发送帧率极低；
- 接收端持续请求关键帧或重传；
- 发送端内部排队时间持续增加。

因此，本轮扩展现有采集器和现有 `/api/stats` JSONL 链路，不建立第二套统计系统。内部仍约每 2 秒采样一次，网页仍约每 5 个样本、即约 10 秒上报一次。日志同时保存累计值、最近采样窗口增量和派生值，使单条日志可直接观察；相邻持久化记录之间没有基线变化标记时，还可用累计计数器重新计算更长窗口。

## 2. 目标

- 补齐 `framesCaptured`、`framesSent`、`keyFramesEncoded`、PLI/NACK、RTX 重传量和发送队列延迟。
- 同时记录浏览器累计计数器、约 2 秒采样窗口增量以及 FPS 或平均延迟等派生值。
- 保留 `framesCaptured -> framesEncoded -> framesSent` 的阶段性证据，区分采集、编码和发送瓶颈。
- 明确发送端收到的反馈与接收端发出的反馈，避免 PLI/NACK 方向歧义。
- 正确处理浏览器不支持字段、流身份变化和单个可选计数器重置。
- 保持根目录测试版与 Screego 部署版文件一致，并通过缓存版本更新确保以后部署时浏览器可取得新脚本。

## 3. 非目标

- 不改变分辨率、帧率、码率、编码器、simulcast、TURN、ICE restart 或 PeerConnection 重建策略。
- 不让新增指标参与 `GameCastWebRTCQuality` 或现有自适应状态机的判定。
- 不修改客户端原生采集、原生编码或原生观看链路。
- 不新增统计接口、数据库、仪表盘或第二套采样定时器。
- 不在本轮连接生产服务器，不替换生产文件，也不重载 Nginx 或服务进程。
- 不记录候选地址、SDP、cookie、认证信息、TURN credential 或其他敏感数据。

## 4. 架构与文件边界

### 4.1 浏览器采集与计算

`server/webrtc_stats.js` 继续作为可单测的源文件，`server/screego/deploy/webrtc-stats.js` 作为部署副本。两份文件必须保持字节一致。

现有 `buildWebRTCSample(stats, previous, context)` 保持唯一入口：

1. 从本次 `RTCStatsReport` 按统计对象保存累计值，再形成聚合累计值；
2. 将累计值、逐对象计数器及内部身份写入 `snapshot`；
3. 在当前对象与上一快照可比较时逐对象求差，再汇总窗口增量；
4. 根据 `elapsedMs` 计算 FPS 和平均发送排队延迟；
5. 保持现有质量样本的有效性判定不变。

不新建并行采集器，也不改变 `inject.js` 约 2 秒调用一次的节奏。

### 4.2 网页上报

`server/screego/deploy/inject.js` 继续约每 5 个采样窗口构造一次 `reportPayload`。新增字段加入现有显式复制列表，字段不存在时不写入请求。

这里上报的 `*Delta` 和 FPS 是最近一次约 2 秒采样窗口的数据，不是约 10 秒上报间隔的聚合值。累计值允许日志分析程序通过两条持久化记录和 `_ts` 重新计算约 10 秒或更长窗口，但仅限后记录没有携带 `telemetryResetReason` 的对应指标。

`inject.js` 将两个持久化上报之间出现的 reset reason 合并并锁存在 PeerConnection 状态中。`reportStats()` 返回可观察的请求结果；只有 `/api/stats` 成功响应后才清除本次已经发送的原因，失败时留待下一次上报。若请求飞行期间又出现新原因，不得由旧请求的成功回调清除。这样，持久化记录中的 `telemetryResetReason` 表示“从上一条成功记录到本条记录之间发生过基线变化”，不会因变化恰好落在未上报的四个采样窗口中而丢失。

### 4.3 服务端落盘

`server/register.py` 与 `server/screego/deploy/register.py` 继续使用扁平字段白名单清洗 `/api/stats` 请求，并逐行写入 `stats_YYYY-MM-DD.jsonl`。两份文件必须保持字节一致。

新增数值字段进入 `STATS_NUMBER_FIELDS`，`telemetryResetReason` 进入 `STATS_STRING_FIELDS`。未知、嵌套或敏感字段仍被丢弃；JSONL 结构和 `_ts` 语义不变。

### 4.4 浏览器缓存版本

本地部署配置将 `webrtc-stats.js` 从 `v=3` 提升到 `v=4`，并将包含新增转发字段的 `inject.js` 从 `v=6.18` 提升到 `v=6.19`。其他资源版本不变。版本更新只进入仓库，不代表已部署到生产。

## 5. 字段契约

参与本轮新增诊断聚合的对象使用确定谓词：`type` 为 `outbound-rtp` 或 `inbound-rtp`，`kind === 'video'` 或 `mediaType === 'video'`，并且 `active !== false`。明确标记 `active: false` 的暂停层不参与；`active` 缺失时为兼容旧浏览器仍参与。不使用本窗口包数或字节数是否增长来猜测活动状态。对象从报告中消失、重新出现或 ID 变化均视为相应方向身份变化。现有核心累计值仍使用当前过滤方式，不增加 `active` 条件。

### 5.1 采集、编码与发送

| 字段 | 类型 | 来源与语义 |
|---|---:|---|
| `framesCaptured` | number | 被当前视频 outbound 引用的 `media-source.frames` 累计值，按 `mediaSourceId` 去重 |
| `framesCapturedDelta` | number | 当前与上一可比较快照的采集帧差值 |
| `capturedFps` | number | `framesCapturedDelta * 1000 / elapsedMs` |
| `framesEncoded` | number | 所有活动视频 `outbound-rtp.framesEncoded` 的累计和，保留现有字段名 |
| `framesEncodedDelta` | number | 编码帧累计和的窗口差值，保留现有字段名 |
| `encodedFps` | number | `framesEncodedDelta * 1000 / elapsedMs` |
| `framesSent` | number | 所有活动视频 `outbound-rtp.framesSent` 的累计和 |
| `framesSentDelta` | number | 发送帧累计和的窗口差值 |
| `sentFps` | number | `framesSentDelta * 1000 / elapsedMs` |
| `keyFramesEncoded` | number | 所有活动视频 `outbound-rtp.keyFramesEncoded` 的累计和 |
| `keyFramesEncodedDelta` | number | 关键帧累计和的窗口差值 |

同一个 `media-source` 可能被多个 outbound 层引用，`framesCaptured` 只计一次。只有至少一个参与聚合的 outbound，且每个参与对象的 `mediaSourceId` 都能解析到提供有限 `frames` 的视频 `media-source` 时，才输出采集字段。`framesEncoded` 和 `framesSent` 按所有 outbound 层求和，因此在 simulcast 场景中 `encodedFps`、`sentFps` 是各层总处理率，不能当成单一画面的展示帧率；`capturedFps` 才代表共享源产帧率。

### 5.2 PLI/NACK 方向

| 字段组 | 来源与语义 |
|---|---|
| `outboundPliCount`, `outboundPliCountDelta` | 视频 `outbound-rtp.pliCount`；发送端收到的 PLI |
| `outboundNackCount`, `outboundNackCountDelta` | 视频 `outbound-rtp.nackCount`；发送端收到的 NACK |
| `inboundPliCount`, `inboundPliCountDelta` | 视频 `inbound-rtp.pliCount`；接收端发出的 PLI |
| `inboundNackCount`, `inboundNackCountDelta` | 视频 `inbound-rtp.nackCount`；接收端发出的 NACK |

所有同方向视频 RTP 报告均求和。PLI/NACK 是反馈消息次数，不等同于请求或实际重传的数据包数，分析时必须与 RTX 字段并列观察。

### 5.3 RTX 与发送队列

| 字段 | 类型 | 来源与语义 |
|---|---:|---|
| `retransmittedPacketsSent` | number | 视频 `outbound-rtp.retransmittedPacketsSent` 累计和 |
| `retransmittedPacketsSentDelta` | number | 重传包窗口增量 |
| `retransmittedBytesSent` | number | 视频 `outbound-rtp.retransmittedBytesSent` 累计和 |
| `retransmittedBytesSentDelta` | number | 重传字节窗口增量 |
| `totalPacketSendDelayMs` | number | 视频 `outbound-rtp.totalPacketSendDelay` 累计和，由秒转换为毫秒 |
| `totalPacketSendDelayDeltaMs` | number | 累计发送延迟的窗口差值，单位毫秒 |
| `averagePacketSendDelayMs` | number | `totalPacketSendDelayDeltaMs / diagnosticPacketsSentDelta`；分母仅在内部使用，不新增持久化字段 |

`averagePacketSendDelayMs` 表示本窗口内每个已发送 RTP 包的平均发送排队延迟，不是网络 RTT。内部 `diagnosticPacketsSentDelta` 必须使用与 `totalPacketSendDelayDeltaMs` 完全相同的诊断 outbound 对象集合，并按 report ID 对 `packetsSent` 逐对象求差。只有延迟和包数在前后快照都完整、身份相同、各对象差值非负且内部包增量大于零时才输出平均值；零发送包或任一对象重置时省略。对外已有 `packetsSentDelta` 保持旧语义，不作为该平均值分母。

## 6. 可选字段、聚合与基线规则

新增 RTCStats 字段全部按可选能力处理：

- 只有至少存在一个对应的活动统计对象，且这些对象都提供有限数值时，才输出对应聚合累计值；对象不存在或字段缺失时省略字段，不能用 `0` 冒充浏览器支持。
- 所有本轮新增的持久化累计值都保存字段存在性；这也包括首次对外输出的 `framesEncoded` 累计值。现有内部 `framesEncoded` 总量继续保留旧兼容语义，避免改变核心样本判定。
- 只有当前快照和上一快照都存在对应逐对象累计值，且该指标所属的对象 ID 集合相同时，才输出增量及其派生值。
- 新增字段不改变现有 `number()` 对核心旧字段的兼容语义。
- 除现有核心 `framesEncoded` 的旧判定外，单个新增计数器下降时只省略该字段的增量和依赖它的派生值；累计值仍可输出，现有样本不得因此变为 `counter-reset` 或 `valid: false`。
- 一个新增字段的异常不得阻止其他新增字段生成有效增量。

快照额外保存仅供本地比较的稳定身份和逐对象计数器：排序后的活动视频 outbound report ID、inbound report ID、去重后的 `mediaSourceId`，以及以这些 ID 为键的可选计数器映射。用于排队延迟分母的逐 outbound `packetsSent` 也保存在诊断映射中。这些内部数据不进入 `/api/stats`。

增量必须先按相同 report ID 或 `mediaSourceId` 逐对象相减，再把非负差值求和，不能先对累计值求和后再相减。只要某一贡献对象的该计数器下降，就省略该聚合增量及派生值并记录 `optional-counter-reset`；这避免 simulcast 中一个层的重置被另一个层的增长掩盖。其他指标仍独立计算。

身份或可选计数器基线变化时，可选字符串 `telemetryResetReason` 使用逗号分隔的稳定原因标记：

- `outbound-identity-changed`；
- `inbound-identity-changed`；
- `media-source-identity-changed`；
- `optional-counter-reset`。

存在多个原因时去重并按上述顺序拼接。受影响字段在该窗口省略增量；不受影响字段仍正常计算。该原因按 4.2 节规则锁存到下一次成功上报。离线重算时，三个身份标记分别使对应 outbound、inbound 或 media-source 指标不可跨该记录做差；`optional-counter-reset` 不携带字段名，因此保守地使该区间内所有本轮新增累计指标都不可跨记录做差。`telemetryResetReason` 只解释新增诊断指标的基线变化，不替代现有 `invalidReason`，也不参与质量策略。

`framesEncoded` 需要同时满足兼容和诊断两种用途：现有内部核心聚合总量继续包含所有匹配的视频 outbound，不增加 `active` 过滤，并保持它对 `valid`/`invalidReason` 的判断；对外累计值、`framesEncodedDelta` 和新 `encodedFps` 则使用本轮诊断对象及逐 outbound 对象差值。outbound 身份变化或单层计数器下降时，诊断增量省略；现有核心聚合总量若下降，仍按旧规则得到 `invalidReason: 'counter-reset'`，若未下降则保持原有有效性结果。这是有意保留的质量策略兼容边界。

## 7. 数据流与诊断解释

```text
RTCStatsReport
  -> buildWebRTCSample() 汇总累计值和身份
  -> 与约 2 秒前 snapshot 比较
  -> 生成增量、FPS、平均发送排队延迟
  -> inject.js 每约 10 秒选取当前样本上报
  -> register.py 白名单清洗
  -> stats_YYYY-MM-DD.jsonl
```

日志分析优先按以下阶段判断：

1. `capturedFps` 持续极低：共享源或浏览器采集阶段受阻；
2. `capturedFps` 正常而聚合 `encodedFps` 持续为零或接近零：全部 outbound 的编码阶段整体受阻；
3. 聚合 `encodedFps` 非零而聚合 `sentFps` 持续为零或接近零，并伴随排队延迟升高：全部 outbound 的发送阶段整体受阻；
4. 发送帧正常但 PLI/NACK、RTX 持续增长：链路损伤或接收恢复压力较高；
5. 发送侧正常而观看端解码帧极低：继续结合现有接收侧 `framesDecodedDelta`、丢包、抖动和码率判断。

这些解释只用于离线或人工诊断，代码不据此自动调档或重连。simulcast 下不能直接比较 `capturedFps` 与聚合 `encodedFps`/`sentFps` 的数值比例；聚合值非零也不能证明每一个层都正常，只能判断全部层是否整体停滞。

## 8. 错误处理与兼容性

- 旧浏览器完全不提供新增字段时，旧字段、样本有效性和上报行为保持不变。
- 部分支持时只输出能够可靠聚合的字段，不制造零值。
- 页面隐藏、源变化、分辨率变化、候选对变化、ICE generation 变化、核心计数器重置和包数不足继续沿用现有 `invalidReason`。
- outbound、inbound 或 media-source 身份变化只重建相应诊断指标的比较基线；`valid`/`invalidReason` 对既有核心累计值的判定严格保持旧语义。
- 服务端清洗器只接受声明的扁平字符串、数值和布尔值，保持敏感字段拒绝边界。
- 新增字段不得被复制到 viewer quality report，也不得改变 `/api/quality/report` 契约。

## 9. 测试策略

### 9.1 WebRTC 统计单元测试

扩展 `server/test_webrtc_stats.mjs`，覆盖：

- 两次可比较采样产生全部累计值、窗口增量和派生值；
- 多个 outbound/inbound 报告正确求和；
- 多个 outbound 引用同一 `mediaSourceId` 时采集帧只计算一次；
- `active: false` 的暂停层被排除，`active` 缺失以及仅有 `mediaType: 'video'` 的旧式报告仍参与；
- 一个旧 report 消失或 ID 变化时，相应方向的增量被省略并记录身份变化；
- simulcast 单层计数器下降但聚合总量仍增长时，不生成伪增量；
- `totalPacketSendDelay` 从秒转换为毫秒，平均值使用本窗口发送包数；
- 平均值分母与延迟使用相同诊断 outbound 集合及逐对象差值，`active: false` 层不进入任一侧；
- 某一层 `packetsSent` 重置但聚合包数仍增长时省略平均值，不使用旧 `packetsSentDelta` 生成伪结果；
- 零发送包时省略 `averagePacketSendDelayMs`；
- 浏览器不支持某字段时累计值、增量和派生值均省略；
- 除现有核心 `framesEncoded` 旧规则外，单个可选计数器下降时核心样本有效性不变，并记录 `telemetryResetReason`；
- `framesEncoded` 诊断层单对象下降时省略诊断增量，同时断言 `valid`/`invalidReason` 仍严格等于旧聚合算法结果；
- outbound、inbound 或 media-source 身份变化只重建相关基线；
- 现有候选对变化、核心计数器重置和 viewer quality report 测试继续通过。

### 9.2 服务端与集成测试

扩展 `server/test_register_control_api.py` 和 `server/test_webrtc_recovery_integration.mjs`，覆盖：

- 所有新增数值字段和 `telemetryResetReason` 通过白名单并可写入 JSONL；
- 候选地址、credential、SDP、嵌套对象和未知字段仍被丢弃；
- `inject.js` 的显式复制列表包含全部新增字段；
- reset reason 出现在第 1 至第 4 个未持久化窗口时会锁存到第 5 个窗口；上报失败不清除，新原因不被在途旧请求的成功回调清除；
- `optional-counter-reset` 在跨日志分析中阻止所有新增累计指标做差，三个 identity token 只阻止对应指标组；
- 根目录与部署目录的 `webrtc_stats.js`、`register.py` 分别保持字节一致；
- Nginx 使用 `webrtc-stats.js?v=4` 和 `inject.js?v=6.19`，其他资源版本不变；
- 现有质量、自适应和恢复集成断言继续通过，证明新增遥测没有进入策略路径。

### 9.3 本地回归

实施阶段至少运行：

```powershell
node --test server/test_webrtc_stats.mjs
python -m unittest discover -s server -p "test_register_control_api.py"
node --test server/test_webrtc_recovery_integration.mjs
```

随后按仓库现有服务端测试入口运行相关 Node/Python 回归，并执行 `git diff --check`。生产日志验证、真实全屏共享和跨运营商弱网复测属于后续部署验证，不以本地自动测试替代。

## 10. 实施范围

本设计批准后的实施只允许修改以下本地文件：

- `server/webrtc_stats.js`；
- `server/screego/deploy/webrtc-stats.js`；
- `server/screego/deploy/inject.js`；
- `server/register.py`；
- `server/screego/deploy/register.py`；
- `server/screego/deploy/nginx-screego.conf`；
- `server/test_webrtc_stats.mjs`；
- `server/test_register_control_api.py`；
- `server/test_webrtc_recovery_integration.mjs`。

若实现必须超出该列表，应先更新计划并说明原因。生产部署、服务重载、客户端 EXE 重新打包、提交业务代码和推送远端均需另行明确授权。

## 11. 完成标准

- 约 2 秒样本能够区分采集、编码和发送帧率。
- JSONL 同时保存已支持指标的累计值、窗口增量和派生值。
- PLI/NACK 方向、RTX 数量与发送排队延迟语义明确且单位正确。
- 可选字段缺失不伪造零值，单个新增计数器重置不破坏现有质量样本。
- 多层 outbound 正确求和，共享媒体源正确去重。
- 多层计数器按对象求差，基线变化证据不会在持久化间隔内丢失。
- 零发送包时不生成平均排队延迟。
- 平均排队延迟的分子与分母来自同一对象集合，并使用同样的逐对象差分规则。
- 根目录与部署副本一致，缓存版本断言更新。
- 定向测试和相关回归通过，新增指标未参与任何弱网策略。
- 本地实现不改变生产服务器状态。
