# PartyLAN 原生共享动态画质与采集健康保护实施计划

**目标：** 按已确认设计实现原生单轨全局动态画质、观众质量反馈、NVENC 热切档和采集健康保护。

**设计依据：** `docs/superpowers/specs/2026-07-13-partylan-native-adaptive-quality-design.md`

## Task 1：实现纯 Go 自适应策略（测试先行）

**文件：**

- 新增：`internal/gamecast/adaptive/controller.go`
- 新增：`internal/gamecast/adaptive/controller_test.go`

1. 用失败测试锁定画质梯度、用户上限、24 FPS 优先和不放大规则。
2. 用可控时间测试本机 5 取 4、末 3 连续、6 秒观察与 15 秒降档冷却。
3. 测试观众 7 取 5、末 3 连续、15 秒观察及“丢包 + 第二证据”组合条件。
4. 测试多观众最差已确认样本、8 秒过期、代际重置、30 秒恢复和服务不可用降级。
5. 实现无媒体依赖的状态机，使目标测试通过。

## Task 2：实现服务端房间质量快照（测试先行）

**文件：**

- 修改：`server/register.py`
- 修改：`server/screego/deploy/register.py`
- 修改：`server/test_register_control_api.py`
- 修改：`internal/gamecast/client.go`
- 修改：`internal/gamecast/client_test.go`

1. 先增加身份、房间成员、房主读取、房间隔离和 8 秒过期的失败测试。
2. 新增 `/api/quality/report` 与 `/api/quality/snapshot`。
3. 只接受规范化差值字段，内存保存约 15 秒，返回安全视频出站预算与有效观众样本。
4. 在 Go client 增加上报和读取类型化 API，并覆盖请求/响应测试。
5. 同步部署副本，运行 Python 和 Go 目标测试。

## Task 3：修正观众端统计并提高上报频率（测试先行）

**文件：**

- 修改：`frontend/src/media/gamecastReceiver.ts`
- 修改：`frontend/src/types.ts`
- 修改：`frontend/src/App.tsx`
- 修改：`frontend/tests/*`

1. 先测试累计收包、丢包、丢帧转换为增量及计数器重置。
2. 上报增加连接代际、候选对标识、包增量、丢包率和丢帧增量。
3. 将原生 GameCast 观众质量上报节奏调整为 2 秒；其他统计展示保持 1 秒。
4. 上报失败不得影响播放。

## Task 4：接入本机采样与动态编码器（测试先行）

**文件：**

- 修改：`app.go`
- 修改：`app_test.go`
- 修改：`internal/app/events.go`
- 修改：`internal/gamecast/nativehost/host.go`
- 修改：`internal/gamecast/nativehost/*_test.go`

1. 先测试新鲜帧和重复帧分离、编码耗时采样及档位决定。
2. 原生 Host 后台轮询本房间质量快照；接口失败只记录诊断。
3. 编码循环改为按当前档位动态节拍，记录采集、编码和发送压力。
4. 档位变化时创建新 NVENC 编码器、强制 IDR，成功写入后替换旧实例。
5. 配置、首帧或写轨失败时继续旧流，应用失败冷却和冻结。
6. 扩展诊断事件，显示用户档位、实际档位、状态和原因。

## Task 5：实现采集健康旁路（测试先行）

**文件：**

- 新增：`internal/gamecast/capturehealth/health.go`
- 新增：`internal/gamecast/capturehealth/health_test.go`
- 修改：`app.go`
- 修改：相关 Windows 采集实现和测试

1. 测试静态画面不误判、主黑备非黑才切换、主无时间戳与备用变化组合条件。
2. 增加低成本黑帧取样和备用 Windows 截图探针。
3. 全屏主采集确认异常后切换系统截图后端，保持房间和音频。
4. 主后端每 10 秒探测，连续 3 次健康后切回；主备失败进入低频重试。

## Task 6：验证、构建与部署准备

1. 运行新 Go、Python、前端目标测试。
2. 运行 `go test ./...` 和 `go test -tags partylan_native_media ./...`。
3. 运行前端全部测试和 `npm run build`。
4. 使用正式脚本构建原生客户端并记录 EXE 时间、大小和 SHA256。
5. 检查服务端部署文件一致性；部署前运行服务端测试，不在测试失败时切换生产。
6. 记录限速、性能不足、恢复、静态桌面和全屏采集复测指标。

## STOP 条件

- SFU 或 Pion 不能在同一已协商 H.264 轨道内接受低于初始上限的分辨率切换。
- 现有登录/房间成员模型无法安全识别原生房主和观众。
- Windows 备用采集不能在当前进程和 DPI 模式下得到与共享源一致的画面。
- 原生编码器无法在旧实例运行时创建第二实例，且原地重配会中断连接。

## 执行记录

- 已实现 `native-global-adaptive-v1` 纯 Go 控制器，覆盖完整档位、组合触发、观察期、
  冷却、逐级恢复、样本去重和切档失败回滚。
- 已实现本机新鲜采集 FPS、重复比例、编码 FPS、p95 编码预算和写入失败采样；静态
  桌面稀疏更新不会单独触发性能降档。
- 已实现受身份保护的 `/api/quality/report` 与 `/api/quality/snapshot`，并同步根服务和
  部署副本；客户端和普通浏览器观众均按 2 秒节奏上报有效差值。
- 已实现后台 NVENC 预热、SPS/PPS + IDR 校验、同轨接管、失败回滚和可选服务器安全
  出站预算除以活跃观众数的码率重配。
- 已实现 DXGI 与系统截图主备对照：黑帧需备用非黑佐证，无帧需备用画面变化佐证；
  备用模式连续三次主采集健康后恢复。
- 已补 Nginx `/api/quality` 路由、浏览器缓存版本和诊断面板实际档位信息。生产服务器
  尚未部署，本轮只完成实现、自动测试和客户端构建。
- 前端 28 项 Node 测试和生产构建通过；服务器 17 项 Python 测试、19 项 WebRTC
  Node 测试通过；`go test ./...` 与 `go test -tags partylan_native_media ./...` 通过。
- 正式脚本已生成 `build/bin/PartyLAN.exe`，原生媒体元数据验证通过并复制 `opus.dll`。
