# v1.4.57 Agent 跟随面板自动更新，告警防抖

本版本让 Agent 跟随面板版本自动升级，并避免同一个问题反复发送告警邮件。

## 新功能

- **Agent 自动更新**：面板升级后，支持自动更新的 Agent 会在下次上报时收到新版本通知，自动下载、校验发布签名（Ed25519）和 SHA-256 后替换并重启；新版本 3 分钟内未成功上报会自动回滚。失败后 6 小时再试，不发送邮件，失败原因显示在面板中。Agent 只升级不降级。
- **需要处理的服务器自动标记**：服务器列表显示“Agent 需手动升级”和“Agent 更新失败”标签；服务器详情页显示 Agent 版本、相应提示，并新增“升级 Agent”命令。
- **“升级 Agent”命令**：沿用服务器上现有的 Agent 配置和 Key，面板无需任何改动；安装最新 Agent 和自动更新组件，任一步骤失败都会自动恢复原有 Agent。

## 改进

- **告警防抖**：同一服务器同一类告警在恢复后 60 分钟内再次出现时，重新打开原告警，不再重复发送邮件。适用于 HTTP 探测、CPU、内存、磁盘和离线告警；从“探针失联”升级为“服务器离线”仍会通知。
- 安装命令和升级命令所用的 Agent 校验和由面板先校验发布签名后写入命令，GitHub 反代无法替换安装的程序。
- 安装命令安装与面板相同的版本。

## 升级说明

- **已安装的旧 Agent 需要手动升级一次**：旧版 Agent 不包含自动更新组件。请在服务器详情页点击“升级 Agent”生成命令，在对应服务器上以 root 执行一次；之后的版本会跟随面板自动升级。
- 数据库升级 1.8.0：只新增字段，回退到旧版本面板仍可使用。
- 每次面板发布后，各 Agent 会自动重启一次，只需几秒。
- 生成 Agent 安装或升级命令时，不再支持部署在局域网内的 GitHub 反代。

---

# v1.4.57 Agents Follow the Panel Version; Alert Debounce

- **Agent self-update**: after a panel upgrade, update-capable Agents receive the new version in their next report, then download it, verify the Ed25519 release signature and SHA-256, replace the binary and restart. If the new version does not report successfully within 3 minutes, the Agent rolls back automatically. A failed update is retried after 6 hours, sends no email, and the failure reason is shown in the panel. Agents never downgrade.
- **Servers that need attention are marked**: the server list shows "Agent needs manual upgrade" and "Agent update failed" badges. Server details show the Agent version and the matching notice, plus a new "Upgrade Agent" command.
- **"Upgrade Agent" command**: keeps the server's existing Agent config and key, so nothing changes in the panel. It installs the latest Agent and the self-update helper, and restores the previous Agent if any step fails.
- **Alert debounce**: when the same alert type on the same target recurs within 60 minutes of being resolved, the original alert is reopened without another email. This applies to HTTP probe, CPU, memory, disk and offline alerts; escalation from "Agent lost" to "server offline" still notifies.
- Install and upgrade commands embed an Agent checksum that the panel has verified against the release signature, so a GitHub proxy cannot substitute the installed binary. The install command installs the panel's own version.
- **Existing Agents need one manual upgrade**: run the "Upgrade Agent" command once as root on each server. Later versions then follow the panel automatically.
- Database upgrade 1.8.0 only adds columns; rolling back to an older panel version remains possible.
- Each panel release restarts every Agent once, which takes a few seconds.
- LAN-hosted GitHub proxies are no longer supported for generating Agent install or upgrade commands.
