# 部署演练环境（P8-A）

`drill.sh` 在一个 **Docker-in-Docker** 容器中模拟一台全新的 Linux 服务器，用来验证 `deploy/*.sh` 的安装、更新、回滚、备份和恢复。演练环境包含：

- 自己的 Docker 守护进程；
- 一个扮演 Nginx Proxy Manager 的 nginx 容器（自签名 HTTPS，配置为 `deploy/nginx/npm-equivalent.conf`）；
- Mailpit，扮演 SMTP；
- 一个裸 Git 仓库，扮演 GitHub。

它**不连接任何服务器，也不使用开发机上的 Docker 卷**。

## 启动

```bash
# 可选：先在开发机上导出基础镜像，避免在演练容器里重新下载
docker save golang:1.27.3-alpine alpine:3.22 node:24.22.1-alpine postgres:18.6-alpine \
  axllent/mailpit:v1.32.0 nginx:1.30.5-alpine -o <演练目录>/base-images.tar

docker run -d --privileged --name xwb-drill-a -p 127.0.0.1:18443:443 -p 127.0.0.1:18025:8025 \
  -v "$PWD:/src:ro" -v "<演练目录>:/drill" -e DOCKER_TLS_CERTDIR= docker:29.8.2-dind --tls=false
docker exec xwb-drill-a sh -c 'apk add -q bash && tr -d "\r" < /src/deploy/test/drill.sh > /usr/local/bin/drill && chmod +x /usr/local/bin/drill'
docker exec xwb-drill-a drill prepare
```

## 演练步骤（与 P8-A 交付记录一致）

```bash
docker exec xwb-drill-a bash -c 'cd /opt/xiaowork-blog && deploy/install.sh'   # 生成 deploy/.env
docker exec xwb-drill-a drill configure                                        # 填入演练值
docker exec xwb-drill-a bash -c 'cd /opt/xiaowork-blog && deploy/install.sh'   # 安装；再运行一次检验幂等
# 浏览器或脚本访问 https://localhost:18443（自签名证书），邮件在 http://127.0.0.1:18025
docker exec xwb-drill-a drill counts                                           # 记录数据基线
docker exec xwb-drill-a drill sync                  # 把开发机工作区作为新提交推到演练仓库
docker exec xwb-drill-a drill scenario migration    # 或 broken-api / fix-api / bad-migration / drop-bad-migration
docker exec xwb-drill-a bash -c 'cd /opt/xiaowork-blog && deploy/update.sh'
docker exec xwb-drill-a bash -c 'cd /opt/xiaowork-blog && deploy/rollback.sh'
```

### 版本管理演练（P8-B2）

`drill mock start` 在演练容器中启动一个模拟 GitHub Releases API（`http://127.0.0.1:9999`，仓库名 `xwb/blog`），不访问真实的 GitHub。为方便起见，可以建一个包装命令：

```bash
docker exec xwb-drill-a sh -c 'printf "#!/bin/sh\ncd /opt/xiaowork-blog && GITHUB_API=http://127.0.0.1:9999 GITHUB_REPO=xwb/blog exec deploy/manage.sh \"\$@\"\n" > /usr/local/bin/m && chmod +x /usr/local/bin/m'
docker exec xwb-drill-a drill mock start
docker exec xwb-drill-a drill release v0.1.0 stable            # 在 origin 的 main 头部打标签并发布
docker exec xwb-drill-a drill release v0.2.0-rc.1 prerelease   # 预发布版；draft 表示草稿（应被忽略）
docker exec xwb-drill-a drill scenario unreleased              # main 上的未发布提交
docker exec -it xwb-drill-a m install                          # 交互式首次安装（引导配置）
docker exec xwb-drill-a m check
docker exec -it xwb-drill-a m update                           # 显示计划，输入版本号确认
docker exec xwb-drill-a drill mock mode 403                    # 也可以用 500、badjson、ok；drill mock stop 模拟断网
docker exec xwb-drill-a drill move-tag v0.1.0                  # 模拟发布标签被篡改
```

恢复演练使用第二个全新的演练容器：把备份目录复制到 `/drill`，然后按运维手册 §11 执行。

演练值说明：

- `PUBLIC_BASE_URL=https://localhost:18443`；
- 只有 `localhost` 站点允许 `SMTP_TLS=none`；
- `GOPROXY` 使用国内镜像，因为演练容器无法直接访问 proxy.golang.org。

## 清理

```bash
docker rm -f -v xwb-drill-a xwb-drill-b    # -v：同时删除演练容器自己的匿名卷 /var/lib/docker（只含演练数据）
```
