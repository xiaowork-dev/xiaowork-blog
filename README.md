# XiaoWork Blog

一个自托管的个人博客系统：博主在后台用 Markdown 写作和管理网站，读者浏览、搜索、注册并发表评论。使用 Docker 部署在你自己的 Linux 服务器上，由服务器上现有的反向代理（Nginx Proxy Manager，或 1Panel 的 OpenResty 等主机网络反代）提供 HTTPS。

> **当前状态：最新预发布版为 [`v1.0.0-rc.4`](https://github.com/xiaowork-dev/xiaowork-blog/releases/tag/v1.0.0-rc.4)，尚未发布正式版本。**
> - 预发布版用于生产环境上线验收；验收通过后在同一提交上发布正式版 `v1.0.0`。
> - 在此之前，`deploy/manage.sh` 默认找不到可安装的正式版本，这是预期行为；安装预发布版需要显式指定：`sudo deploy/manage.sh install v1.0.0-rc.4 --pre`。
> - 本项目以 [MIT 许可证](LICENSE) 开源。

## 功能

| 方面 | 内容 |
|---|---|
| 阅读 | 首页、文章页、分类、标签、归档、站内搜索、关于页；手机 / 平板 / 电脑自适应；深色模式；代码高亮；RSS 与 sitemap |
| 写作 | Markdown 编辑器：实时预览、自动保存、粘贴或拖入图片；草稿与线上版本分离，确认后才发布；撤回、回收站、旧链接自动跳转 |
| 读者 | 邮箱注册与验证、找回密码；评论与回复（发表即公开）；个人资料、头像与“我的评论” |
| 管理 | 控制台、文章、分类与标签、评论治理、用户禁用、媒体库、网站设置（站名、Logo、个人栏、外链、导航、SEO）、关于页、审计日志 |
| 隐私与安全 | 上传图片自动去除 GPS 等元数据；未发布内容和图片不公开；HTTPS 安全 Cookie、CSRF 防护、限流、安全响应头 |
| 运维 | 一个命令安装；检查、确认后再更新到正式版本；更新前自动备份；失败自动回滚；备份与恢复；只读环境预检 |

## 部署要求

- 64 位 Linux 服务器，内存 2 GB 以上，`/srv`（或你选择的数据目录）所在磁盘剩余 3 GB 以上；
- Docker Engine 24+ 与 Docker Compose 插件 2.20+（主机网络反代模式需要 2.24.4+），以及 `git`、`curl`、`openssl`；
- **一个已经运行、占用 80/443 的反向代理**。博客本身不占用 80/443，支持两种接入方式（安装时选择，`deploy/.env` 中的 `PROXY_MODE`）：

  | 方式 | 适用 | 博客端口 |
  |---|---|---|
  | `docker-network`（默认） | [Nginx Proxy Manager](https://nginxproxymanager.com/)（NPM）等容器反代，位于一个自定义 Docker 网络上 | 不发布任何主机端口 |
  | `host-loopback` | 1Panel 的 OpenResty 等使用**主机网络**的反代，或主机上直接安装的 nginx | 只发布到 `127.0.0.1:13000`（web）与 `127.0.0.1:18081`（api），端口可改 |

  两种方式下数据库都不发布端口。1Panel 用户请看 [1Panel / OpenResty 部署指南](docs/deploy/1Panel-OpenResty部署指南.md)；
- 一个指向服务器的域名，以及一个可用的 SMTP 发信账号（用于注册验证和找回密码）。

所有操作都由服务器所有者在自己的终端中执行。**不需要、也不应该**把 SSH 密钥、服务器密码或 SMTP 密码交给任何人或任何 AI 工具。

## 安装

```bash
sudo git clone https://github.com/xiaowork-dev/xiaowork-blog.git /opt/xiaowork-blog
cd /opt/xiaowork-blog
sudo deploy/manage.sh precheck      # 可选：只读检查服务器环境，不做任何修改
sudo deploy/manage.sh install
```

`install` 的执行过程：

1. 列出 GitHub 上最新的**正式版本**，确认后切换到该版本；
2. 首次运行时，自动生成数据库密码和应用密钥（只保存在服务器的 `deploy/.env` 中），然后逐项询问：
   - 域名、数据目录；
   - 反代方式（检测到主机网络上的 OpenResty/nginx 时建议 `host-loopback`）；
   - NPM 所在的网络（docker-network），或 web/api 的本机端口（host-loopback）；
   - 内部网段（自动避开冲突）；
   - SMTP 设置；
3. 构建镜像、初始化数据库并启动，最后做健康检查。

我们不提供 `curl … | bash` 形式的安装：请先 `git clone`，需要时可以先阅读 `deploy/` 中的脚本再执行。

安装完成后：

1. **创建管理员**：`sudo deploy/manage.sh admin create-admin -email 你的邮箱 -nickname 昵称`（密码输入两次，不回显）。
2. **配置反向代理**（路径必须原样转发，不能去掉 `/api/v1` 或 `/media` 前缀）：
   - NPM（docker-network）：
     - 域名转发到 `xiaowork-blog-web:3000`；
     - 自定义位置 `/api/v1/` 与 `/media/` 转发到 `xiaowork-blog-api:8080`；
     - 申请 Let's Encrypt 证书，开启 Force SSL；
     - 详见运维手册 §4。
   - 1Panel OpenResty（host-loopback）：
     - `/` → `http://127.0.0.1:13000`；
     - `/api/v1/` 与 `/media/` → `http://127.0.0.1:18081`；
     - 详见 [1Panel 部署指南](docs/deploy/1Panel-OpenResty部署指南.md)。
3. **配置每日备份与异地备份**：运维手册 §6、§10。

## 日常使用

| 操作 | 命令 |
|---|---|
| 菜单（交互式） | `sudo deploy/manage.sh` |
| 查看状态（版本、提交、服务健康） | `sudo deploy/manage.sh status` |
| 检查新版本 | `sudo deploy/manage.sh check`（加 `--pre` 可同时显示预发布版本） |
| 更新到最新正式版 | `sudo deploy/manage.sh update` |
| 更新到指定版本 | `sudo deploy/manage.sh update v1.0.1` |
| 回滚到上一版本 | `sudo deploy/manage.sh rollback` |
| 立即备份 | `sudo deploy/manage.sh backup` |
| 恢复备份 | `sudo deploy/manage.sh restore /srv/xiaowork-blog/backups/<备份目录>` |
| 只读环境预检 | `sudo deploy/manage.sh precheck` |

关于更新：

- **只推荐 GitHub Releases 中的正式版本。** `main` 分支上的新代码不会被当作更新，也不会自动部署；没有任何定时自动更新。
- 更新前会列出当前版本、目标版本、发布日期、更新说明，以及**将要执行的数据库迁移**，输入版本号确认后才开始。
- 更新时先完整备份，再迁移和切换；新版本没有通过健康检查时，会自动切回原版本。**数据库结构不会自动回退**：迁移遵循“只增不删”，旧版本可以继续运行；如需旧数据，可以用更新前的备份恢复。
- 如果无法访问 GitHub，`check` / `update` 会报错退出，正在运行的网站不受影响。
- 在脚本或定时任务中执行时，必须显式加 `--yes`，否则会拒绝执行。

## 文档

| 文档 | 内容 |
|---|---|
| `docs/deploy/部署与运维手册.md` | 安装、NPM 配置、更新与回滚、备份与恢复、异地备份、换服务器、安全、故障排查 |
| `docs/deploy/1Panel-OpenResty部署指南.md` | 1Panel / 主机网络反代（`PROXY_MODE=host-loopback`）的网络结构、配置与检查 |
| `docs/deploy/上线检查清单.md` | 正式上线前逐项检查 |
| `docs/deploy/性能基线与剩余风险.md` | 性能数据与已知风险 |
| `CHANGELOG.md` | 各版本的变化与升级说明 |
| `docs/releases/` | 各版本的发布说明（与 GitHub Release 内容一致） |
| `docs/RELEASING.md` | 版本号规则与发布流程（维护者） |
| `docs/开发指南.md` | 本地开发、测试、各模块说明（开发者） |
| `docs/reference/` | 需求、架构、数据库、API 与 UI 设计 |
| `docs/adr/`、`docs/handoffs/` | 设计决策与各阶段交付记录 |

## 参与与安全

- 参与贡献：`CONTRIBUTING.md`。
- 报告安全问题：`SECURITY.md`，请私下报告，不要公开提交 Issue。

## 许可证

[MIT](LICENSE)，Copyright (c) 2026 xiaowork-dev。

源代码中使用的第三方素材（部分图标路径来自 [Tabler Icons](https://github.com/tabler/tabler-icons)，MIT）及其许可声明见 `THIRD_PARTY_NOTICES.md`。通过 npm 和 Go 模块安装的依赖沿用各自的许可，审查结果见 `docs/开源许可审查.md`。

## 技术栈

Go 1.27（Chi、pgx、sqlc、goose）· PostgreSQL 18 · Nuxt 4 / Vue 3 · Docker Compose。代码托管于 `https://github.com/xiaowork-dev/xiaowork-blog`，CI 为 GitHub Actions。
