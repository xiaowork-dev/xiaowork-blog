# XiaoWork Blog — 1Panel / OpenResty 主机网络部署指南

> 适用于 v1.0.0-rc.3 起的版本（P8-B6 新增 `PROXY_MODE=host-loopback`）。
>
> 适用场景：服务器上已经有 1Panel，它的 OpenResty 容器使用**主机网络**（`network_mode: host`）并占用 80/443。这时 OpenResty 不在任何 Docker 自定义网络中，无法按 Nginx Proxy Manager 的方式（共享 Docker 网络 + 容器名）访问博客。
>
> 本文的部署脚本、端口绑定和反代规则都已在隔离的 Docker-in-Docker 演练中实际验证过（演练用主机网络上的 nginx 模拟 OpenResty，配置即 [`deploy/nginx/openresty-host-loopback.conf`](../../deploy/nginx/openresty-host-loopback.conf)）。**1Panel 界面本身没有在演练中操作过**；不同 1Panel 版本的菜单名称可能略有差别，请以“最终生成的 OpenResty 配置”与第 6 节的检查结果为准。

## 1. 网络结构

```
浏览器 ──HTTPS──► 1Panel OpenResty（主机网络，占用 80/443，负责证书）
                    │  只经过本机回环地址 127.0.0.1
                    ├─ /api/v1/*、/media/* ──► 127.0.0.1:18081 ──► xiaowork-blog-api 容器 :8080（Go）
                    └─ 其他路径           ──► 127.0.0.1:13000 ──► xiaowork-blog-web 容器 :3000（Nuxt SSR）
                                                                  │ app 网络（APP_SUBNET，默认 172.30.240.0/24）
                                                                  ▼
                                              api ──backend 网络（internal，无外网）──► db 容器（PostgreSQL 18）
```

- Nuxt、Go API、PostgreSQL 仍是三个独立容器，使用各自的桥接网络，**不使用主机网络**。
- 只有 web 与 api 发布端口，而且只绑定 `127.0.0.1`：外网和局域网都无法直接访问；数据库不发布任何端口。
- 博客使用自己的 PostgreSQL（数据卷 `xiaowork-blog_pgdata`），与 1Panel 的 MySQL 和其他应用完全分开。
- 本模式不需要 `PROXY_NETWORK`，也不需要 Nginx Proxy Manager。
- 端口可以修改：`deploy/.env` 中的 `WEB_HOST_PORT`（默认 13000）和 `API_HOST_PORT`（默认 18081）。

## 2. 安装前预检（只读）

```bash
sudo git clone https://github.com/xiaowork-dev/xiaowork-blog.git /opt/xiaowork-blog
cd /opt/xiaowork-blog
sudo deploy/manage.sh precheck > precheck.txt
```

预检**不做任何修改**。第 7b 节会列出：

- 使用主机网络的容器。发现 OpenResty 时，会建议选择 `PROXY_MODE=host-loopback`；
- 80/443 由哪个程序监听；
- 13000/18081 是空闲、已被本博客使用，还是“冲突”；
- Compose 版本是否满足要求（host-loopback 需要 **Docker Compose 2.24.4 或以上**）。

如果端口冲突，换一个空闲端口（见第 3 步）。安装脚本**不会停止或修改任何其他服务**。

## 3. 一键安装

```bash
sudo deploy/manage.sh install
```

在终端中运行时，脚本会依次询问：

1. **反代方式**。检测到主机网络上的 OpenResty/nginx 时，默认建议 `host-loopback`，直接回车即可。
2. **Web 与 API 的本机端口**。默认 13000 / 18081；被占用时会提示重新输入。
3. 域名、数据目录、内部网段（会自动推荐不与现有 Docker 网络重叠的网段）、SMTP。

也可以手动编辑 `deploy/.env`（模板为 [`deploy/env.production.example`](../../deploy/env.production.example)）后再运行一次 install。生产配置示例：

```dotenv
PUBLIC_BASE_URL=https://blog.example.com
DATA_DIR=/srv/xiaowork-blog
PROXY_MODE=host-loopback
WEB_HOST_PORT=13000
API_HOST_PORT=18081
APP_SUBNET=172.30.240.0/24
# 留空：脚本自动写入 APP_SUBNET（见第 7 节）
TRUSTED_PROXY_CIDRS=
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_TLS=starttls
SMTP_USERNAME=CHANGE_ME
SMTP_PASSWORD=CHANGE_ME
SMTP_FROM='XiaoWork Blog <no-reply@example.com>'
# POSTGRES_PASSWORD、APP_SECRET_KEY 由脚本首次运行时随机生成，不要手填，也不要修改
```

安装成功后会显示“反代方式 host-loopback”，以及需要在 1Panel 中填写的两个地址。

确认端口只监听在本机：

```bash
sudo ss -tlnp | grep -E ':13000|:18081'   # 应只出现 127.0.0.1:13000 和 127.0.0.1:18081
```

## 4. 在 1Panel 中配置反向代理

### 4.1 创建网站

1. 打开 **网站 → 网站 → 创建网站 → 反向代理**。
2. 主域名填正式域名，例如 `blog.example.com`。
3. 代理地址填 `http://127.0.0.1:13000`（等于 `WEB_HOST_PORT`）。
4. 创建。

### 4.2 添加 `/api/v1/` 与 `/media/` 两条反代

进入该网站的 **设置 → 反向代理**，新建两条规则：

| 名称 | 前端请求路径 | 后端代理地址 |
|---|---|---|
| api | `/api/v1/` | `http://127.0.0.1:18081` |
| media | `/media/` | `http://127.0.0.1:18081` |

⚠️ 后端代理地址**末尾不要加 `/`，也不要加任何路径**。

- 写成 `http://127.0.0.1:18081/` 时，nginx 会把 `/api/v1/` 前缀去掉，所有 API 都会返回 404。
- 这是唯一需要特别注意的地方：OpenResty 必须原样转发 `/api/v1/...` 与 `/media/...`。

### 4.3 HTTPS 证书

在网站的 **HTTPS** 页面申请或选择证书（Let's Encrypt 即可），并开启 HTTP 跳转 HTTPS。应用本身会发送 HSTS。

### 4.4 核对生成的配置

在网站设置里打开 **配置文件**，确认以下内容。1Panel 生成的反代配置默认已经包含这些请求头；如果缺少，请补上：

```nginx
client_max_body_size 12m;                 # 图片上传上限 10 MiB + multipart 开销

location ^~ /api/v1/ {
    proxy_pass http://127.0.0.1:18081;    # 不带结尾斜杠：保留 /api/v1 前缀
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
# /media/ 同上，proxy_pass 相同
# location / { proxy_pass http://127.0.0.1:13000; ...相同的四个请求头... }
```

要点：

- **`Host` 必须是浏览器访问的域名**（`$host` 或 `$http_host`）。如果被改成 `127.0.0.1:13000`，CSRF 来源校验会拒绝登录等写操作。
- **`X-Forwarded-Proto` 必须是 `$scheme`**，即 https。
- **`X-Forwarded-For` 用 `$proxy_add_x_forwarded_for`**。API 会从右向左读取，只信任本机的 app 网络（见第 7 节）。
- 不要为本站开启 1Panel 的缓存。后台、个人中心与认证页面都带有 `Cache-Control: private, no-store`。

完整的等价配置见 [`deploy/nginx/openresty-host-loopback.conf`](../../deploy/nginx/openresty-host-loopback.conf)，也可以作为纯 nginx 的参考。

## 5. 创建管理员与定时备份

```bash
sudo deploy/manage.sh admin create-admin -email you@example.com -nickname 博主
sudo crontab -e   # 30 3 * * * /opt/xiaowork-blog/deploy/manage.sh backup --label daily >> /srv/xiaowork-blog/backups/backup.log 2>&1
```

更新、回滚、备份、恢复的用法与 NPM 模式完全相同（见 [部署与运维手册](部署与运维手册.md) §6–§11）。脚本会按 `.env` 中的 `PROXY_MODE` 自动选择对应的 compose 配置。

## 6. 上线检查

在服务器上执行：

```bash
curl -sI https://blog.example.com/ | grep -iE 'HTTP/|strict-transport|content-security'
curl -s  https://blog.example.com/api/v1/site | head -c 200; echo
curl -s  https://blog.example.com/robots.txt
curl -sI https://blog.example.com/media/not-exist.webp | head -1   # 应为 404，由 API 返回，说明 /media/ 已转发
curl -s -o /dev/null -w '%{http_code}\n' --max-time 3 http://<服务器公网IP>:13000/   # 应为 000（连不上）
```

**确认真实客户端 IP**：

1. 用手机网络打开站点。
2. 执行 `sudo tail -n 5 /opt/1panel/apps/openresty/openresty/www/sites/<站点>/log/access.log`。日志路径以 1Panel 网站设置里的“日志”为准。
3. 日志中应为手机的公网 IP。

限流按这个 IP 计算。如果站点前面还有 CDN（例如 Cloudflare），OpenResty 看到的是 CDN 地址。此时需要先在 OpenResty 中按 CDN 官方网段配置 `real_ip`，本指南不涉及。

## 7. 可信代理与安全说明

- 本模式下 `TRUSTED_PROXY_CIDRS` 由脚本写为 `APP_SUBNET`（默认 `172.30.240.0/24`），**只信任这一个网段**。原因：
  - 请求经 127.0.0.1 的发布端口进入容器时，来源地址是 app 网络的网关；
  - Nuxt 服务端渲染调用 API 时，也来自这个网段。
- 浏览器伪造的 `X-Forwarded-For` 会被忽略。演练中，同一来源用 4 个不同的伪造地址注册，第 4 次被限流（429）。
- Cookie 使用 `__Host-` 前缀，带 `Secure`，`SameSite=Lax`；跨站写请求返回 403。
- **剩余风险**：本机上任何使用主机网络的进程（包括 1Panel 中其他 host 网络容器）都能连到 127.0.0.1:13000/18081，并自带 `X-Forwarded-For`。这样它可以影响按 IP 计算的限流，但无法绕过登录、CSRF 或权限检查。只在可信服务器上使用本模式；不要把这两个端口改成绑定 `0.0.0.0`。
- 防火墙只需开放 22、80、443。13000/18081 只监听 127.0.0.1，不需要、也不应该在 1Panel 防火墙中放行。

## 8. 从 NPM 模式切换，或切回

1. 编辑 `deploy/.env`：
   - 设置 `PROXY_MODE=host-loopback` 或 `docker-network`；
   - 切到 docker-network 时，需要 `PROXY_NETWORK`；
   - **清空 `TRUSTED_PROXY_CIDRS=`**，让脚本按新模式重新检测（否则会保留旧模式的可信网段）。
2. 运行 `sudo deploy/manage.sh install`：
   - 服务正常时，它只按新配置重建 api/web 容器，不切换版本、不动数据库；
   - `.env` 没有改动时，不会重建任何容器。
3. 修改代理：在 1Panel 或 NPM 中改成对应地址。

演练中已验证两个方向的切换，切换前后数据完全一致。

## 9. 故障排查

| 现象 | 处理 |
|---|---|
| 安装提示端口被占用 | 13000/18081 已被其他程序使用。换一个空闲端口写入 `.env`，1Panel 中的地址同步修改 |
| 提示需要 Docker Compose 2.24.4 | 升级 Docker 的 compose 插件（`docker compose version` 查看） |
| `/api/v1/...` 全部 404，而首页正常 | 后端代理地址末尾多了 `/`，或填了路径。改成 `http://127.0.0.1:18081` |
| 502 Bad Gateway | 运行 `sudo deploy/manage.sh status`；`curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:13000/health` 应为 200 |
| 登录或评论返回 403 | `PUBLIC_BASE_URL` 必须与浏览器地址完全一致；`Host` 必须是域名；`X-Forwarded-Proto` 必须是 https |
| 所有人共享限流 | OpenResty 没有传 `X-Forwarded-For`，或前面有 CDN（见第 6 节） |
| 上传图片返回 413 | 网站配置中加 `client_max_body_size 12m;` |
