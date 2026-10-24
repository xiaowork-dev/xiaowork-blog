# 参与贡献

感谢关注 XiaoWork Blog。

> **许可证尚未确定**（见 `docs/开源许可审查.md`）。在仓库加入 `LICENSE` 文件之前，维护者暂不合并外部贡献的代码。欢迎先提交 Issue 讨论问题与想法。

## 提交 Issue

- **安全问题**：请按 `SECURITY.md` 私下报告，不要公开提交。
- **缺陷**：请附上版本（`deploy/manage.sh status`）、复现步骤、预期与实际结果。日志中如有邮箱、IP、Cookie 等个人数据，请先删除。
- **新功能**：请先说明使用场景。V1.0 的范围以 `docs/reference/XiaoWork_Blog_V1.0_PRD.md` 为准，范围外的功能需要先讨论。

## 开发环境

本地开发使用根目录的 `compose.yaml`，步骤见 `docs/开发指南.md`。

需要的环境：Docker 与 Compose v2；直接开发时还需要 Node.js 24、pnpm 10、Go 1.27 和 PostgreSQL 18。

## 提交 Pull Request 之前

1. 一个 PR 只做一件事；修改接口或设计时，同步更新 `docs/reference/` 与对应的 ADR（`docs/adr/`）。
2. 在本地运行通过：
   - `apps/api`：`go vet ./...`、`go test ./...`（数据库测试需要 `TEST_DATABASE_URL`），sqlc 生成代码与 SQL 一致；
   - `apps/web`：`pnpm typecheck`、`pnpm test`、`pnpm build`；
   - 修改了 `deploy/` 时：ShellCheck（`-S warning`）通过；如有可能，在 `deploy/test` 演练环境中实际执行。
3. 数据库变更：
   - 只能新增 goose 迁移，不修改已有的迁移文件；
   - 迁移只增不删，保证旧版本代码在新结构上仍能运行（回滚不回退数据库）；
   - 迁移会写入 CHANGELOG 的“升级说明”。
4. 不提交 `.env`、密码、令牌、私钥、数据库导出、备份、真实用户数据或图片。测试使用 `example.test` 等保留域名，以及每次随机生成的密码。
5. 提交信息使用 `类型(范围): 说明`，例如 `fix(media): …`、`docs(deploy): …`。
6. CI（GitHub Actions `ci`）必须全部通过。

## 发布

只有维护者发布版本，流程见 `docs/RELEASING.md`。合并到 `main` 不等于发布：服务器只会推荐已发布的正式版本。
