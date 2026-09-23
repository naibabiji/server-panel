# v1.4.55 安全更新

本版本集中修复发布供应链、扫描防御资源上限和外部链接校验问题，建议所有用户升级。

## 安全改进

- 发布版改用 Go 1.26.6 构建，并将 `golang.org/x/crypto` 升级到 v0.56.0，修复旧版二进制中的已知标准库和 SSH 相关漏洞。
- GitHub Actions 全部固定到审核过的完整提交 SHA；构建任务仅保留只读仓库权限，只有发布任务拥有写入 Release 的权限。
- 扫描防御记录最多保存 256 个 UTF-8 字节的原因文本，避免超长请求路径无限写入数据库。
- IPv4/IPv6 nftables 封禁集合现在具有容量上限和内核超时；集合已满或单次写入失败时，数据库和应用层封禁仍然生效。
- 服务器和网站面板地址只接受有效的 HTTP/HTTPS URL。旧数据或备份中的不安全地址会显示为普通文本，不再生成可点击链接。

## 升级说明

- 无数据库结构迁移，无需修改现有配置。
- 升级时会重建 Server Panel 自己的 `inet sp_filter` nftables 表，并从数据库恢复仍然有效的封禁；不会修改其他防火墙表或规则。
- 模板和前端资源已随主程序二进制重新构建。

---

# v1.4.55 Security Update

This release hardens the release supply chain, bounds scan-defense resources, and validates external panel links. Upgrading is recommended for all users.

- Release binaries are built with Go 1.26.6 and `golang.org/x/crypto` v0.56.0.
- GitHub Actions are pinned to reviewed commit SHAs with job-scoped permissions.
- Scan reasons are limited to 256 UTF-8 bytes; IPv4/IPv6 nftables ban sets now have capacity limits and per-entry timeouts.
- Server and website panel URLs must use HTTP or HTTPS. Unsafe legacy values are rendered as non-clickable text.
- No database schema or configuration migration is required.
