# partylan

`partylan` 是面向浏览器的低延迟屏幕共享与房间协作服务。网页端是当前的主力产品；Windows/Wails 客户端仅保留为兼容接入方式。

在线入口：[partylanapp.com](https://partylanapp.com)

## 当前能力

- 中文网页端登录、注册、创建房间与加入活动房间。
- 浏览器屏幕或窗口共享，默认 `720p / 30 FPS`，默认共享窗口或标签页音频。
- 成员列表、共享设置、连接数据和全屏控制均使用原生网页 UI；桌面浏览时控制区会按播放器式鼠标活动显隐，移动端保留底部收纳布局。
- 登录会话在设备上保持 30 天；房间不再区分房主，成员尚未全部离开时房间不会因创建者离开而关闭。
- 屏幕共享和房间语音分别由独立 SFU 服务提供，运行时会采集质量遥测并执行兼容的自适应策略。

## 网页端架构

网页并不是通过脚本注入重绘出来的。`server/screego/patches/` 将锁定版本的 Screego React UI 补丁化为 partylan 的登录、房间、成员、共享和设置界面；补丁后的 UI 被编译并嵌入 Screego 二进制。

`server/screego/deploy/inject.js` 仍用于媒体、SFU、质量自适应和遥测兼容层，不负责注入或替换页面 UI。

| 组件 | 职责 |
| --- | --- |
| Screego + 原生 React UI | 浏览器房间、屏幕共享、WebRTC 屏幕 SFU |
| `server/register.py` | 注册、登录、房间控制、在线状态、共享租约和质量遥测 |
| `server/voice-sfu` | 独立的房间语音 SFU |
| Nginx | HTTPS、静态运行时脚本与反向代理 |

## 网页端开发

网页 UI 基于指定的 Screego 上游提交构建。请使用新的工作目录，避免对已经应用过补丁的目录重复执行补丁。

```powershell
$source = 'tmp/screego-upstream-clean'
git clone https://github.com/screego/server.git $source
git -C $source checkout f946cee83b61d9f47c2f1c2238fb31f4042b628a

.\server\screego\apply-patches.ps1 -SourceRoot $source

Push-Location "$source\ui"
corepack yarn install --frozen-lockfile
Pop-Location

.\server\screego\verify-build.ps1 -SourceRoot $source
```

验证完成后，在同一个工作目录中构建 Screego 二进制与新的不可变镜像标签。不要复用或覆盖正在使用的镜像标签。

```powershell
Push-Location $source
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
go build -o screego .
$tag = "gamecast-sfu:partylan-web-$(Get-Date -Format yyyyMMdd-HHmmss)"
docker build -t $tag .
Pop-Location
```

网页控制服务的回归检查：

```powershell
Push-Location server
python -m unittest discover -s . -p 'test_*.py'
Pop-Location

$tests = Get-ChildItem .\server -Filter 'test_*.mjs' | Sort-Object Name
foreach ($test in $tests) {
  node --test $test.FullName
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
```

更详细的补丁说明见 [server/screego/README.md](server/screego/README.md)，生产发布和回滚步骤见 [server/screego/deploy.md](server/screego/deploy.md)。

## 生产发布

生产环境必须使用服务器上的运行时环境文件，并只更新目标服务的不可变镜像引用。不要用仓库模板整份覆盖生产 Nginx 配置或运行时环境文件。

```bash
docker compose --env-file /etc/gamecast-sfu/runtime.env config
docker compose --env-file /etc/gamecast-sfu/runtime.env up -d --force-recreate screego
```

只有本次发布同时修改了语音 SFU 镜像或配置时，才单独重建语音服务：

```bash
docker compose --env-file /etc/gamecast-sfu/runtime.env up -d --force-recreate voice-sfu
```

发布前先备份当前 compose、注册服务、静态运行时脚本和 Nginx 配置；发布后检查首页、登录、创建或加入房间、共享、观看、语音以及 `nginx -t`。完整安全流程见上述部署文档。

## 目录

| 路径 | 用途 |
| --- | --- |
| `server/screego/patches/` | 锁定 Screego 上游的网页 UI 和服务补丁链 |
| `server/screego/deploy/` | 网页部署模板、Nginx 与运行时兼容脚本 |
| `server/register.py` | 注册与房间控制服务 |
| `server/voice-sfu/` | 房间语音 SFU |
| `frontend/`、`app.go` | Windows/Wails 兼容客户端，不是网页端源码 |
| `scripts/Start-PartyLAN.ps1` | Windows 兼容客户端的历史启动脚本 |

## Windows 兼容客户端

Windows 客户端不再是主开发入口，但仍可按需构建：

```powershell
.\scripts\Start-PartyLAN.ps1 -Action build
```

构建产物为 `build/bin/partylan.exe`。历史脚本文件名保留 `PartyLAN`，以避免破坏既有自动化；它们会构建和启动新的 `partylan.exe`。

## 命名与兼容性

所有用户可见的产品名称统一为小写 `partylan`。`Screego` 是上游和内部实现组件；`GameCast` 仅作为历史 API、模式、环境变量或数据类型名称保留。为避免中断已有部署和本地数据，以下内部标识不会因品牌调整而改名：

- Go 模块路径 `PartyLAN`
- `PARTYLAN_*`、`SCREEGO_*`、`GAMECAST_*` 环境变量
- 已发布的历史脚本名、存储目录和兼容接口

不要将密码、令牌、`runtime.env`、TLS 私钥或生产用户数据提交到仓库。
