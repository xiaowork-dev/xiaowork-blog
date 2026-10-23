# XiaoWork Blog — P0 工程骨架

> **当前状态：P0 工程骨架，本地 Docker Compose 集成验证已通过（2026-10-23，见 `docs/handoffs/P0_交付记录.md`）**。尚无真实文章、用户或评论功能。代码托管于 `https://github.com/xiaowork-dev/xiaowork-blog`（`main`），CI 为 GitHub Actions `p0-checks`；尚未部署 VPS。

## 目录

```text
apps/web/             Nuxt 4 SSR，占位首页及 /health
apps/api/             Go + Chi + pgx，/health/live 和 /health/ready
compose.yaml          web / api / db 三容器、健康检查
.data/media/          宿主机本地媒体持久化（运行时创建，不纳入 Git）
docs/reference/       需求、架构、数据库、API、UI 规范
docs/handoffs/        每阶段交付记录
.github/workflows/    最小 CI
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
4. 运行 `docker compose --env-file .env up --build -d --wait`（`--wait` 会等到三个服务健康后才返回）。
5. 运行 `docker compose ps` 确认 `db/api/web` 均为 `healthy`；或运行 `bash scripts/verify-p0.sh` 一次性检查配置与全部健康端点。
6. 浏览器访问 `http://127.0.0.1:3000/`，应出现 **P0 骨架占位** 页面；`http://127.0.0.1:3000/health` 返回 JSON。
7. `curl http://127.0.0.1:8080/health/live` 和 `curl http://127.0.0.1:8080/health/ready` 返回 JSON，其中 ready 会检测真实 PostgreSQL 连通性。
8. 停止：`docker compose down`（**不要**添加 `-v`，否则可能清除数据库卷）。

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

## 安全与数据

- 未实现的功能不提供伪装业务 API。
- 图片媒体目录 `.data/media` 保留给 P3，本阶段并无上传接口。
- PostgreSQL 18 官方镜像须挂载 `/var/lib/postgresql`，不能沿用旧版常见 `/var/lib/postgresql/data` 方案。
- 网站始终只允许通过 Nginx Proxy Manager 反代暴露正式流量；P8 才实施正式域名与 HTTPS。
- `.env`、用户数据、图片、备份与私钥禁止提交 Git。

## 验收门槛

参见 `docs/handoffs/P0_交付记录.md`。本地构建、测试、三容器健康与持久化已真实验证；P0 已推送 GitHub 并通过 CI。
