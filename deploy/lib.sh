#!/usr/bin/env bash
# Shared helpers for the XiaoWork Blog deploy scripts (P8-A). Sourced, not executed.
#
# Safety rules followed by every script:
#   * never `docker compose down -v`, `docker volume rm` or `rm -rf` on data directories;
#   * data is only ever moved aside (…before-restore-<time>), never deleted, except old backups past BACKUP_KEEP;
#   * every variable used in a path is checked with ${VAR:?} first.
set -Eeuo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$DEPLOY_DIR/.." && pwd)"
ENV_FILE="${ENV_FILE:-$DEPLOY_DIR/.env}"
COMPOSE_FILE="$DEPLOY_DIR/compose.prod.yaml"   # + compose.<PROXY_MODE>.yaml, see dc()
PROJECT=xiaowork-blog

c_red=$'\033[31m'; c_grn=$'\033[32m'; c_ylw=$'\033[33m'; c_off=$'\033[0m'
[ -t 1 ] || { c_red=; c_grn=; c_ylw=; c_off=; }
log()  { printf '%s[%s]%s %s\n' "$c_grn" "$(date '+%F %T')" "$c_off" "$*"; }
warn() { printf '%s[warn]%s %s\n' "$c_ylw" "$c_off" "$*" >&2; }
die()  { printf '%s[error]%s %s\n' "$c_red" "$c_off" "$*" >&2; exit 1; }
trap 'die "命令失败（第 $LINENO 行）：$BASH_COMMAND"' ERR

require_cmd() { command -v "$1" >/dev/null 2>&1 || die "缺少命令：$1（$2）"; }

check_tools() {
  require_cmd docker "安装 Docker Engine 24+"
  docker compose version >/dev/null 2>&1 || die "缺少 Docker Compose v2 插件（docker compose）"
  require_cmd git "安装 git"
  require_cmd openssl "安装 openssl"
  require_cmd sha256sum "安装 coreutils"
  docker info >/dev/null 2>&1 || die "无法连接 Docker 守护进程（是否用 root 或 docker 组用户运行？）"
  local v; v="$(docker compose version --short 2>/dev/null | sed 's/^v//')"
  case "$v" in 1.*|2.[0-9].*|2.1[0-9].*) die "Docker Compose 版本过旧（$v），需要 2.20 或以上";; esac
}

# compose_at_least MAJOR MINOR PATCH: the installed Docker Compose is at least that version.
compose_at_least() {
  local v a b c
  v="$(docker compose version --short 2>/dev/null | sed 's/^v//; s/[^0-9.].*$//')"
  IFS=. read -r a b c <<< "$v"
  a="${a:-0}"; b="${b:-0}"; c="${c:-0}"
  [ "$a" -gt "$1" ] || { [ "$a" -eq "$1" ] && { [ "$b" -gt "$2" ] || { [ "$b" -eq "$2" ] && [ "$c" -ge "$3" ]; }; }; }
}

# load_env reads deploy/.env into the environment and refuses placeholders or unsafe values.
load_env() {
  [ -f "$ENV_FILE" ] || die "未找到 $ENV_FILE；先运行 deploy/install.sh 生成"
  local perms; perms="$(stat -c '%a' "$ENV_FILE" 2>/dev/null || echo 600)"
  [ "$perms" = 600 ] || [ "$perms" = 400 ] || { warn "$ENV_FILE 权限为 $perms，已收紧为 600"; chmod 600 "$ENV_FILE"; }
  # The file is in docker compose dotenv syntax (KEY=value, optional quotes), not shell: values such as
  # SMTP_FROM=Blog <no-reply@…> must not be evaluated, so it is parsed line by line instead of sourced.
  local line n=0 key val
  while IFS= read -r line || [ -n "$line" ]; do
    n=$((n + 1)); line="${line%$'\r'}"
    [[ "$line" =~ ^[[:space:]]*(#.*)?$ ]] && continue
    [[ "$line" =~ ^[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]] || die "$ENV_FILE 第 $n 行格式错误（应为 KEY=value）"
    key="${BASH_REMATCH[1]}"; val="${BASH_REMATCH[2]}"
    if [[ "$val" =~ ^\"(.*)\"$ ]] || [[ "$val" =~ ^\'(.*)\'$ ]]; then val="${BASH_REMATCH[1]}"; fi
    export "$key=$val"
  done < "$ENV_FILE"
  local bad=() name
  # Older deploy/.env files have no PROXY_MODE: they keep the original Nginx Proxy Manager network mode.
  PROXY_MODE="${PROXY_MODE:-docker-network}"; export PROXY_MODE
  local required="PUBLIC_BASE_URL DATA_DIR POSTGRES_USER POSTGRES_DB POSTGRES_PASSWORD APP_SECRET_KEY SMTP_HOST SMTP_FROM"
  case "$PROXY_MODE" in
    docker-network) required="$required PROXY_NETWORK";;
    host-loopback)
      WEB_HOST_PORT="${WEB_HOST_PORT:-13000}"; API_HOST_PORT="${API_HOST_PORT:-18081}"; export WEB_HOST_PORT API_HOST_PORT
      for name in WEB_HOST_PORT API_HOST_PORT; do
        [[ "${!name}" =~ ^[0-9]+$ ]] && [ "${!name}" -ge 1024 ] && [ "${!name}" -le 65535 ] || bad+=("$name 必须是 1024–65535 之间的端口号")
      done
      [ "$WEB_HOST_PORT" != "$API_HOST_PORT" ] || bad+=("WEB_HOST_PORT 与 API_HOST_PORT 不能相同")
      # compose.host-loopback.yaml uses the !override / !reset merge tags.
      compose_at_least 2 24 4 || bad+=("host-loopback 需要 Docker Compose 2.24.4 或以上（当前 $(docker compose version --short 2>/dev/null || echo 未知)）");;
    *) bad+=("PROXY_MODE 只能是 docker-network（Nginx Proxy Manager 等容器反代）或 host-loopback（1Panel OpenResty 等主机网络反代）");;
  esac
  for name in $required; do
    [ -n "${!name:-}" ] || bad+=("$name 未设置")
    case "${!name:-}" in *CHANGE_ME*) bad+=("$name 仍是示例值");; esac
  done
  case "${PUBLIC_BASE_URL:-}" in https://*) ;; *) bad+=("PUBLIC_BASE_URL 必须以 https:// 开头");; esac
  case "${DATA_DIR:-}" in /*) ;; *) bad+=("DATA_DIR 必须是绝对路径");; esac
  case "${DATA_DIR:-}" in "$REPO_DIR"|"$REPO_DIR"/*) bad+=("DATA_DIR 不能放在代码目录内");; esac
  [[ "${POSTGRES_PASSWORD:-}" =~ ^[A-Za-z0-9_-]{16,}$ ]] || bad+=("POSTGRES_PASSWORD 需为 16 位以上字母、数字、_ 或 -")
  [ "${#APP_SECRET_KEY}" -ge 32 ] || bad+=("APP_SECRET_KEY 至少 32 字节")
  [ "${SMTP_TLS:-starttls}" != none ] || [[ "$PUBLIC_BASE_URL" =~ ^https://(localhost|127\.0\.0\.1)(:|$) ]] || bad+=("公网站点的 SMTP_TLS 不能为 none")
  if [ ${#bad[@]} -gt 0 ]; then
    printf '  - %s\n' "${bad[@]}" >&2
    die "$ENV_FILE 配置不完整，请修正后重试"
  fi
  BACKUP_KEEP="${BACKUP_KEEP:-14}"
  STATE_DIR="$DATA_DIR/state"
  BACKUP_DIR="$DATA_DIR/backups"
  export APP_VERSION="${APP_VERSION:-$(current_version || true)}"
}

dc() {
  local overlay=()
  [ "${PROXY_MODE:-docker-network}" = host-loopback ] && overlay=(-f "$DEPLOY_DIR/compose.host-loopback.yaml")
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" "${overlay[@]}" --env-file "$ENV_FILE" "$@"
}

current_version()  { [ -f "${STATE_DIR:?}/current" ] && cat "$STATE_DIR/current"; }
previous_version() { [ -f "${STATE_DIR:?}/previous" ] && cat "$STATE_DIR/previous"; }
repo_version()     { git -C "$REPO_DIR" rev-parse --short=12 HEAD; }

image_exists() { docker image inspect "$1" >/dev/null 2>&1; }

build_images() {
  local ver="${1:?version}"
  log "构建镜像 $ver（api / web）…"
  docker build -q -t "xiaowork-blog-api:$ver" --build-arg "GOPROXY=${GOPROXY:-https://proxy.golang.org,direct}" "$REPO_DIR/apps/api" >/dev/null
  docker build -q -t "xiaowork-blog-web:$ver" --build-arg "NPM_REGISTRY=${NPM_REGISTRY:-https://registry.npmjs.org/}" "$REPO_DIR/apps/web" >/dev/null
  log "镜像构建完成"
}

# detect_trusted_proxies fills TRUSTED_PROXY_CIDRS (proxy network + app subnet) in deploy/.env when empty.
detect_trusted_proxies() {
  if [ -n "${TRUSTED_PROXY_CIDRS:-}" ]; then return; fi
  local subnet
  if [ "$PROXY_MODE" = host-loopback ]; then
    # Requests from the host proxy reach the published ports through this stack's own app network (they
    # arrive from its gateway), and Nuxt SSR calls the API from the same network: nothing else is trusted.
    TRUSTED_PROXY_CIDRS="${APP_SUBNET:-172.30.240.0/24}"
  else
    subnet="$(docker network inspect "$PROXY_NETWORK" -f '{{range .IPAM.Config}}{{.Subnet}},{{end}}' 2>/dev/null | sed 's/,$//')"
    [ -n "$subnet" ] || die "无法读取代理网络 $PROXY_NETWORK 的网段，请手动设置 TRUSTED_PROXY_CIDRS"
    TRUSTED_PROXY_CIDRS="$subnet,${APP_SUBNET:-172.30.240.0/24}"
  fi
  sed -i "s|^TRUSTED_PROXY_CIDRS=.*$|TRUSTED_PROXY_CIDRS=$TRUSTED_PROXY_CIDRS|" "$ENV_FILE"
  export TRUSTED_PROXY_CIDRS
  log "已写入 TRUSTED_PROXY_CIDRS=$TRUSTED_PROXY_CIDRS"
}

check_proxy_network() {
  if [ "$PROXY_MODE" = host-loopback ]; then return 0; fi   # no shared Docker network in this mode
  # Docker's predefined networks have no service-name DNS (bridge) or no isolation (host/none), so NPM could
  # not reach xiaowork-blog-web / xiaowork-blog-api by name.
  case "$PROXY_NETWORK" in
    bridge|host|none) die "PROXY_NETWORK 不能是 Docker 预置网络 $PROXY_NETWORK；需要 NPM 所在的自定义网络（见运维手册 §4 与 P8-B1 预检文档）";;
  esac
  docker network inspect "$PROXY_NETWORK" >/dev/null 2>&1 \
    || die "Docker 网络 $PROXY_NETWORK 不存在。请填写 Nginx Proxy Manager 所在网络（docker network ls），或先创建：docker network create $PROXY_NETWORK"
  local members
  members="$(docker network inspect "$PROXY_NETWORK" -f '{{range .Containers}}{{.Name}} {{end}}' 2>/dev/null || true)"
  members="${members//xiaowork-blog-api-1/}"; members="${members//xiaowork-blog-web-1/}"
  [ -n "${members// /}" ] || warn "网络 $PROXY_NETWORK 中没有其他容器：请确认 Nginx Proxy Manager 确实在这个网络上"
}

prepare_dirs() {
  mkdir -p "${DATA_DIR:?}/media" "$DATA_DIR/backups" "$DATA_DIR/state"
  chmod 700 "$DATA_DIR/backups" "$DATA_DIR/state"
  # The api container runs as uid/gid 10001 (apps/api/Dockerfile).
  chown 10001:10001 "$DATA_DIR/media" 2>/dev/null || warn "无法 chown $DATA_DIR/media 为 10001:10001（需要 root）"
}

check_disk() {
  local need_mb="${1:-2048}" avail
  avail="$(df -Pm "${DATA_DIR:?}" | awk 'NR==2 {print $4}')"
  [ "${avail:-0}" -ge "$need_mb" ] || die "$DATA_DIR 所在磁盘剩余 ${avail}MB，少于所需 ${need_mb}MB"
}

# wait_healthy waits until every listed service reports healthy.
wait_healthy() {
  local timeout="${WAIT_TIMEOUT:-240}" start svc cid state
  start=$(date +%s)
  for svc in "$@"; do
    while :; do
      cid="$(dc ps -q "$svc" 2>/dev/null || true)"
      state="$( [ -n "$cid" ] && docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$cid" 2>/dev/null || echo missing)"
      [ "$state" = healthy ] && break
      [ "$state" = missing ] && return 1   # no container for this service at all
      [ "$state" = unhealthy ] && { dc logs --tail 40 "$svc" >&2 || true; return 1; }
      if [ $(( $(date +%s) - start )) -gt "$timeout" ]; then
        dc logs --tail 40 "$svc" >&2 || true
        return 1
      fi
      sleep 3
    done
  done
}

# smoke_test checks the running stack from inside the web container: API readiness, an SSR page and the
# same-origin API proxy. It does not depend on NPM or DNS.
smoke_test() {
  dc exec -T web node -e '
    const ok = async (u, want) => { const r = await fetch(u); if (!want(r)) throw new Error(u + " -> " + r.status); return r };
    (async () => {
      await ok("http://api:8080/health/ready", r => r.ok);
      const home = await ok("http://127.0.0.1:3000/", r => r.status === 200);
      if (!(await home.text()).includes("<html")) throw new Error("home page is not HTML");
      await ok("http://127.0.0.1:3000/api/v1/site", r => r.ok);
      await ok("http://127.0.0.1:3000/robots.txt", r => r.ok);
    })().then(() => process.exit(0), e => { console.error(e.message); process.exit(1) });'
}

schema_version() {
  dc exec -T db sh -c 'psql -tA -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT coalesce(max(version_id),0) FROM goose_db_version WHERE is_applied"' 2>/dev/null | tr -d '[:space:]' || echo 0
}

# backup_now LABEL: database dump + media archive + settings copy + manifest with checksums.
backup_now() {
  local label="${1:-manual}" ts dir
  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  dir="${BACKUP_DIR:?}/$ts-$label"
  mkdir -p "$dir"
  chmod 700 "$dir"
  log "备份到 $dir"
  dc exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc --no-owner --no-privileges' > "$dir/db.dump"
  [ -s "$dir/db.dump" ] || die "数据库备份为空"
  tar -C "${DATA_DIR:?}" -czf "$dir/media.tar.gz" media
  cp "$ENV_FILE" "$dir/env.backup"
  chmod 600 "$dir/env.backup" "$dir/db.dump" "$dir/media.tar.gz"
  {
    echo "created_at=$ts"
    echo "label=$label"
    echo "app_version=${APP_VERSION:-unknown}"
    echo "schema_version=$(schema_version)"
    echo "media_files=$(find "$DATA_DIR/media" -type f | wc -l | tr -d ' ')"
  } > "$dir/manifest.txt"
  (cd "$dir" && sha256sum db.dump media.tar.gz env.backup > SHA256SUMS)
  prune_backups
  # shellcheck disable=SC2034  # read by the calling script (update.sh, restore.sh)
  BACKUP_PATH="$dir"
  log "备份完成：$(du -sh "$dir" | cut -f1)"
}

prune_backups() {
  local keep="${BACKUP_KEEP:-14}" stale d
  [[ "$keep" =~ ^[0-9]+$ ]] && [ "$keep" -ge 1 ] || keep=14
  # Only directories created by backup_now (UTC timestamp prefix) inside BACKUP_DIR are considered.
  mapfile -t stale < <(find "${BACKUP_DIR:?}" -mindepth 1 -maxdepth 1 -type d -name '20[0-9][0-9][01][0-9][0-3][0-9]T*' | sort | head -n "-$keep")
  for d in "${stale[@]}"; do
    case "$d" in "$BACKUP_DIR"/20*) rm -rf -- "$d"; log "已清理旧备份 $(basename "$d")";; esac
  done
}

record_release() {
  local new="${1:?}" old="${2:-}" ref="${3:-}" tag
  [ -n "$old" ] && [ "$old" != "$new" ] && echo "$old" > "$STATE_DIR/previous"
  echo "$new" > "$STATE_DIR/current"
  echo "$(date -u +%FT%TZ) $new ${ref:--}" >> "$STATE_DIR/history"
  # Pin every release tag on the deployed commit: a tag that later points elsewhere is refused (pinned_tag_ok).
  for tag in $(release_tags_at "$new"); do
    grep -qx "$tag [0-9a-f]*" "$STATE_DIR/tags" 2>/dev/null || echo "$tag $(git -C "$REPO_DIR" rev-parse "$tag^{commit}")" >> "$STATE_DIR/tags"
  done
}

# Release tags follow vMAJOR.MINOR.PATCH with an optional pre-release suffix (docs/RELEASING.md).
RELEASE_TAG_RE='^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'

# release_tags_at COMMIT: release tags pointing at COMMIT (nothing when it is an unreleased commit).
release_tags_at() {
  git -C "$REPO_DIR" tag --points-at "${1:?}" 2>/dev/null | grep -E "$RELEASE_TAG_RE" || true
}

# port_listening PORT: true when something listens on TCP PORT (any address).
port_listening() {
  if command -v ss >/dev/null 2>&1; then
    [ -n "$(ss -Htln "sport = :${1:?}" 2>/dev/null || true)" ]
  else
    (exec 3<>"/dev/tcp/127.0.0.1/${1:?}") 2>/dev/null
  fi
}

# check_host_ports: host-loopback mode — WEB_HOST_PORT / API_HOST_PORT must be free, unless this stack
# already publishes them (re-run, update). Nothing else is stopped or touched.
check_host_ports() {
  [ "$PROXY_MODE" = host-loopback ] || return 0
  local port svc ours
  for svc in web api; do
    if [ "$svc" = web ]; then port="$WEB_HOST_PORT"; else port="$API_HOST_PORT"; fi
    port_listening "$port" || continue
    ours="$(docker ps --filter "label=com.docker.compose.project=$PROJECT" --format '{{.Ports}}' 2>/dev/null | grep -c "127.0.0.1:$port->" || true)"
    [ "$ours" -gt 0 ] && continue
    die "端口 $port（$svc）已被其他程序占用。请在 $ENV_FILE 中把 $( [ "$svc" = web ] && echo WEB_HOST_PORT || echo API_HOST_PORT ) 改为空闲端口；本脚本不会停止任何其他服务"
  done
}

# fetch_release_tags: git fetch --tags from origin. git refuses ("would clobber existing tag") to replace a
# local tag by a different tag object. That also happens when only the object differs — e.g. a CI checkout
# creates a lightweight local tag for an annotated remote tag — so each refused tag is compared by the commit
# it points to: the same commit means the local tag is replaced by the remote one; a different commit means
# the published tag was moved and is refused.
# Returns 0 on success, 1 when origin cannot be reached (FETCH_ERROR), 2 when a tag was moved (TAG_CONFLICTS).
# shellcheck disable=SC2034  # FETCH_ERROR / TAG_CONFLICTS are read by the calling script (manage.sh, update.sh)
fetch_release_tags() {
  local err tag rejected local_commit remote_commit
  FETCH_ERROR=""; TAG_CONFLICTS=""
  err="$(git -C "$REPO_DIR" fetch --tags origin 2>&1 >/dev/null)" && return 0
  rejected="$(printf '%s\n' "$err" | awk '/would clobber existing tag/ { for (i = 1; i <= NF; i++) if ($i == "[rejected]") { print $(i + 1); break } }')"
  if [ -z "$rejected" ]; then
    FETCH_ERROR="$(printf '%s\n' "$err" | grep -m 1 -iE 'fatal|error' || printf '%s\n' "$err" | head -n 1)"
    return 1
  fi
  for tag in $rejected; do
    local_commit="$(git -C "$REPO_DIR" rev-parse -q --verify "refs/tags/$tag^{commit}" 2>/dev/null || true)"
    remote_commit="$(git -C "$REPO_DIR" ls-remote origin "refs/tags/$tag^{}" 2>/dev/null | cut -f1 || true)"
    [ -n "$remote_commit" ] || remote_commit="$(git -C "$REPO_DIR" ls-remote origin "refs/tags/$tag" 2>/dev/null | cut -f1 || true)"
    if [ -n "$local_commit" ] && [ "$local_commit" = "$remote_commit" ]; then
      git -C "$REPO_DIR" fetch -q origin "+refs/tags/$tag:refs/tags/$tag" 2>/dev/null \
        || { FETCH_ERROR="无法更新本地标签 $tag"; return 1; }
    else
      TAG_CONFLICTS="${TAG_CONFLICTS:+$TAG_CONFLICTS }$tag(本地 ${local_commit:0:12} / 远端 ${remote_commit:0:12})"
    fi
  done
  [ -z "$TAG_CONFLICTS" ] || return 2
  err="$(git -C "$REPO_DIR" fetch --tags origin 2>&1 >/dev/null)" && return 0
  FETCH_ERROR="$(printf '%s\n' "$err" | head -n 1)"
  return 1
}

# pinned_tag_ok TAG: false when TAG was deployed here before and now resolves to a different commit.
pinned_tag_ok() {
  local pinned now
  pinned="$(awk -v t="${1:?}" '$1 == t {print $2}' "${STATE_DIR:-/nonexistent}/tags" 2>/dev/null | head -n 1 || true)"
  [ -z "$pinned" ] && return 0
  now="$(git -C "$REPO_DIR" rev-parse -q --verify "$1^{commit}" 2>/dev/null || true)"
  [ "$now" = "$pinned" ]
}

# cidr_overlap A/len B/len: true when two IPv4 ranges intersect (IPv6 ranges are ignored).
ip4_to_int() { local IFS=.; set -- $1; echo $(( ($1 << 24) + ($2 << 16) + ($3 << 8) + $4 )); }
cidr_overlap() {
  local a="${1%/*}" al="${1#*/}" b="${2%/*}" bl="${2#*/}" as ae bs be
  case "$a$b" in *:*) return 1;; esac
  as=$(( $(ip4_to_int "$a") & ~((1 << (32 - al)) - 1) & 0xFFFFFFFF )); ae=$(( as + (1 << (32 - al)) - 1 ))
  bs=$(( $(ip4_to_int "$b") & ~((1 << (32 - bl)) - 1) & 0xFFFFFFFF )); be=$(( bs + (1 << (32 - bl)) - 1 ))
  [ "$as" -le "$be" ] && [ "$bs" -le "$ae" ]
}

# subnet_conflicts SUBNET: names of Docker networks (other than this stack's own) whose ranges overlap SUBNET.
subnet_conflicts() {
  local id name nets s
  for id in $(docker network ls -q); do
    name="$(docker network inspect -f '{{.Name}}' "$id")"
    [ "$name" = "${PROJECT}_app" ] && continue
    nets="$(docker network inspect -f '{{range .IPAM.Config}}{{.Subnet}} {{end}}' "$id")"
    for s in $nets; do cidr_overlap "$s" "${1:?}" && echo "$name($s)"; done
  done
  return 0
}

# prune_images keeps the current and previous release images and removes older xiaowork-blog tags.
prune_images() {
  local keep_cur keep_prev tag
  keep_cur="$(current_version || true)"; keep_prev="$(previous_version || true)"
  # The exited one-shot migrate container (left by `up`, since api depends on it) would pin an old image.
  dc rm -f -s migrate >/dev/null 2>&1 || true
  for repo in xiaowork-blog-api xiaowork-blog-web; do
    for tag in $(docker image ls "$repo" --format '{{.Tag}}'); do
      case "$tag" in "$keep_cur"|"$keep_prev") ;; *) docker image rm "$repo:$tag" >/dev/null 2>&1 || true;; esac
    done
  done
}
