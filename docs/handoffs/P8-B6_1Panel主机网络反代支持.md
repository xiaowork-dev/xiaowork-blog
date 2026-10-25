# XiaoWork Blog — P8-B6 交付记录（1Panel OpenResty 主机网络反代支持）

- 日期：2026-10-25
- 范围限制：
  - **没有连接 VPS**；
  - 没有修改 1Panel、OpenResty 或现有 ERP 服务；
  - 没有创建任何标签或 Release；
  - `v1.0.0-rc.2` 的标签与 Release 未改动；
  - 只修改了代码、部署脚本、CI 与文档。所有测试都在本机的隔离 Docker-in-Docker 容器中进行，演练结束后已删除。

## 1. 阶段名称和目标

**P8-B6：新增 `PROXY_MODE=host-loopback`，支持 1Panel OpenResty（主机网络）部署，同时保持 NPM 模式不变。**

背景：目标 VPS（项目负责人只读预检）的情况如下：

- Debian 12，Docker 29.9.1，Compose 5.6.0；
- 1Panel 的 OpenResty 使用主机网络，占用 80/443；
- `1panel-network` 中只有 MySQL；
- 另有 ERP 容器在运行。

在这种环境下，原有的“共享 Docker 网络 + 容器名”方式无法工作。

要求：

- 三个容器保持独立，不使用主机网络；
- web/api 只绑定 127.0.0.1 上可配置的端口，数据库不发布端口；
- host 模式不依赖 `PROXY_NETWORK` 和 NPM；
- 现有 NPM 部署继续可用；
- 检查端口冲突、网段冲突和已有容器，不停止任何其他服务；
- 安装、更新、回滚、备份、恢复、预检、manage.sh 在两种模式下行为一致；
- HTTPS、Cookie、CSRF、Origin、X-Forwarded-For 与真实 IP 都正确，可信代理最少化；
- 提供 1Panel 教程、生产配置模板、网络结构图；
- 在隔离环境实际测试两种模式；
- 准备 rc.3 发布说明，但不打标签。

## 2. 实际完成内容

1. **`PROXY_MODE`**：
   - 取值 `docker-network`（默认）与 `host-loopback`；
   - 旧的 `.env` 没有这一行时按 docker-network 处理；
   - `load_env` 检查：
     - docker-network 要求 `PROXY_NETWORK`；
     - host-loopback 要求两个端口在 1024–65535 之间且不相同，并要求 Compose ≥ 2.24.4；
     - 非法值报错。
2. **Compose 结构**：
   - `compose.prod.yaml` 本身仍是 docker-network 结构（与 rc.2 相同，只把 `PROXY_NETWORK` 改为由 `lib.sh` 检查）；
   - host-loopback 叠加 `compose.host-loopback.yaml`：
     - `!override` 去掉 api/web 的 proxy 网络；
     - `!reset` 删除顶层 proxy 网络；
     - 发布 `127.0.0.1:${WEB_HOST_PORT:-13000}:3000` 与 `127.0.0.1:${API_HOST_PORT:-18081}:8080`；
   - `lib.sh` 的 `dc` 按模式选择文件，所有脚本（install、update、rollback、backup、restore、admin、manage）都经过 `dc`。
3. **可信代理**：host-loopback 的 `TRUSTED_PROXY_CIDRS` 只写入 `APP_SUBNET`。发布端口的流量来自该网络的网关，Nuxt SSR 也在这个网络中。
4. **冲突检查**：
   - 新增 `check_host_ports`（install、update 都执行）：端口有程序监听、且不是本项目发布的，就拒绝，并提示“本脚本不会停止任何其他服务”；
   - 原有的网段冲突检查保留。
5. **引导式安装**：
   - 检测到使用主机网络的 openresty / nginx / caddy / traefik 容器时，建议 host-loopback；
   - host-loopback 询问端口（拒绝已占用、相同的端口）；docker-network 询问 NPM 网络；
   - 结束时按模式提示反代配置。
6. **预检**：
   - `precheck.sh` 新增 `--proxy-mode`、`--web-port`、`--api-port`；
   - 新增第 7b 节：主机网络容器、80/443 的监听进程、两个端口是“空闲 / 本博客使用 / 冲突”中的哪一种、Compose 版本；
   - `manage.sh precheck` 自动带上 `.env` 中的参数。
7. **install 应用 `.env` 改动**：服务正常时，再次运行 install 会执行 `dc up -d --no-deps api web`，只重建配置有变化的容器，用于切换模式或修改端口。
8. **反代配置与文档**：
   - `deploy/nginx/openresty-host-loopback.conf`；
   - `docs/deploy/1Panel-OpenResty部署指南.md`，包含网络结构图、1Panel 操作步骤、生产 `.env` 示例、检查与真实 IP 验证、安全说明、模式切换、排障；
   - 运维手册 §1/§2/§3/§4/§12/§13/§14、上线检查清单（按 [NPM] / [1Panel] 分项）、README、`env.production.example`、`deploy/test/README.md`；
   - ADR-0010。
9. **演练脚本**：`drill.sh` 新增：
   - `DRILL_MODE=host-loopback`：主机网络 nginx 模拟 OpenResty；Mailpit 运行在主机网络，并关闭反向 DNS；
   - `SNAPSHOT_SRC`：用旧版本源码作为第一个提交，测试升级兼容；
   - `app_gateway`。
10. **CI**：部署作业新增两项：
    - 两种模式的 compose 校验（JSON 断言：host 模式下没有 proxy 网络、只绑定 127.0.0.1、db 没有端口）；
    - “切换到 host-loopback”步骤：主机网络 nginx 后的 HTTPS 冒烟、端口绑定、不在 npm_proxy 中、非回环地址不可达、预检、重复运行不重建容器、端口冲突被拒绝且占用者继续运行。
11. **rc.3 准备**：`docs/releases/v1.0.0-rc.3.md` 与 `CHANGELOG.md` 的 Unreleased 条目。**未创建标签或 Release。**

## 3. 新增、修改的主要文件

| 文件 | 说明 |
|---|---|
| `deploy/compose.host-loopback.yaml`（新） | host 模式覆盖文件 |
| `deploy/compose.prod.yaml` | 注释（两种模式）；`PROXY_NETWORK` 改为 `${PROXY_NETWORK:-}` |
| `deploy/lib.sh` | `PROXY_MODE` 校验、`compose_at_least`、`dc` 选择覆盖文件、`detect_trusted_proxies`、`check_proxy_network`、`port_listening`、`check_host_ports` |
| `deploy/install.sh` | 引导选择模式与端口、端口检查、已安装时应用 `.env` 改动、按模式给出下一步 |
| `deploy/update.sh`、`deploy/manage.sh`、`deploy/precheck.sh` | 端口检查；预检参数；第 7b 节 |
| `deploy/env.production.example` | 反代配置段 |
| `deploy/nginx/openresty-host-loopback.conf`（新） | OpenResty / nginx 参考配置（演练实际使用） |
| `deploy/test/drill.sh`、`deploy/test/README.md` | 两种模式与旧版本快照 |
| `.github/workflows/ci.yml` | 两种模式的校验与 host-loopback 测试 |
| `docs/deploy/1Panel-OpenResty部署指南.md`（新） | 1Panel 教程 |
| `docs/deploy/部署与运维手册.md`、`docs/deploy/上线检查清单.md`、`README.md` | 两种模式 |
| `docs/adr/ADR-0010-P8B6反代方式与主机网络部署决策.md`（新） | 决策记录 |
| `docs/releases/v1.0.0-rc.3.md`（新）、`CHANGELOG.md` | rc.3 草稿 |

## 4. 实际执行的测试和结果

环境：Windows 11 + Docker Desktop。每轮演练都使用一个新的 `docker:29.8.2-dind` 特权容器模拟全新 Linux 服务器（Compose v5.6.0，与目标 VPS 相同），使用模拟 GitHub Releases API，以及自签名 HTTPS（`https://localhost:18443`）。

### 4.1 host-loopback（第一轮，原实现）

- 预检识别主机网络 OpenResty 并建议 host-loopback；pty 中的引导式安装写出正确的 `.env`。
- 端口冲突：其他容器占用 `127.0.0.1:13000` 时，安装被拒绝，该容器继续运行。
- 安装：
  - `TRUSTED_PROXY_CIDRS=172.30.240.0/24`；
  - 端口为 web `127.0.0.1:13000`、api `127.0.0.1:18081`，db 未发布；
  - 从主机 eth0 地址和其他容器都无法访问这两个端口。
- HTTPS：`/`、`/api/v1/site`、`/robots.txt` 返回 200；HSTS 与 CSP 存在；`__Host-blog_csrf` 带 Secure。
- e2e 6 套全部通过：auth 23、media 11、articles 29、public 52、comments 47、admin 31。
  - comments 与 admin 运行时，临时把 `172.17.0.1/32` 加入可信代理，让测试脚本自带的不同 IP 生效，避免限流；运行后已撤销。
- 生命周期，数据始终与基线一致：
  - 更新到 v0.2.0（迁移 7→8）；
  - 坏版本 v0.2.1 自动回滚；
  - 手动回滚；
  - 恢复备份；
  - `down`（不带 -v）后重新 install。

### 4.2 NPM 模式与旧版本升级兼容（rc.2 → 新代码）

- 使用 `git archive b81de95`（rc.2）的源码作为第一个提交，以 docker-network 安装，旧 `.env` 中没有 `PROXY_MODE`。
- rc.2 上的 e2e：articles 29、public 52 全部通过。
- 写入种子数据并备份后，用 **rc.2 的 update.sh** 升级到新代码。
- **第一次结果失败**：
  - 现象：更新报告成功，但 web/api 不在 `npm_proxy` 中，站点返回 **502**；
  - 原因见第 6 节第 1 条；修复后，整轮演练在新的容器中重新执行。
- 修复后：
  - 升级成功（7→8），`.env` 未改动；
  - web/api 都在 `npm_proxy` 中；
  - 数据与升级前完全一致；
  - 站点返回 200。
- 新代码上的 e2e：
  - auth 23/23、media 11/11、articles 29/29、comments 47/47、admin 31/31 通过；
  - public 51/52：唯一的失败（第 18 项）是测试断言写死了站点名 “XiaoWork Blog”，而演练种子把站点名改成了“演练站点 NPM”。实际标题 `… · 演练站点 NPM` 是正确行为，属于测试数据干扰；
  - 全新安装后 public 52/52 通过（见下文）。
- 真实 IP：伪造 4 个不同的 `X-Forwarded-For` 后注册，结果为 201、201、201、**429**。
- 生命周期，数据完全一致：
  - 坏版本 v0.2.1 自动回滚；
  - **手动回滚到 rc.2 代码**（站点 200）；
  - 再次更新到新代码；
  - 恢复备份；
  - `down` 后重新 install。
- 新代码全新安装（显式 `PROXY_MODE=docker-network`）：安装成功，auth 23/23、public 52/52 通过。

### 4.3 host-loopback（第二轮，修复后的覆盖文件）

- 安装：端口只在 127.0.0.1 上，db 未发布；从 eth0 访问失败（`000`）。
- HTTPS：`/`、`/api/v1/site`、`/robots.txt` 返回 200；`/media/不存在` 返回 404（说明请求确实转发到了 API）。
- e2e：auth 23、media 11、public 52 全部通过。
- 真实 IP：201、201、201、**429**。
- 生命周期，数据完全一致：
  - 更新 7→8；
  - 坏版本自动回滚；
  - 手动回滚；
  - 恢复；
  - down 后 install。
- **模式切换**：
  - host→docker-network：web/api 加入 `npm_proxy`，取消端口发布，同网络容器可按别名访问；
  - docker-network→host：重新发布回环端口，HTTPS 返回 200。
  - 第一次测试发现 install 不应用 `.env` 改动（第 6 节第 2 条），修复后通过；
  - `.env` 未改动时再运行 install，api 容器 ID 不变；
  - 双向切换后数据一致。

### 4.4 静态检查

- ShellCheck v0.11.0（`-x -P deploy -S warning`，`deploy/*.sh` 与 `drill.sh`）：通过。
- actionlint 1.7.7：通过。
- 两种模式的 compose 校验（本机 Compose 5.6.0）：
  - docker-network `config --quiet` 通过；
  - host-loopback 的 JSON 断言通过：没有 proxy 网络，端口只绑定 127.0.0.1。
- 文档路径检查：检查了 160 处引用，2 处缺失都是已有项（ADR-0004 的 URL 模式；清单中对未来的 `P8-B_交付记录.md` 的引用）。
- gitleaks v8.28.0：
  - 暂存区扫描：no leaks found；
  - 工作区扫描：命中 2 处，都在本地开发用的 `.env`。该文件已被 Git 忽略、未提交（`git check-ignore` 确认）；
  - 没有暂存任何 `.env`、证书、私钥、数据库或备份文件。

## 5. GitHub Commit SHA 与 CI 状态

- 功能提交：`d886fbbb1bfb2ae34951561ddf2f1609d95d1478`（`feat(deploy): PROXY_MODE=host-loopback …`），已推送到 `origin/main`，远端 SHA 已核对一致。
- CI：[#38](https://github.com/xiaowork-dev/xiaowork-blog/actions/runs/40186190531)，**success**：
  - web、api、compose、db-integration、deploy 全部通过；
  - deploy 作业中，新增的“两种模式 compose 校验”和“切换到 host-loopback”步骤均为 success。
- 本交付文档的补记提交见第 11 节。

## 6. 发现并修复的问题

1. **从 rc.2 升级后 NPM 返回 502**（严重，在隔离演练中发现）。
   - 原因：rc.2 的 `update.sh` 在执行前已被 bash 整体解析，切换到新代码后仍使用旧的 `dc`（只有 `-f compose.prod.yaml`）。最初的设计把 proxy 网络移到了单独的 `compose.docker-network.yaml`，所以新版本启动后不在 NPM 网络中。
   - 修复：`compose.prod.yaml` 保持原来的 docker-network 结构，改由 host 覆盖文件用 `!reset` / `!override` 去掉 proxy 网络；删除 `compose.docker-network.yaml`。
   - 由于覆盖语法的要求，host 模式需要 Compose 2.24.4+，增加了对应检查。
2. **服务正常时再次运行 install，不应用 `.env` 改动**，导致无法切换模式或修改端口。已改为执行一次 `dc up -d --no-deps api web`。
3. **host 演练中的邮件不可达**（只影响演练环境），原因有三，分别修复：
   - 跨 bridge 的流量被丢弃 → Mailpit 改为使用主机网络；
   - dind 中的 `172.17.0.1` 是外层 Docker Desktop 的地址 → 改用 app 网络网关；
   - Mailpit 的反向 DNS 导致 SMTP 问候超时 → `MP_SMTP_DISABLE_RDNS=true`。
4. 预检中 80/443 的进程列表重复 → 合并为进程名。

## 7. 已知风险和未完成事项

- **host-loopback 的剩余风险**：本机任何使用主机网络的进程都能访问 127.0.0.1 上的两个端口，并自带 `X-Forwarded-For`。这只影响按 IP 计算的限流，不能绕过认证、CSRF 与权限检查。已写入 1Panel 指南 §7、运维手册 §12、ADR-0010。
- host-loopback 的安装无法回滚到 rc.2 或更早版本，因为旧版本不支持该模式。NPM 用户回滚到 rc.2 已经验证。
- **1Panel 界面本身没有实际操作**（不允许连接 VPS，也没有 1Panel 测试实例）。指南中的菜单名称来自公开资料，可能因版本而不同。验收时请以 1Panel 生成的配置文件和指南 §6 的检查结果为准。
- 切换模式时需要手动清空 `TRUSTED_PROXY_CIDRS`（已写入文档），脚本不会自动清除。
- 站点前面如果有 CDN，需要另外配置 `real_ip`，本阶段不涉及。
- **rc.3 的标签与 Release 尚未创建**，需要项目负责人确认。

## 8. 当前项目总体进度

- P0–P7 功能完成。
- P8-A 部署体系完成；P8-B2 版本管理完成；P8-B3 许可证确认（MIT）。
- P8-B4 发布 rc.1；P8-B5 修复误报并发布 rc.2。
- P8-B6（本阶段）新增 1Panel 主机网络部署支持，等待验收，并等待确认是否发布 rc.3。
- 生产环境尚未部署。

## 9. 下一阶段的具体建议

1. 项目负责人验收本阶段：重点看 1Panel 指南和上线检查清单中的 [1Panel] 项。
2. 确认后，在本阶段最终提交上发布 `v1.0.0-rc.3` 预发布：
   - 按 `docs/RELEASING.md` 执行；
   - 发布说明使用 `docs/releases/v1.0.0-rc.3.md`；
   - 发布后把 CHANGELOG 的 Unreleased 改为正式条目。
3. 获得针对具体 VPS 的授权后，项目负责人在服务器上自行执行：
   ```bash
   sudo deploy/manage.sh precheck
   sudo deploy/manage.sh install v1.0.0-rc.3 --pre
   ```
   然后按指南在 1Panel 中添加反向代理，逐项填写上线检查清单。

## 10. 最终验收结论

**等待验收。**

- 功能、隔离环境测试与文档已完成。
- 已推送，CI #38 全部通过。
- rc.3 的标签与 Release 按要求未创建，等待项目负责人确认。

## 11. 推送与 CI 结果

- `d886fbb` → CI #38 success（见第 5 节）。
- `87023c8`（本文档补记）→ CI [#39](https://github.com/xiaowork-dev/xiaowork-blog/actions/runs/40186424273) success。
- 未创建任何标签或 Release；`v1.0.0-rc.2` 未改动。
