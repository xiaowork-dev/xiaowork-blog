#!/usr/bin/env bash
# Restore a backup made by backup.sh (P8-A).
#
#   sudo deploy/restore.sh /srv/xiaowork-blog/backups/20261024T030000Z-daily [--yes] [--no-safety-backup]
#
# Order: verify checksums → safety backup of the current state → stop api/web → replace the database
# (drop + create + pg_restore) → move the current media directory aside (media.before-restore-<time>) and
# unpack the backup → run migrations (the code may be newer than the backup) → start api/web → health.
# Nothing is deleted: the previous media directory stays next to the new one until you remove it yourself.
#
# On a new machine: install.sh first (same deploy/.env, or copy env.backup to deploy/.env), then restore.sh.
source "$(dirname "$0")/lib.sh"

SRC=""; YES=0; SAFETY=1
while [ $# -gt 0 ]; do
  case "$1" in
    --yes) YES=1; shift;;
    --no-safety-backup) SAFETY=0; shift;;
    -h|--help) sed -n '2,12p' "$0"; exit 0;;
    -*) die "未知参数：$1";;
    *) SRC="$1"; shift;;
  esac
done
[ -n "$SRC" ] || die "用法：deploy/restore.sh <备份目录> [--yes]"
SRC="$(cd "$SRC" && pwd)" || die "备份目录不存在"
for f in db.dump media.tar.gz SHA256SUMS manifest.txt; do [ -f "$SRC/$f" ] || die "备份不完整，缺少 $f"; done

check_tools
load_env
[ -n "$APP_VERSION" ] || die "尚未安装；请先运行 deploy/install.sh（可使用备份中的 env.backup 作为 deploy/.env）"
log "校验备份文件…"
(cd "$SRC" && sha256sum -c --quiet SHA256SUMS) || die "备份校验失败（文件损坏或被修改）"
sed 's/^/  /' "$SRC/manifest.txt"

if [ "$YES" != 1 ]; then
  printf '将用此备份替换当前网站的数据库和图片。输入 RESTORE 确认：'
  read -r answer
  [ "$answer" = RESTORE ] || die "已取消"
fi
check_disk 2048
wait_healthy db || die "数据库未运行"
if [ "$SAFETY" = 1 ]; then
  backup_now "before-restore"
  log "当前数据已先备份到 $BACKUP_PATH"
fi

log "停止 api / web…"
dc stop api web
log "恢复数据库…"
dc exec -T db sh -c 'dropdb -U "$POSTGRES_USER" --if-exists --force "$POSTGRES_DB" && createdb -U "$POSTGRES_USER" -O "$POSTGRES_USER" "$POSTGRES_DB"'
dc exec -T db sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner --no-privileges --exit-on-error' < "$SRC/db.dump" \
  || die "pg_restore 失败；数据库可能为空，可再次运行本脚本或恢复 before-restore 备份"

log "恢复图片…"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
if [ -d "${DATA_DIR:?}/media" ]; then
  mv "$DATA_DIR/media" "$DATA_DIR/media.before-restore-$stamp"
fi
tar -C "$DATA_DIR" -xzf "$SRC/media.tar.gz"
chown -R 10001:10001 "$DATA_DIR/media" 2>/dev/null || warn "无法 chown $DATA_DIR/media（需要 root）"

log "执行数据库迁移（如代码比备份新）…"
dc run --rm migrate || die "迁移失败"
dc up -d api web
wait_healthy api web || die "恢复后服务未能通过健康检查"
smoke_test || die "冒烟测试失败"
log "恢复完成。数据库结构版本 $(schema_version)；图片文件 $(find "$DATA_DIR/media" -type f | wc -l | tr -d ' ') 个。"
log "原图片目录保留在 $DATA_DIR/media.before-restore-$stamp，确认无误后可自行删除。"
log "建议运行：deploy/admin.sh media-check"
