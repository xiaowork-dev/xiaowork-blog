# XiaoWork Blog

> **当前状态：P2 用户认证已完成（2026-10-23）**：注册、邮箱验证、登录/退出、找回密码、修改密码/邮箱、管理员初始化，以及统一响应、CSRF、限流等 API 基础设施。文章、评论、媒体与后台尚未实现（P3 起）。代码托管于 `https://github.com/xiaowork-dev/xiaowork-blog`（`main`），CI 为 GitHub Actions `ci`；尚未部署 VPS。各阶段详情见 `docs/handoffs/`。

## 目录

```text
apps/web/                     Nuxt 4 SSR：首页占位、/auth/* 认证页面、/api/v1 同源代理、/health
apps/api/                     Go + Chi + pgx，/health/live 和 /health/ready
apps/api/db/migrations/       goose 版本化迁移（嵌入二进制）
apps/api/db/queries/          sqlc 查询源 SQL
apps/api/internal/store/      sqlc 生成的类型安全数据访问代码（勿手改）+ 事务封装
apps/api/cmd/blog-migrate/    迁移命令 up / status / version / down
apps/api/cmd/blog-admin/      首个管理员初始化（仅运维命令）
apps/api/internal/auth/       注册、验证、登录、会话、CSRF、限流、找回密码
scripts/e2e-auth.mjs          认证端到端自检
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
- 媒体：宿主机 `./.data/media` 绑定到 API 容器 `/data/media`。API 以固定 UID/GID `10001:10001` 非 root 运行；**Linux 宿主机**首次启动前执行 `mkdir -p .data/media && sudo chown 10001:10001 .data/media`（Windows/macOS Docker Desktop 无需此步）。
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
- 图片媒体目录 `.data/media` 保留给 P3，本阶段并无上传接口。
- PostgreSQL 18 官方镜像须挂载 `/var/lib/postgresql`，不能沿用旧版常见 `/var/lib/postgresql/data` 方案。
- 网站始终只允许通过 Nginx Proxy Manager 反代暴露正式流量；P8 才实施正式域名与 HTTPS。
- `.env`、用户数据、图片、备份与私钥禁止提交 Git。

## 验收门槛

参见 `docs/handoffs/` 下各阶段交付记录（P0、P1、P2）。
