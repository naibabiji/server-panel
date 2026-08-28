# Server Panel — 自托管 VPS 资产管理与服务器监控面板

[English](README_EN.md) | **简体中文**

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-single%20binary-00ADD8?logo=go)](go.mod)
[![Platform](https://img.shields.io/badge/Linux-amd64%20%7C%20arm64-FCC624?logo=linux&logoColor=black)](#安装要求)

Server Panel 是一款安全优先、轻量级的开源自托管服务器管理面板，面向个人站长、独立开发者、小型运维团队和主机服务人员。它把 **VPS/独立服务器资产、网站、客户、服务商、到期续费、Agent 性能监控、告警、备份和日常服务器文件维护** 集中在一个私有面板中。

项目采用 Go + SQLite，单二进制部署，提供中英文界面、内置 HTTPS、分层登录保护和敏感凭据加密。无论你在寻找 VPS 管理面板、服务器资产管理系统、网站到期提醒工具，还是轻量级 Linux 服务器监控面板，都可以用 Server Panel 管理自己的基础设施，而不必把密码和客户资料交给第三方 SaaS。

> 主要开发和测试环境为 Debian 13。其他 Debian/Ubuntu systemd 系统可以尝试使用，但不保证完全兼容。

## 功能亮点

- **资产集中管理**：管理服务器、网站、客户和服务商，记录配置、归属、机房、价格、币种、购买及到期信息。
- **Agent 性能监控**：采集 CPU、内存、磁盘、网络、负载、运行时间和心跳，提供总览及历史曲线。
- **可用性与到期管理**：HTTP 探测、Agent 离线检查、服务器连通性检查、服务器/网站到期提醒和按周期自动顺延。
- **灵活告警通知**：支持 CPU、内存、磁盘、离线、HTTP 探测及到期告警规则，并可通过 SMTP 发送邮件。
- **安全凭据存储**：服务器 SSH 密码、面板密码、网站面板密码、服务商私密备注和 Agent 密钥加密保存。
- **文件与本地存储**：在受限根目录中上传、下载、复制、移动、重命名、压缩和解压文件；查看、挂载、卸载或初始化本机数据盘。
- **备份与恢复**：手动或定时生成包含 SQLite 数据库和加密密钥的完整备份，支持保留数量、邮件发送和上传恢复。
- **更新与系统维护**：检查及安装面板更新，支持自动更新、签名校验、健康检查和失败回滚；可检查并更新 Debian/Ubuntu 系统包。
- **双语界面**：面板原生支持简体中文和 English，可在登录页及后台随时切换。

## 快速安装

在 Debian/Ubuntu 等使用 systemd 的服务器上，以 `root` 执行：

```bash
curl -fsSL https://raw.githubusercontent.com/naibabiji/server-panel/master/install.sh | bash
```

安装程序会下载适配 `amd64` 或 `arm64` 的最新版本，创建自签 TLS 证书、随机访问路径、面板账号、入口 BasicAuth 凭据和 systemd 服务。

安装完成后访问：

```text
https://your-server:8444/<随机路径>/login
```

登录信息会显示在安装日志中。安装脚本会为已启用的 UFW 或 firewalld 放行 HTTPS 端口；如果云厂商还配置了安全组，请在控制台手动放行对应 TCP 端口（默认 `8444`）。

## 界面预览

<table>
  <tr>
    <td width="50%" align="center"><a href="screenshots/dashboard.png"><img src="screenshots/dashboard.png" width="100%" alt="Server Panel 服务器管理仪表盘"></a><br><sub>仪表盘：资产、Agent、到期和告警概览</sub></td>
    <td width="50%" align="center"><a href="screenshots/servers.png"><img src="screenshots/servers.png" width="100%" alt="VPS 和服务器资产管理"></a><br><sub>服务器资产列表</sub></td>
  </tr>
  <tr>
    <td width="50%" align="center"><a href="screenshots/servers-2.png"><img src="screenshots/servers-2.png" width="100%" alt="服务器详情和加密凭据"></a><br><sub>配置、续费、Agent 和加密凭据</sub></td>
    <td width="50%" align="center"><a href="screenshots/websites.png"><img src="screenshots/websites.png" width="100%" alt="网站和域名到期管理"></a><br><sub>网站、域名和到期管理</sub></td>
  </tr>
  <tr>
    <td width="50%" align="center"><a href="screenshots/monitor.png"><img src="screenshots/monitor.png" width="100%" alt="多服务器 Agent 性能监控"></a><br><sub>多服务器监控总览</sub></td>
    <td width="50%" align="center"><a href="screenshots/monitor-2.png"><img src="screenshots/monitor-2.png" width="100%" alt="CPU 内存磁盘网络监控曲线"></a><br><sub>CPU、内存、磁盘、网络和负载历史</sub></td>
  </tr>
  <tr>
    <td width="50%" align="center"><a href="screenshots/settings.png"><img src="screenshots/settings.png" width="100%" alt="Server Panel 系统设置"></a><br><sub>访问、安全、备份、通知和更新设置</sub></td>
    <td width="50%"></td>
  </tr>
</table>

## 详细功能

### 服务器、网站、客户与服务商

- 管理 VPS、独立服务器、共享主机及其他服务器资产。
- 记录 IP、操作系统、CPU、内存、磁盘、带宽、机房、SSH/面板信息、日期、续费周期、价格和状态。
- 服务器可关联客户和服务商，网站可关联服务器和客户；详情页可反向查看相关资源。
- 列表支持搜索、筛选和分页；系统类型和网站类型字典可自定义。
- 服务器支持 HTTP 探测、自动续费日期顺延和独立 Agent 安装密钥。

### 监控与告警

- Agent 上报 CPU、内存、磁盘、网络、系统负载、运行时间和版本/心跳信息。
- 监控页按服务商归类显示服务器，单机页提供历史趋势图。
- 面板本机也采集资源指标，并按可配置的保留天数清理历史数据。
- 告警规则覆盖 Agent 离线、CPU/内存/磁盘过高、HTTP 探测异常、服务器到期和网站到期。
- 支持 SMTP 测试、邮件通知、告警日志及处理状态。

### 文件、磁盘与运维

- 文件管理器仅操作面板数据目录、已挂载存储或管理员显式添加的受限根目录。
- 支持上传、下载、新建目录、重命名、删除、复制、移动、压缩和解压，并记录关键操作。
- 本地存储页可识别磁盘和分区，执行挂载、卸载、权限检查、格式化或初始化，并维护自动挂载。
- 设置页可查看面板操作日志和后台任务状态。

> 格式化和初始化磁盘会清除目标数据。执行前务必核对设备路径并准备独立备份。

### HTTPS、更新与备份

- 支持自签证书、上传证书及 Let's Encrypt ACME 自动签发。
- 面板更新包含下载、签名/校验和验证、数据库及二进制备份、替换、重启、健康检查和失败回滚。
- 可配置每日或每周自动备份、备份保留数量，以及在文件大小允许时通过 SMTP 发送备份。
- 支持从本地备份列表或上传的 `.tar.gz` 归档恢复数据库与加密密钥。

## 安全设计

Server Panel 可能保存基础设施入口和客户资料，因此采用多层保护：

1. 安装时生成随机访问路径，减少通用扫描器发现入口的机会。
2. 可选 BasicAuth 入口验证与独立面板登录共同保护后台。
3. 登录失败、入口密码失败和恶意路径扫描会触发自动封禁；可与 nftables 联动，并支持白名单及手动解封。
4. 敏感凭据加密保存，登录后台后仍需再次输入“查看密码”才能读取。
5. 查看密码连续输错 5 次会清空已保存的敏感凭据，但不会删除服务器、网站、客户、监控或到期数据。
6. 每台 Agent 使用独立密钥；重生成安装命令后旧密钥失效，Agent 只上报监控数据。
7. 可配置可信反向代理和 Cloudflare 真实来源 IP 识别，Cloudflare IP 段会定期刷新。

备份必须同时包含数据库和加密密钥，否则迁移后无法解密原有凭据。设置页生成的完整备份已包含两者。

> 自托管不等于自动安全。建议使用强密码、HTTPS，并按需要把面板放在云防火墙、VPN、Tailscale、WireGuard、Cloudflare 或可信反向代理之后。

## 安装与维护

### 安装要求

- Linux + systemd（Debian 13 为主要目标环境）
- `amd64` 或 `arm64`
- 安装时需要 `root` 权限
- 服务器能够访问 GitHub Releases；Agent 下载可临时指定 GitHub 反代地址

已有安装再次运行安装脚本时，会保留配置、数据库、证书和登录信息，仅替换程序。若要重新生成配置和登录信息：

```bash
curl -fsSL https://raw.githubusercontent.com/naibabiji/server-panel/master/install.sh | INSTALL_MODE=reinstall bash
```

离线安装时，将 `install.sh` 与对应架构的 Release 二进制放在同一目录，然后执行 `bash install.sh`。

常用命令：

```bash
systemctl status server-panel
systemctl restart server-panel
journalctl -u server-panel -f
server-panel --reset-password
```

命令行恢复完整备份：

```bash
systemctl stop server-panel
server-panel -config /www/server/server-panel/config.json -restore-backup=/path/to/server-panel-backup.<timestamp>.tar.gz
systemctl start server-panel
```

## 适用场景与限制

适合管理多台 VPS/独立服务器的个人站长，需要跟踪客户网站、服务商、成本和到期日的小团队，以及希望自托管监控与运维资料的开发者。

Server Panel 不是大型多租户云平台，也不提供复杂 RBAC、审批流、工单系统或容器编排。它更适合作为单一管理员或可信小团队使用的轻量级私有面板。

## 参与项目

欢迎提交 Issue 和 Pull Request。报告问题时请附带系统版本、Server Panel 版本、复现步骤和相关日志，并在公开内容中删除密码、密钥、IP 和客户信息。

## 开源许可

Server Panel 基于 [GNU General Public License v3.0](LICENSE) 开源。

<!--
Search topics: self-hosted server management panel, VPS management panel, Linux server monitoring,
server asset management, website expiry reminder, uptime monitoring, Go SQLite admin panel,
服务器管理面板, VPS 管理面板, 服务器监控, 服务器资产管理, 网站到期提醒, 自托管运维面板
-->
