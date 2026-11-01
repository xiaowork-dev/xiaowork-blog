# XiaoWork Blog — P8-B11 交付记录（生产 SMTP 修复、v1.0.0-rc.5 发布与上线）

- 日期：2026-11-01
- 授权：
  - 项目负责人通过 `/server-ssh` 授权连接主服务器；
  - 指示“按上面的配置重新配置 SMTP”（用户提供的 2465 端口配置）；
  - 随后指示“GitHub 版本也改一下……在服务器上帮我更新成新 UI 版本”。
- 目标服务器：SSH 别名 `xiaowork-main`（生产，`https://blog.xiaoworks.com`，1Panel OpenResty + host-loopback）。本记录不包含 IP、密码或任何密钥。

## 1. 阶段名称和目标

**P8-B11**，目标三项：

1. 修复生产环境“注册 / 找回密码邮件收不到”；
2. 把 P8-B10 的新配色发布为 `v1.0.0-rc.5`；
3. 在生产服务器上更新到 rc.5。

## 2. 实际完成内容

**邮件故障排查（只读）**

- api 日志中有 6 次 `mail delivery failed`，错误都是 `smtp dial: dial tcp …:465: i/o timeout`。
- 端口测试结果：
  - 服务器（宿主机与 api 容器）连任何邮件服务商的出站 25 / 465 / 587 / 2525 都超时，连 443 正常；
  - 本机防火墙 `OUTPUT` 为 ACCEPT。
- 结论：**VPS 服务商在网络层封锁了出站邮件端口**。邮件账号和密码本身没有问题。

**SMTP 修复（经授权的生产配置变更）**

1. 备份 `deploy/.env` 为 `deploy/.env.bak-smtp-20261101T115739Z`（权限 600）。
2. 修改两项：
   - `SMTP_PORT` 465 → **2465**；
   - `SMTP_FROM` 改为 `admin@xiaoworks.com`。
3. `SMTP_HOST`、`SMTP_TLS=tls`、`SMTP_USERNAME` 本来就是这个值；`SMTP_PASSWORD` **未改动**，与备份逐字一致。
4. 运行 `deploy/install.sh`：只按新配置重建了 api 容器，web / db 没有重启，版本不变。
5. api 容器到邮件服务器 2465 端口可以连通。项目负责人确认：发信成功。

**GitHub 文档与 rc.5 发布**

- 提交 `77d59bb`，内容如下：
  - 运维手册 §13、1Panel 指南与 `env.production.example` 新增“出站邮件端口被封”的排查与解决方法；
  - CHANGELOG `[1.0.0-rc.5]` 正式条目；
  - 发布说明定稿；
  - README 改为 rc.5。
- CI [#49](https://github.com/xiaowork-dev/xiaowork-blog/actions/runs/41304602503) 通过后：
  - 在 `77d59bb` 上创建带注释标签 `v1.0.0-rc.5`（标签对象 `33ecbd2`）并推送；
  - 在已登录的 Chrome 中创建 GitHub Release：Pre-release、不可变，正文与 `docs/releases/v1.0.0-rc.5.md` 逐字一致。

**生产更新到 rc.5**

1. 手动备份 `20261101T120816Z-before-rc5`（3.9 MB）。
2. `manage.sh check --pre` 显示 rc.5。
3. 运行 `deploy/manage.sh update v1.0.0-rc.5 --pre --yes`：
   - 构建镜像；
   - 自动备份 `20261101T120906Z-pre-update-77d59bbcd1b4`；
   - 数据库迁移（结构版本 7 → 7）；
   - 切换 api / web；
   - 结果：“更新成功”。

## 3. 新增、修改的主要文件 / 对象

| 对象 | 说明 |
|---|---|
| `deploy/env.production.example`、`docs/deploy/部署与运维手册.md` §13、`docs/deploy/1Panel-OpenResty部署指南.md` §9 | SMTP 出站端口被封的排查与解决 |
| `CHANGELOG.md`、`docs/releases/v1.0.0-rc.5.md`、`README.md` | rc.5 定稿 |
| Git 标签 `v1.0.0-rc.5` / GitHub Release（id 430557716） | 新增，Pre-release，不可变 |
| 生产 `deploy/.env`（不在 Git 中） | `SMTP_PORT=2465`、`SMTP_FROM`；有备份 |
| 本记录 | 新增 |

## 4. 实际执行的测试和结果

| 检查 | 结果 |
|---|---|
| 生产 SMTP 连通性 | 465：超时（被封）；2465：api 容器内连通 |
| 实际发信 | 项目负责人确认“现在可以了” |
| CI | `77d59bb` #49 success（web、api、compose、db-integration、deploy） |
| Release 核验 | `prerelease: true`、`draft: false`、`immutable: true`；正文一致；`/releases/latest` 404（没有正式版） |
| 生产更新后状态 | `manage.sh status`：当前 rc.5（`77d59bbcd1b4`），上一版 rc.4；api / web / db 都是 healthy；端口只监听 127.0.0.1（13000 / 18081），db 不发布端口 |
| 更新后 SMTP | api 环境变量为 `us2.workspace.org:2465 tls`，2465 连通；更新后 api 没有 ERROR 日志 |
| 公网访问 | `https://blog.xiaoworks.com/`、`/api/v1/site`、`/rss.xml`、`/sitemap.xml` 都返回 200；线上 CSS 含方案 B 色值（`#0c1312`、`#2dd4bf`、`--profile-band`）；首页深色和浅色已截图确认新配色 |

## 5. GitHub Commit SHA 与 CI 状态

| 对象 | SHA | CI |
|---|---|---|
| rc.5 定稿 + SMTP 文档 | `77d59bbcd1b42918b868aad322f5ef458e55fa08` | #49 success |
| 标签 `v1.0.0-rc.5` | 标签对象 `33ecbd206985b03db86fbe65608e17f1299476d0` → `77d59bb` | 标签触发的 CI 见第 11 节 |
| 本记录 | 见第 11 节 | 见第 11 节 |

## 6. 发现并修复的问题

- **生产收不到邮件**：
  - 原因：VPS 服务商封锁了出站 25 / 465 / 587 / 2525；
  - 修复：改用邮件服务商提供的 2465 隐式 TLS 端口；
  - 防止再犯：已写入运维文档。
- **排查脚本的问题（只影响排查过程）**：远程检查时 `manage.sh` 读走了通过 stdin 传入的其余命令，导致部分输出缺失。已改为给相关命令加 `</dev/null` 后重新执行。

## 7. 已知风险和未完成事项

- 项目负责人曾在对话中发送过邮箱密码。
  - 本次没有使用或写入该密码，服务器上的密码保持原值；
  - **建议负责人修改该邮箱密码**，然后自行更新生产 `.env` 的 `SMTP_PASSWORD`（值用单引号括起来），并运行 `sudo deploy/install.sh`。
- 2465 端口取决于邮件服务商；如果它以后变化，按运维手册 §13 排查。
- `v1.0.0` 正式版尚未发布，需要负责人在线上验收后确认。

## 8. 当前项目总体进度

- P0–P8 完成。
- rc.1–rc.5 已发布，生产运行 **rc.5**（新配色），邮件已恢复。
- 下一步：线上验收 rc.5，然后决定是否在同一提交上发布 `v1.0.0`。

## 9. 下一阶段的具体建议 / 回滚

- 验收：
  1. 浏览深色 / 浅色、手机端；
  2. 注册一个测试账号，确认能收到验证邮件。
- 回滚 UI 到 rc.4（没有数据库迁移，可直接回滚）：

  ```bash
  sudo deploy/manage.sh rollback
  ```

- 恢复 SMTP 原配置：

  ```bash
  cd /opt/xiaowork-blog/deploy && cp -p .env.bak-smtp-20261101T115739Z .env && cd .. && deploy/install.sh
  ```

## 10. 最终验收结论

**完成（等待项目负责人线上验收）**：

- 邮件已恢复（负责人确认）；
- rc.5 已发布并部署到生产，服务健康，新配色已生效；
- 没有创建 `v1.0.0`，没有改动旧标签。

## 11. 推送与 CI 结果

本记录提交的 SHA、标签 CI 与本记录 CI，在对话的最终验收总结中报告。
