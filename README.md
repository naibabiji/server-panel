# Server Panel — 自托管 VPS 资产管理与服务器监控面板

> 🌐 **English documentation:** [Read the full English README →](README_EN.md)

**简体中文说明**

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-single%20binary-00ADD8?logo=go)](go.mod)
[![Platform](https://img.shields.io/badge/Linux-amd64%20%7C%20arm64-FCC624?logo=linux&logoColor=black)](#安装要求)

Server Panel 是一款安全优先、轻量级的开源自托管服务器管理面板，面向个人站长、独立开发者、小型运维团队和主机服务人员。它把 **VPS/独立服务器资产、网站、客户、服务商、到期续费、Agent 性能监控、告警、备份和日常服务器文件维护** 集中在一个私有面板中。

项目采用 Go + SQLite，单二进制部署，提供中英文界面、内置 HTTPS、分层登录保护和敏感凭据加密。无论你在寻找 VPS 管理面板、服务器资产管理系统、网站到期提醒工具，还是轻量级 Linux 服务器监控面板，都可以用 Server Panel 管理自己的基础设施，而不必把密码和客户资料交给第三方 SaaS。

> 主要开发和测试环境为 Debian 13。其他 Debian/Ubuntu systemd 系统可以尝试使用，但不保证完全兼容。

## 安全不是附加功能，而是核心设计

Server Panel 针对两个不同层次的问题设计防线：**攻击者攻进面板有多难？即使拿到后台会话，他取得已保存密码又有多难？**

| 攻击阶段 | 攻击者需要突破的防线 | 即使突破后的限制 |
|---|---|---|
| 发现并进入面板 | 随机访问路径、HTTPS、可选 BasicAuth、独立面板登录、失败限速与自动封禁、恶意扫描识别、nftables 联动 | 突破入口或拿到普通后台会话，仍不能直接读取已保存的敏感凭据 |
| 读取敏感信息 | 独立“查看密码”、bcrypt 校验、AES-256-GCM 加密、与会话及来源 IP 绑定的单次查看令牌、2 分钟有效期 | 连续猜错 5 次会清空已保存的服务器和网站密码副本，而不是继续开放暴力破解机会 |

因此，**面板登录权限不等于敏感信息读取权限**。攻击者仅拿到登录密码、浏览器会话或普通后台访问能力时，还需要突破第二套独立验证，才能调用敏感信息读取接口。完整威胁模型和边界见[安全设计](#安全设计)。

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

### 1. 攻进面板需要突破什么

Server Panel 不把安全寄托在单个登录表单上，而是让攻击者依次面对多层边界：

1. **发现入口**：安装时生成随机访问路径，常见的 `/admin`、`/login` 和漏洞扫描路径不会直接暴露面板入口。
2. **通过入口验证**：可选 BasicAuth 位于面板登录之前，与面板账号密码相互独立。
3. **避免被封禁**：入口密码或面板登录连续失败会记录来源 IP 并触发封禁；恶意扫描未知路径也会触发防御。
4. **突破网络拦截**：安装了 nftables 时，封禁来源会在面板端口被系统防火墙直接阻断；没有 nftables 时仍会在应用层拒绝。
5. **伪造有效会话**：后台使用服务端会话、HttpOnly/Secure Cookie、30 分钟滑动有效期和 CSRF 校验保护已登录操作。
6. **绕过真实 IP 判断**：只有显式信任的反向代理或已验证的 Cloudflare 网络才能提供真实来源 IP，减少伪造转发头绕过封禁的机会。

这些措施不能让互联网服务“绝对无法攻破”，但显著提高了批量扫描、密码猜测、会话滥用和持续暴力尝试的成本。

### 2. 已进入后台，为什么仍难以取得敏感信息

服务器 SSH 密码、服务器/网站面板密码和服务商私密备注不会因后台登录成功而直接返回：

1. **独立查看密码**：查看密码与入口密码、面板登录密码分离，并使用 bcrypt cost 12 保存哈希。
2. **加密静态数据**：敏感内容使用 AES-256-GCM 加密；数据库中保存的是带随机 nonce 的密文，不是可直接读取的明文。
3. **短时单次授权**：查看密码验证成功后只生成一次性查看令牌。令牌与当前服务端会话和来源 IP 绑定，有效期仅 2 分钟，读取一次后立即失效。
4. **接口再次检查**：敏感信息接口不会只检查“是否登录”，还会消费有效查看令牌；普通后台会话本身不足以读取密码。
5. **暴力破解止损**：同一来源连续输错查看密码 5 次后，系统会清空已保存的服务器 SSH 密码、服务器面板密码和网站面板密码副本，并吊销查看令牌。服务器、网站、客户、监控和到期记录不会被删除。
6. **Agent 隔离**：每台 Agent 使用独立密钥；重新生成后旧密钥失效。Agent 只上报监控数据，不会读取面板保存的密码。

这意味着：**攻击者即使窃取普通面板账号或一个已登录会话，也不能直接导出敏感凭据。** 他还必须在触发清空机制前得到独立查看密码，并满足令牌、会话、来源 IP 和有效期约束。

### 必须明确的安全边界

如果攻击者已经获得面板宿主机的 `root` 权限、能够任意读取面板进程内存，或能替换正在运行的二进制，那么主机已经完全失陷。由于面板必须能够为合法用户解密凭据，任何软件都无法在这种权限级别下承诺密码仍绝对安全。此时应立即隔离主机、轮换全部凭据，并从可信备份重建环境。

备份同样属于敏感资产：完整备份同时包含数据库和解密密钥，以保证灾难恢复后仍能读取原凭据。因此应限制备份文件权限，并把异地副本存放在独立可信的位置。

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
