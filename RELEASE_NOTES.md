# v1.4.56 网站详情操作改进

本版本改善网站详情页的日常管理操作。

## 改进

- 网站详情页新增删除按钮，删除前沿用现有二次确认流程，成功后返回网站列表。
- 修复网站详情页“返回列表”链接错误跳转到面板首页的问题。

## 升级说明

- 无数据库结构迁移，无需修改现有配置。
- API、Agent、安装、备份和更新机制没有变化。
- 模板已随 Server Panel 主程序二进制重新构建。

---

# v1.4.56 Website Detail Improvements

This release improves routine website management from the detail page.

- Adds a delete button to website details, using the existing confirmation
  flow and returning to the website list after a successful deletion.
- Fixes the “back to list” link incorrectly navigating to the panel dashboard.
- No database schema or configuration migration is required.
- APIs, the Agent, installation, backup, and update behavior are unchanged.
