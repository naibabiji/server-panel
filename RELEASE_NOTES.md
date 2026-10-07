# v1.4.58 性能监控详情显示运行时间

本版本在服务器性能监控详情页补充显示最近一次采集的系统运行时间。

## 改进

- 性能监控详情页新增“运行时间”卡片，与 CPU、内存、磁盘和负载指标并列显示。
- 运行时间沿用 Agent 已上报的数据，以天、小时和分钟显示，不增加新的 Agent 协议或数据库字段。
- 尚无性能数据时显示 `--`，避免把缺失数据误显示为运行不足一分钟。
- 大屏幕使用五列指标布局，小屏幕继续使用两列响应式布局。

## 升级说明

- 无数据库迁移、配置变更或安装步骤变化。
- 页面模板和样式嵌入面板二进制，升级面板后即可生效。
- 本版本不改变 Agent 采集或上报协议；支持自动更新的 Agent 仍会按既有策略跟随面板版本升级。

---

# v1.4.58 Show Uptime in Monitoring Details

This release adds the latest reported system uptime to each server's monitoring detail page.

## Improvements

- Added an uptime card alongside CPU, memory, disk, and load metrics on the monitoring detail page.
- Reuses the uptime already reported by the Agent and formats it in days, hours, and minutes, with no new Agent protocol or database field.
- Shows `--` when no performance sample is available instead of incorrectly showing less than one minute.
- Uses a five-column metric layout on large screens while retaining the two-column responsive layout on smaller screens.

## Upgrade notes

- No database migration, configuration change, or installation change is required.
- The page template and styles are embedded in the panel binary and take effect after upgrading the panel.
- This release does not change Agent collection or reporting. Agents with automatic updates continue to follow the panel version under the existing policy.
