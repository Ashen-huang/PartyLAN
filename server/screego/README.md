# partylan 网页端 Screego 补丁集

partylan 当前以网页端为主要开发和交付形态。此目录将 partylan 对
Screego 上游的网页、房间和媒体兼容改动维护为可复现的补丁集；Screego
只是上游实现和内部服务名称，不是对外产品名称。

网页入口为 `https://partylanapp.com`。桌面端 Wails 工程仍保留在仓库根目录，
但不属于本目录的日常网页开发流程。

## 目录职责

- `upstream.lock`：锁定可应用补丁的 Screego 上游版本和基线文件哈希。
- `patches/`：按文件名顺序应用的 partylan 补丁。当前链路覆盖原生网页 UI、
  中文主题、房间外数据面板、设备会话与房间生命周期、悬停控制、播放器控制及
  原始应用图标。
- `apply-patches.ps1`：先验证基线，再顺序应用全部补丁；它只能用于干净的上游
  工作树，不能在已经应用过补丁的目录重复执行。
- `verify-build.ps1`：执行 Screego 网页行为测试、网页构建及相关 Go 回归测试。
- `deploy/`：受版本控制的网页运行时静态脚本、Nginx 位置块模板、控制服务副本和
  Compose 模板。它们是部署输入，不代表可以直接覆盖生产主 Nginx 配置。

`inject.js` 保留 WebRTC、SFU、统计、动态画质和兼容逻辑；网页主界面由原生
React UI 负责，补丁不再通过脚本注入第二套工具栏或面板。

## 获取并验证上游源码

请使用新的临时目录，避免对现有本地工作树重复应用补丁。以下命令从仓库根目录
执行：

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

上游 UI 使用 Yarn v1 锁文件，因此依赖安装必须使用上述 Corepack Yarn 流程。
验证脚本随后会通过 `npm run build` 调用已经安装好的项目构建脚本。

## 构建网页服务镜像

验证通过后，先在上游源码根目录构建嵌入网页资源的 Screego 可执行文件，再生成
新的不可变镜像标签。标签必须是本次发布的新值，不能覆盖正在运行或可回滚的旧
镜像。

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

镜像仓库名前缀 `gamecast-sfu` 是既有部署兼容名称；对外产品和网页文案统一使用
partylan。发布时将已验证的 `$tag` 写入生产 Compose 文件中的 `screego.image`，
并按 [deploy.md](deploy.md) 的备份、灰度和回滚流程执行。

## 仓库级网页验证

补丁构建验证之外，部署输入还应运行控制服务和浏览器运行时测试：

```powershell
Push-Location server
python -m unittest discover -s . -p 'test_*.py'
Pop-Location

$tests = Get-ChildItem .\server -Filter 'test_*.mjs' | Sort-Object Name
foreach ($test in $tests) {
  & node --test $test.FullName
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
```

这些测试覆盖控制服务、网页共享音频、SFU 兼容、WebRTC 自适应、Nginx 静态资源和
原始应用图标的部署约束。真实浏览器联调还应至少覆盖登录、建房/加入、共享、观看、
刷新重连和移动端控制。

## 相关配置

当前 Compose 模板位于 `deploy/docker-compose.yml`，运行时变量名位于
`deploy/runtime.env.example`。敏感值只放在生产服务器的运行时环境文件中，不能
提交到仓库、写入镜像、日志或命令历史。

网页端会话与房间生命周期的兼容配置包括：

- `SCREEGO_SESSION_TIMEOUT_SECONDS=2592000`：设备登录会话保持 30 天。
- `SCREEGO_CLOSE_ROOM_WHEN_OWNER_LEAVES=false`：用户离开不因旧“房主”语义关闭
  仍有成员的房间。

不要仅为了统一产品名称而改名既有 `SCREEGO_*` 或 `GAMECAST_*` 环境变量、接口
和镜像前缀；它们属于已部署服务之间的兼容契约。
