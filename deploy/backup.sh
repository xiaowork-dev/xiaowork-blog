#!/usr/bin/env bash
# Full backup (P8-A): PostgreSQL dump (pg_dump -Fc), media archive, deploy/.env copy, manifest and SHA256SUMS
# in DATA_DIR/backups/<UTC time>-<label>/. Keeps the newest BACKUP_KEEP backups.
#
#   sudo deploy/backup.sh [--label daily]
#
# Off-site copy (recommended): sync DATA_DIR/backups to another machine, e.g.
#   rsync -a --delete-after /srv/xiaowork-blog/backups/ backup@other-host:/backups/xiaowork-blog/
# The backups contain secrets (env.backup) and personal data: keep the target private and encrypted.
source "$(dirname "$0")/lib.sh"

LABEL=manual
while [ $# -gt 0 ]; do
  case "$1" in
    --label) LABEL="${2:?--label 需要参数}"; shift 2;;
    -h|--help) sed -n '2,10p' "$0"; exit 0;;
    *) die "未知参数：$1";;
  esac
done
[[ "$LABEL" =~ ^[A-Za-z0-9._-]{1,40}$ ]] || die "--label 只能包含字母、数字、. _ -"

check_tools
load_env
[ -n "$APP_VERSION" ] || die "尚未安装，无数据可备份"
wait_healthy db || die "数据库未运行"
check_disk 1024
backup_now "$LABEL"
echo "$BACKUP_PATH"
