#!/usr/bin/env bash
# One-command update to a new version (P8-A).
#
#   sudo deploy/update.sh                 # fetch origin/main and deploy it
#   sudo deploy/update.sh --ref v1.0.1    # deploy a tag / branch / commit
#   sudo deploy/update.sh --no-fetch      # deploy the commit that is already checked out
#
# Steps: pre-checks → build the new images (the running site is untouched) → full backup → migrate →
# switch api/web → health checks + smoke test. If anything fails after the switch, the previous images are
# started again automatically. Database migrations are not reverted automatically: the pre-update backup is
# printed so it can be restored deliberately with deploy/restore.sh if the old version needs the old schema.
source "$(dirname "$0")/lib.sh"

# The whole body is one compound command: bash parses it completely before running it, so the git checkout
# below cannot change the script text that is still to be executed.
{

REF=origin/main
FETCH=1
while [ $# -gt 0 ]; do
  case "$1" in
    --ref) REF="${2:?--ref 需要参数}"; shift 2;;
    --no-fetch) FETCH=0; shift;;
    -h|--help) sed -n '2,12p' "$0"; exit 0;;
    *) die "未知参数：$1";;
  esac
done

check_tools
load_env
check_proxy_network
detect_trusted_proxies
OLD="$(current_version || true)"
[ -n "$OLD" ] || die "尚未安装（$STATE_DIR/current 不存在）；请先运行 deploy/install.sh"
check_disk 3072

# Never overwrite local changes on the server.
if [ -n "$(git -C "$REPO_DIR" status --porcelain --untracked-files=no)" ]; then
  git -C "$REPO_DIR" status --short >&2
  die "代码目录有未提交的修改，已停止更新（不会覆盖本地改动）"
fi
if [ "$FETCH" = 1 ]; then
  log "获取最新代码…"
  git -C "$REPO_DIR" fetch --tags origin
  git -C "$REPO_DIR" checkout --detach -q "$REF" || die "无法切换到 $REF"
fi
NEW="$(repo_version)"

if [ "$NEW" = "$OLD" ] && wait_healthy db api web 2>/dev/null; then
  log "当前已是版本 $NEW，且服务正常；无需更新"
  exit 0
fi

log "更新：$OLD → $NEW"
build_images "$NEW"     # the site keeps running on $OLD while this builds

# Pre-update backup with the running (old) version.
export APP_VERSION="$OLD"
backup_now "pre-update-$NEW"
PRE_BACKUP="$BACKUP_PATH"
SCHEMA_BEFORE="$(schema_version)"

export APP_VERSION="$NEW"
# Apply changes to the db service definition (compose recreates it only when its configuration changed;
# the data stays in the named volume).
dc up -d db
wait_healthy db || die "数据库未能通过健康检查；备份：$PRE_BACKUP"
log "执行数据库迁移（新版本）…"
if ! dc run --rm migrate; then
  export APP_VERSION="$OLD"
  git -C "$REPO_DIR" checkout --detach -q "$OLD" || true
  die "数据库迁移失败，服务仍在运行旧版本 $OLD（未切换）。迁移在事务中执行，结构版本仍为 $SCHEMA_BEFORE；备份：$PRE_BACKUP"
fi
SCHEMA_AFTER="$(schema_version)"

log "切换 api / web 到 $NEW…"
# Inside the condition so that a failing start (compose exits non-zero when api never gets healthy) leads to
# the automatic rollback below instead of aborting the script.
if dc up -d --no-deps api web && wait_healthy api web && smoke_test; then
  record_release "$NEW" "$OLD"
  prune_images
  log "更新成功：$NEW（数据库结构版本 $SCHEMA_BEFORE → $SCHEMA_AFTER）。回滚：deploy/rollback.sh"
  exit 0
fi

warn "新版本 $NEW 未通过健康检查，自动回滚到 $OLD…"
export APP_VERSION="$OLD"
git -C "$REPO_DIR" checkout --detach -q "$OLD" 2>/dev/null || true
if dc up -d --no-deps api web && wait_healthy api web && smoke_test; then
  warn "已回滚到 $OLD，网站恢复运行。"
  if [ "$SCHEMA_AFTER" != "$SCHEMA_BEFORE" ]; then
    warn "注意：新版本的数据库迁移已执行（$SCHEMA_BEFORE → $SCHEMA_AFTER）。迁移遵循「只增不删」原则，旧版本通常可正常运行；"
    warn "如旧版本出现数据库相关错误，请恢复更新前的备份：deploy/restore.sh $PRE_BACKUP"
  fi
  die "更新失败，已回滚到 $OLD"
fi
die "回滚到 $OLD 也未能通过健康检查。请查看日志（docker compose -p $PROJECT logs api web），必要时执行：deploy/restore.sh $PRE_BACKUP"
exit 0
}
