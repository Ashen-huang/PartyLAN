# GameCast / Screego 服务迁移与开发参考

> Scope: 从一次实际 SSH 备份、迁移与联调会话中提取可复用信息，整理为 PartyLAN 当前依赖的 GameCast / Screego 服务端参考文档。本文档只保留开发和运维需要的事实，不记录密码等敏感凭据。

## 1. 文档目的

这份文档用于沉淀下面几类信息：

- 已迁移成功的 GameCast / Screego 服务端拓扑
- 旧机与新机的角色、目录和端口关系
- 迁移时必须一起备份的“项目外依赖”
- 当前新服务器的实际运行状态
- PartyLAN 客户端后续开发应依赖的服务端边界

本文档基于一次真实操作会话整理，关键事件时间如下：

- 2026-06-28：从旧服务器完成主项目备份
- 2026-07-04：完成新服务器恢复、启服与健康检查
- 2026-07-08：将会话信息提炼为仓库内参考文档

## 2. 基础结论

当前 PartyLAN 依赖的网页侧服务不是“原版 Screego 单机目录”，而是一个组合部署：

- `Screego` 负责房间、网页入口和基础共享能力
- `nginx` 负责 HTTPS 暴露和反向代理
- `gamecast-sfu` 负责 SFU WebSocket 与媒体中转
- 一个独立的 register/history/stats/room-state 服务负责扩展接口
- 前端还依赖注入脚本与网页资源

因此，`/root/screego` 只包含项目主体，并不等于完整可迁移系统。

## 3. 主机角色

### 3.1 旧服务器

- 角色：原始运行节点
- IP：`103.236.92.3`
- SSH 端口：`29941`
- 登录用户：`root`
- 真实项目目录：`/root/screego`

会话早期给出的 `/screego` 路径不准确，后续实际确认目录为 `/root/screego`。

SSH 配置修正示例：

```sshconfig
Host 103.236.92.3-29941
    HostName 103.236.92.3
    Port 29941
    User root
    ForwardX11 yes
    ForwardX11Trusted yes
    RequestTTY yes
```

### 3.2 新服务器

- 角色：迁移后运行节点
- IP：`113.142.217.42`
- SSH 端口：`22`
- 登录用户：`root`
- 当前公网入口：`https://113.142.217.42:5050/`

## 4. 实际部署结构

迁移过程确认，这套服务的关键内容分布在多个目录和系统组件中：

- `/root/screego`
  - Screego 项目主体
  - 历史数据与主业务目录
- `/etc/gamecast-sfu`
  - 运行时配置
  - 外部 IP 等环境变量
  - 运行时密钥相关内容
- `/root/gamecast-sfu`
  - SFU 程序与相关二进制
- `/var/www/screego`
  - 前端注入资源与网页静态侧内容
- `systemd`
  - 服务定义与启动方式
- Docker 本地镜像
  - 关键镜像：`gamecast-sfu:v2`

还明确提到以下内容不在单独项目目录内，但属于完整恢复所必需的资产：

- `users.htpasswd`
- TLS 证书
- systemd 服务定义
- 本地 Docker 镜像
- SFU 二进制与运行配置

## 5. 迁移所需备份清单

会话中最终确认，以下备份材料构成了一套可恢复迁移包：

- `screego-backup-full.tar.gz`
- `gamecast-runtime-backup.tar.gz`
- `gamecast-sfu-binaries-backup.tar.gz`
- `screego-systemd-backup.tar.gz`
- `gamecast-sfu-image-v2.tar`

其中第一份只覆盖项目主体，不足以保证 100% 原样恢复；必须与其余四份组合使用。

已知的校验事实：

- `screego-backup-full.tar.gz` 在旧机与本地之间做过大小校验
- 校验值为 `1768783` 字节
- 更早的一份流式备份不完整，未作为最终交付物使用

## 6. 迁移恢复策略

恢复到新服务器时，采用的是保守迁移策略：

- 先上传全部备份包
- 如新机已有同名目录，先移动到 `/root/screego-migration-prebackup-*`
- 再恢复项目目录、运行时配置、systemd 服务和 Docker 镜像
- 最后根据新机公网地址更新外部 IP 配置并启服

会话中保留下来的一个预备份目录示例：

- `/root/screego-migration-prebackup-20260704T022919Z`

迁移期间还生成过一个自动化部署脚本：

- `deploy_screego_to_new_host.py`

从会话表现看，这个脚本后续做过几项重要修正：

- 改为更兼容 Ubuntu 22.04 的 compose 依赖安装方式
- 恢复阶段改为上传独立 shell 脚本执行，避免远程多行引号解析问题
- 自动补启 nginx 的 `screego` 站点

## 7. 新服务器当前运行状态

截至 2026-07-04，会话已确认新服务器迁移完成，且外部访问正常。

### 7.1 可访问地址

- `https://113.142.217.42:5050/`

### 7.2 健康检查结果

- 外部访问返回 `HTTP 200`
- 本机 `127.0.0.1:5050` 健康检查通过
- `5050` 已监听

### 7.3 核心服务状态

已确认以下服务处于可用状态：

- `screego-register`: `active`
- `gamecast-sfu`: `active`
- `nginx`: `active`
- Docker 容器 `screego`: 运行中

已确认监听端口：

- `5050`
- `5052`
- `5053`
- `5060`

### 7.4 已更新的运行时配置

迁移后明确修改过：

- `SCREEGO_EXTERNAL_IP=113.142.217.42`
- `GAMECAST_SFU_PUBLIC_IP=113.142.217.42`

配置位置来自运行时目录：

- `/etc/gamecast-sfu/runtime.env`

## 8. nginx 与代理关系

迁移过程中暴露了一个关键事实：服务恢复后并不是“文件到位即可访问”，还依赖 nginx 站点启用状态。

实际遇到的问题：

- `screego` 配置文件在 `sites-available`
- 但没有链接到 `sites-enabled`
- 导致 nginx 初始只监听默认 `80` 端口
- 补上站点启用后，`5050` 才开始正常监听

因此，恢复验证时至少要检查：

- `sites-available/screego` 是否存在
- `sites-enabled` 是否已建立对应链接
- nginx reload 后 `5050` 是否在监听

## 9. 已知冲突与限制

### 9.1 TLS 仍为自签证书

当前新服务器使用的是迁移后为新 IP 重新生成的自签证书。

影响：

- 浏览器仍会出现证书警告
- 原生客户端需要证书信任或指纹固定策略

建议：

- 生产环境优先绑定域名并换为可信 CA 证书
- 若短期继续用 IP + 自签证书，客户端侧必须保留显式信任流程

### 9.2 `screego-proxy` 不应继续启用

迁移后确认：

- `screego-proxy` 与当前 Screego 容器争抢 `5060` 端口
- 当前有效链路是 nginx 直接代理到 Screego 容器
- 为避免 systemd 反复失败重启，`screego-proxy` 已被禁用但未删除

因此，不应再把 `screego-proxy` 视为当前生产链路的一部分。

## 10. 对 PartyLAN 开发的重要边界

从这段会话可以明确，PartyLAN 客户端现在应对齐的是“已经跑通的 GameCast / Screego 服务端协议”，而不是旧的自研 P2P 路线。

服务端边界至少包括：

- `GET /`
- `POST /api/register`
- `GET /api/history`
- `POST /api/history`
- `GET /api/stats`
- `POST /api/stats`
- `GET /api/room-state`
- `POST /api/room-state`
- `WebSocket /sfu-ws`

会话后期还补充出一个更适合客户端房间列表的接口：

- `GET /api/rooms`

这与仓库现状是对齐的：

- 默认运行模式已经是 `gamecast-client`
- 当前代码和设计文档都围绕 GameCast/Screego 服务端边界工作
- 客户端需要处理自签证书信任、房间列表、历史记录、room-state 与 SFU WebSocket

## 11. 开发与运维建议

### 11.1 不要再把完整恢复理解为“复制 `/root/screego`”

任何后续备份、迁移、容灾或环境重建，都至少要同时覆盖：

- 项目目录
- `/etc/gamecast-sfu`
- `/root/gamecast-sfu`
- `/var/www/screego`
- systemd 服务
- Docker 镜像
- 认证文件与 TLS 证书

### 11.2 对仓库内文档和脚本做进一步固化

后续值得补齐：

- 一份正式的恢复 runbook
- 一份服务拓扑图
- 一份接口契约文档
- 一份证书与凭据管理说明
- 一份备份校验与回滚说明

### 11.3 凭据不要入库

原始会话中包含 SSH 密码等敏感信息。本文档刻意不记录这些内容。

建议后续统一改为：

- 密码与密钥存放在受控密码库
- 仓库只保留主机角色、端口、目录和操作步骤
- 自动化脚本通过环境变量或安全注入方式读取凭据

## 12. 与现有设计文档的关系

本文档偏“事实记录”和“运维参考”，不替代客户端架构设计。

相关设计文档：

- [2026-07-04-partylan-gamecast-client-refactor-design.md](./2026-07-04-partylan-gamecast-client-refactor-design.md)
- [2026-07-04-partylan-gamecast-client-feature-completion-design.md](./2026-07-04-partylan-gamecast-client-feature-completion-design.md)

如果后续要改 PartyLAN 客户端，应把本文档当作服务端现状基线，再结合上述设计文档推进实现。
