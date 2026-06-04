# PartyLAN 大厅态 / 房间态 UI 重构任务 Spec

> 目标：把当前“大厅和房间共用同一块深色主舞台”的界面，重构为两套明显不同的场景；优先解决单人建房不能直接共享、共享后舞台文案误导、诊断信息价值过低三个问题，并在不重写媒体链路的前提下提升单机可验证体验。

## 1. 背景

当前前端和共享行为存在几处明显不顺手的地方：

- `frontend/src/App.tsx` 无论是否 `inRoom` 都使用同一套左右分栏，右侧总是保留共享舞台。
- `frontend/src/components/RoomControls.tsx` 用 `!hostVideoCapable || !canShareToPeers` 禁用 `Start Sharing`，导致单人房间不能直接开播。
- `frontend/src/components/ScreenPreview.tsx` 的空态逻辑偏“等别人共享给我看”，单人共享时仍容易落到 `Waiting for shared screen...` 这类 viewer 视角文案。
- `frontend/src/components/DiagnosticsPanel.tsx` 虽然已经不只看 `networkHealth[0]`，但仍只优先匹配 `path=derp`，不能稳定表达“当前 peer 实际路径”。
- `frontend/src/components/MemberList.tsx` 仍是扁平列表，没有把“我自己”和“其他成员”分开，房间内角色感不清晰。
- 全局主题变量目前定义在 `frontend/src/style.css`，整体仍偏深蓝紫黑，进入大厅态时会显得压抑且浪费空间。

这轮任务不是媒体链路重写任务，但必须把以下体验问题一次收拢：

- 大厅态和房间态的信息架构不再混在一起。
- 单人建房可以直接开始共享。
- 单人共享时舞台进入明确的“本地预览/正在共享”状态，不再被“等待共享画面”误导。
- 网络诊断优先显示真正有价值的 DERP 和路径信号。
- 视觉从厚重深色改为偏亮一点的赛博霓虹风格。

## 2. 目标与非目标

### 2.1 目标

- 将 UI 明确拆成大厅态和房间态两套布局，而不是继续共用同一块主舞台。
- 大厅态不显示大黑屏共享区，重点放在可加入主机、手动加入、创建房间、网络状态和快速说明。
- 房间态才显示主舞台；单人房间也允许直接 `Start Sharing`。
- 单人共享时，舞台进入本地共享态，优先显示本地预览；即使暂时没有本地帧，也必须显示明确的本地共享文案，而不是等待观众的文案。
- 后续观众加入后自动接入当前共享会话，用户不需要再次点击 `Start Sharing`。
- 成员区明确拆成“我自己”和“其他成员”两组。
- `Network note` 只显示一条最高价值信号，优先级固定。
- 全局视觉升级为“偏亮一点的赛博霓虹”，保留对比但不走紫黑压抑路线。
- 移动端和窄窗口优先保证大厅态的加入与状态、房间态的舞台可用性。

### 2.2 非目标

- 不重写编码、捕获、解码或 tsnet/Tailscale 链路。
- 不做媒体性能优化、多观众扩容或带宽策略优化。
- 不引入新的重型全局状态模型或前端状态库。
- 不新增前端测试框架。
- 不修改房间协议的长期架构，只允许补最小共享会话衔接能力。

## 3. 方案选型

### 方案 A：在现有 `App.tsx` 上增加显式场景分支，复用现有状态字段

按 `大厅态 / 房间待机 / 单人共享本地预览 / 房间观看` 派生视图状态，在现有 Wails 事件模型上改布局与交互。

优点：

- 改动集中，和当前前端架构最兼容。
- 可以直接复用现有 `inRoom`、`sharing`、`selfSharing`、`canShareToPeers`、`runtime.networkHealth`。
- 后端只需要补两个关键行为：单人可开播、后来者自动接流。

缺点：

- `App.tsx` 会出现更明显的场景分支，需要顺手整理渲染结构。

### 方案 B：把大厅态和房间态拆成两个独立页面或路由

优点：

- 结构最清楚。

缺点：

- 对现有单页 Wails 结构来说过重。
- 需要更多公共状态同步，超出本轮“最小分支改造”的范围。

### 方案 C：只调整 CSS 和文案，不改场景结构

优点：

- 改动最小。

缺点：

- 解决不了大厅态黑屏、单人共享门槛、成员角色不清、诊断优先级这几个根问题。

### 结论

采用方案 A。

实现上允许把 `App.tsx` 中的大厅态和房间态 JSX 抽成轻量渲染分支或小组件，但不引入新路由，也不引入新的全局状态层。

## 4. 视图状态模型

本轮不新增重型状态枚举，优先从现有字段派生四种 UI 场景：

- 大厅态：`!inRoom`
- 房间待机态：`inRoom && !sharing`
- 单人共享本地预览态：`inRoom && sharing && selfSharing`
- 房间观看态：`inRoom && sharing && !selfSharing`

额外派生值：

- `remoteViewerCount`：继续沿用现有“其他成员数量”计算方式。
- `selfMember`：按 `room.selfPeerId` 从 `members` 中找自己；如果事件时序短暂缺失，允许用 `selfPeerId` 兜底渲染一个本机占位卡片。
- `otherMembers`：`members` 中除 `selfPeerId` 外的成员。
- `shareReady`：改为“本机是否支持发起共享”，不再等同于“当前是否已有远端观众”。

`canShareToPeers` 仍可保留，用于表达“当前是否已有其他成员可接收共享”，但不再作为 `Start Sharing` 的硬门槛。

## 5. 交互与信息架构

### 5.1 大厅态

大厅态要从“黑屏等待室”改成“控制台 + 网络大厅”。

布局要求：

- 不渲染主舞台黑屏，也不显示 `Waiting for shared screen...` 相关空态。
- 左侧作为主操作区，包含：
  - `Create Room` 主按钮
  - 可加入主机列表
  - 手动输入目标 Node ID 的加入控件
- 右侧作为轻量欢迎 / 诊断区，包含：
  - 网络状态摘要
  - DERP 状态摘要
  - 版本信息
  - 2-4 条快速说明，例如“先创建房间再共享”“网络授权未完成时可从这里打开认证页”

显示要求：

- 大厅态不显示成员列表主卡片。
- 大厅态不把音频面板放在主操作视觉中心；音频能力可通过诊断摘要与设置面板继续查看。
- 大厅态必须把“可加入主机”和“网络状态”放在首屏，不让用户为了加入房间先看一块无意义的黑舞台。

### 5.2 房间态

房间态采用“舞台 + 侧栏”结构。

桌面宽屏布局要求：

- 左侧侧栏承载：
  - 成员列表
  - 房间控制区
  - 紧凑诊断区或状态摘要
- 右侧主区域承载：
  - 共享舞台
  - 音频面板
  - 错误提示

房间行为要求：

- 只有在 `inRoom === true` 时才显示共享舞台。
- 单人进入房间后，如果本机支持共享，`Start Sharing` 必须可点击。
- 房间待机态如果只有自己，舞台文案要偏“准备开始本地共享”；如果房间里已有其他成员但还没人共享，舞台文案要偏“暂无人在共享”。

### 5.3 主舞台状态

`ScreenPreview` 需要明确区分 viewer 空态和 self-share 空态。

要求如下：

- 大厅态：不显示 `ScreenPreview`。
- 房间待机态，且只有自己：
  - 主文案应类似 `No active share yet. Start Sharing to open local preview.`
- 房间待机态，且已有其他成员：
  - 主文案应类似 `No one is sharing right now.`
- 单人共享本地预览态：
  - 不允许出现 `Waiting for shared screen...`
  - 首选显示真实本地预览帧
  - 如果当前实现还没有本地预览帧源，至少显示明确的本地共享态文案，例如 `Local Preview Live` / `You are sharing now`
  - 共享参数已知时，可继续显示 `width x height @ fps`
- 房间观看态：
  - 保留“流正在启动，等待首帧”的文案逻辑

本轮的硬性要求是：`selfSharing === true` 时，舞台永远不能退回“等待别人共享给我看”的语义。

### 5.4 共享行为

用户可见行为必须满足：

1. 单人建房后可以直接点击 `Start Sharing`
2. 不再提示“至少需要 1 个观众才能开始共享”
3. 用户进入共享态后，不需要因为后来有人加入而重新点一次 `Start Sharing`
4. 用户点击 `Stop Sharing` 后，舞台回到房间待机态
5. 用户离开房间后，所有本地共享相关状态清空

实现约束：

- 允许内部仍保持“每个远端连接一个 sender”的模型。
- 允许为了后来者自动接流而引入一个很轻量的 host share session 抽象。
- 但无论内部怎么做，用户视角都必须是“共享会话持续存在，后来的人自动接上”。

### 5.5 成员列表

成员列表改成两组：

- `Me`
- `Others`

显示规则：

- `Me` 永远显示在最上方，突出本机身份。
- `Me` 卡片显示本机名字、在线状态，以及例如“Sharing / Muted / Ready”这类和自己直接相关的状态。
- `Others` 列表显示其他成员。
- 如果没有其他成员，显示明确空态，如 `No one else in the room yet.`

这样做的目标不是增加新状态，而是把“我自己”和“房间里的其他人”从视觉层面明确分开。

### 5.6 Diagnostics / Network Note

`Network note` 只显示一条最有价值信号，优先级固定为：

1. `custom DERP present`
2. `DERP map regions: ...`
3. 当前 peer 路径，也就是任意包含 `path=` 的行，不再只匹配 `path=derp`
4. 其他泛化告警或错误

补充规则：

- 如果没有任何非空 `networkHealth` 行，则不显示 `Network note`
- `custom DERP missing`、本地 API 失败等信息属于第 4 档泛化告警
- 主界面只显示优先挑出的那一条；完整原始列表继续留在日志或设置面板上下文中

诊断徽标语义也需要同步调整：

- `Share` 不再因为“当前没有观众”而显示 `Blocked`
- `Share` 应表达“本机能否发起共享”以及“是否正在共享”
- `Viewers` 继续单独显示远端成员数量

推荐语义：

- `Live`：`selfSharing === true`
- `Ready`：`hostVideoCapable === true` 且当前未自共享
- `Watching`：`sharing === true && !selfSharing`
- `Unavailable`：`hostVideoCapable === false`

## 6. 视觉方向

### 6.1 主题方向

整体主题从现在的深蓝紫黑，调整为“偏亮一点的赛博霓虹”：

- 底色：冷白蓝 / 冰雾蓝
- 功能成功态：薄荷青
- 风险态：珊瑚红
- 对比色：少量深海蓝

明确避免：

- 大面积紫黑
- 厚重深灰胶囊
- 纯黑大舞台占满大厅态

### 6.2 主题变量

优先通过 `frontend/src/style.css` 中的 CSS 变量统一换肤，而不是在各组件 CSS 中继续散落硬编码颜色。

变量方向：

- `--bg-primary`：从纯深色改为冷白蓝渐层底
- `--panel-bg`：改为浅深混合、带轻微透光层次的面
- `--panel-header-bg`：改为更轻、更亮的半透明仪表条
- `--accent-bg`：用深海蓝或偏蓝绿作为次级按钮底
- `--highlight`：从当前高饱和粉红切到薄荷青系功能高亮
- `--danger` 相关颜色：统一到珊瑚红，不再用紫红

### 6.3 面板与舞台

视觉要求：

- 卡片边框更柔和，阴影更轻，不要再像厚重深色胶囊。
- 大厅态右侧欢迎/诊断区要更像“网络控制台”而不是“播放器空态”。
- 房间态主舞台保留深色承托共享画面，但周围 UI 本身更亮、更轻。
- 顶部指标条改成半透明霓虹仪表感，而不是厚重的深灰状态带。

### 6.4 响应式

窄窗口与移动端下的优先级：

- 大厅态优先显示加入与状态
- 房间态优先显示舞台
- 成员区、诊断区折叠到主舞台下方
- 不允许继续把所有区域等权挤压在同一屏

具体断点建议：

- `<= 960px`：大厅态与房间态都切换为单列；房间态保持舞台优先
- `<= 720px`：房间控制中的分辨率 / 帧率设置改为单列；成员与诊断卡片顺序放到舞台后

## 7. 技术落点

### 7.1 前端文件

前端主改动文件：

- `frontend/src/App.tsx`
  - 增加大厅态 / 房间态显式渲染分支
  - 推导 `selfMember` / `otherMembers`
  - 推导房间视图状态，不新增重型全局模型
  - 调整 `StatusBar` 的布局位置和全局结构
- `frontend/src/App.css`
  - 重新定义大厅态 / 房间态布局
  - 添加响应式优先级规则
- `frontend/src/style.css`
  - 统一更新主题变量
- `frontend/src/components/RoomControls.tsx`
  - 移除“必须已有观众才能开始共享”的按钮禁用逻辑
  - 在无远端成员时显示“将进入本地预览态”的辅助说明，而不是报错
- `frontend/src/components/ScreenPreview.tsx`
  - 区分大厅态不渲染、房间待机态、自共享本地预览态、观看态
  - 更新空态文案优先级
  - 自共享时优先显示本地共享态，而不是 viewer 空态
- `frontend/src/components/DiagnosticsPanel.tsx`
  - 提取 `Network note` 选择逻辑
  - 改 `Share` 徽标语义
  - 支持大厅态与房间态两种密度使用
- `frontend/src/components/MemberList.tsx`
  - 拆成 `Me` / `Others`
- `frontend/src/components/AvailableNodes.tsx`
  - 调整为更像大厅态主操作卡片
- `frontend/src/components/StatusBar.tsx`
  - 保持数据来源，改成更轻、更亮的顶部指标条

受影响的样式文件：

- `frontend/src/components/MemberList.css`
- `frontend/src/components/AvailableNodes.css`
- `frontend/src/components/RoomControls.css`
- `frontend/src/components/ScreenPreview.css`
- `frontend/src/components/DiagnosticsPanel.css`
- `frontend/src/components/StatusBar.css`

### 7.2 前端状态要求

优先复用现有字段：

- `inRoom`
- `sharing`
- `selfSharing`
- `canShareToPeers`
- `remoteViewerCount`
- `runtime.networkHealth`

允许新增的仅是轻量派生逻辑，例如：

- `scene`
- `shareReady`
- `selfMember`
- `otherMembers`
- `networkNote`

不要求新增新的 reducer 顶层状态枚举。

### 7.3 后端行为补点

虽然本轮是 UI / UX 改造，但以下后端行为是必做项，否则 spec 无法成立：

- `app.go`
  - 去掉 `StartSharing` 中“没有 peer 就报错”的硬限制
  - 共享会话启动后，本地要进入 `selfSharing` 状态，不能依赖“至少成功起了一个远端 sender”才算共享
  - 缓存当前共享参数，至少包括 display、width、height、fps，以及当前流元数据
  - 当新 peer 加入且本机正在共享时，自动把当前共享会话接到该 peer
- `app.go` / `setupRoomCallbacks`
  - `OnPeerJoin` 不能再只发一个 UI 事件
  - 如果当前 `selfSharing` 为真，需要尝试为新加入 peer 自动建立视频连接，并把当前流状态同步给它
- `internal/room/room_impl.go`
  - 需要支持把当前共享状态补发给新加入 peer，避免“房主已经共享，但后来进来的人不知道现在有流”

实现说明：

- 当前 `StreamSender` 是“每条连接一个 sender + 一个 capturer”的模型，不天然等于一个可复用的多观众共享会话。
- 本轮不强制把它改成完整多路复用架构。
- 但实现层必须补出“后来的人自动接上现有共享”的用户行为，可通过以下两类方式择一：
  - 继续使用 sender-per-peer 模型，但在 host 侧缓存当前共享配置，并在 `OnPeerJoin` 时自动启动新的 sender
  - 抽一个轻量 host share session，统一维护当前共享配置和新 peer 附着逻辑

### 7.4 本地预览约束

当前 `ScreenPreview` 的真实帧主要来自 `stream:frame`，而这条链路通常依赖 receiver。

因此本轮对“本地预览”的要求分成两层：

- 必达：自共享时 UI 进入明确的本地共享态，不再显示等待他人共享的文案
- 优选：如果以较小代价即可从 sender 侧或本地回环拿到预览帧，则显示真实本地预览

也就是说，本轮不允许为了做“真实本地预览”而重写媒体链路，但允许补最小预览输出。

## 8. 验收标准

满足以下条件即可视为本轮完成：

1. 应用启动后默认进入大厅态，主区域不再显示黑色共享舞台
2. 大厅态首屏可直接看到创建房间、可加入主机、手动加入和网络状态
3. 单人创建房间后，如果本机支持共享，`Start Sharing` 可点击
4. 单人点击 `Start Sharing` 后，不再收到“至少需要 1 个观众”的错误
5. 单人共享时，主舞台显示本地共享态；不得再出现 `Waiting for shared screen...`
6. 后续有成员加入时，房主不需要再次点击 `Start Sharing`
7. 成员区明确分成 `Me` 和 `Others`
8. `Network note` 的展示优先级符合本 spec
9. 全局状态条视觉明显变轻，整体主题不再偏紫黑压抑
10. 窄窗口下大厅态优先加入与状态，房间态优先舞台

## 9. 测试与验证

### 9.1 静态验证

- 前端构建通过：`cd frontend && npm run build`
- Go 测试通过：`go test ./...`

### 9.2 单机手工验证

1. 启动应用
2. 观察默认大厅态是否不再出现黑色共享舞台
3. 点击 `Create Room`
4. 观察房间态是否出现明确的成员区与舞台区
5. 在只有自己时点击 `Start Sharing`
6. 观察舞台是否进入本地共享态，而不是等待观众的文案
7. 点击 `Stop Sharing`
8. 观察舞台是否恢复房间待机态

### 9.3 双机补充验证

如果有第二台设备或第二个可用节点，继续验证：

1. 房主单人先开始共享
2. 第二个成员稍后加入房间
3. 不做任何二次点击，确认后来者能接到当前共享流

### 9.4 诊断优先级验证

至少验证以下几类 `runtime.networkHealth` 输入：

- 同时存在 `custom DERP present`、`DERP map regions`、`path=direct`
- 只有 `DERP map regions` 和 `path=direct`
- 只有 `path=peer-relay` 或 `path=derp`
- 只有 `custom DERP missing` 或 LocalAPI 失败等泛化告警

主界面必须按本 spec 的固定顺序挑出 `Network note`。

## 10. 风险与后续

- 当前 sender 是按连接创建的，后来者自动接流可能需要一个很轻量的 host share session 抽象；这是本轮唯一允许顺手做的小型结构补点。
- 如果真实本地预览拿不到帧，本轮至少要先把自共享文案和状态做对，不能再误导用户。
- 本轮不处理性能和多观众扩容，因此“后来者自动接流”只要求行为成立，不要求一次把底层架构优化到最终形态。
- 主题刷新会触及 `style.css` 全局变量，实施时需要小心不影响设置面板和已有音频组件可读性。

## 11. 结论

这轮任务的本质不是“换一套颜色”，而是把 PartyLAN 客户端的两个核心使用阶段真正拆清楚：

- 大厅态负责加入、创建、诊断、理解网络
- 房间态负责共享、观看、成员协作

只要以上 spec 被严格落地，PartyLAN 的单机可验证体验、房间可理解性和网络诊断可读性都会明显改善，而且不会把范围扩散到新的媒体或网络大工程中。
