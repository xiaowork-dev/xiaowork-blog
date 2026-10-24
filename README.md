# XiaoWork Blog

> **当前状态：P7 管理后台已完成（2026-10-24）**：V1.0 的业务功能已全部完成——公开博客、Markdown 编辑与发布、评论与用户中心，以及完整后台（控制台、文章、分类标签、评论、用户、媒体库、网站设置、关于页）。生产部署与上线（P8）尚未进行。代码托管于 `https://github.com/xiaowork-dev/xiaowork-blog`（`main`），CI 为 GitHub Actions `ci`；尚未部署 VPS。各阶段详情见 `docs/handoffs/`。

## 目录

```text
apps/web/                     Nuxt 4 SSR：公开博客、/auth/* 认证、/admin/* 后台、/api/v1 同源代理、sitemap/rss/robots、/health
apps/api/                     Go + Chi + pgx，/health/live 和 /health/ready
apps/api/db/migrations/       goose 版本化迁移（嵌入二进制）
apps/api/db/queries/          sqlc 查询源 SQL
apps/api/internal/store/      sqlc 生成的类型安全数据访问代码（勿手改）+ 事务封装
apps/api/cmd/blog-migrate/    迁移命令 up / status / version / down
apps/api/cmd/blog-admin/      首个管理员初始化（仅运维命令）
apps/api/internal/auth/       注册、验证、登录、会话、CSRF、限流、找回密码
apps/api/internal/media/      图片校验、缩略图、本地存储、媒体 API、删除状态机、一致性检查
apps/api/internal/article/    文章草稿、发布事务、Slug、撤回/回收站、分类标签、公开列表/搜索/归档/浏览量
apps/api/internal/site/       站点资料与关于页：公开读取 + 后台设置、外链、导航、关于页编辑
apps/api/internal/comment/    评论读取/发表/回复/删除、我的评论、后台评论治理
apps/api/internal/admin/      后台控制台统计、用户管理（禁用/恢复并撤销会话）
scripts/e2e-auth.mjs          认证端到端自检
scripts/e2e-media.mjs         媒体端到端自检（含容器重建后持久化校验）
scripts/e2e-articles.mjs      文章端到端自检（草稿隔离、发布、旧 Slug 301、XSS、撤回/回收站）
scripts/e2e-public.mjs        前台端到端自检（分页、分类标签归档、中文搜索、SEO、sitemap/RSS、浏览量）
scripts/e2e-comments.mjs      评论、用户中心与评论治理端到端自检（真实注册并通过 Mailpit 验证）
scripts/e2e-admin.mjs         后台端到端自检（控制台、禁用用户、站点设置/外链/导航/关于页前台生效、XSS、引用保护）
compose.yaml                  db / migrate（一次性）/ api / web / mailpit（本地邮件）、健康检查
.data/media/                  宿主机本地媒体持久化（运行时创建，不纳入 Git）
docs/reference/               需求、架构、数据库、API、UI 规范
docs/adr/                     设计决策记录
docs/handoffs/                每阶段交付记录
.github/workflows/            CI
```

## 环境建议

- Docker Engine + Docker Compose v2（本地容器化首选）。
- 直接开发：Node.js 24.22.1、pnpm 10.34.7、Go 1.27.3、PostgreSQL 18.6。
- P0 测试端口仅监听 **127.0.0.1:3000** 与 **127.0.0.1:8080**，数据库无宿主机端口映射。
- 不要把本开发示例直接当作生产部署；正式域名、HTTPS、Session、SMTP、备份属于后续阶段。

## 本地首次启动（需能访问官方依赖源与 Docker 镜像仓库）

1. 在仓库根目录复制 `.env.example` 为 `.env`（Windows PowerShell：`Copy-Item .env.example .env`）。该文件不进 Git。
2. 把 `POSTGRES_PASSWORD` 换成你自己的随机值（仅字母、数字、`_`、`-`，因为它会原样拼入 `DATABASE_URL`）；示例值只用于 loopback 本地开发。注意：数据库卷首次初始化后再改密码不会生效。
3. 运行 `docker compose --env-file .env config --quiet` 验证配置。
4. 运行 `docker compose --env-file .env up --build -d --wait`（`--wait` 会等到各服务健康后才返回）。
5. 运行 `docker compose ps -a` 确认 `db/api/web/mailpit` 均在运行（前三者 `healthy`）、`migrate` 为 `Exited (0)`；或运行 `bash scripts/verify-p0.sh` 一次性检查配置与全部健康端点。
6. 浏览器访问 `http://127.0.0.1:3000/`（请用 `127.0.0.1`，它就是 `PUBLIC_BASE_URL`；用 `localhost` 打开会被 CSRF Origin 检查拒绝写操作），右上角可注册/登录；`http://127.0.0.1:3000/health` 返回 JSON。
7. 本地发出的所有邮件（验证、重置、换邮箱）都在 Mailpit：`http://127.0.0.1:8025`，不会发往真实邮箱。
8. `curl http://127.0.0.1:8080/health/live` 和 `curl http://127.0.0.1:8080/health/ready` 返回 JSON，其中 ready 会检测真实 PostgreSQL 连通性。
9. 端到端自检（需 Node 24）：`node scripts/e2e-auth.mjs`（注册→邮件→验证→登录→退出→找回密码，经 Nuxt 代理与 Mailpit）。
10. 停止：`docker compose down`（**不要**添加 `-v`，否则可能清除数据库卷）。

## 账号与管理员

- 公开注册只能得到普通用户。**首个管理员**只能由运维命令创建（密码从标准输入读取，不出现在命令行或环境变量里；已存在管理员时拒绝执行）：

```bash
docker compose --env-file .env run --rm -T --no-deps --entrypoint /usr/local/bin/blog-admin api create-admin -email you@example.com -nickname XiaoWork < admin-password.txt
```

- 生产 SMTP：在 `.env` 设置 `SMTP_HOST/PORT/TLS/USERNAME/FROM`，密码优先用 `SMTP_PASSWORD_FILE` 指向挂载的密钥文件；`PUBLIC_BASE_URL` 必须是 https 正式域名，`APP_SECRET_KEY` 用 `openssl rand -base64 48` 生成。配置有误时 API 启动即失败并指出变量名（不回显取值）。
- 认证细节与安全取舍见 `docs/adr/ADR-0002-P2认证实现决策.md`。

## 直接开发（可选）

- Nuxt：`cd apps/web && corepack enable && pnpm install && pnpm dev`。
- Go：`cd apps/api && go test ./... && go run ./cmd/blog-api`，并设置 `DATABASE_URL` 环境变量；直跑 Go 需先有可连接的 PostgreSQL。
- 预期 `/health/ready` 只有在 DB 真的能连通时才返回 200。

## 依赖锁定

- `apps/web/pnpm-lock.yaml` 与 `apps/api/go.sum` 已真实生成并必须纳入版本管理。
- Docker 构建与 CI 均使用锁定安装：`pnpm install --frozen-lockfile`、`go mod download && go mod verify`、`GOFLAGS=-mod=readonly`。修改依赖时先在本地更新锁文件再构建。
- 国内网络可在 `.env` 中设置可选的 `GOPROXY` / `NPM_REGISTRY` 构建参数（见 `.env.example`）；镜像源只影响下载位置，校验仍以锁文件为准。
- 本机若同时装有多个 Node，直接开发前确认 `node --version` 为 24.x。

## 持久化

- PostgreSQL：命名卷 `xiaowork-blog_blog_postgres` 挂载到 `/var/lib/postgresql`（PG18 实际 `PGDATA=/var/lib/postgresql/18/docker`）。
- 媒体：宿主机 `./.data/media` 绑定到 API 容器 `/data/media`。API 以固定 UID/GID `10001:10001` 非 root 运行；**Linux 宿主机**首次启动前执行 `mkdir -p .data/media && sudo chown 10001:10001 .data/media`（Windows/macOS Docker Desktop 无需此步）。自 P3 起若该目录不可写，API 会在启动时直接失败并在日志中说明。
- `docker compose down` 与 `up --build --force-recreate` 均不会丢失上述数据；只有 `down -v` 或手动删除目录才会。容器卷**不是备份**，备份/恢复属于 P8。

## 数据库迁移、sqlc 与数据库测试

- Compose 启动时 `migrate` 服务先执行 `blog-migrate up`（成功后才启动 `api`）；已是最新版本时为空操作，可重复部署。查看状态：`docker compose --env-file .env run --rm migrate status`。
- **生产/共享数据库不要执行 `down`**；回滚依赖备份恢复（DD §11.3）。升级前先做备份。
- 修改 `db/migrations` 或 `db/queries` 后重新生成代码（Windows 无 cgo，使用官方镜像；PowerShell 中把 `$(pwd -W)` 换成 `${PWD}`）：

```bash
docker run --rm -v "$(pwd -W)/apps/api:/src" -w /src sqlc/sqlc:1.31.0 generate
```

- 数据库集成测试需要一个**独立的**测试 PostgreSQL（每个测试自动创建并删除临时库，绝不使用 Compose 的数据卷）：

```bash
docker run -d --name xiaowork-blog-testdb --tmpfs /var/lib/postgresql -e POSTGRES_HOST_AUTH_METHOD=trust -p 127.0.0.1:55432:5432 postgres:18.6-alpine
```

```bash
cd apps/api && TEST_DATABASE_URL="postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable" REQUIRE_DB_TESTS=1 go test -count=1 ./...
```

  未设置 `TEST_DATABASE_URL` 时这些测试会被跳过；CI 设置 `REQUIRE_DB_TESTS=1`，缺库即失败。真实 SMTP 投递测试另需 `TEST_SMTP_ADDR`、`TEST_MAILPIT_API` 指向一个 Mailpit（`REQUIRE_DB_TESTS=1` 时同样必需）。
- Windows 本机若 `go test` 报 `fork/exec … Access is denied`（安全软件拦截刚生成的测试程序），可改为 `go test -c -o <dir>/x.test.exe ./internal/auth/` 后直接运行该程序。

## 安全与数据

- 未实现的功能不提供伪装业务 API。
- 图片只能经 Go 的 `/media/{id}/file` 受控读取（草稿图片游客 404）；`.data/media` 不得配置为反代静态目录，也不得提交 Git。
- PostgreSQL 18 官方镜像须挂载 `/var/lib/postgresql`，不能沿用旧版常见 `/var/lib/postgresql/data` 方案。
- 网站始终只允许通过 Nginx Proxy Manager 反代暴露正式流量；P8 才实施正式域名与 HTTPS。
- `.env`、用户数据、图片、备份与私钥禁止提交 Git。

## 验收门槛

参见 `docs/handoffs/` 下各阶段交付记录（P0–P7）。

## 图片与媒体（P3）

- 管理员登录后右上角进入“后台”→ **媒体库** `/admin/media`：上传（选择/拖拽/粘贴，失败可重试）、预览、搜索、按用途筛选、复制地址或 Markdown、删除。被文章、头像、站点设置或关于页引用的图片无法删除，会提示引用位置数量。
- 支持 JPG/PNG/WebP/GIF；默认单张 ≤10 MiB、≤2000 万像素（头像 ≤2 MiB）；扩展名必须与实际格式一致。原图逐字节保存，静态大图另生成缩略图（`?variant=thumb`），GIF 保留动画。
- 新上传图片默认不公开：只有被已发布文章、站点设置、关于页或正常用户头像引用后，游客才能访问。
- 一致性检查（只读，适合恢复备份或重建容器后执行）：

```bash
docker compose --env-file .env run --rm -T --no-deps --entrypoint /usr/local/bin/blog-admin api media-check
```

- 媒体端到端自检需要一个管理员账号：`E2E_ADMIN_EMAIL=… E2E_ADMIN_PASSWORD=… node scripts/e2e-media.mjs`（会自行清理上传的测试图片）。
- 设计细节见 `docs/adr/ADR-0003-P3媒体实现决策.md`。

## 文章与编辑器（P4）

- 后台“文章管理” `/admin/articles`：按状态（草稿 / 有未发布修改 / 已发布 / 已撤回 / 回收站）筛选与搜索；“新建文章”会先在服务器创建空草稿再进入编辑器。
- 编辑器 `/admin/articles/{id}/edit`：源码 / 分栏 / 预览三种模式，工具栏与快捷键（Ctrl+B 加粗、Ctrl+I 斜体、Ctrl+K 链接、Ctrl+E 行内代码、Ctrl+S 立即保存），粘贴或拖入图片自动上传，可从媒体库插入图片、导入/导出 `.md`；摘要、分类、标签、封面、Slug、SEO、评论开关在“文章设置”中。
- **所有编辑只保存到草稿**（停顿约 1.5 秒自动保存，浏览器内另有本地备份）；只有点击“发布 / 发布更新”并确认后才替换线上版本。多个窗口同时编辑时会提示版本冲突，本地内容不会丢失。
- 撤回后文章与旧地址对游客 404；移入回收站不会删除评论和图片；恢复后仍为未公开状态，需要重新发布。
- 分类与标签在 `/admin/taxonomy` 管理；公开文章地址为 `/posts/{slug}`（P4 为最小页面，完整阅读页在 P5）。
- 文章端到端自检：`E2E_ADMIN_EMAIL=… E2E_ADMIN_PASSWORD=… node scripts/e2e-articles.mjs`（测试文章最后留在回收站）。
- Markdown 渲染安全测试：`cd apps/web && pnpm test`。设计取舍见 `docs/adr/ADR-0004-P4文章与编辑器实现决策.md`。

## 博客前台（P5）

- 公开页面：`/`（最新文章，每页 10 篇）、`/posts/{slug}`、`/categories`、`/categories/{slug}`、`/tags`、`/tags/{slug}`、`/archives`（可按 `?year=` 筛选）、`/search?q=`、`/about`。PC 为“左侧个人栏 + 右侧内容”，手机为单栏。
- 左侧个人栏、外链图标、顶部导航与关于页的内容来自数据库（`site_settings` 等表），后台编辑界面在 P7 提供；未配置时使用迁移写入的默认值。
- 搜索是**关键词匹配**：用空格分隔多个词，全部命中才返回，中文短词可直接搜索；不提供语义搜索。
- 浏览量：读者停留约 5 秒后才计数，同一浏览器 30 分钟内只计一次，机器人不计数，详见 `docs/adr/ADR-0005-P5博客前台与内容发现实现决策.md`。
- `/sitemap.xml`、`/rss.xml`、`/robots.txt` 每次请求都读取公开 API，发布或撤回后立即生效。站点地址取自 `PUBLIC_BASE_URL`（compose 中传给 Nuxt 的 `NUXT_PUBLIC_SITE_URL`），上线时必须设置为正式的 HTTPS 域名。
- 日期显示与归档统一使用 `Asia/Shanghai` 时区。
- 前台端到端自检：`E2E_ADMIN_EMAIL=… E2E_ADMIN_PASSWORD=… node scripts/e2e-public.mjs`（测试文章最后留在回收站）。加 `--seed FILE` 可保留一组示例文章用于人工检查，之后用 `--cleanup FILE` 移入回收站。

## 评论与用户中心（P6）

- 文章页底部的评论区在浏览器端加载。游客可以阅读；登录且邮箱已验证的用户可以评论和回复，发表后**立即公开**（不设审核）。评论只支持纯文本、换行、Emoji 和链接，长度不超过 2000 字；每人每分钟最多 5 条、每小时最多 20 条。
- 回复保存真实的父评论，界面只显示两级缩进，并标注“回复 @昵称”。删除有回复的评论时保留“该评论已删除”的占位；没有回复的评论删除后直接消失。文章关闭评论后仍可阅读历史评论，但不能再发表。
- 用户中心：`/account/profile`（昵称、简介、头像、邮箱状态）、`/account/security`（修改密码后需重新登录、更换邮箱需在已登录状态下打开确认邮件、退出当前或所有设备）、`/account/comments`（我的评论）。
- 后台 `/admin/comments`：按文章、用户、状态、日期和关键词筛选，可以隐藏、恢复或删除（删除不可恢复）；所有操作都写入审计日志。
- 评论端到端自检：`E2E_ADMIN_EMAIL=… E2E_ADMIN_PASSWORD=… node scripts/e2e-comments.mjs http://127.0.0.1:3000 http://127.0.0.1:8025`（需要 Mailpit）。设计取舍见 `docs/adr/ADR-0006-P6评论与用户中心实现决策.md`。

## 管理后台（P7）

- 入口 `/admin`（仅管理员）。左侧导航依次为：控制台、文章管理、分类与标签、评论管理、用户管理、媒体库、网站设置、关于页；手机上通过顶栏的菜单按钮展开。
- **控制台**：文章、用户、评论、媒体的实时数量，最近更新的文章和管理动态；没有图表，也没有“待审核”。
- **用户管理**：搜索、筛选、查看详情和最近评论，可以禁用或恢复**普通用户**。禁用会立即让该用户在所有设备上退出，之后无法登录、评论或找回密码；管理员账号与自己的账号不能被禁用。新管理员只能在服务器上用 `blog-admin create-admin` 创建。
- **网站设置**：可修改站名、描述、Logo、Favicon、个人栏头像、昵称和简介、个人栏外链（仅限 https 与 mailto，可排序、启停）、顶部导航（为空时使用默认导航）和默认 SEO；图片从媒体库选择。保存后前台立即生效。SMTP、数据库等部署密钥只放在环境变量中，后台不显示也不能修改。
- **关于页**：Markdown 编辑加实时预览。**保存即公开**，没有草稿。
- 后台端到端自检：`E2E_ADMIN_EMAIL=… E2E_ADMIN_PASSWORD=… node scripts/e2e-admin.mjs http://127.0.0.1:3000 http://127.0.0.1:8025`（会恢复原有的站点设置）。设计取舍见 `docs/adr/ADR-0007-P7管理后台实现决策.md`。
