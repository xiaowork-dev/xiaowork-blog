#!/usr/bin/env bash
# XiaoWork Blog server management — the single entry point for the VPS owner (P8-B2).
#
#   sudo deploy/manage.sh                      interactive menu (on a terminal)
#   sudo deploy/manage.sh install [VERSION]    first installation of the newest stable release (or VERSION)
#   sudo deploy/manage.sh status               installed version, commit, schema version, service health
#   sudo deploy/manage.sh check [--pre]        published releases on GitHub and whether an update exists
#   sudo deploy/manage.sh update [VERSION]     show the plan, confirm, then run deploy/update.sh
#   sudo deploy/manage.sh rollback             back to the previous release (database schema stays)
#   sudo deploy/manage.sh backup | restore DIR | precheck | admin ...   wrappers around the other scripts
#
# Options: --pre (also offer pre-releases), --yes (no question; required when not on a terminal),
#          --allow-unreleased (install/update a branch or commit that is not a published release).
# Only published GitHub Releases whose tag is vMAJOR.MINOR.PATCH are offered; the newest main commit is
# never installed by default, and nothing here runs automatically (no cron, no webhook).
#
# Environment: GITHUB_REPO=owner/name (default: from the origin URL), GITHUB_API (default
# https://api.github.com), GITHUB_TOKEN (optional, only raises the API rate limit).
source "$(dirname "$0")/lib.sh"

{
GITHUB_API="${GITHUB_API:-https://api.github.com}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf -- "$TMP_DIR"' EXIT

usage() { sed -n '2,20p' "$0"; }

# ---- GitHub releases -------------------------------------------------------------------------------------

github_repo() {
  if [ -n "${GITHUB_REPO:-}" ]; then echo "$GITHUB_REPO"; return; fi
  local url; url="$(git -C "$REPO_DIR" remote get-url origin 2>/dev/null || true)"
  url="${url%.git}"
  case "$url" in
    https://github.com/*/*) echo "${url#https://github.com/}";;
    git@github.com:*/*) echo "${url#git@github.com:}";;
    *) die "无法从 origin（$url）识别 GitHub 仓库；请设置 GITHUB_REPO=所有者/仓库名";;
  esac
}

# The JSON is parsed with node from the same pinned image the web build uses (no jq/python needed on the
# host); the container has no network access.
node_image() { awk '/^FROM node:/ {print $2; exit}' "$REPO_DIR/apps/web/Dockerfile"; }
releases_js='
const [mode, arg] = process.argv.slice(1)
let s = ""
process.stdin.on("data", d => (s += d)).on("end", () => {
  let list
  try { list = JSON.parse(s) } catch { console.error("响应不是有效的 JSON"); process.exit(3) }
  if (!Array.isArray(list)) { console.error("意外的响应：" + ((list && list.message) || typeof list)); process.exit(3) }
  const re = /^v(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$/
  const key = t => { const m = re.exec(t); return [+m[1], +m[2], +m[3], m[4] || ""] }
  const cmp = (a, b) => {
    const x = key(a), y = key(b)
    for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return x[i] - y[i]
    if (!x[3] !== !y[3]) return x[3] ? -1 : 1
    return x[3].localeCompare(y[3], "en", { numeric: true })
  }
  const rel = list.filter(r => r && !r.draft && typeof r.tag_name === "string" && re.test(r.tag_name))
  rel.sort((a, b) => cmp(b.tag_name, a.tag_name))
  const kind = r => (r.prerelease || key(r.tag_name)[3] ? "prerelease" : "stable")
  if (mode === "list") {
    for (const r of rel) console.log([r.tag_name, kind(r), (r.published_at || "").slice(0, 10), (r.name || r.tag_name).replace(/\s+/g, " ")].join("\t"))
  } else if (mode === "notes") {
    const r = rel.find(x => x.tag_name === arg)
    if (r) process.stdout.write((r.body || "（没有填写更新说明）").replace(/\r/g, "") + "\n")
  }
})'

# fetch_releases: download the release list once into $TMP_DIR/releases.json and parse it into
# $TMP_DIR/releases.tsv (tag, stable|prerelease, date, title), newest first. Never touches the site.
fetch_releases() {
  local repo code err
  repo="$(github_repo)" || exit 1
  log "从 GitHub 获取 $repo 的发布版本…"
  local auth=()
  [ -n "${GITHUB_TOKEN:-}" ] && auth=(-H "Authorization: Bearer $GITHUB_TOKEN")
  code="$(curl -sS -m 20 -o "$TMP_DIR/releases.json" -w '%{http_code}' -H 'Accept: application/vnd.github+json' \
      "${auth[@]}" "$GITHUB_API/repos/$repo/releases?per_page=100" 2>"$TMP_DIR/curl.err" || true)"
  if [ -z "$code" ] || [ "$code" = 000 ]; then
    err="$(tr -d '\r' < "$TMP_DIR/curl.err" | head -n 1 || true)"
    die "无法连接 GitHub（${err:-无响应}）。网站没有受到影响；请检查网络后重试"
  fi
  case "$code" in
    200) ;;
    403|429) die "GitHub API 拒绝请求（HTTP $code，通常是每小时 60 次的匿名速率限制）。网站没有受到影响；稍后重试或设置 GITHUB_TOKEN";;
    404) die "GitHub 上找不到仓库 $repo（HTTP 404）。网站没有受到影响；检查 GITHUB_REPO 或 origin 地址";;
    *) die "GitHub API 返回 HTTP $code。网站没有受到影响；稍后重试";;
  esac
  docker run --rm -i --network none "$(node_image)" node -e "$releases_js" list \
      < "$TMP_DIR/releases.json" > "$TMP_DIR/releases.tsv" 2>"$TMP_DIR/parse.err" \
    || die "无法解析 GitHub 的发布列表：$(head -n 1 "$TMP_DIR/parse.err")。网站没有受到影响"
}

release_notes() {
  docker run --rm -i --network none "$(node_image)" node -e "$releases_js" notes "$1" < "$TMP_DIR/releases.json"
}
latest_stable() { awk -F'\t' '$2 == "stable" {print $1; exit}' "$TMP_DIR/releases.tsv"; }
latest_any()    { awk -F'\t' 'NR == 1 {print $1}' "$TMP_DIR/releases.tsv"; }
release_line()  { awk -F'\t' -v t="$1" '$1 == t' "$TMP_DIR/releases.tsv"; }

# ---- local state -----------------------------------------------------------------------------------------

# try_env: load deploy/.env when it is complete; commands that only read GitHub still work without it.
INSTALLED=""
try_env() {
  if [ -f "$ENV_FILE" ] && ( load_env ) >/dev/null 2>&1; then
    load_env
    INSTALLED="$(current_version || true)"
  fi
}

describe() {  # describe SHA -> "v1.2.0 (abc123def456)" or "未发布的提交 abc123def456"
  # Release tags at the commit, plus tags this server recorded for it when deploying (state/tags), so a tag
  # moved later does not make an installed release look unreleased.
  local tags
  tags="$( { release_tags_at "$1"; awk -v s="$1" 'index($2, s) == 1 {print $1}' "${STATE_DIR:-/nonexistent}/tags" 2>/dev/null; } \
    | sort -u | tr '\n' ' ' || true)"
  if [ -n "$tags" ]; then echo "${tags% } ($1)"; else echo "未发布的提交 $1"; fi
}

fetch_tags() {
  local rc=0
  fetch_release_tags || rc=$?
  case "$rc" in
    0) ;;
    2) die "远端发布标签指向的提交与本地不同：$TAG_CONFLICTS。正式版本的标签不应改变，已停止；请先核实仓库是否被篡改。网站没有受到影响";;
    *) die "无法从 origin 获取标签（${FETCH_ERROR:-未知错误}）。网站没有受到影响";;
  esac
}

confirm() {  # confirm PROMPT EXPECTED
  if [ "$YES" = 1 ]; then return 0; fi
  [ -t 0 ] || die "非交互终端中需要加 --yes 才会执行（请先在终端中查看计划）"
  local answer
  printf '%s' "$1"
  read -r answer
  [ "$answer" = "$2" ] || die "已取消，没有做任何修改"
}

# resolve_target VERSION: sets TARGET (ref passed to update.sh), TARGET_SHA, TARGET_KIND and checks
# that a release tag is pinned to the commit seen before.
resolve_target() {
  local want="$1" line
  if [ -z "$want" ]; then
    if [ "$PRE" = 1 ]; then want="$(latest_any)"; else want="$(latest_stable)"; fi
    [ -n "$want" ] || die "GitHub 上还没有正式发布的版本$([ "$PRE" = 1 ] || echo '（加 --pre 可包含预发布版本）')。开发代码只能显式指定：--allow-unreleased main"
  fi
  line="$(release_line "$want")"
  if [ -n "$line" ]; then
    TARGET="$want"; TARGET_KIND="$(cut -f2 <<<"$line")"; TARGET_DATE="$(cut -f3 <<<"$line")"
    [ "$TARGET_KIND" = stable ] || [ "$PRE" = 1 ] || [ -n "$1" ] || die "内部错误：默认目标不是正式版本"
  else
    [ "$ALLOW_UNRELEASED" = 1 ] || die "$want 不是 GitHub 上已发布的版本。可用版本：deploy/manage.sh check；部署开发代码需加 --allow-unreleased"
    TARGET="$want"; TARGET_KIND="unreleased"; TARGET_DATE="-"
  fi
  fetch_tags
  if git -C "$REPO_DIR" rev-parse -q --verify "refs/remotes/origin/$TARGET" >/dev/null; then
    TARGET_SHA="$(git -C "$REPO_DIR" rev-parse --short=12 "origin/$TARGET")"
  else
    TARGET_SHA="$(git -C "$REPO_DIR" rev-parse -q --verify --short=12 "$TARGET^{commit}" 2>/dev/null)" \
      || die "仓库中找不到 $TARGET（发布存在但标签缺失？）。网站没有受到影响"
  fi
  if [ "$TARGET_KIND" != unreleased ]; then
    pinned_tag_ok "$TARGET" || die "标签 $TARGET 现在指向 $TARGET_SHA，与本机之前部署时记录的提交不同。正式版本的标签不应改变，已停止"
  fi
}

# show_plan CURRENT_SHA: what changes between the running version and TARGET.
show_plan() {
  local cur="$1" mig dir
  echo
  echo "  当前版本：$(describe "$cur")"
  echo "  目标版本：$TARGET ($TARGET_SHA)  类型：$(kind_label "$TARGET_KIND")  发布日期：$TARGET_DATE"
  if git -C "$REPO_DIR" merge-base --is-ancestor "$cur" "$TARGET_SHA" 2>/dev/null; then dir="升级"
  elif git -C "$REPO_DIR" merge-base --is-ancestor "$TARGET_SHA" "$cur" 2>/dev/null; then dir="降级（回到旧版本）"
  else dir="切换到另一条提交线"; fi
  echo "  方向：$dir"
  echo "  数据库结构变更："
  mig="$(git -C "$REPO_DIR" diff --name-status "$cur" "$TARGET_SHA" -- apps/api/db/migrations 2>/dev/null || true)"
  if [ -z "$mig" ]; then
    echo "    无（两个版本的迁移文件相同）"
  else
    awk -F'\t' '
      $1 == "A" { n = $2; sub(/.*\//, "", n); print "    + 新增迁移 " n " —— 更新时执行，之后回滚不会撤销" }
      $1 == "D" { n = $2; sub(/.*\//, "", n); print "    - 目标版本没有迁移 " n " —— 数据库结构不会回退，旧代码将在较新的结构上运行" }
      $1 ~ /^[MR]/ { n = $NF; sub(/.*\//, "", n); print "    ! 迁移 " n " 在两个版本间被修改（不符合项目规范，请先确认）" }' <<<"$mig"
  fi
  echo "  更新前会自动完整备份（数据库、图片、配置）；失败时自动切回当前版本，但数据库结构不会自动回退。"
  if [ "$TARGET_KIND" != unreleased ]; then
    echo "  更新说明（$TARGET）："
    release_notes "$TARGET" | head -n 25 | sed 's/^/    | /'
  else
    echo "  注意：这是未发布的开发代码，没有经过正式发布流程，不建议用于生产网站。"
  fi
  echo
}

kind_label() { case "$1" in stable) echo "正式版";; prerelease) echo "预发布版";; *) echo "开发代码（未发布）";; esac; }

# ---- commands ----------------------------------------------------------------------------------------------

cmd_status() {
  check_tools
  try_env
  if [ -z "$INSTALLED" ]; then echo "尚未安装（或 deploy/.env 未完成）。安装：sudo deploy/manage.sh install"; return 0; fi
  echo "当前版本：$(describe "$INSTALLED")"
  echo "上一版本：$( p="$(previous_version || true)"; [ -n "$p" ] && describe "$p" || echo -)"
  echo "代码目录：$(git -C "$REPO_DIR" rev-parse --short=12 HEAD)  数据库结构版本：$(schema_version)"
  dc ps --format 'table {{.Service}}\t{{.Status}}' || true
  echo "最近记录："
  tail -n 5 "$STATE_DIR/history" 2>/dev/null | sed 's/^/  /'
}

cmd_check() {
  check_tools
  try_env
  fetch_releases
  local stable cur_tags main_sha
  stable="$(latest_stable)"
  echo
  if [ -s "$TMP_DIR/releases.tsv" ]; then
    echo "GitHub 发布版本（最新在前）："
    while IFS=$'\t' read -r tag kind date title; do
      [ "$kind" = stable ] || [ "$PRE" = 1 ] || continue
      printf '  %-16s %-8s %s  %s%s\n' "$tag" "$(kind_label "$kind")" "$date" "$title" "$([ "$tag" = "$stable" ] && echo '   ← 最新正式版')"
    done < "$TMP_DIR/releases.tsv"
    [ "$PRE" = 1 ] || [ "$(grep -c $'\tprerelease\t' "$TMP_DIR/releases.tsv" || true)" = 0 ] || echo "  （另有预发布版本，加 --pre 显示）"
  else
    echo "GitHub 上还没有已发布的版本。"
  fi
  main_sha="$(git -C "$REPO_DIR" ls-remote origin refs/heads/main 2>/dev/null | cut -c1-12 || true)"
  [ -n "$main_sha" ] && echo "  main 分支最新提交：$main_sha（开发代码，不作为更新推荐）"
  echo
  if [ -z "$INSTALLED" ]; then
    echo "本机尚未安装。$([ -n "$stable" ] && echo "安装最新正式版：sudo deploy/manage.sh install")"
    return 0
  fi
  fetch_tags   # first, so the current commit is described with tags published since the last update
  echo "本机当前：$(describe "$INSTALLED")"
  [ -n "$stable" ] || { echo "没有可推荐的正式版本。"; return 0; }
  local stable_sha
  stable_sha="$(git -C "$REPO_DIR" rev-parse -q --verify --short=12 "$stable^{commit}" 2>/dev/null || true)"
  cur_tags="$(release_tags_at "$INSTALLED")"
  local newest; newest="$(latest_any)"
  if [ "$PRE" = 1 ] && [ "$newest" != "$stable" ] && [ -z "$(release_tags_at "$INSTALLED" | grep -Fx "$newest" || true)" ]; then
    echo "有更新的预发布版本：$newest（不建议用于生产）。安装：sudo deploy/manage.sh update $newest --pre"
  fi
  if [ "$stable_sha" = "$INSTALLED" ]; then
    echo "已是最新正式版 $stable，无需更新。"
  elif [ -n "$stable_sha" ] && git -C "$REPO_DIR" merge-base --is-ancestor "$INSTALLED" "$stable_sha" 2>/dev/null; then
    echo "有可用更新：$stable（$(release_line "$stable" | cut -f3)）。更新：sudo deploy/manage.sh update"
  else
    echo "最新正式版 $stable 不比当前版本新$([ -z "$cur_tags" ] && echo '（当前运行的是未发布代码）')；不提示更新。"
  fi
}

cmd_install() {
  check_tools
  try_env
  if [ -n "$INSTALLED" ]; then
    log "已安装 $(describe "$INSTALLED")；运行 install.sh 检查并在需要时重新启动该版本（不切换版本、不删除数据）"
    exec "$DEPLOY_DIR/install.sh"
  fi
  [ -z "$(git -C "$REPO_DIR" status --porcelain --untracked-files=no)" ] || die "代码目录有未提交的修改，已停止"
  if [ "$ALLOW_UNRELEASED" = 1 ] && [ -z "$VERSION" ]; then VERSION=main; fi
  if [ "$VERSION" = main ] || [ "$ALLOW_UNRELEASED" = 1 ]; then
    { fetch_releases; } 2>/dev/null || : > "$TMP_DIR/releases.tsv"
  else
    fetch_releases
  fi
  resolve_target "$VERSION"
  echo
  echo "  将安装：$TARGET ($TARGET_SHA)  类型：$(kind_label "$TARGET_KIND")  发布日期：$TARGET_DATE"
  [ "$TARGET_KIND" = unreleased ] && warn "这是未发布的开发代码，不建议用于生产网站"
  confirm "输入 $TARGET 确认安装该版本：" "$TARGET"
  git -C "$REPO_DIR" checkout -q --detach "$TARGET_SHA"
  log "代码已切换到 $TARGET ($TARGET_SHA)"
  RELEASE_REF="$TARGET" exec "$DEPLOY_DIR/install.sh"
}

cmd_update() {
  check_tools
  try_env
  [ -n "$INSTALLED" ] || die "尚未安装；先运行 sudo deploy/manage.sh install"
  if [ "$ALLOW_UNRELEASED" = 1 ]; then
    { fetch_releases; } 2>/dev/null || : > "$TMP_DIR/releases.tsv"
  else
    fetch_releases
  fi
  resolve_target "$VERSION"
  if [ "$TARGET_SHA" = "$INSTALLED" ]; then
    log "当前已是 $(describe "$INSTALLED")，无需更新。"
    return 0
  fi
  show_plan "$INSTALLED"
  confirm "确认更新请输入目标版本号 $TARGET：" "$TARGET"
  exec "$DEPLOY_DIR/update.sh" --ref "$TARGET"
}

cmd_rollback() {
  check_tools
  try_env
  [ -n "$INSTALLED" ] || die "尚未安装"
  local prev; prev="$(previous_version || true)"
  [ -n "$prev" ] || die "没有可回滚的上一版本"
  echo
  echo "  当前版本：$(describe "$INSTALLED")"
  echo "  回滚到：  $(describe "$prev")"
  echo "  只切换应用镜像；数据库结构与数据保持不变（需要旧数据时使用 restore 恢复更新前的备份）。"
  echo
  confirm "输入 rollback 确认：" rollback
  exec "$DEPLOY_DIR/rollback.sh"
}

cmd_precheck() {
  local args=()
  if [ -f "$ENV_FILE" ] && ( load_env ) >/dev/null 2>&1; then
    load_env
    args=(--npm-network "$PROXY_NETWORK" --data-dir "$DATA_DIR" --app-subnet "${APP_SUBNET:-172.30.240.0/24}")
    local host="${PUBLIC_BASE_URL#https://}"; args+=(--domain "${host%%[:/]*}")
    [ -n "${SMTP_HOST:-}" ] && args+=(--smtp "$SMTP_HOST:${SMTP_PORT:-587}")
  fi
  exec bash "$DEPLOY_DIR/precheck.sh" "${args[@]}" "$@"
}

menu() {
  local c
  while :; do
    cat <<'EOF'

XiaoWork Blog 管理
  1) 查看状态          2) 检查新版本        3) 更新到最新正式版
  4) 回滚到上一版本    5) 立即备份          6) 只读环境预检
  7) 首次安装          0) 退出
EOF
    printf '选择：'
    read -r c || exit 0
    case "$c" in
      1) ( cmd_status ) || true;;
      2) ( cmd_check ) || true;;
      3) ( cmd_update ) || true;;
      4) ( cmd_rollback ) || true;;
      5) ( "$DEPLOY_DIR/backup.sh" ) || true;;
      6) ( cmd_precheck ) || true;;
      7) ( cmd_install ) || true;;
      0|q) exit 0;;
      *) echo "无效选择";;
    esac
  done
}

# ---- arguments ---------------------------------------------------------------------------------------------

CMD="${1:-}"; [ $# -gt 0 ] && shift
YES=0; PRE=0; ALLOW_UNRELEASED=0; VERSION=""; PASS=()
case "$CMD" in
  backup) exec "$DEPLOY_DIR/backup.sh" "$@";;
  restore) exec "$DEPLOY_DIR/restore.sh" "$@";;
  admin) exec "$DEPLOY_DIR/admin.sh" "$@";;
esac
while [ $# -gt 0 ]; do
  case "$1" in
    --yes|-y) YES=1;;
    --pre) PRE=1;;
    --allow-unreleased) ALLOW_UNRELEASED=1;;
    -h|--help) usage; exit 0;;
    -*) [ "$CMD" = precheck ] && PASS+=("$1") || die "未知参数：$1";;
    *) if [ "$CMD" = precheck ]; then PASS+=("$1"); elif [ -z "$VERSION" ]; then VERSION="$1"; else die "多余的参数：$1"; fi;;
  esac
  shift
done

case "$CMD" in
  "") if [ -t 0 ]; then menu; else usage; exit 2; fi;;
  status) cmd_status;;
  check) cmd_check;;
  install) cmd_install;;
  update) cmd_update;;
  rollback) cmd_rollback;;
  precheck) cmd_precheck "${PASS[@]}";;
  -h|--help|help) usage;;
  *) usage; die "未知命令：$CMD";;
esac
exit 0
}
