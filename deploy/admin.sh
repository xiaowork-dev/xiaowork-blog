#!/usr/bin/env bash
# Administrator tools inside the production stack (P8-A).
#
#   sudo deploy/admin.sh create-admin -email you@example.com -nickname 博主   # asks for the password twice (hidden)
#   sudo deploy/admin.sh media-check                                         # database ↔ media file consistency
#   sudo deploy/admin.sh status                                              # versions and health
source "$(dirname "$0")/lib.sh"

check_tools
load_env
[ -n "$APP_VERSION" ] || die "尚未安装"
case "${1:-}" in
  create-admin)
    # blog-admin reads the password from the first line of stdin. On a terminal it is asked for here without
    # echo (twice); otherwise it is piped in, e.g. from a file with mode 600.
    if [ -t 0 ]; then
      read -rsp "管理员密码（12–128 个字符）：" pw; echo
      read -rsp "再次输入密码：" pw2; echo
      [ "$pw" = "$pw2" ] || die "两次输入的密码不一致"
      printf '%s\n' "$pw" | dc run --rm -T --no-deps --entrypoint /usr/local/bin/blog-admin api "$@" \
        || die "blog-admin create-admin 未成功（原因见上方输出）"
      unset pw pw2
    else
      dc run --rm -T --no-deps --entrypoint /usr/local/bin/blog-admin api "$@" || die "blog-admin create-admin 未成功（原因见上方输出）"
    fi
    ;;
  media-check)
    dc run --rm -T --no-deps --entrypoint /usr/local/bin/blog-admin api "$@" || die "blog-admin media-check 未成功（原因见上方输出）"
    ;;
  status)
    echo "当前版本：$(current_version || echo -)   上一版本：$(previous_version || echo -)"
    echo "数据库结构版本：$(schema_version)"
    dc ps --format 'table {{.Service}}\t{{.Status}}'
    ;;
  *) sed -n '2,6p' "$0"; exit 1;;
esac
