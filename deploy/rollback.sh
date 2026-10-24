#!/usr/bin/env bash
# Switch back to the previous release (P8-A).
#
#   sudo deploy/rollback.sh
#
# Starts the previous api/web images recorded by update.sh (they are kept on the host) and checks health.
# The database is not changed; to also return the data to the pre-update state use
#   deploy/restore.sh <DATA_DIR>/backups/<time>-pre-update-<version>
source "$(dirname "$0")/lib.sh"

# The whole body is one compound command: bash parses it completely before running it, so the git checkout
# below cannot change the script text that is still to be executed.
{

check_tools
load_env
CUR="$(current_version || true)"
PREV="$(previous_version || true)"
[ -n "$PREV" ] || die "没有可回滚的上一版本（$STATE_DIR/previous 不存在）"
image_exists "xiaowork-blog-api:$PREV" && image_exists "xiaowork-blog-web:$PREV" \
  || die "上一版本 $PREV 的镜像已不存在；可用 git checkout $PREV 后运行 deploy/update.sh --no-fetch 重新构建"

log "回滚：$CUR → $PREV"
export APP_VERSION="$PREV"
dc up -d --no-deps api web && wait_healthy api web || die "上一版本 $PREV 未能通过健康检查"
smoke_test || die "冒烟测试失败"
git -C "$REPO_DIR" checkout --detach -q "$PREV" 2>/dev/null || warn "代码目录未能切换到 $PREV（不影响运行中的服务）"
echo "$CUR" > "$STATE_DIR/previous"
echo "$PREV" > "$STATE_DIR/current"
echo "$(date -u +%FT%TZ) rollback $PREV" >> "$STATE_DIR/history"
log "已回滚到 $PREV（再次运行可切回 $CUR）"
exit 0
}
