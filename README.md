# Server Panel

Server Panel 是一个面向单管理员的服务器资产与网站运维管理面板。它使用 Go 单二进制部署，内置 HTTPS、登录保护、SQLite 数据库和 Agent 探针，适合在自己的 VPS 或独立服务器上集中管理服务器、网站、客户、服务商、监控和更新。

## 主要功能

- 服务器资产管理：记录 VPS、独立服务器、共享主机等资产信息，包括 IP、系统、配置、到期时间、价格、客户、服务商和备注。
- 网站管理：按服务器归属管理网站、域名、客户、到期时间、状态和控制面板信息。
- 客户与服务商管理：维护客户资料、服务商资料，并关联服务器和网站资产。
- Agent 监控：支持在被管理服务器上安装独立 Agent，上报 CPU、内存、磁盘、网络和心跳数据。
- 告警规则：支持 CPU、内存、磁盘、网站探测等告警规则，记录告警日志，并可配置邮件通知。
- HTTP 探测：可对网站或服务进行可用性检查，辅助发现异常状态。
- 凭据保护：服务器 SSH 密码、服务器面板密码、网站面板密码会加密保存；查看敏感凭据时需要单独的查看密码授权。
- 面板安全：强制 BasicAuth、面板登录、Session、CSRF、防爆破、随机访问路径、安全响应头和扫描防御。
- TLS 访问：安装时自动生成自签证书，也支持在面板设置里配置域名和证书。
- 在线更新：支持面板检查更新、一键更新、自动更新策略，以及 SHA256 + Ed25519 签名校验。
- 系统更新：支持检查 apt 可升级包，并可在面板内执行系统包更新。

## 系统要求

- Linux + systemd
- Debian/Ubuntu 推荐，Debian 13 为主要目标环境
- amd64 或 arm64 架构
- root 权限安装
- 服务器可访问 GitHub Releases；如果无法直连，可使用下方离线安装方式

## 一键安装

在 Debian/Ubuntu 等 systemd 服务器上使用 root 执行：

```bash
curl -fsSL https://raw.githubusercontent.com/naibabiji/server-panel/master/install.sh | bash
```

安装脚本会自动下载 GitHub 最新 Release 中匹配当前架构的 `server-panel-linux-amd64` 或 `server-panel-linux-arm64` 二进制，生成自签 TLS 证书、随机访问路径、面板密码和 BasicAuth 密码。

指定版本安装：

```bash
curl -fsSL https://raw.githubusercontent.com/naibabiji/server-panel/master/install.sh | VERSION=v1.4.0 bash
```

已有安装时，默认会保留配置、数据库、证书和登录信息，只覆盖升级二进制。

如果需要重新生成配置和登录信息：

```bash
curl -fsSL https://raw.githubusercontent.com/naibabiji/server-panel/master/install.sh | INSTALL_MODE=reinstall bash
```

离线/手动兜底：把 `install.sh` 和 release 附件 `server-panel-linux-amd64` 或 `server-panel-linux-arm64` 放在同一目录，执行：

```bash
bash install.sh
```

常用管理命令：

```bash
systemctl status server-panel
systemctl restart server-panel
journalctl -u server-panel -f
server-panel --reset-password
```

从设置页生成的备份（含数据库和加密密钥）恢复数据，比如服务器重装后：

```bash
systemctl stop server-panel
server-panel -config /www/server/server-panel/config.json -restore-backup=/path/to/server-panel-backup.<timestamp>.tar.gz
systemctl start server-panel
```

## 授权协议

本项目基于 [GPL-3.0](LICENSE) 协议开源。
