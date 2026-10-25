# 更新日志

本文件记录每个正式版本的变化，格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循[语义化版本](https://semver.org/lang/zh-CN/)。发布流程见 `docs/RELEASING.md`。

> 预发布版用于生产环境上线验收（P8-B）；验收通过后在同一提交上发布正式版 `v1.0.0`。目前还没有正式版本。当前预发布版为 `v1.0.0-rc.3`。

## [Unreleased]

（暂无）

## [1.0.0-rc.3] — 2026-10-25（预发布）

发布说明：`docs/releases/v1.0.0-rc.3.md`。取代 `v1.0.0-rc.2`。

### 新增

- 反代方式 `PROXY_MODE`：
  - `docker-network`（默认，Nginx Proxy Manager，与以前相同）；
  - `host-loopback`（1Panel OpenResty 等主机网络反代）：web/api 只发布到 `127.0.0.1:13000` / `127.0.0.1:18081`，端口可配置，数据库不发布端口。
- host-loopback 的 `TRUSTED_PROXY_CIDRS` 只包含 `APP_SUBNET`。
- 端口冲突检查：发现冲突时拒绝安装，不停止其他服务。
- 引导式安装会自动识别主机网络上的反代。
- `precheck` 新增第 7b 节。
- 新增 `deploy/compose.host-loopback.yaml`、`deploy/nginx/openresty-host-loopback.conf` 与 `docs/deploy/1Panel-OpenResty部署指南.md`。

### 修复

- 服务正常时再次运行 `install`，现在会应用 `deploy/.env` 的改动（只重建有变化的容器）。

### CI

- 部署作业校验两种模式的 compose 配置。
- 部署作业新增测试：把安装切换到 host-loopback，在主机网络 nginx 后完成 HTTPS 冒烟；同时检查端口只绑定回环地址、预检结果、重复运行不重建容器、端口冲突被拒绝。

### 升级说明

- 没有数据库迁移，结构版本仍为 7。
- 现有 `deploy/.env` 不需要修改。
- host-loopback 需要 Docker Compose 2.24.4+。

## [1.0.0-rc.2] — 2026-10-25（预发布）

发布说明：`docs/releases/v1.0.0-rc.2.md`。取代 `v1.0.0-rc.1`。

### 修复

- `manage.sh` / `update.sh` 拉取发布标签时，把“指向同一提交、只是标签对象不同”的本地标签（例如 CI 浅克隆创建的轻量标签）误判为“远端标签被移动”并停止（推送 rc.1 标签触发的 CI #31）。现在会比较两者指向的提交：相同则改用远端标签并继续；不同才判定为篡改并拒绝，同时显示本地与远端的提交。逻辑统一放在 `lib.sh` 的 `fetch_release_tags` 中。

### CI

- 部署测试作业拉取完整历史与标签（`fetch-depth: 0`），并新增回归测试：同一提交的轻量标签被接受，被移动的标签被拒绝（返回状态 2）。

### 升级说明

- 没有数据库迁移，结构版本仍为 7；配置没有变化。
- 已安装 rc.1 的服务器：`sudo deploy/manage.sh update v1.0.0-rc.2 --pre`。

## [1.0.0-rc.1] — 2026-10-25（预发布）

- 标签 `v1.0.0-rc.1` → 提交 `7660028d0d1f476d186d846e8f92411d3df18c18`；GitHub Release 标记为 Pre-release 且不可变。
- 发布说明：`docs/releases/v1.0.0-rc.1.md`。
- 注：标签所在提交中的本文件，此标题仍写着“等待确认后创建”；本次更新只更正这一描述。

### 许可证

- 以 MIT 许可证开源（`LICENSE`，Copyright (c) 2026 xiaowork-dev）；第三方素材声明见 `THIRD_PARTY_NOTICES.md`。
- 移除了无法确认再分发权限的设计概念图 `docs/reference/visual_overview.png`。

### 新增

- **博客前台**：
  - 首页、文章、分类、标签、归档、站内搜索与关于页；
  - 响应式布局与深色模式；代码高亮；
  - SEO：canonical、Open Graph、sitemap、RSS、robots；
  - 阅读量统计。
- **写作**：
  - Markdown 编辑器（自动保存、实时预览、插图、导入导出）；
  - 草稿与已发布版本分离；
  - 修改地址后旧地址 301 跳转；撤回、回收站与恢复。
- **用户与评论**：
  - 邮箱注册与验证、登录、找回密码、修改邮箱与密码、会话管理；
  - 评论与回复（发表即公开），作者删除，管理员隐藏 / 删除 / 禁用账号；
  - 个人中心。
- **管理后台**：控制台、文章、分类与标签、评论、用户、媒体库、网站设置、关于页、审计日志。
- **媒体**：
  - JPEG / PNG / WebP / GIF 按内容校验，生成缩略图；
  - 未被引用的图片不公开，被引用的图片受删除保护；
  - 上传时自动移除 EXIF / GPS / XMP 等隐私元数据。
- **安全**：
  - `__Host-` 会话 Cookie、CSRF 令牌与来源校验；
  - 多项限流；只信任可信代理传来的客户端 IP；
  - Markdown 关闭原始 HTML；安全响应头（CSP、HSTS 等）。
- **部署与运维**：
  - 生产 Docker Compose（不发布任何主机端口），接入现有 Nginx Proxy Manager；
  - 统一管理入口 `deploy/manage.sh`：
    - 首次安装（引导填写配置）、状态；
    - 从 GitHub Releases 检查新版本，区分正式版、预发布版与开发代码；
    - 显示计划后确认更新；回滚、备份、恢复、只读预检；
  - 更新前自动备份；迁移失败不切换；新版本未通过健康检查时自动回滚；
  - 正式版本标签与提交的对应关系记录在服务器上，标签被移动时拒绝更新；
  - 只读环境预检 `deploy/precheck.sh`。

### 升级说明

首个版本，无需升级。数据库结构版本为 7。
