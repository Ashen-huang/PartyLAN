# Plan 006：修复 xiusxiu 共享持续低帧、降档触底和恢复失效

> 执行者须完整阅读本文件，先完成基线核对和失败复现，再修改实现。每步验证通过后更新文末记录及 `plans/README.md`。本文件是实施计划，不代表功能已修复或已部署。

## 状态与目标

- 状态：PARTIALLY_DEPLOYED；优先级：P0；工作量：L；实施风险：中高。
- 计划日期：2026-09-18；核对提交：`abf838e`；编写前工作树干净。
- 首要目标：解决 `test` 房间中 `xiusxiu` 长期低帧、个位数帧率和降档后无法恢复，优先改善连续观看的流畅度。
- 对照目标：保持 `Bug_yellow` 房间已经获得用户认可的效果，用作不退化对照。
- 架构：沿用逐观众 P2P / TURN；不恢复自研屏幕 SFU、Simulcast 或新的传输协议。
- 档位约束：保持用户选定上限；每个分辨率层使用 `60 / 45 / 30 FPS` 中不高于用户上限的档位；最低 `540p / 30 FPS`。降分辨率后重新从允许的最高帧率尝试，不继承旧层的 30 FPS 上限。
- 缓冲约束：应用设置的接收缓冲目标最高 300ms，300ms 不是默认值，也不是可以保证的端到端延迟上限。

## 0. 必须先对齐的源码基线

本地当前代码与故障发生时的部署代码不一致，不能直接把本地 v7 当作线上 v11 修复：

| 项目 | 本次只读核对结果 |
| --- | --- |
| 本地 HEAD | `abf838e` |
| 本地策略版本 | `server/webrtc_adaptive_policy.js:8` 为 `adaptive-per-peer-v7` |
| 2026-09-09 日志版本 | `adaptive-per-peer-v11-buffered-45fps` |
| 本地旧档位 | `buildEncodingBands` 仍含 21 FPS、360p18；不满足当前产品约束 |
| 本地能力缺口 | 未找到历史 v11 的 soft protection 与接收缓冲实现 |
| 历史计划 | 对话中存在计划 005，但当前工作树没有 `plans/`；本计划接续编号 006，不重建或声称旧计划已完成 |

基线检查命令，在仓库根目录执行：

```powershell
git status --short
git rev-parse --short HEAD
git diff --stat abf838e..HEAD -- server/webrtc_stats.js server/webrtc_adaptive_policy.js server/register.py server/screego/deploy server/test_webrtc_stats.mjs server/test_webrtc_adaptive_policy.mjs server/test_webrtc_adaptive_integration.mjs
git log --all --oneline -- server/webrtc_adaptive_policy.js plans
rg -n 'POLICY_VERSION|softProtection|jitterBufferTarget|playoutDelayHint' server/webrtc_adaptive_policy.js server/screego/deploy/inject.js
```

执行者先从现有 Git 引用、本地备份/发布产物及已授权的服务器只读检查中找到部署基线，记录浏览器公开资源、服务工作副本、控制服务、测试端与正式端的版本及 SHA-256。需要比较 `inject.js`、`webrtc-adaptive.js`、`webrtc-stats.js`、`register.py` 和实际加载页面的资源 URL；策略版本字符串本身不足以证明代码相同。

在独立分支/工作树恢复可审查的 v11 等效源码和测试基线，保留原工作树及其他改动；不得 reset、清空 stash 或把旧 v7 文件直接覆盖正式资源。若当前正式端已经升级到 v11 之后，以核实的新版本为实施基线，并重新运行本计划反例。恢复工作仅限现有已交付能力，不借机改架构。

**完成标志：** 在文末填写 `implementationBaseCommit`、资源哈希、来源、已有功能清单、基线测试结果；不能追溯来源时将实施状态写为 `BLOCKED_BASELINE`，可以继续设计测试，但不得发布。

## 1. 证据及结论边界

数据文件：`tmp/remote-stats-2026-09-09.jsonl`；SHA-256：`497E97D823669F6D8A82B490132AC67247ADBD3473087876D71EEECBF32E8044`。这是本地留存的历史正式端日志，不代表 9 月 18 日服务器状态。时间按日志原有时间标记列出。

### 1.1 对照与故障会话

| 房间 / 共享者 | 时间 | 路径及会话 ID | 主机样本 | 发送 / 接收 FPS 中位数 | 其他证据 |
| --- | --- | --- | ---: | --- | --- |
| Bug_yellow / Bug_yellow | 13:48–14:50 | P2P `dagf64kl2uq0sf1brung` | 374 | 38.19 / 38.44 | 请求720p45；主机全部可见且反馈全部绑定；L0 |
| Bug_yellow / Bug_yellow | 14:51–15:12 | TURN `dagg3l4l2uq0sf1bruo0` | 128 | 43.09 / 43.00 | 请求720p45；主机全部隐藏但反馈全部绑定；L0→L2后恢复L0 |
| test / xiusxiu → test观众 | 21:15–22:33 | P2P `daglnosl2uq0sf1brurg` | 467 | 23.63 / 23.54 | 请求720p60；反馈绑定0次；观众468条均隐藏；61条发送低于10 FPS |
| test / xiusxiu → hw观众 | 21:18–22:15 | TURN `daglp0kl2uq0sf1brut0` | 344 | 26.97 / 26.01 | 请求720p60；反馈344条全部绑定；62条发送低于10 FPS |

P2P 故障会话在 21:22:24 到 22:33:04 的 425 条主机记录保持 L5 / 540p30，未观察到回升。以上是采样点分布；约每 10 秒落一条日志，不能据此声称精确的逐秒卡顿时长。

### 1.2 必须覆盖的三个故障形态

| 场景 | 原始证据 | 可以据此下的结论 |
| --- | --- | --- |
| A：共同帧供给不足 | 21:23:04 两条连接采集均约3.47 FPS，编码/发送约3.97 FPS。TURN估计可用5.52Mbps、平均包发送等待44.3ms；该窗口记录编码均耗约88.6ms | 不足以用“观众带宽低”解释全部低帧；需检查源内容、捕获调度、编码负荷及多连接共享源影响。不能仅此确诊CPU不足 |
| B：单连接发送崩溃 | 21:28:44 P2P采集29.59 FPS、发送0.50 FPS、估计可用37.7Kbps、平均包发送等待1536.5ms；同期TURN发送31.11 FPS、等待约1.63ms | 存在逐连接拥塞/发送退化，不能据此全局降低其他观众画质；需发送端独立拥塞保护 |
| C：TURN后段拥塞 | 22:02:04 TURN采集26.04 FPS、发送3.00 FPS、估计可用155.4Kbps、平均包发送等待821.1ms；同期P2P发送25.51 FPS | TURN也会单独退化；有反馈仍无法保证稳定，反馈修复不能替代发送侧拥塞治理 |

修正此前分析中的过强表述：

- `Bug_yellow` 并非502条主机记录全部隐藏：P2P的374条可见，TURN的128条隐藏；后者仍有完整反馈，证明“主机隐藏”不是低帧的充分原因。
- `receiverReportAgeMs` 已经存在，且已有限龄检查；应改善实际采样年龄、重复/乱序和窗口关联，不是简单再加同名字段。
- 不同采样窗口出现发送6.5 FPS、接收40 FPS，不能单独证明数据错配；解码先前缓冲的帧、静态低码率、窗口错位都可能解释。
- `availableOutgoingBitrate` 是浏览器链路估计，不是宽带合同速率；`maxBitrate` 是上限，不是强制发送的最低码率。
- `ΔtotalPacketSendDelay / ΔpacketsSent` 是已发送包在本地缓冲的平均等待时间，不是当前队列长度或端到端延迟；`totalEncodeTime` 也不能直接换算设备CPU占用。
- 浏览器 JS 没有通用的“清空 WebRTC RTP 队列/任意删除过期预测帧”接口。不能承诺在页面里实现发送队列硬截止300ms。

指标语义依据：[W3C WebRTC Stats](https://www.w3.org/TR/webrtc-stats/#dom-rtcoutboundrtpstreamstats-totalpacketsenddelay)、[W3C 接收缓冲目标](https://www.w3.org/TR/webrtc/#dom-rtcrtpreceiver-jitterbuffertarget)。

## 2. 当前代码定位与待复现缺陷

以下行号针对 `abf838e`，实施时按函数名在对齐后的基线重新定位。它们是本地证据，不自动等价于正式 v11 根因。

| 文件 / 符号 | 当前行为与实施关注点 |
| --- | --- |
| `server/webrtc_stats.js:483` `buildWebRTCSample` | 第518行因 `visible=false` 提前判无效；第578行 `buildViewerQualityReport` 要求有效且至少20包，隐藏或无包时反馈中断 |
| `server/screego/deploy/inject.js:1132` `refreshAdaptiveQualitySnapshot` | 按会话保存质量快照；轮询结束后才采集发送端，控制请求变慢可能拖延本地保护 |
| 同文件第1166行 `receiverQualityForPeer` | `ageSeconds + 本地缓存时间`，6秒过滤；返回结构未保留接收侧pair/generation供时序判断 |
| 同文件第1193行 `sampleAdaptiveStreamingPeer` | 两套采样结果与原生瞬时FPS混用；需要独立来源、窗口与实际应用参数 |
| 同文件第1031行 `applyAdaptiveProfile`、第2326行附近 `applyBitrate` | 更新sender码率/FPS/scale；成功、失败及并发完成顺序必须与策略状态一致 |
| `server/webrtc_adaptive_policy.js:143` `assessReceiver` | 直接比较采集帧率与编码/发送帧率，不考虑该peer已成功设置的帧率上限 |
| 同文件第217行 `hasSenderCpuPressure` | 将senderCollapsed作为CPU压力，可能把网络限发误标为CPU |
| 同文件第464行附近 `evaluateAdaptiveSample` | 无反馈且非CPU直接返回；降档后仅凭发送端恢复无法推进升档 |
| 同文件第653行 `buildEncodingBands` | 本地仍是旧档位；实施应保留经确认的v11档位约束 |
| `server/register.py:266` `normalize_quality_sample` | 收到的数字缺少完整finite校验；扩展字段时保持鉴权和边界校验 |
| 同文件第302行 `store_quality_sample` | 按参与者+会话覆盖并重新盖接收时间戳，重复/乱序报告可能显得“新鲜” |
| 同文件第323行 `get_quality_snapshot` | 返回服务器接收年龄；不是源采样结束到现在的完整年龄 |

典型旧代码片段，供漂移核对：

```javascript
// server/webrtc_stats.js:518
if (context.visible === false) sample.invalidReason = 'page-hidden';
// server/webrtc_stats.js:587
if (!sample || !sample.valid || /* 身份校验等 */ totalPackets < 20) return null;
// server/webrtc_adaptive_policy.js:158
var senderDeliveryCollapsed = sourceActive &&
  senderFps < Math.max(RECEIVER_ACTIVE_FPS, sourceFps * RECEIVER_DECODE_RATIO);
```

本地只读复现还发现：采集60、peer按档位限制30且实际29.6时可能被误判CPU继续下降；无反馈下发生CPU降档后，即使发送恢复健康也停在 `sender-observing`。须在正式基线重跑反例后才决定修复方式。

## 3. 范围与约定

允许修改：

- `server/webrtc_stats.js` 及部署副本 `server/screego/deploy/webrtc-stats.js`。
- `server/webrtc_adaptive_policy.js` 及部署副本 `server/screego/deploy/webrtc-adaptive.js`。
- `server/screego/deploy/inject.js`。
- `server/register.py` 及部署副本 `server/screego/deploy/register.py`。
- 对应 `server/test_webrtc_stats.mjs`、`server/test_webrtc_adaptive_policy.mjs`、`server/test_webrtc_adaptive_integration.mjs`、`server/test_register_control_api.py`。
- 新建 `server/test_webrtc_low_fps_regression.mjs`、`server/fixtures/webrtc-low-fps/` 脱敏场景数据；必要时在现有测试框架内增加运行时mock。
- `server/screego/deploy/nginx-screego.conf` 仅更新资源缓存版本及相关测试；本计划和索引。

明确不在本轮重写：屏幕SFU、TURN实现、语音、账号、房间租约/断线重连、原生Windows媒体链路、音频捕获/播放、UI主题。除用户主动停止或真实连接故障外，不以重建PC、ICE重启、重新弹采集框作为画质切换手段。不得因为某观众变差全局降低共享track约束。

代码沿用现有纯JS模块导出、Node `node:test` + `assert/strict` 风格；以 `test_webrtc_adaptive_policy.mjs` 的 `good/receiverGood/evaluateMany` 为例。协议测试沿用 `test_register_control_api.py` 的可控clock和临时HTTP服务。字符串匹配测试仅验证装配与副本一致性，不能替代执行策略/异步更新的行为测试。

Git分支建议：`codex/006-xiusxiu-low-fps`；基线迁移与针对性修复分开提交/审查。用户当前要求仅写计划，本次不实施、不发布。

## 4. 实施步骤

### P0-A：基线、复现场景和可解释遥测

1. 完成第0节；重跑下方基线检查。记录正式/测试实际加载版本，而不是从目录名推测。
2. 从第1节生成脱敏fixture：共同源低帧、单peer拥塞、后台观众无反馈、恢复、健康限30/45帧、静态桌面。用明确合成的连续1秒或2秒输入测试状态机；不能把每10秒一条历史日志冒充连续采样。
3. 增加并优先复现以下失败：后台仍有有效RTP却不报告；合法540p30被当成CPU压力；无反馈降后不恢复；旧报告重复累计；sender更新乱序后实际仍低FPS；最低档拥塞无进一步软保护。
4. 每连接记录请求FPS、track settings、实际采集/编码/发送/接收/呈现FPS、目标档位、成功应用的 `maxFramerate/scaleResolutionDownBy/maxBitrate`、参数版本、异常/恢复原因及最近决策时间。未知值为null/缺失，不填0伪装测量。
5. 统一窗口内counter delta；使用RTCStats自己的timestamp检查窗口，保留瞬时FPS作为独立诊断；不以两者最大值当作“真实FPS”。SSRC、track、pair或generation变化时重置相应基线。发送侧也要分开“counter有效”和“包量足够评估丢包率”：`computeSenderDeltas` 的少于20包不能让整个低帧保护永久停止；零包时不计算新的平均包等待，保留停滞与有效的源/编码证据。
6. 增加可选编码实现标识、每帧编码时间、丢帧/冻结、sender平均包等待、接收实际buffer及目标buffer。硬件加速与源静态/动态在日志无法确认时明确unknown；不要推断共享者设备性能差。

**验证：** 先运行新增场景，保留失败输出及对应根因；修复后相同测试通过。最终使用 `node --test server/test_webrtc_low_fps_regression.mjs`；fixture须说明“历史观测值”和“合成连续输入”的区别。

### P0-B：恢复后台反馈，修正报告年龄和身份

1. 页面隐藏不使有连续counter的媒体样本失效；可见性独立记录。维持counter回退、身份变化、无基线等真正失效规则。浏览器冻结/节流无法绕过，长采样间隔必须降级为不适合快速决策。
2. 区分 `measurementValid` 与 `trafficActive`。稳定身份且正常计时的零包/零帧窗口可发送“停滞/空闲报告”，无需等到20包；不能从零包本身推导丢包或CPU。切换身份后的第一个无基线窗口仍无效。
3. 报告携带协议版本、PC生命周期标识、单调 `reportSeq`、receiver本地pair/generation、采样起止/时长和生成后排队年龄。`pairId/generation` 只在各自一侧生命周期中比较，不能要求两端本地ID相等。逻辑绑定使用房间+共享会话+观众身份。
4. 服务器同生命周期只接受递增序号，拒绝旧generation回写；重复报告不刷新新鲜度。换PC时使用已注册的新会话绑定；保留已退役生命周期的拒绝信息至会话TTL，延迟到达的旧报告不能重新激活。缺协议字段的旧客户端可继续展示诊断，但不提供快速切档/升档提交证据。
5. 年龄区分客户端生成后等待、HTTP上传、服务端驻留、快照GET往返、本地缓存等待；不直接相减两台设备墙钟。**只有可证明完整年龄上界的报告才能进入快速决策集合**；上传延迟未知的报告仅诊断，不能把“不确定”当0。实施时优先验证服务端有期限的采样凭据方案：既有认证质量响应返回绑定房间/会话/观众生命周期的凭据，客户端收到后才开始可用于决策的新采样窗口；服务端用自身时钟校验签发至收报期限，并在快照返回保守年龄上界。首报/旧协议仍可诊断，不新增未鉴权接口，不让观众读取房主专用快照。凭据轮换须有界并支持正常在途请求，凭据等待不得阻塞本地采样；用真实2秒报告节奏验证不会长期无法获得新鲜报告。若选其他协议，也必须通过同样的端到端年龄上界测试。本地请求超时后只发最新样本；客户端取消不能替代服务端拒绝迟到旧样本。
6. **首轮保持2秒报告/轮询间隔**，避免贸然增加负载。完整年龄上界指“采样结束到当前决策时刻”，采样时长另列；凭据方案需用已验证的采样时长推导结束年龄上界，避免重复计时。初始新鲜阈值4秒（严格小于）、4–6秒仅诊断、6秒以上过期；报告seq必须新的且采样窗口不超过4秒。连续两个独立新鲜报告才能触发接收驱动的快速动作/提交恢复。测试2秒上传/轮询的最差相位，确保正常反馈不会全部超龄。可配置，不硬编码到多处。
7. 快照请求失败或超时不能阻塞本地sender采样；并发最多一个fetch，失败后按失联反馈规则运行。网络快速反应由本地连续有效采样承担，不把已有2秒轮询简单改成1.5秒过期阈值。
8. 当前质量API按IP限120次/分钟；3观众×2秒约90次/分钟。补同NAT多观众与429退避测试。若后续要改1秒，必须先单独设计每会话/账号预算及总IP保护，不能直接把现有限额放宽或取消。

**验证：** stats测试覆盖前台/后台、0包、长gap、可选字段缺失、counter复位；API测试覆盖重复、乱序、过期、生命周期切换、旧客户端、NaN/Infinity及越权；运行时mock验证控制接口挂起时sender仍采样。对应Node与Python命令全部通过。

### P0-C：按实际帧供给和已应用参数分类，避免误降档

1. 为每个peer计算 `expectedSendFps = min(有效采集帧率, 已成功应用的peer帧率上限)`。合法限30帧输出29.6、限45帧输出43不得相对原始60判定崩溃。源有效性和采集活跃度独立判定，不用一次低FPS证明静态或CPU。
2. 输出明确分类：`source-limited / encoder-limited / network-limited / receiver-limited / healthy / unknown`，并携带支持证据；混合压力允许多原因，决策必须说明主因。不能把 `qualityLimitationReason=none` 当作健康证明。
3. source-limited：采集与发送共同低而传输跟得上；区分可验证静态、源本身30帧、捕获调度/应用负载。先观察源，不靠反复降低单peer帧率“修复”源。多连接同时低帧只能提示共同来源，不能断言共用单编码器或设备故障。
   - 用同一动态60FPS源分别做1/3观众、monitor/window/browser捕获与前后台对照；采集源计数与各sender的处理后源计数分开记录，不能把单sender自适应后的帧率直接当作原始屏幕供给。
   - 对账每次自动动作前后的track constraints/settings，确认没有旧保护、迟到的applyConstraints或配置恢复把共享源保持在30FPS；若复现，由唯一的用户采集配置入口修正，并只在约束确实不符时有界重试，不能按观众状态反复applyConstraints。
   - 对账 `getContentHint`：当前smooth映射motion，其他模式映射detail。验证流畅优先的实际用户配置与sender `maintain-framerate` 是否真正生效；若只是未正确传递既有设置，则修复传递，不全局强制编码器/图像风格。
   - 若只有多sender负荷下采集显著下降，用可回滚的单peer编码负荷调整做因果对照，记录其他peer及原始采集是否一起恢复。仅在复现编码负荷挤占源供给后调整该peer的负荷保护；不能借此恢复全局“最弱观众决定所有人”的策略。
   - 若动态源采集持续不足且没有上述代码问题，在报告保留浏览器/驱动/共享源负荷诊断，源低帧子项保持未验收。不能仅给分类加日志就宣布A类故障已修复。
4. encoder-limited：采集充足、编码持续低于当前peer有效上限，结合CPU limitation、同窗口编码时间、发送等待未升高等多项证据。只做该peer的有冷却单档保护；CPU压力不等待接收buffer升满，也不触发网络快速降档。
5. network-limited：发送等待持续升高，加上RTCP/NACK/重传、交付缺口或有效链路估计下降等证据；即使编码计数一起减少也不能归为CPU。无观看反馈时允许本地确认的网络软保护，但不使用接收驱动的快速连续降档。
6. receiver-limited：发送正常但观众解码/呈现慢，且没有对应传输拥塞；只保护该观众。缺字段/跨窗口矛盾时unknown，冻结新增降档证据，已有软保护设超时并按本地压力缓慢释放，不制造永久码率上限。
7. 增加发送参数单写入口：每sender最多一个 `setParameters` 在途，后续合并到最新目标；成功并read-back后更新 `appliedProfile`。旧promise完成不得覆盖新目标；失败保留上一成功状态并报告，不假装档位已执行。
8. 降到540p重新允许60（用户上限为60时）；跨层及soft protection恢复均重设完整期望参数并确认，不能留下前档30FPS。软保护期间普通采样无需重复写入相同参数。若现有控制器已做到上述行为，则保留并仅补回归。
9. 每次参数成功应用后清空接收侧切档证据，记录sender本地单调 `appliedAt` 和参数版本。新seq不等于新档位的样本：仅当 `本地now - 报告完整年龄上界 - 采样时长` 晚于 `appliedAt + 过渡等待` 时，才用该窗口对照新目标FPS。过渡等待初值1秒，并覆盖已观测媒体滞留；旧窗口可诊断真实拥塞，但不能否决新档位的FPS探测。不具备完整年龄上界时转入有界的无反馈路径，不能无限等。无需比较设备墙钟，也不能假定观众已经知道房主本地参数版本。

**验证：** 合成多peer：一条540p30另一条720p60；前者限帧不连降且不修改公共track；并发/失败setParameters测试；源60→30/静态1 FPS、编码耗时升高、网络崩溃、接收端单独解码慢分别走预期分类。运行policy、low-fps和integration测试。

### P0-D：发送端软保护、300ms接收缓冲与最低档恢复

1. 保留 v11 已有软保护能力，用新分类驱动，优先保护帧供给。早期码率限幅只降低每帧预算并使用浏览器拥塞控制，不以增加接收buffer作为解决CPU/源低帧的办法。
2. 本地连续两次有效采样确认发送等待上升并超过150ms，进入软保护；超过300ms同时出现实际发送崩帧，可立即启动一次码率保护，后续仍逐样本确认。这里是**拥塞触发指标**，不是硬性队列上限。稀疏包且窗口不可比较时不得触发。
3. 编码档位上限与网络临时码率上限独立。最低540p30仍可降低码率预算，不能被该档的“参考码率下界”挡住。预算由多项有效压力与保守链路估计决定，每次下降最多25%，严重崩帧单次最多50%，1秒内不重复改变；设置可配置100Kbps安全下界，且不等价于承诺此带宽能540p30。
4. `maxBitrate` 始终只是发送上限，不强制提高发送量。0或缺失BWE不能当作真实0带宽；不因静态内容低吞吐压缩预算；不使用单观众估计去改变所有peer。
5. 对网络抖动/接收缺帧先使用已确认v11固定基准buffer（记录实际值），再以小步提高目标、最高300ms。两个新鲜反馈窗口后仍无法跟上当前帧率才降画质；浏览器不支持buffer设置时记录unsupported，最多等待两个有效窗口后继续保护，不永久卡在“等待缓冲”。持续严重发送拥塞不能为了等buffer而保留过量负荷。
6. 实测 `ΔjitterBufferDelay / ΔjitterBufferEmittedCount`；分母为0时未知，目标值不能冒充实际值。实际测量允许超过300ms并如实显示；RTT不能冒充视频端到端延迟。若仅有RTT与buffer可给明确标识的估算，不承诺传输、捕获和解码全部在300ms内。
7. 有新鲜接收反馈时，区分“解除自身限幅”和“提高档位”：
   - 发送等待/交付压力已回落、源供给及编码证据健康并持续8秒，即可有界探测释放临时码率。**不要求被低码率预算压住的FPS先恢复至30/60**；接收端应能跟随实际输出，未出现新的解码/冻结压力。正常源静态也不能维持永久限幅。
   - 每2秒最多释放25%临时预算，需新的有效压力证据且不超过该档配置；码率仍由浏览器拥塞控制决定。观察到拥塞重新增长时停止/回滚，不连续强推预算。用行为测试验证100Kbps软上限不会反过来成为恢复前置障碍。
   - 当前档可用且临时预算释放后，一次探测上一档；参数实际生效，经过P0-C.9过渡期后收到两条属于新档位的新鲜健康报告才提交。探测最长12秒，包含应用与过渡时间；反馈不足时有界结束为未确认，不能拿旧30FPS窗口否决新60FPS档。明显本地拥塞可提前回滚，无需等满。
   - 坏反馈回滚一次、冷却至少8秒；成功的相邻升档无需重复等待最初8秒健康期，但正常切档驻留至少4秒。区分普通恢复与极深限幅的验收时限，见第6节，并用fake clock测试最坏路径，不能以永远等待下一条反馈绕过时限。
8. 无反馈时：先修报告通路；仍无法取得时，本地连续健康10秒后可以作一次有限恢复探测，不永久锁底。首选只增加10%临时码率预算；释放该预算后才允许每15秒最多一个档位试探。以本地发送跟随、实际队列/编码压力校验，标记 `receiver-unverified`；不能声称观众健康。缺少这些本地证据则暂停探测并明确原因，不能盲升。每次探测最长10秒，包含参数应用时间；有至少两个新有效本地样本时结束为本地确认状态，否则回退至最后成功参数并冷却至少8秒，记录 `probe-inconclusive`，不把缺证据冒充网络恶化。任何正在等待反馈的probe均不能无限保留。
9. 仅修sender参数和判定，正常恢复不重建PC、不黑屏。没有支持的API就不实现任意RTP丢帧、关键帧替代视频或重置encoder来伪装流畅；真实连接失效由已有重连模块负责。

**验证：** 重放B/C类合成连续事件：软预算降低、P2P/TURN隔离、540p30继续保护、恢复后预算/帧率/分辨率逐步释放；用健康与静态对照保证不会无故压码率。buffer不支持/无emitted帧/达到上限必须终止等待。不能用mock状态机测试宣称真实网络已维持30FPS。

## 5. 测试矩阵与命令

至少包含以下回归，先在实施基线上证明缺陷，再改代码：

| 编号 | 输入或动作 | 机器断言 |
| --- | --- | --- |
| T01 | 页面隐藏但有效RTP持续 | 有效sample与quality report持续，visible=false |
| T02 | 稳定身份、0包0帧/少于20包与长采样gap | 收发两侧保留有效counter/停滞；不伪造丢包与平均等待；长gap不算快速证据 |
| T03 | seq重复、乱序、旧PC/旧generation、错房间 | 不覆盖新报告，不刷新年龄，不触发决策；越权拒绝 |
| T04 | 同报告两次轮询/跨主机时钟偏差 | 独立证据计数不增长；不依赖两端墙钟直接相减 |
| T05 | 控制请求永不完成、多个观众同IP | sender采样仍运行；超时有界；正常频率不触发429 |
| T06 | 采集60，peer上限30/45，实发29.6/43 | 不判CPU崩帧，不连降 |
| T07 | A：多peer同时采集约3.5，发送约4；旧采集约束/配置回写 | source/unknown，可疑编码证据独立记录；配置实际不符的可复现路径恢复用户采集上限，不按观众改公共track |
| T08 | B：采集30，发0.5，37.7Kbps，等待1537ms | 网络保护；不确诊CPU；健康peer不变 |
| T09 | 采集60，持续编码3，CPU证据成立，网络健康 | encoder保护；不等待300msbuffer，单次一档 |
| T10 | 发送60、接收解码3且本地解码负荷高 | receiver保护，不更改其他peer |
| T11 | setParameters旧请求迟到/失败/关闭PC | 应用状态不倒退，生命周期销毁不再写入 |
| T12 | 720p30→540p60与软保护释放 | read-back maxFramerate=60；实际FPS由真实浏览器门验证 |
| T13 | floor + 严重拥塞 + 恢复 | 档位不低于540p30；临时码率能下降并自动释放 |
| T14 | 无反馈120秒；本地先坏后好；probe途中样本消失 | 不靠刷新恢复；probe最多10秒有明确结束，不累计接收快速降档 |
| T15 | 静态画面/源30 FPS/缺少BWE或编码时间 | 不凭低FPS连降；缺值不当0；不承诺超过源FPS |
| T16 | buffer拒绝设置、未生效、测量超过300ms | 有界等待、目标≤300ms、实测不裁剪、不伪装RTT |
| T17 | 30→60参数已生效，seq虽新但窗口仍覆盖旧30FPS | 不以旧窗口误回滚；过渡等待与探测都有截止时间 |
| T18 | 上传滞留10秒后首次入库/客户端取消后迟到/设备墙钟偏差 | 无完整年龄上界则不能快速决策；迟到样本不伪装新鲜 |
| T19 | 链路恢复但100Kbps自身限幅仍造成低FPS | 无需先达到30FPS即开始有界预算恢复，最终释放；不放大仍在的真实拥塞 |
| T20 | 最深降档、最低预算、持续健康反馈；无反馈对照 | fake clock验证正常/深度恢复时限；无反馈探测有界且不冒充观众验收通过 |

本次只读基线已运行：stats+policy+integration共69项Node测试通过；`test_register_control_api` + `test_gamecast_control` 共32项Python测试通过；`inject.js`语法检查通过。此结果仅针对本地v7，说明验证命令可用，不代表覆盖v11故障。

仓库根目录的精确检查命令：

```powershell
node --test server/test_webrtc_stats.mjs server/test_webrtc_adaptive_policy.mjs server/test_webrtc_adaptive_integration.mjs
node --test server/test_webrtc_low_fps_regression.mjs  # 实施时新增
node --check server/screego/deploy/inject.js
node --check server/webrtc_adaptive_policy.js
node --check server/webrtc_stats.js
python -B -m unittest discover -s server -p 'test_register_control_api.py'
python -B -m unittest discover -s server -p 'test_gamecast_control.py'
$serverTests = Get-ChildItem server -Filter 'test_*.mjs' | Select-Object -ExpandProperty FullName
node --test $serverTests
python -B -m unittest discover -s server -p 'test_*.py'
git diff --check
```

预期：各命令exit 0、没有失败；新增文件未创建前不宣称该命令可运行。源/部署副本必须字节一致，在integration测试中覆盖stats、policy、register三组；语法通过不能替代副本一致性。

若基线对齐发现确实需要改React UI或补丁，先列出增量范围并增加对应浏览器验证；再按 `server/screego/README.md` 在干净上游源码目录应用补丁及 `server/screego/verify-build.ps1 -SourceRoot <已验证目录>`，不得直接对不明 `web/ui` 或复用补丁目录发布。当前不存在历史对话提到的 `scripts/Web-Verify.ps1`，不要伪造此检查已通过。

## 6. 真实浏览器验收及发布门

顺序：确定源码基线 → 回归通过 → 5151隔离测试端 → 相同设备/片源对照 → 通过后按既有授权范围发布正式域名。写计划期间不执行部署。

测试媒体使用明确的动态60 FPS素材（可验证时间码），并分别记录标签页/窗口/整个屏幕、浏览器版本、设备负载、编码器实际实现。默认既有编码兼容路径；可做已有H264/VP8模式的对照，但不在证据不足时强切编码器。至少覆盖：

1. `Bug_yellow` 对照类：720p45、单观众，P2P和TURN各30分钟。
2. `xiusxiu` 故障类：720p60、1080p60，1与3观众，整个屏幕；其中一名观众后台、一名持续前台，覆盖P2P/TURN混合。
3. 在受控测试链路引入1–2秒波动、10秒持续限流及CPU负荷，随后解除；每类重复3次。至少一条长测60分钟，不能只截图证明稳定。
4. 源静止与高动态交替，窗口切换、画质切换、源实际30FPS；真实浏览器验证 shared track/多sender是否相互限帧，不能从JS mock推断其底层调度行为。

预先记录同设备、同素材的基线。验收计算使用新遥测实际窗口，排除并单列启动/重连/路径切换/静态/故意极端限流窗口，必须输出各类窗口占比，禁止通过大量剔除掩盖失败。

- 健康动态源且单链路基线可达目标时：接收FPS中位数≥目标的90%，P10≥目标的75%；60FPS目标对应54与45，45FPS对应40.5与33.75。
- 不施加约束的动态场景：低于10FPS有效窗口占比<1%，没有连续超过2秒的可确认停帧；同`Bug_yellow`对照的FPS与冻结指标不退化超过5%。日志不够细时本条待测，不能用10秒稀疏样本宣布通过。
- 解除限流或CPU负荷后，在源供给及路径能力恢复的前提下15秒内开始释放保护/探测。持续健康反馈下，普通恢复（最多下降3个帧率/分辨率档、预算仍≥该档正常值的25%）60秒内回到此前可持续档位；从100Kbps及540p30恢复至此前可持续1080p60的极深场景上限180秒。分别用fake clock验证最坏时序并以实测确认，不能把单次预算释放误作全部恢复。无需刷新、退房或重新共享；恢复中再次变坏则单列回滚和原因。
- 反馈仍缺失时，适用P0-D.8：15秒内尝试本地有证据的恢复，每次探测最长10秒，档位探测间隔至少15秒；不承诺套用上述有反馈的60/180秒全程恢复时限。测试需证明本地持续健康时会继续释放预算/探测，而非只动一次便锁底；仍标为receiver-unverified。后台反馈修复未通过时，不得据此宣布观众端完整验收成功。
- 正常画质切换不产生额外房间断开、ICE重启、采集轨道更换或全黑；弱观众不触发健康观众的参数变更。
- 支持buffer API时设置目标始终≤300ms；观测实际buffer独立记录；不能承诺不可实现的发送队列硬上限。
- 网络带宽或源供给不足以540p30时仍保持该配置下限并明确瓶颈，实际低帧如实报告；极限压力下不得伪称“稳定30FPS”，此情况不能算正常动态场景验收通过。

发布时记录资源哈希、策略/协议版本、测试报告、容器/控制服务身份；备份现有公开资源和工作副本。先兼容控制协议再发客户端资源，旧客户端共存须已测。控制服务若因重启丢失内存中的在线/租约状态，应使用已有安全发布流程安排时机，不声称无中断。无需改服务镜像的静态修复不重建屏幕/语音服务；只增量修改Nginx缓存引用并 `nginx -t`，禁止整份覆盖正式配置。失败按成套哈希清单回滚，不能只退一个相互依赖的JS文件。

## 7. 完成标准与暂停条件

完成标准：

- [ ] 对齐并记录实施基线；本地v7未误覆盖线上v11能力。
- [ ] T01–T20各有实际行为测试或明确的浏览器实测结果；相应红灯复现记录可查。
- [ ] A类共同源低帧有受控实验及因果结论；确认为代码问题则验证帧供给恢复，确认为浏览器/设备能力限制则单列未修复，不能用分类测试通过替代。
- [ ] 完成针对性修复、全量server Node/Python回归、语法/副本/差异检查；未测内容单列。
- [ ] 5151真实浏览器验收覆盖持续低帧、恢复、后台与多观众；对照没有回归。
- [ ] 区分 `IMPLEMENTED_LOCAL`、`VALIDATED_TEST`、`DEPLOYED_FORMAL`；仅单测绿不能标为正式验收完成。
- [ ] 发布/回滚证据及遗留限制记录，更新索引。

暂停对应实施/发布步骤并报告的条件：

- 找不到与实际部署相符、可审查的源码基线；不通过重写全部缺失功能绕过。
- 无法重现所拟修改的行为：保留证据、转为诊断项，不用只测新API存在的测试冒充修复。
- 仅凭本地v7缺陷就准备修改不同的线上v11逻辑，或发现策略已经被其他变更修复。
- 声称可直接flush浏览器内部RTP队列、无需测量保证300ms端到端、或保证实际30FPS不受源/设备/链路限制。
- 核实实际源长时间低帧，但尚未区分静态画面与采集调度/设备压力；可以交付已证实的反馈/策略修复，不能把源低帧根因标成已修复。
- 需要修改账户、语音、房间生命周期、替换传输架构或用户工作区其他无关文件；先说明范围差异。
- 任一回归严重破坏音频、既有重连或健康观众流畅度；保留诊断，阻止正式发布。

维护注意：帧率阈值必须与已成功应用的peer参数保持同一版本；反馈计数必须由真实新报告推进；旧客户端兼容不能恢复旧报告的快速决策权限；后续调整采样频率时同步调整新鲜度、限流与测试。

## 执行记录

- 2026-09-18：从正式端已加载的 v11 资源恢复可审查基线；未以本地 v7 覆盖线上能力。`implementationBaseCommit` 为 `1e4e955873d4fcb4a47c1cdd8274d43ddad2c1c9`，实际发布输入以正式端发布前的四项资源逐项差异校验。
- 已完成 P0-B 的最小修复：页面隐藏不再使连续有效 RTP 窗口失效；报告携带协议版本、单调序号和采样时长；服务端拒绝同一生命周期的重复或乱序报告，且不会刷新其新鲜度；策略以稳定报告标识而非服务端接收时间识别新反馈。
- 红灯到绿灯：新增“后台观众仍报告有效 RTP”及“重复/乱序报告不能覆盖新报告”回归。`node --test server/test_webrtc_stats.mjs server/test_webrtc_adaptive_policy.mjs server/test_webrtc_adaptive_integration.mjs` 为 114/114 通过；`test_register_control_api.py` 为 20/20；`test_gamecast_control.py` 为 18/18；静态语法、源/部署镜像与差异检查通过。
- 正式发布：2026-09-18 22:39 CST，发布 `webrtc-stats.js`、`webrtc-adaptive.js`、`inject.js`、`register.py`。正式端哈希依次为 `3e828c053be498eeed3687ad8e9226f98bc3531b52cb27028da49922fb90df17`、`70753374f62c072b3fd452676146653f301035d8328af57ab721644e26a35553`、`9da7819d4e8123d451567ca7a5d0797b02ac68970945749d0b7d060f33e9a66b`、`5eb5dffb28f0bb576c3053fe08db78b9ad14e3c36fd48a12f9b57985faf53236`。发布前文件备份位于服务器 `/root/screego/backups/plan006-20260918-1030/`。
- 发布后：`screego-register` 为 `active`，正式域名首页和三项静态资源烟测返回预期哈希；服务启动日志无异常。
- 未完成：P0-A 的连续低帧 fixture、P0-C 的源/编码/网络分类及参数串行化、P0-D 的发送软保护恢复细化，以及计划第 6 节所列真实浏览器长测。当前状态仅为 `DEPLOYED_FORMAL` 的反馈链修复，不构成 xiusxiu 全场景性能验收。
