#!/usr/bin/env bash
# Installation engine (P8-A; guided setup since P8-B2). Normally started by `deploy/manage.sh install`,
# which first checks out the newest published release.
#
#   sudo deploy/install.sh
#
# 1st run without deploy/.env: creates it from env.production.example with a generated database password
#   and APP_SECRET_KEY. On a terminal it then asks for the domain, data directory, Nginx Proxy Manager
#   network, app subnet and SMTP settings and continues; otherwise it stops so you can edit the file.
# 2nd run: checks the host, builds the images of the current commit, starts db → migrate → api → web,
#   waits for health checks and a smoke test. Safe to run again: an installed, healthy stack is left as is.
source "$(dirname "$0")/lib.sh"

# set_env KEY VALUE: write KEY='VALUE' into deploy/.env. Single quotes keep docker compose from expanding
# $ or treating # as a comment, so a single quote is the one character a value cannot contain.
set_env() {
  case "$2" in *"'"*|*$'\n'*) die "$1 的值不能包含单引号或换行";; esac
  K="$1" V="'$2'" awk 'BEGIN { k = ENVIRON["K"]; v = ENVIRON["V"] }
    index($0, k "=") == 1 { print k "=" v; done = 1; next } { print } END { if (!done) print k "=" v }' "$ENV_FILE" > "$ENV_FILE.tmp"
  mv -f "$ENV_FILE.tmp" "$ENV_FILE"
}

ask() {  # ask VAR PROMPT DEFAULT
  local reply
  read -r -p "$2${3:+ [$3]}：" reply
  printf -v "$1" '%s' "${reply:-$3}"
}

guided_config() {
  echo
  echo "== 首次配置（直接回车使用方括号中的默认值；之后可随时编辑 $ENV_FILE）=="
  local domain data mode net subnet web_port api_port smtp_host smtp_port smtp_tls smtp_user smtp_pass smtp_from n i free host_proxy
  while :; do
    ask domain "网站域名（不带 https://，例如 blog.example.com）" ""
    [[ "$domain" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]+)?$ ]] && break
    echo "  域名格式不正确"
  done
  ask data "数据目录（图片、备份与状态；数据库在 Docker 数据卷中）" "/srv/xiaowork-blog"
  # Which reverse proxy owns 80/443? A container on the host network (1Panel's OpenResty) suggests host-loopback.
  host_proxy="$(docker ps --filter network=host --format '{{.Names}} ({{.Image}})' 2>/dev/null | grep -iE 'openresty|nginx|caddy|traefik' | head -n 3 | tr '\n' ' ' || true)"
  echo "  反向代理方式："
  echo "    docker-network — 反代是容器（如 Nginx Proxy Manager），与博客共用一个自定义 Docker 网络；博客不占用任何主机端口"
  echo "    host-loopback  — 反代在主机网络上（如 1Panel 的 OpenResty），通过 127.0.0.1 上的端口转发到博客"
  [ -n "$host_proxy" ] && echo "  检测到主机网络中的反代容器：$host_proxy→ 建议选择 host-loopback"
  while :; do
    ask mode "反代方式" "$([ -n "$host_proxy" ] && echo host-loopback || echo docker-network)"
    case "$mode" in docker-network|host-loopback) break;; *) echo "  请输入 docker-network 或 host-loopback";; esac
  done
  if [ "$mode" = docker-network ]; then
    echo "  Nginx Proxy Manager 必须与博客在同一个自定义 Docker 网络中。现有自定义网络："
    for n in $(docker network ls --filter driver=bridge --format '{{.Name}}'); do
      [ "$n" = bridge ] && continue
      echo "    $n  （成员：$(docker network inspect -f '{{range .Containers}}{{.Name}} {{end}}' "$n")）"
    done
    while :; do
      ask net "NPM 所在网络名" "npm_default"
      case "$net" in bridge|host|none) echo "  不能使用 Docker 预置网络 $net（见运维手册 §4）"; continue;; esac
      docker network inspect "$net" >/dev/null 2>&1 && break
      echo "  网络 $net 不存在"
    done
  else
    echo "  博客只在 127.0.0.1 上发布两个端口（外网无法直接访问），由主机上的反代转发："
    while :; do
      ask web_port "网站（Nuxt）端口" "13000"
      [[ "$web_port" =~ ^[0-9]+$ ]] && [ "$web_port" -ge 1024 ] && [ "$web_port" -le 65535 ] || { echo "  端口应为 1024–65535"; continue; }
      port_listening "$web_port" && { echo "  端口 $web_port 已被占用"; continue; }
      break
    done
    while :; do
      ask api_port "API 端口" "18081"
      [[ "$api_port" =~ ^[0-9]+$ ]] && [ "$api_port" -ge 1024 ] && [ "$api_port" -le 65535 ] || { echo "  端口应为 1024–65535"; continue; }
      [ "$api_port" != "$web_port" ] || { echo "  不能与网站端口相同"; continue; }
      port_listening "$api_port" && { echo "  端口 $api_port 已被占用"; continue; }
      break
    done
  fi
  free=""
  for i in 172.30.240.0/24 172.31.240.0/24 10.254.240.0/24 192.168.240.0/24; do
    if [ -z "$(subnet_conflicts "$i")" ]; then free="$i"; break; fi
  done
  while :; do
    ask subnet "博客内部网络网段（不能与现有网络重叠）" "$free"
    [[ "$subnet" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+$ ]] || { echo "  格式应为 a.b.c.d/24"; continue; }
    n="$(subnet_conflicts "$subnet")"
    [ -z "$n" ] && break
    echo "  与现有网络重叠：$n"
  done
  echo "  发信服务（注册验证与找回密码邮件）。公网站点必须使用 TLS："
  ask smtp_host "SMTP 主机" ""
  ask smtp_port "SMTP 端口（587 = STARTTLS，465 = TLS）" "587"
  if [ "$smtp_port" = 465 ]; then smtp_tls=tls; else smtp_tls=starttls; fi
  ask smtp_tls "TLS 方式（starttls / tls）" "$smtp_tls"
  ask smtp_user "SMTP 用户名" ""
  read -r -s -p "SMTP 密码 / 授权码（输入时不显示）：" smtp_pass; echo
  ask smtp_from "发件人" "XiaoWork Blog <no-reply@${domain%%:*}>"
  set_env PUBLIC_BASE_URL "https://$domain"
  set_env DATA_DIR "$data"
  set_env PROXY_MODE "$mode"
  if [ "$mode" = docker-network ]; then
    set_env PROXY_NETWORK "$net"
  else
    set_env WEB_HOST_PORT "$web_port"
    set_env API_HOST_PORT "$api_port"
  fi
  set_env APP_SUBNET "$subnet"
  set_env SMTP_HOST "$smtp_host"
  set_env SMTP_PORT "$smtp_port"
  set_env SMTP_TLS "$smtp_tls"
  set_env SMTP_USERNAME "$smtp_user"
  set_env SMTP_PASSWORD "$smtp_pass"
  set_env SMTP_FROM "$smtp_from"
  unset smtp_pass
}

check_tools

if [ ! -f "$ENV_FILE" ]; then
  umask 077
  sed -e "s|^POSTGRES_PASSWORD=.*$|POSTGRES_PASSWORD=$(openssl rand -hex 24)|" \
      -e "s|^APP_SECRET_KEY=.*$|APP_SECRET_KEY=$(openssl rand -base64 48 | tr -d '\n')|" \
      "$DEPLOY_DIR/env.production.example" > "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  log "已生成 $ENV_FILE（数据库密码与 APP_SECRET_KEY 已随机生成，只保存在本机）。"
  if [ -t 0 ] && [ "${XWB_NONINTERACTIVE:-0}" != 1 ]; then
    guided_config
    printf '配置已保存。现在开始安装？[Y/n] '
    read -r answer
    case "${answer:-y}" in [Yy]*) ;; *) log "稍后运行 sudo deploy/manage.sh install 继续"; exit 0;; esac
  else
    log "请编辑其中标为 CHANGE_ME 的项（PUBLIC_BASE_URL、SMTP_*）并确认 DATA_DIR、PROXY_NETWORK、APP_SUBNET，然后再次运行。"
    exit 0
  fi
fi

load_env
check_proxy_network
if [ -z "$(current_version || true)" ]; then
  # Conflict prechecks before the first start (the full read-only report is deploy/precheck.sh).
  conflicts="$(subnet_conflicts "${APP_SUBNET:-172.30.240.0/24}")"
  [ -z "$conflicts" ] || die "APP_SUBNET=${APP_SUBNET:-172.30.240.0/24} 与现有 Docker 网络重叠：$conflicts。请在 $ENV_FILE 中换一个空闲网段"
  port_listening 443 || warn "本机没有程序监听 443 端口：反向代理（Nginx Proxy Manager / OpenResty）是否在运行？（博客本身不占用 80/443）"
fi
# host-loopback: the published 127.0.0.1 ports must be free unless this stack already owns them.
check_host_ports
prepare_dirs
check_disk 3072
detect_trusted_proxies

if [ -n "$(current_version || true)" ]; then
  APP_VERSION="$(current_version)"; export APP_VERSION
  if image_exists "xiaowork-blog-api:$APP_VERSION" && dc ps --status running -q web >/dev/null 2>&1 && wait_healthy db api web 2>/dev/null && smoke_test; then
    # Apply edits to deploy/.env (PROXY_MODE, ports, TRUSTED_PROXY_CIDRS…): compose recreates only the
    # containers whose configuration changed and leaves the others running.
    if dc up -d --no-deps api web >/dev/null 2>&1 && wait_healthy api web && smoke_test; then
      log "已安装版本 $APP_VERSION 且运行正常（反代方式 $PROXY_MODE，deploy/.env 的改动已生效）；无需重复安装。升级请使用 deploy/manage.sh update"
      exit 0
    fi
    warn "按 deploy/.env 重新创建 api/web 后未通过检查，将重新启动该版本（数据不会被删除）。"
  fi
  warn "检测到已安装版本 $APP_VERSION，但服务未正常运行，将重新启动该版本（数据不会被删除）。"
fi

if docker volume inspect xiaowork-blog_pgdata >/dev/null 2>&1 && [ -z "$(current_version || true)" ]; then
  warn "数据库卷 xiaowork-blog_pgdata 已存在（可能来自之前的安装或恢复），将直接使用其中的数据。"
fi

# A recorded release whose images still exist is restarted as is (e.g. after an interrupted update);
# otherwise the checked-out commit is built.
VERSION="$(current_version || true)"
if [ -z "$VERSION" ] || ! image_exists "xiaowork-blog-api:$VERSION" || ! image_exists "xiaowork-blog-web:$VERSION"; then
  VERSION="$(repo_version)"
fi
export APP_VERSION="$VERSION"
image_exists "xiaowork-blog-api:$VERSION" && image_exists "xiaowork-blog-web:$VERSION" || build_images "$VERSION"

log "启动数据库…"
dc up -d db
wait_healthy db || die "数据库未能通过健康检查"
log "执行数据库迁移…"
dc run --rm migrate || die "数据库迁移失败，请查看上方日志"
log "启动 api / web…"
dc up -d api web || true   # compose exits non-zero if api never gets healthy; wait_healthy reports why
wait_healthy api web || die "服务未能通过健康检查（查看：docker compose -p $PROJECT logs api web）"
smoke_test || die "冒烟测试失败"

record_release "$VERSION" "$(current_version || true)" "${RELEASE_REF:-checked-out}"
log "安装完成：版本 $VERSION，数据库结构版本 $(schema_version)，反代方式 $PROXY_MODE"
proxy_next_step() {
  if [ "$PROXY_MODE" = host-loopback ]; then
    printf '%s\n' "在 1Panel（OpenResty）或主机上的 nginx 中添加反向代理（见 docs/deploy/1Panel-OpenResty部署指南.md）：" \
      "       / → http://127.0.0.1:$WEB_HOST_PORT ；/api/v1/ 与 /media/ → http://127.0.0.1:$API_HOST_PORT（保留路径）"
  else
    printf '%s\n' "在 Nginx Proxy Manager 中添加代理主机（见 docs/deploy/部署与运维手册.md §4）：" \
      "       域名 → http://xiaowork-blog-web:3000；自定义位置 /api/v1/ 与 /media/ → http://xiaowork-blog-api:8080"
  fi
}
cat <<EOF

下一步：
  1. 创建首个管理员：  sudo $DEPLOY_DIR/manage.sh admin create-admin -email you@example.com -nickname 博主
  2. $(proxy_next_step)
  3. 配置定时备份：  sudo crontab -e  →  30 3 * * * $DEPLOY_DIR/manage.sh backup --label daily >> $DATA_DIR/backups/backup.log 2>&1
  4. 以后检查与安装新版本：  sudo $DEPLOY_DIR/manage.sh check / update
EOF
