# PartyLAN 原生动态画质与窗口音频修复实施计划

**目标：** 在不修改广州节点网页端、SFU 或服务配置的前提下，使 Windows 客户端默认
使用“原始画质 + 60 FPS”，验证并保持单轨动态画质升降，同时可靠捕获所选窗口的安全
应用进程树音频；全屏共享继续排除 PartyLAN 房间语音。

**设计依据：**
`docs/superpowers/specs/2026-08-05-partylan-native-adaptive-window-audio-design.md`

**实施边界：** 在当前 `fix/web-first-room-connect` 分支继续工作，保留广州节点迁移的
既有未提交修改；不切换分支，不推送，不创建 PR，不部署或重启广州节点。只允许对
`partylanapp.com` 执行只读访问和正常用户流程冒烟。

**测试纪律：** 每项新行为先增加能因现有缺口而失败的目标测试，确认红灯原因正确后
再实现最小改动。前端生产构建必须在全仓 Go 测试之前完成，避免嵌入资源哈希竞争。

## Task 1：固定基线与保护边界

**只读检查：**

- 当前分支、HEAD、未提交文件和暂存区。
- `release/PartyLAN-v0.5.5-win64.zip` 的存在性、大小和 SHA256。
- `server/`、`server/screego/deploy/` 和相关 Nginx/SFU 文件当前无工作区差异。
- 当前前端 Node 测试、目标 Go 音频策略测试和自适应控制器测试结果。

执行：

```powershell
git status --short --branch
git diff --cached --name-only
Get-FileHash -Algorithm SHA256 release\PartyLAN-v0.5.5-win64.zip
git diff -- server
node --test frontend/tests/*.test.mjs
go test . ./internal/gamecast/adaptive/... ./internal/native/windowsaudio/...
```

任何既有失败必须先记录并判断是否阻断本计划，不能通过删除用户改动恢复基线。

## Task 2：共享弹窗默认值红灯与实现

**文件：**

- 修改：`frontend/tests/roomVoiceControls.test.mjs` 或新增专用共享设置测试
- 修改：`frontend/src/components/RoomToolsDialog.tsx`

测试先行覆盖：

1. 分辨率首项标签为“原始画质”，不再出现英文 `Original`。
2. 初始 `resolutionKey` 为 `original`。
3. 初始 `fpsValue` 为 `60`。
4. 原始画质提交 `width=0`、`height=0`、`fps=60`。
5. 手动选择固定分辨率或低帧率后仍提交用户选择。

先运行目标 Node 测试，确认因当前英文标签和 `720p30` 默认值形成红灯。随后只修改
弹窗常量和初始状态，使测试转绿，并运行全部前端 Node 测试与 TypeScript/Vite 构建。

## Task 3：建立共享音频解析领域模型和红灯测试

**新增：**

- `internal/gamecast/shareaudio/resolver.go`
- `internal/gamecast/shareaudio/resolver_test.go`

纯数据类型至少包含：

- `ProcessInfo`：PID、父 PID、规范化可执行文件完整路径、Windows 会话 ID；
- `AudioSession`：活动音频会话 PID；
- `WindowTarget`：窗口 PID、PartyLAN PID；
- `Target`：捕获 PID、`CaptureIncludeProcessTree`、应用身份和选择原因。

红灯测试覆盖：

1. 窗口 PID 的子树已有活动音频会话时选择窗口 PID。
2. 音频会话位于兄弟进程时选择最近的安全同应用祖先。
3. 同应用多个活动会话仍只返回一个应用树目标。
4. 无活动会话时选择最高的安全同应用祖先以覆盖后续子进程。
5. 不同完整路径只有在直接祖先关系、同一安装目录且非通用启动器时才可归为应用族。
6. Explorer、服务宿主、系统阻止进程或跨 Windows 会话时不得向上提升。
7. 窗口属于 PartyLAN 树或 PartyLAN 属于候选树时拒绝。
8. PID 缺失、父子循环、目标退出、路径未知且需要提升时安全降级。
9. 返回模式永远不是 `CaptureSystemMix` 或 `CaptureExcludeProcessTree`。

先写测试并确认缺少解析器形成红灯，再实现无操作系统调用的确定性纯解析算法。

## Task 4：实现 Windows 进程与音频会话快照

**新增或修改：**

- 新增：`internal/gamecast/shareaudio/snapshot_windows.go`
- 新增：`internal/gamecast/shareaudio/snapshot_stub.go`
- 新增或修改：对应 Go 测试
- 复用：`internal/native/windowsaudio.ListActiveAudioSources`

实现要求：

1. 使用当前进程枚举取得 PID、父 PID 和可执行文件名。
2. Windows 构建使用受限查询权限读取规范化完整映像路径和 Windows 会话 ID。
3. 无权限读取单个进程时保留该进程但将路径标记未知，不使整个快照失败。
4. 只把 `ListActiveAudioSources` 返回的活动会话 PID 映射为解析输入。
5. 非 Windows 或能力不可用构建返回明确 unavailable，不伪造安全目标。
6. 日志和错误不得包含 PCM、Opus、令牌、SDP 或 ICE 数据。

Windows API 边界用小接口封装并注入 fake，覆盖路径读取失败、会话 ID 失败、进程退出和
部分快照可用场景。运行普通与 `partylan_native_media` 标签目标测试。

## Task 5：接入窗口音频安全目标

**修改：**

- `app.go`
- `app_test.go`
- `internal/gamecast/sharecatalog/sharecatalog.go`
- 相关 `internal/gamecast/shareaudio` 测试

测试先行覆盖：

1. 窗口共享调用新解析器并使用返回的应用树 PID。
2. 请求中的旧 `AudioMode`、`AudioProcessID` 和进程名不能覆盖解析结果。
3. PartyLAN、自身 WebView2、未知或不安全目标保持 `audioMode=off`。
4. 全屏共享仍固定返回
   `CaptureExcludeProcessTree(os.Getpid())`，不调用窗口解析器。
5. 解析失败后视频共享继续，且没有系统混音回退。

实现时把音频目标解析从 `app.go` 的旧窗口 PID 直绑逻辑收敛到可注入解析器。共享会话
保存已验证应用身份和目标 PID，供后续同应用重试使用。保留现有全屏排除策略和语音
隔离，不改变 GameCast 请求或服务器接口。

## Task 6：初始重试和运行时同应用重绑定

**修改：**

- `app.go`
- `app_test.go`

红灯测试覆盖：

1. 窗口音频首次启动失败后刷新快照并最多重试一次。
2. 第二次解析必须仍为同一安全应用；不同身份、未知身份或不安全目标不得重试。
3. 运行时致命音频错误最多触发一次同应用重绑定。
4. 重绑定成功后保持视频、共享租约、Presence 和 `VoiceSession`，并继续上报音频开启。
5. 重绑定失败后只上报 `audio-disabled` 和 `audioMode=off`。
6. 全屏音频运行时失败不切换系统混音，也不绑定其他进程。
7. 旧共享代际或已结束会话的异步失败不能重启音频。

实现有界计数和同应用身份比较。重绑定只复用当前原始 `HWND`，不得按相似名称搜索。
静音或 PCM 全零不触发重绑定。

## Task 7：动态画质回归证明与必要修复

**修改范围：**

- `internal/gamecast/adaptive/controller_test.go`
- `internal/gamecast/adaptive/metrics_test.go`
- `app_test.go`
- 只有失败测试证明存在缺口时才修改对应生产代码

测试覆盖：

1. 高于 1080p 的原始 `60 FPS` 请求从请求档位开始，随后包含 `1080p60` 以下阶梯。
2. 低分辨率或低帧率上限不会被放大或超出。
3. 本机编码压力和有效观看端弱网样本能够逐级降档。
4. 稳定窗口后能够逐级恢复，且无效、过期、换代或样本不足不会误触发。
5. 编码器准备或 IDR 写入失败回滚，连续失败进入临时冻结。
6. 窗口和全屏原生视频循环都调用同一动态控制路径。

若现有实现已满足测试，不为“显示改动量”而重写控制器；本任务的成果可以是新增回归
证明。若测试暴露与批准设计一致的真实缺口，只做使测试转绿的最小修复。

## Task 8：版本、完整自动化与客户端构建

**修改：**

- `pkg/version/version.go`：在现有广州迁移 `v0.5.5` 基础上提升为 `v0.5.6`
- 必要的 Wails 生成或嵌入资源

依次执行：

```powershell
node --test frontend/tests/*.test.mjs
npm.cmd run build --prefix frontend
go test . ./internal/gamecast/shareaudio/... ./internal/gamecast/adaptive/... ./internal/native/windowsaudio/...
go test ./...
go test -tags partylan_native_media ./...
git diff --check
```

随后使用正式项目脚本构建原生 Windows 客户端，检查：

- `build/bin/PartyLAN.exe` 存在且带 `partylan_native_media` 构建信息；
- `opus.dll` 与 EXE 相邻；
- 客户端启动冒烟成功且测试进程完全清理；
- `server/`、网页部署资源、Nginx 和 SFU 文件仍无工作区差异。

## Task 9：Windows 实机音频标记与弱网验收

在当前机器可用的独立音频环境中，为目标窗口、同应用其他窗口、其他应用、PartyLAN
远端语音和麦克风注入可区分数字标记，并分别记录共享轨与语音轨。

必须验证：

1. 单进程和多进程窗口共享轨含目标标记。
2. 窗口共享轨不含其他应用、PartyLAN 语音或麦克风；同应用多窗口标记允许存在。
3. 全屏共享轨含系统应用标记，不含 PartyLAN 语音或麦克风。
4. 一次安全音频重绑定不重建视频或语音。
5. 施加弱网后画质至少下降一级，解除后至少恢复一级，观看会话保持不变。

若当前物理/虚拟音频环境无法形成独立来源，必须记录缺失的具体设备和未完成项，不得
用单元测试代替实机隔离证据；但这不允许放宽软件隔离策略或改用系统混音。

## Task 10：广州网页冒烟与 `v0.5.6` 生产包

先只读验证 `https://partylanapp.com`：

- HTTPS 首页与静态资源正常；
- 网页用户可以入房；
- 网页共享、停止共享和观看流程正常；
- 原生客户端共享及质量轮询期间网页访问和观看不受影响。

不得通过部署、服务重启或配置修改取得结果。

然后执行正式打包：

```powershell
powershell -ExecutionPolicy Bypass -File scripts\Package-PartyLAN.ps1 -Mode gamecast-client
```

产物要求：

- 新增 `release/PartyLAN-v0.5.6-win64.zip`，报告大小和 SHA256；
- 不覆盖或删除 `release/PartyLAN-v0.5.5-win64.zip`；
- 生产 ZIP 只包含运行文件、运行文档、启动脚本和许可证，不含设计/计划、源码、测试、
  日志、缓存或个人文件；
- ZIP 内 EXE、`opus.dll`、版本信息和哈希清单一致；
- 解压后再次执行客户端启动与进程清理冒烟。

## STOP 条件

- 实现窗口音频必须修改广州节点网页端、SFU、Nginx、路由或服务配置。
- 窗口或全屏音频必须回退 `CaptureSystemMix` 才能工作。
- 候选应用身份只能通过窗口标题或模糊进程名证明。
- 失败测试证明批准设计与当前代码结构冲突，且需要重新设计协议或媒体所有权。
- 自动或实机证据显示 PartyLAN 语音、麦克风或其他应用声音泄漏到不允许的共享轨。
- 继续验证需要未经授权的部署、服务重启、远端推送、PR 或真实密钥变更。
- 连续两到三次诚实尝试仍无法形成与报告问题一致的失败测试。

## 完成标准

- Task 1 至 Task 8 的失败测试、实现、全量自动化和正式客户端构建全部通过。
- Task 9 的每项实机证据逐项报告；环境不足的项目明确保留为未完成，不能伪装通过。
- Task 10 的网页只读冒烟和生产包审计通过，广州节点状态未被修改。
- 新客户端默认原始 `60 FPS`、动态降档/恢复、窗口应用树音频和全屏语音排除均有对应
  自动化或实机证据。
- `v0.5.6` 与 `v0.5.5` 回滚包并存，并分别记录大小和 SHA256。
