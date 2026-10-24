#!/usr/bin/env bash
# First installation of XiaoWork Blog on a Linux Docker host (P8-A).
#
#   sudo deploy/install.sh
#
# 1st run without deploy/.env: creates it from env.production.example with generated database password and
#   APP_SECRET_KEY, then stops so you can fill PUBLIC_BASE_URL, DATA_DIR, SMTP_* and PROXY_NETWORK.
# 2nd run: checks the host, builds the images of the current commit, starts db → migrate → api → web,
#   waits for health checks and a smoke test. Safe to run again: an installed, healthy stack is left as is.
source "$(dirname "$0")/lib.sh"

check_tools

if [ ! -f "$ENV_FILE" ]; then
  umask 077
  sed -e "s|^POSTGRES_PASSWORD=.*$|POSTGRES_PASSWORD=$(openssl rand -hex 24)|" \
      -e "s|^APP_SECRET_KEY=.*$|APP_SECRET_KEY=$(openssl rand -base64 48 | tr -d '\n')|" \
      "$DEPLOY_DIR/env.production.example" > "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  log "已生成 $ENV_FILE（数据库密码与 APP_SECRET_KEY 已随机生成）。"
  log "请编辑其中标为 CHANGE_ME 的项（PUBLIC_BASE_URL、SMTP_*）并确认 DATA_DIR、PROXY_NETWORK，然后再次运行本脚本。"
  exit 0
fi

load_env
check_proxy_network
prepare_dirs
check_disk 3072
detect_trusted_proxies

if [ -n "$(current_version || true)" ]; then
  APP_VERSION="$(current_version)"; export APP_VERSION
  if image_exists "xiaowork-blog-api:$APP_VERSION" && dc ps --status running -q web >/dev/null 2>&1 && wait_healthy db api web 2>/dev/null && smoke_test; then
    log "已安装版本 $APP_VERSION 且运行正常；无需重复安装。升级请使用 deploy/update.sh"
    exit 0
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

record_release "$VERSION" "$(current_version || true)"
log "安装完成：版本 $VERSION，数据库结构版本 $(schema_version)"
cat <<EOF

下一步：
  1. 创建首个管理员：  sudo deploy/admin.sh create-admin -email you@example.com -nickname 博主
  2. 在 Nginx Proxy Manager 中添加代理主机（见 docs/deploy/部署与运维手册.md §4）：
       域名 → http://xiaowork-blog-web:3000；自定义位置 /api/v1 与 /media → http://xiaowork-blog-api:8080
  3. 配置定时备份：  sudo crontab -e  →  30 3 * * * $DEPLOY_DIR/backup.sh --label daily >> $DATA_DIR/backups/backup.log 2>&1
EOF
