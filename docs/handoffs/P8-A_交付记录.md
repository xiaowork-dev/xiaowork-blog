# XiaoWork Blog — P8-A 阶段交付记录（上线准备与全面验收）

- 日期：2026-10-24
- 状态：**P8-A 完成**。以下工作均已真实执行：
  - 综合功能与安全验收（14 项 UAT）；
  - 上传图片隐私元数据剥离、Web 安全响应头；
  - 生产 Docker 部署方案与一键脚本（安装、更新、回滚、备份、恢复、管理员）；
  - 在**隔离 Linux 环境**中完成安装、更新、回滚、失败自动回滚、迁移失败保护，以及在第二个全新环境中的恢复演练；
  - 运维手册、上线检查清单、性能基线与剩余风险、v1.0.0 发布说明草稿。
- 范围限制（遵守“八、明确限制”）：
  - **没有连接、修改或部署生产 VPS**；
  - 没有配置正式域名，没有使用正式 SMTP 密钥；
  - 没有触碰现有 Nginx Proxy Manager；
  - 没有创建 v1.0.0 标签或 GitHub Release，**v1.0.0 尚未正式发布**。
  - 所有生产操作留到 P8-B，由项目负责人单独授权。

## 1. 开发前检查

| 检查 | 结果 |
|---|---|
| P7 最新提交 | `00b7a70`：本地 `main` 与 `origin/main` 一致，工作区干净，没有未提交的修改需要保护 |
| P7 CI | #19（`00b7a70`）**Success**；#18（`dd4adb8`）Success |
| 文档 | 重读 AGENTS、README、P0–P7 交付记录与 ADR-0001~0007；PRD（UAT §11、MEDIA-07、非功能需求）、架构（部署视图）、API-DD、DD、UI/UX、实施计划 P8 |
| 架构 | 沿用现有 Go + Nuxt + PostgreSQL + Docker Compose，**没有新增服务**；生产栈仍为 db / migrate / api / web 四个服务 |
| 数据库 | **没有新增迁移，仍为版本 7**；历史迁移文件没有改动 |

## 2. 完成内容

### 2.1 隐私：上传图片去除元数据（处理 P7 记录的 EXIF 风险）

- 新增 `apps/api/internal/media/metadata.go`：在格式与尺寸校验之后、计算 SHA-256 之前，**无损**改写临时文件：
  - **JPEG**：删除 APP1（EXIF、XMP）、APP3–13、APP15 与 COM，保留 JFIF、ICC 色彩和 Adobe 段；方向不为 1 时，写回一个只含方向的最小 EXIF 块，保证显示方向不变；
  - **PNG**：删除 tEXt / zTXt / iTXt / eXIf / tIME；
  - **WebP**：删除 EXIF、XMP 块，并修正 VP8X 标志与 RIFF 长度；
  - **GIF**：删除注释扩展，以及循环（NETSCAPE2.0 / ANIMEXTS1.0）之外的应用扩展。
- 改写后重新校验格式与尺寸。`size_bytes` 和 `sha256` 按改写后的文件记录；缩略图按 EXIF 方向旋转（8 种方向全部支持）。
- 头像与文章、站点图片走同一流程。上传组件和个人资料页提示“会自动移除 EXIF/GPS 等隐私信息”。
- 已经存储的图片不回溯处理（生产环境尚无数据）。

### 2.2 安全响应头

新增 `apps/web/server/plugins/security-headers.ts`：

- 删除 `X-Powered-By`；
- 添加 `nosniff`、`X-Frame-Options: DENY`、`Referrer-Policy`（认证页保留 `no-referrer`）、`Permissions-Policy` 和 CSP；
- 只在 https 站点发送 HSTS。

浏览器实测：后台编辑器（CodeMirror）、实时预览（Shiki）、外部图片、评论，以及前台与后台 20 个页面**没有 CSP 违规**。

### 2.3 性能

开启 Nitro `compressPublicAssets`（gzip + brotli）：首页首屏 JS 的传输量从 241 KiB（未压缩）降到 **96 KiB**（JS + CSS，brotli）。

### 2.4 生产部署（`deploy/`）

| 文件 | 作用 |
|---|---|
| `compose.prod.yaml` | 生产栈，**不发布任何主机端口**：<br>- 数据库只在 `internal` 网络上；<br>- api / web 只读根文件系统，`cap_drop: ALL`，`no-new-privileges`，日志轮转；<br>- api / web 通过别名 `xiaowork-blog-api` / `xiaowork-blog-web` 加入 NPM 的外部网络；<br>- 数据卷固定命名 `xiaowork-blog_pgdata`，图片目录为 `DATA_DIR/media`；<br>- PostgreSQL 使用 `log_error_verbosity=terse` |
| `env.production.example` | 配置模板，不含任何真实值；`CHANGE_ME` 会被脚本拒绝 |
| `lib.sh` | 公共函数：<br>- 按 dotenv 语法读取并校验 `.env`（https、绝对路径、密码格式、密钥长度、公网站点必须使用 TLS 发信）；<br>- 健康检查与冒烟测试；<br>- 备份、版本记录、镜像清理；<br>- 自动检测可信代理网段 |
| `install.sh` | 首次运行生成 `.env`（随机数据库密码与 APP_SECRET_KEY）；之后完成安装；可重复执行，服务异常时重启已记录的版本 |
| `update.sh` | 拒绝脏工作区 → 构建新镜像（旧版本继续服务）→ 完整备份 → 应用 db 变更 → 迁移（失败不切换）→ 切换 → 健康检查与冒烟测试 → 失败自动回滚 |
| `rollback.sh` | 切回上一版本镜像 |
| `backup.sh` / `restore.sh` | 备份数据库、图片、配置、清单与校验和；恢复前先做安全备份，原图片目录改名保留 |
| `admin.sh` | 创建管理员（密码输入两次且不回显）、媒体一致性检查、状态 |
| `nginx/npm-equivalent.conf` | 与 NPM 代理主机等价的 nginx 配置（参考与演练用） |
| `test/drill.sh`、`test/README.md` | 隔离演练环境（Docker-in-Docker） |

所有脚本都**不包含** `down -v`、`docker volume rm` 或删除 `DATA_DIR` 的命令；唯一的删除操作是清理超出保留数量的旧备份，并且限定在备份目录内、按时间戳目录名匹配。

### 2.5 CI

新增 `deploy` 作业，在全新的 Ubuntu 24.04 上执行：

1. ShellCheck；
2. 校验生产 compose 配置；
3. 模拟 NPM 网络、nginx 与 Mailpit；
4. `install.sh` 三次（生成配置、安装、幂等）；
5. 确认没有发布端口；
6. 检查 HTTPS 响应头与 `__Host-` Secure Cookie；
7. 创建管理员，运行认证 e2e；
8. 备份、恢复、`media-check`。

### 2.6 文档

- `docs/adr/ADR-0008-P8上线准备与生产部署决策.md`
- `docs/deploy/部署与运维手册.md`：安装、NPM 配置、管理员初始化、日常运维、更新、回滚、备份恢复、异地备份、灾难恢复、安全、故障排查
- `docs/deploy/上线检查清单.md`（供 P8-B 使用）
- `docs/deploy/性能基线与剩余风险.md`
- `RELEASE_NOTES_v1.0.0.md`（**草稿**，注明尚未正式发布）
- README 更新

## 3. 新增 / 修改文件

```text
新增  apps/api/internal/media/metadata.go, metadata_test.go
修改  apps/api/internal/media/image.go, service.go, media_test.go
新增  apps/web/server/plugins/security-headers.ts
修改  apps/web/nuxt.config.ts, app/components/MediaUploader.vue, app/pages/account/profile.vue
新增  deploy/{compose.prod.yaml, env.production.example, lib.sh, install.sh, update.sh, rollback.sh, backup.sh, restore.sh, admin.sh}
新增  deploy/nginx/npm-equivalent.conf, deploy/test/{drill.sh, README.md}
修改  .github/workflows/ci.yml（新增 deploy 作业）
新增  docs/adr/ADR-0008-…, docs/deploy/{部署与运维手册, 上线检查清单, 性能基线与剩余风险}.md, docs/handoffs/P8-A_交付记录.md, RELEASE_NOTES_v1.0.0.md
修改  README.md
```

## 4. 测试（真实执行）

### 4.1 自动化测试

| 项目 | 环境 | 结果 |
|---|---|---|
| Go：article、auth、comment、config、database、httpapi、httpx、mail、media、site、store | Windows，使用隔离测试库（tmpfs PostgreSQL 18.6，`REQUIRE_DB_TESTS=1`） | **全部 PASS**；mail 的真实 SMTP 投递测试本地跳过、在 CI 中执行 |
| 新增元数据测试 | 同上 | TestStripJPEG / PNG / WebP / GIF、TestOrientThumbnail、TestUploadStripsPrivacyMetadata **PASS**。最后一项用带 GPS 和 XMP 的 JPEG 上传文章图片与头像，验证下载的文件不含 GPS 和作者信息、方向块保留、`size_bytes` 与 `sha256` 和实际文件一致 |
| `go vet`、`gofmt -l`、`sqlc diff` | 本地 | 通过，无输出 |
| Nuxt `typecheck`、vitest（56 个）、`build` | 本地 | 通过 |
| ShellCheck（warning 级别） | `koalaman/shellcheck:v0.11.0` | 修正 4 处后 0 警告 |
| **P0–P7 全部 e2e**：auth 23、media 11、articles 29、public 52、comments 47、admin 31，**共 193 项** | **隔离 Linux 演练环境的生产栈**，经模拟 NPM 的 HTTPS 访问 `https://localhost:18443` | 初次安装（演练 A）与最终代码（演练 B）**两轮均 193/193 通过** |

e2e 说明：

- 在 HTTPS 生产模式下，演练主机网关 `172.17.0.1` 是所有请求的真实来源，而它不在可信网段内。因此 e2e 脚本为每个模拟用户设置的 `X-Forwarded-For` **被 API 正确忽略**（这本身是一项安全验证）；但所有用户共用一个 IP，comments 与 admin 套件会触发注册限流（每 IP 每小时 3 次）。
- 为在不修改代码的前提下跑通，只在运行这两个套件时**临时**把 `172.17.0.1/32` 加入演练环境的 `TRUSTED_PROXY_CIDRS`，跑完立即恢复。
- 最终代码一轮中，public 套件第 18 项第一次失败：断言 `<title>` 以 “XiaoWork Blog” 结尾，而恢复演练把站名改成了“演练站点 R1”。把站名改回后重跑，52/52 通过。这是测试数据造成的，不是产品缺陷。

### 4.2 浏览器验收（内置浏览器，本地开发环境，临时测试管理员）

- **CSP**：
  - 登录后进入编辑器，输入含代码块与外部图片的 Markdown，实时预览正常（Shiki 高亮 1 处）；
  - 依次访问前台与后台 20 个页面；
  - `securitypolicyviolation` 监听器记录到的违规为 **0**。
- **响应式**：
  - 宽度 360 / 375 / 768 / 1024 / 1280 / 1440 px；
  - 页面：首页、文章、分类（列表与详情）、标签（列表与详情）、归档、搜索、关于、个人资料、我的评论、后台控制台 / 文章 / 新建文章 / 分类标签 / 媒体 / 设置 / 关于页 / 用户 / 评论，共 20 个；
  - `scrollWidth` 均不超过视口宽度，**没有横向溢出**。

### 4.3 UAT（PRD §11，14 项）

| 编号 | 场景 | 结论 | 依据 |
|---|---|---|---|
| UAT-01 | 浏览与响应式 | **通过** | §4.2，20 页 × 6 种宽度无溢出；public e2e 52 项 |
| UAT-02 | 文章发布 | **通过** | articles e2e 01–14（草稿、插图、发布、SSR 渲染、代码高亮）；浏览器中编辑器预览正常 |
| UAT-03 | 版本隔离 | **通过** | articles e2e 15–18：编辑草稿不影响前台，发布更新后才显示新版本；旧地址 301 |
| UAT-04 | 撤回与回收站 | **通过** | articles e2e 24–28、public 28–29、comments 30；演练 B 中把一篇有 5 条评论的回收站文章恢复并重新发布，**5 条评论全部重新显示**，之后移回回收站 |
| UAT-05 | 邮箱注册 | **通过** | auth e2e 02–18（经演练环境 SMTP 收信）；comments e2e 06：未验证用户不能评论 |
| UAT-06 | 密码找回 | **通过** | auth e2e 19–23；Go `TestPasswordResetFlow`：重复使用与过期的重置链接都返回 422 `TOKEN_INVALID_OR_EXPIRED`，重置后撤销全部会话 |
| UAT-07 | 评论直接公开 | **通过** | comments e2e（发表即显示；游客 401） |
| UAT-08 | 评论治理 | **通过** | comments e2e 16–26；admin e2e 08–12（禁用账号、撤销会话） |
| UAT-09 | 本地媒体持久化 | **通过** | media e2e；演练中 `docker compose down` 后重新 `install.sh`，数据与图片校验和完全一致；删除保护返回 409 |
| UAT-10 | SEO 与可见性 | **通过** | public e2e 28/39/42/43；HTTPS 下 robots、`X-Robots-Tag`、搜索页 noindex；SSR 页面不含邮箱 |
| UAT-11 | 后台与设置 | **通过** | admin e2e 31 项；普通用户与游客访问后台 API 返回 403 / 401 |
| UAT-12 | 部署与恢复 | **通过（隔离环境）** | §5：全新环境按文档部署；备份恢复后完全一致；多次升级不丢数据。**正式服务器上尚未执行**，留待 P8-B |
| UAT-13 | 基础安全 | **通过** | §4.4 |
| UAT-14 | 文档与版本 | **通过** | P0–P8-A 交付记录与 ADR 完整；GitHub 提交与 CI 见 §7；部署与更新文档见 `docs/deploy/` |

### 4.4 安全检查

| 项目 | 方法 | 结果 |
|---|---|---|
| CSRF | e2e：缺少令牌时注册、评论、阅读上报均返回 403；curl：令牌正确但 `Origin: https://evil.example` 时返回 **403** | 通过 |
| XSS | e2e：Markdown 中的 XSS 载荷被转义，`javascript:` 链接不渲染；评论按纯文本存储与显示；关于页与外链拒绝 `javascript:` | 通过 |
| 会话安全 | HTTPS 下的 Cookie：`__Host-blog_session`、`__Host-blog_csrf`，带 `Secure`、`SameSite=Lax`，会话 Cookie 带 HttpOnly；退出、改密码与禁用账号都会撤销会话 | 通过 |
| 越权 | 普通用户访问 `/api/v1/admin/*` 返回 403；不能删除他人评论；修改邮箱的链接没有会话时无效 | 通过 |
| 管理员权限 | 不能禁用管理员和自己；没有角色提升接口；`create-admin` 在已有管理员时拒绝执行 | 通过 |
| 上传安全 | 伪装成 `.png` 的 HTML 返回 415；声明 60000×60000 像素返回 422（解码前拒绝）；`/media` 响应带 `CSP: default-src 'none'; sandbox` 和 `nosniff`；未引用图片不公开；**新增 EXIF/GPS 剥离** | 通过 |
| 评论限流 | comments e2e 27：每分钟 5 条，第 6 条返回 429 | 通过 |
| SSR 私有数据 | 首页、文章、关于、归档页面的 HTML 中没有邮箱、`password` 或 `email_normalized`；公开评论 JSON 不含邮箱、会话或令牌 | 通过 |
| 日志敏感信息 | 在演练环境中检查 api（1013 行）与 web（210 行）日志：没有密码、令牌、Cookie 或邮箱。**发现**：PostgreSQL 日志的唯一约束错误 DETAIL 中出现了邮箱 → 生产库设置 `log_error_verbosity=terse`，并验证更新后已生效 | 已修复 |
| 搜索引擎边界 | robots 屏蔽 /admin、/account、/auth、/api/、/search；后台、个人与认证页返回 `X-Robots-Tag: noindex, nofollow` 与 `private, no-store`；搜索页 `noindex, follow`；sitemap 不含草稿、撤回、回收站文章和旧地址 | 通过 |
| 反向代理信任 | 演练中从外部伪造的 `X-Forwarded-For` 被忽略；可信范围为 NPM 网络与 APP_SUBNET，剩余风险与收紧方法见 ADR-0008 和运维手册 §12 | 已处理，剩余风险已记录 |
| 端口暴露 | 生产栈 `docker ps` 中没有任何 `0.0.0.0` 端口映射（CI 中也会检查） | 通过 |
| 安全响应头 | HTTPS 下有 HSTS、CSP、`X-Frame-Options`、`nosniff`、`Referrer-Policy`，没有 `X-Powered-By` | 通过 |

### 4.5 发现并修复的问题

| 问题 | 发现方式 | 修复 |
|---|---|---|
| `SMTP_FROM=名称 <地址>` 让 `source .env` 报语法错误 | 演练中的首次安装 | `.env` 改为按 dotenv 语法逐行解析 |
| 新版本 api 无法启动时，`compose up` 返回非 0，`set -e` 直接终止脚本，**自动回滚没有执行**，站点 502 | 演练 broken-api 场景 | 切换命令放进成功判断的条件中；回滚时同样处理。修复后重跑：自动回滚，站点保持 200 |
| 更新中断后，`install.sh` 会按 HEAD（可能是坏版本）重启 | 分析上一条时发现 | 优先重启已记录的版本；已用该方法从 502 状态恢复演练站点 |
| `update.sh` / `rollback.sh` 执行 `git checkout` 时可能改动正在执行的脚本 | 代码审查 | 脚本主体包成一个复合命令，由 bash 一次性解析 |
| 已退出的 migrate 容器占用旧镜像，导致镜像清理失败 | 演练中检查镜像 | 清理前先删除 migrate 的一次性容器；已验证 |
| `update.sh` 只重建 api / web，db 服务的配置变更不会生效 | 新增 terse 配置时发现 | 迁移前执行 `up -d db`（只有配置变化才重建）；已验证生效 |
| PostgreSQL 日志包含邮箱 | 日志审查 | `log_error_verbosity=terse` |
| `/_nuxt` 资源没有压缩 | 性能基线 | Nitro 预压缩 |
| `blog-admin` 在终端读取密码时会回显 | 编写手册时核对 | `admin.sh` 用 `read -s` 读取两次后通过管道传入；已在伪终端中验证，包括密码不一致的情况 |
| ShellCheck 警告：变量名冲突、`export` 与命令替换写在一起、中文引号 | ShellCheck | 已修正 |

## 5. 部署、更新、回滚与恢复演练（实际执行环境）

**环境**：Windows 开发机的 Docker Desktop 上运行 `docker:29.8.2-dind` 特权容器，作为全新的 Linux 主机（Alpine 3.22、Docker Engine 29.8.2、Compose v2）。其中包括：

- 模拟 NPM 的 nginx 1.30.5（自签名 HTTPS，配置为 `deploy/nginx/npm-equivalent.conf`）；
- 模拟 SMTP 的 Mailpit；
- 扮演 GitHub 的裸 Git 仓库。

演练环境没有使用开发机的 Docker 卷，**没有连接任何服务器**。CI 的 `deploy` 作业会在 Ubuntu 24.04 上再验证一遍（结果见 §7）。

| # | 场景 | 结果 |
|---|---|---|
| 1 | `install.sh` 第一次运行 | 生成 `.env`（权限 600，随机密码与密钥），提示填写后退出 |
| 2 | `install.sh` 安装 | 成功，约 4 分钟（含构建），结构版本 7；自动写入 `TRUSTED_PROXY_CIDRS`；没有发布任何端口 |
| 3 | 再次运行 `install.sh` | 0.8 秒识别出已安装且正常，直接退出 |
| 4 | `admin.sh create-admin` | 创建成功；第二次运行被拒绝（“an administrator already exists”） |
| 5 | e2e 全量（§4.1） | 193/193 |
| 6 | `backup.sh` | 生成 db.dump、media.tar.gz、env.backup、manifest、SHA256SUMS；目录权限 700，文件权限 600 |
| 7 | `update.sh`：代码更新（压缩与脚本修复） | 成功，约 47 秒；db 按新配置重建（terse 生效）；**数据完全一致** |
| 8 | `update.sh`：新增迁移 00008 | 结构版本 7 → 8，数据完全一致 |
| 9 | `rollback.sh` | 回到上一版本，站点 200；旧代码在结构版本 8 上正常运行，数据不变 |
| 10 | `update.sh`：api 无法启动（修复前） | **发现缺陷**：脚本中止，站点 502（见 §4.5） |
| 11 | `install.sh` 恢复 | 重启已记录的版本，站点 200 |
| 12 | `update.sh`：api 无法启动（修复后，共两次，其中一次使用最终脚本） | 自动回滚到旧版本，站点 200，数据不变，代码目录回到旧提交 |
| 13 | `update.sh`：迁移 SQL 错误 | 迁移失败，**不切换**，旧版本继续服务，结构版本仍为 8，代码目录回到旧提交 |
| 14 | 修复后再次 `update.sh` | 成功 |
| 15 | 已是最新版本时运行 `update.sh` | 输出“无需更新” |
| 16 | `docker compose down`（不带 `-v`）后 `install.sh` | 数据与图片校验和完全一致 |
| 17 | **恢复演练**：第二个全新的演练环境 B，按手册 §11 执行（使用 `env.backup`，清空可信网段，`install.sh` 安装空站点，再 `restore.sh`，输入 RESTORE 确认） | 见下文 |

**恢复演练结果**（与备份前在环境 A 中记录的基线逐行比较，`diff` 无差异）：

- 数据量：
  - 文章 30 篇（已发布 13 篇）；
  - 用户 8 个；
  - 评论 9 条（admin API 列出 VISIBLE / DELETED_AUTHOR / DELETED_ADMIN 三种状态）；
  - 可用图片 6 张，图片文件 10 个，文件校验和相同。
- 站点设置：站名“演练站点 R1”、个人简介、外链 1 条、关于页（含标记“恢复演练标记 R1”）。
- 原管理员密码可以登录；封面图片可以通过 `/media` 正常访问。
- `media-check`：0 个缺失，0 个孤立文件。
- 恢复前的空站点数据已自动备份为 `before-restore`，原图片目录改名保留。

**没有验证的项目（明确标记，不宣称通过）**：

- 正式 VPS 的操作系统、内核与存储；
- 真实 Nginx Proxy Manager 的界面配置与 Let's Encrypt 证书；
- 正式域名、DNS；
- 正式 SMTP 的投递与 SPF / DKIM；
- 异地备份目标；
- NPM 能否拿到真实客户端 IP。

以上都列入 `docs/deploy/上线检查清单.md`，由 P8-B 执行。

## 6. 性能基线

详见 `docs/deploy/性能基线与剩余风险.md`。演练环境（TLS 加 SSR）的结果：

- SSR 页面 p50 为 14–19 ms，p95 不超过 27 ms；
- API p50 约 3–4 ms；
- 首页首屏 JS + CSS 传输 96 KiB；
- 运行时内存：api 11 MB、web 208 MB、db 127 MB。

## 7. Git 与 CI

（推送后填写）

## 8. 清理

- **演练环境**：两个 dind 容器及其匿名卷在交付后删除。临时文件（基础镜像 tar、演练日志、演练备份、测试密码）只存在于会话临时目录，不在仓库中。
- **本地开发数据库**：P8-A 本地测试产生的 15 篇文章已通过后台 API 移入回收站，前台公开文章为 0。仍保留以下数据：
  - 临时管理员 `p8-admin@example.test`；
  - 回收站中的 15 篇文章；
  - 2 个分类、2 个标签；
  - 2 张被这些文章引用、受删除保护的测试图片；
  - 11 条审计日志。

  彻底删除需要对开发库执行硬删除。本阶段的自动安全检查拦截了这类批量删除，因此**没有执行，留待项目负责人决定**。早期阶段的 6 个普通用户没有变动。

## 9. 未解决问题与风险

见 `docs/deploy/性能基线与剩余风险.md`（R1–R10）。要点：

- 可信代理范围为整个 NPM 网络（R1）；
- NPM 可能拿不到真实客户端 IP（R2）；
- 回滚不回退数据库结构（R3）；
- 单机数据，必须配置异地备份（R4）；
- CSP 中保留 `'unsafe-inline'`（R6）；
- 正式 SMTP 与正式服务器尚未实测（R7、R8）。

## 10. 进入 P8-B 的前提条件

1. 项目负责人**单独授权** P8-B，并明确允许：连接指定 VPS、在 NPM 中新增一个代理主机、使用正式 SMTP 凭据。
2. 提供或确认以下信息：
   - 正式域名及 DNS；
   - VPS 访问方式与 `DATA_DIR`；
   - NPM 所在的 Docker 网络名；
   - SMTP 账号（在服务器上填写，不经过聊天或仓库）；
   - 异地备份的目标位置；
   - 首个管理员邮箱。
3. 待部署提交的 CI 全部通过（包括新增的 `deploy` 作业）。
4. 按 `docs/deploy/上线检查清单.md` 的 A–F 逐项执行并记录，全部通过后才创建 `v1.0.0` 标签与 Release（使用 `RELEASE_NOTES_v1.0.0.md`，删除“草稿”字样）。
5. 决定本地开发库中 P8-A 测试数据的处理方式（§8）。
