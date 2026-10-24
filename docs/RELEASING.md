# 版本规范与发布流程

面向维护者。安装者如何选择和更新版本，见 `docs/deploy/部署与运维手册.md` §7。

## 1. 原则

- **`main` 是开发分支，不是发布渠道。** 推送到 `main` 不会部署到任何网站；服务器上的 `deploy/manage.sh` 默认只推荐 GitHub 上已发布的**正式版本**。
- **正式版本 = GitHub Release + 不可变的 Git 标签 + 一个确定的提交。** 标签一经推送就不再移动或删除；发现问题时发布新的补丁版本。
- 服务器每次部署后都会记录“标签 → 提交”。如果同名标签之后指向了别的提交，`manage.sh` 会拒绝更新（视为仓库被篡改）。
- 没有自动部署、没有定时自动更新：每次更新都由 VPS 所有者在终端中查看计划并确认。

## 2. 版本号

采用[语义化版本](https://semver.org/lang/zh-CN/)，标签以 `v` 开头：

| 形式 | 含义 | `manage.sh` 中的类型 |
|---|---|---|
| `v1.4.2` | 正式版本 | 正式版（默认推荐） |
| `v1.5.0-rc.1`、`v1.5.0-beta.2` | 预发布版本 | 预发布版（只有加 `--pre` 才显示和选用） |
| 其他标签、分支、提交 | 不是发布 | 开发代码（必须显式加 `--allow-unreleased`） |

版本号的递增规则：

- **主版本号**：不兼容的变化，例如需要人工迁移数据、修改部署方式、删除配置项。
- **次版本号**：新增功能，包括新增数据库迁移（迁移只增不删）。
- **修订号**：只修复问题，不新增迁移。

GitHub Release 的 “Set as a pre-release” 必须与标签是否带 `-` 后缀一致。`manage.sh` 把两者任一标为预发布的版本都当作预发布。

## 3. 仓库设置（维护者在 GitHub 上一次性完成）

1. **Settings → Rules → Rulesets**：新建标签规则集，目标 `v*`，勾选 *Restrict updates* 与 *Restrict deletions*，防止标签被移动或删除。
2. **Settings → General → Releases**：开启 *Release immutability*（GitHub 提供时），发布后不能再修改资产与标签。
3. **Settings → Security**：开启 *Private vulnerability reporting*（`SECURITY.md` 依赖它）。
4. 默认分支 `main` 开启分支保护：要求 CI（`ci`）全部通过。

## 4. 发布步骤

1. 确认要发布的提交在 `main` 上，并且 CI 全部通过（包括 `deploy` 作业）。
2. 更新 `CHANGELOG.md`：把 `[Unreleased]` 的内容移到新版本标题下，写上日期；**数据库迁移、配置变化和需要人工处理的步骤**写在“升级说明”中。提交并推送，等待 CI 通过。
3. 在该提交上创建**带注释的**标签并推送：

   ```bash
   git switch main && git pull --ff-only
   git tag -a v1.0.1 -m "XiaoWork Blog v1.0.1"
   git push origin v1.0.1
   ```

4. 创建 GitHub Release。发布说明事先写在 `docs/releases/<版本>.md` 中（按 §5 的模板，与 CHANGELOG 中该版本的段落一致），并随第 2 步一起提交：

   ```bash
   gh release create v1.0.1 --verify-tag --title "XiaoWork Blog v1.0.1" --notes-file docs/releases/v1.0.1.md
   # 预发布版本加 --prerelease；正式版本不要勾选 pre-release
   ```

   也可以在网页上操作，选择**已存在的**标签，不要让 GitHub 新建标签。
5. 在测试服务器（或 `deploy/test` 演练环境）上运行 `deploy/manage.sh check`，确认新版本出现在列表中，类型正确。

## 5. 发布说明模板

```markdown
## 亮点
- …

## 升级说明
- 数据库迁移：新增 000NN_xxx.sql（只增不删；回滚不会撤销）。如无迁移，写“无”。
- 配置变化：deploy/.env 新增 / 修改的项及默认值。如无，写“无”。
- 需要人工处理的步骤：如无，写“无”。

## 修复
- …

## 已知问题
- …
```

## 6. 发布之后

- 不修改、不移动、不删除已发布的标签。说明写错时可以编辑 Release 文字，但不能改变它指向的提交。
- 发现严重问题时，发布修订版本，并在 Release 与 CHANGELOG 中注明受影响的版本。
- 撤回某个版本时，在 Release 标题中注明“已撤回”，并改为预发布，使 `manage.sh` 不再默认推荐它。不要删除标签：已经安装该版本的服务器依赖它来识别当前版本。

## 7. v1.0.0

`v1.0.0` 需要在生产上线验收（P8-B，`docs/deploy/上线检查清单.md`）全部通过之后才能发布。在此之前不创建任何 Release。
